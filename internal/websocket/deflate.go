package websocket

import (
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// permessage-deflate (RFC 7692), in the one form this package speaks: no
// context takeover in either direction, so each message is compressed and
// decompressed on its own, with no window kept between messages, and the
// sender's window is Go's compress/flate's, 2^15 bytes.
//
// As a client it offers exactly that, `client_no_context_takeover` and
// `server_no_context_takeover`, and takes a response that agrees to the
// second and names no client_max_window_bits, which it did not offer. As
// a server it takes the first offer whose parameters it can honor (any
// but a server_max_window_bits under 15, which compress/flate cannot keep
// to) and answers with both no_context_takeover parameters, as RFC 7692
// section 7.1.1.1 lets a server ask of a client that did not offer it.
// A browser's offer, `permessage-deflate; client_max_window_bits`, is one
// it takes.

// deflateName is the extension's token.
const deflateName = "permessage-deflate"

// DeflateOffer is the Sec-WebSocket-Extensions value a client sends.
const DeflateOffer = "permessage-deflate; client_no_context_takeover; server_no_context_takeover"

// deflateResponse is the value a server answers an offer it takes with.
const deflateResponse = "permessage-deflate; server_no_context_takeover; client_no_context_takeover"

// extension is one element of a Sec-WebSocket-Extensions header: its name
// and parameters in order, a parameter with no value holding "".
type extension struct {
	name   string
	params [][2]string
}

// parseExtensions is the elements of a Sec-WebSocket-Extensions header's
// values (RFC 6455 section 9.1), or an error for one that does not parse.
// A quoted value is unquoted.
func parseExtensions(values []string) ([]extension, error) {
	var out []extension
	for _, element := range Tokens(values) {
		parts := strings.Split(element, ";")
		e := extension{name: strings.ToLower(strings.TrimSpace(parts[0]))}
		if e.name == "" {
			return nil, fmt.Errorf("an extension with no name: %q", element)
		}
		for _, p := range parts[1:] {
			name, value, _ := strings.Cut(p, "=")
			name = strings.ToLower(strings.TrimSpace(name))
			value = strings.Trim(strings.TrimSpace(value), `"`)
			if name == "" {
				return nil, fmt.Errorf("an empty parameter in %q", element)
			}
			e.params = append(e.params, [2]string{name, value})
		}
		out = append(out, e)
	}
	return out, nil
}

// windowBits reports whether value is a window size RFC 7692 allows, 8
// to 15, and which.
func windowBits(value string) (int, bool) {
	n, err := strconv.Atoi(value)
	return n, err == nil && n >= 8 && n <= 15 && value == strconv.Itoa(n)
}

// AcceptDeflate is the Sec-WebSocket-Extensions value a server answers the
// client's offers with: deflateResponse for the first permessage-deflate
// offer it can honor, and "" when there is none, which leaves the
// connection uncompressed.
func AcceptDeflate(values []string) string {
	offers, err := parseExtensions(values)
	if err != nil {
		return ""
	}
	for _, o := range offers {
		if o.name == deflateName && honorable(o) {
			return deflateResponse
		}
	}
	return ""
}

// honorable reports whether a server can take offer: no parameter twice,
// none unknown, each value valid, and no server window under 15.
func honorable(offer extension) bool {
	seen := map[string]bool{}
	for _, p := range offer.params {
		name, value := p[0], p[1]
		if seen[name] {
			return false
		}
		seen[name] = true
		switch name {
		case "server_no_context_takeover", "client_no_context_takeover":
			if value != "" {
				return false
			}
		case "server_max_window_bits":
			if n, ok := windowBits(value); !ok || n < 15 {
				return false
			}
		case "client_max_window_bits":
			if _, ok := windowBits(value); value != "" && !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// checkDeflateResponse is nil when a server's Sec-WebSocket-Extensions
// answers the client's DeflateOffer as this package can speak it, and
// why not otherwise.
func checkDeflateResponse(values []string) error {
	chosen, err := parseExtensions(values)
	if err != nil {
		return err
	}
	if len(chosen) != 1 || chosen[0].name != deflateName {
		return fmt.Errorf("the server chose extensions that were not offered: %s", strings.Join(values, ", "))
	}
	seen := map[string]bool{}
	for _, p := range chosen[0].params {
		name, value := p[0], p[1]
		if seen[name] {
			return fmt.Errorf("the server named %s twice", name)
		}
		seen[name] = true
		switch name {
		case "server_no_context_takeover", "client_no_context_takeover":
			if value != "" {
				return fmt.Errorf("the server gave %s a value", name)
			}
		case "server_max_window_bits":
			if _, ok := windowBits(value); !ok {
				return fmt.Errorf("the server's server_max_window_bits %q is not 8 to 15", value)
			}
		default:
			return fmt.Errorf("the server answered permessage-deflate with %s, which was not offered", name)
		}
	}
	if !seen["server_no_context_takeover"] {
		return errors.New("the server took permessage-deflate without server_no_context_takeover, which was offered")
	}
	return nil
}

// tail ends a message's DEFLATE data when it is decompressed: the four
// bytes RFC 7692 section 7.2.2 has the sender strip, then a final empty
// stored block, so the reader ends where the message does.
var tail = []byte{0x00, 0x00, 0xff, 0xff, 0x01, 0x00, 0x00, 0xff, 0xff}

// writers keeps compressors between messages: a compress/flate writer
// holds hundreds of kilobytes, and each message reuses one, reset.
var writers = sync.Pool{New: func() any {
	w, _ := flate.NewWriter(nil, flate.DefaultCompression)
	return w
}}

// deflate is payload compressed as one message (RFC 7692 section 7.2.1):
// raw DEFLATE ended by a sync flush, its last four bytes, 00 00 ff ff,
// left off.
func deflate(payload []byte) []byte {
	var buf bytes.Buffer
	w := writers.Get().(*flate.Writer)
	w.Reset(&buf)
	_, _ = w.Write(payload)
	_ = w.Flush()
	writers.Put(w)
	out := buf.Bytes()
	return out[:len(out)-4]
}

// errInflatedTooBig is a message that decompresses past the limit.
var errInflatedTooBig = errors.New("too big once decompressed")

// inflatedHook, when set, is told how many bytes each inflate produced:
// TestDeflate_ABombIsRefusedAtTheLimit counts them.
var inflatedHook func(n int)

// inflate is a message's compressed payload decompressed, or
// errInflatedTooBig once it passes limit bytes (no limit when limit is
// zero or less), having produced no more than limit and one read's worth
// past it, or an error for data that is not DEFLATE.
func inflate(payload []byte, limit int64) ([]byte, error) {
	r := flate.NewReader(io.MultiReader(bytes.NewReader(payload), bytes.NewReader(tail)))
	defer r.Close()
	var src io.Reader = r
	if limit > 0 {
		src = io.LimitReader(r, limit+1)
	}
	out, err := io.ReadAll(src)
	if inflatedHook != nil {
		inflatedHook(len(out))
	}
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(out)) > limit {
		return nil, errInflatedTooBig
	}
	return out, nil
}

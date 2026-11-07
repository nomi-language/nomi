package websocket

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// HandshakeError is a server refusing the opening handshake, or answering
// it with something that is not a WebSocket upgrade.
type HandshakeError struct {
	// Status is the response's status code.
	Status int
	Reason string
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("websocket: handshake refused with %d: %s", e.Status, e.Reason)
}

// Request is the client's side of the opening handshake.
type Request struct {
	// Host is the Host header, host[:port].
	Host string
	// Target is the request target: the path and query.
	Target string
	// Header is sent too; the handshake's own headers replace any of its.
	Header http.Header
	// Protocols are the subprotocols offered, in order of preference.
	Protocols []string
}

// Client runs the opening handshake over nc as a client and returns the
// connection and the server's response, its body unread. The handshake
// stops when ctx ends.
func Client(ctx context.Context, nc net.Conn, req Request, opts Options) (*Conn, *http.Response, error) {
	stop := context.AfterFunc(ctx, func() { _ = nc.SetDeadline(past) })
	defer stop()
	key := NewKey()
	h := http.Header{}
	for name, values := range req.Header {
		h[http.CanonicalHeaderKey(name)] = values
	}
	for _, name := range []string{"Host", "Upgrade", "Connection", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions"} {
		delete(h, name)
	}
	h.Set("Upgrade", "websocket")
	h.Set("Connection", "Upgrade")
	h.Set("Sec-WebSocket-Key", key)
	h.Set("Sec-WebSocket-Version", "13")
	if len(req.Protocols) > 0 {
		h.Set("Sec-WebSocket-Protocol", strings.Join(req.Protocols, ", "))
	}
	target := req.Target
	if target == "" {
		target = "/"
	}
	var head strings.Builder
	fmt.Fprintf(&head, "GET %s HTTP/1.1\r\nHost: %s\r\n", target, req.Host)
	_ = h.Write(&head)
	head.WriteString("\r\n")
	if _, err := io.WriteString(nc, head.String()); err != nil {
		return nil, nil, ctxErr(ctx, err)
	}
	br := bufio.NewReaderSize(nc, 4096)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, nil, ctxErr(ctx, err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		// The body explains the refusal; read a little of it, briefly.
		_ = nc.SetReadDeadline(time.Now().Add(time.Second))
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		reason := resp.Status
		if text := strings.TrimSpace(string(body)); text != "" && utf8.ValidString(text) {
			reason += ": " + text
		}
		return nil, resp, &HandshakeError{resp.StatusCode, reason}
	}
	refuse := func(why string) (*Conn, *http.Response, error) {
		return nil, resp, &HandshakeError{resp.StatusCode, why}
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		return refuse("the response does not upgrade to websocket")
	}
	if !HasToken(resp.Header.Values("Connection"), "upgrade") {
		return refuse("the response's Connection header lacks upgrade")
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != AcceptKey(key) {
		return refuse("the response's Sec-WebSocket-Accept does not match the key")
	}
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		return refuse("the server chose an extension that was not offered: " + ext)
	}
	if chosen := resp.Header.Get("Sec-WebSocket-Protocol"); chosen != "" {
		offered := false
		for _, p := range req.Protocols {
			offered = offered || p == chosen
		}
		if !offered {
			return refuse("the server chose a subprotocol that was not offered: " + chosen)
		}
	}
	_ = nc.SetDeadline(zeroTime)
	opts.Client = true
	return New(nc, br, opts), resp, nil
}

func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// HasToken reports whether one of a header's values, a comma-separated
// list, holds token, compared without case.
func HasToken(values []string, token string) bool {
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// Tokens is a comma-separated header's values, trimmed, empty ones left
// out.
func Tokens(values []string) []string {
	var out []string
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

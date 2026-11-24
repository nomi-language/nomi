package websocket

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// RFC 7692 section 7.2.3's examples of "Hello": compressed in one frame,
// the same split over two frames, and as a DEFLATE stored block. A server
// that agreed to permessage-deflate receives each as "Hello".
func TestDeflate_TheRFCExamples(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames [][2]any // first byte and payload
	}{
		{"one frame", [][2]any{{byte(0xc1), []byte{0xf2, 0x48, 0xcd, 0xc9, 0xc9, 0x07, 0x00}}}},
		{"two fragments", [][2]any{
			{byte(0x41), []byte{0xf2, 0x48, 0xcd}},
			{byte(0x80), []byte{0xc9, 0xc9, 0x07, 0x00}},
		}},
		{"a stored block", [][2]any{{byte(0xc1), []byte{0x00, 0x05, 0x00, 0xfa, 0xff, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x00}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, raw := serverUnderTest(t, Options{Deflate: true})
			go func() {
				for _, f := range tc.frames {
					raw.send(f[0].(byte), f[1].([]byte))
				}
			}()
			m, ok, err := s.Receive(context.Background())
			if err != nil || !ok || !m.Text || string(m.Data) != "Hello" {
				t.Fatalf("received %q text=%v ok=%v err=%v", m.Data, m.Text, ok, err)
			}
		})
	}
	// compress/flate writes "Hello" as the RFC's stored block, which is
	// longer than "Hello", so Send sends it uncompressed.
	packed := deflate([]byte("Hello"))
	if want := []byte{0x00, 0x05, 0x00, 0xfa, 0xff, 0x48, 0x65, 0x6c, 0x6c, 0x6f, 0x00}; !bytes.Equal(packed, want) {
		t.Fatalf("deflate(Hello) = %x, want the RFC's stored block %x", packed, want)
	}
}

// With permessage-deflate on both ends, messages cross both ways: one that
// compresses goes compressed, its first byte's RSV1 set and its payload
// smaller, and one that does not goes as it is.
func TestDeflate_MessagesCrossCompressedWhenSmaller(t *testing.T) {
	s, c := pair(t, Options{Deflate: true}, Options{Deflate: true})
	long := strings.Repeat("all work and no play ", 500)
	ctx := context.Background()
	for _, m := range []Message{{Text: true, Data: []byte(long)}, {Data: []byte{0xff, 0x00, 0x7f}}, {Text: true, Data: []byte("")}} {
		if err := c.Send(ctx, m.Text, m.Data); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Receive(ctx)
		if err != nil || !ok || got.Text != m.Text || !bytes.Equal(got.Data, m.Data) {
			t.Fatalf("server received %q, %v, %v; sent %q", got.Data, ok, err, m.Data)
		}
		if err := s.Send(ctx, got.Text, got.Data); err != nil {
			t.Fatal(err)
		}
		back, ok, err := c.Receive(ctx)
		if err != nil || !ok || !bytes.Equal(back.Data, m.Data) {
			t.Fatalf("client received %q, %v, %v", back.Data, ok, err)
		}
	}

	server, raw := serverUnderTest(t, Options{Deflate: true})
	go func() {
		_ = server.Send(ctx, true, []byte(long))
		_ = server.Send(ctx, false, []byte{1, 2, 3})
	}()
	head := make([]byte, 1)
	if _, err := raw.br.Read(head); err != nil {
		t.Fatal(err)
	}
	_ = raw.br.UnreadByte()
	big := raw.mustRead()
	if head[0] != 0xc1 || len(big.payload) >= len(long) {
		t.Fatalf("a long text went with first byte %#x and %d bytes for %d", head[0], len(big.payload), len(long))
	}
	if got, err := inflate(big.payload, 0); err != nil || string(got) != long {
		t.Fatalf("the long text inflates to %d bytes, %v", len(got), err)
	}
	if _, err := raw.br.Read(head); err != nil {
		t.Fatal(err)
	}
	_ = raw.br.UnreadByte()
	small := raw.mustRead()
	if head[0] != 0x82 || !bytes.Equal(small.payload, []byte{1, 2, 3}) {
		t.Fatalf("three bytes went with first byte %#x as %x", head[0], small.payload)
	}
}

// A compressed message that expands past the limit closes the connection
// with 1009, and the decompression stops at the limit: of 64 MiB of zeros
// sent as some 64 KiB, it produces at most the 1 MiB limit and one byte.
func TestDeflate_ABombIsRefusedAtTheLimit(t *testing.T) {
	const limit = 1 << 20
	var produced int
	inflatedHook = func(n int) { produced += n }
	t.Cleanup(func() { inflatedHook = nil })

	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	zeros := make([]byte, 1<<20)
	for range 64 {
		_, _ = w.Write(zeros)
	}
	_ = w.Flush()
	bomb := buf.Bytes()[:buf.Len()-4]
	if len(bomb) > limit {
		t.Fatalf("the bomb is %d bytes compressed, past the limit itself", len(bomb))
	}

	s, raw := serverUnderTest(t, Options{Deflate: true, MaxMessage: limit})
	go raw.send(0xc2, bomb)
	_, _, err := s.Receive(context.Background())
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != CloseTooBig || !strings.Contains(pe.Reason, "once decompressed") {
		t.Fatalf("err = %v, want a 1009 for a message too big once decompressed", err)
	}
	expectClose(t, raw, CloseTooBig)
	if produced > limit+1 {
		t.Fatalf("decompressing produced %d bytes for a limit of %d", produced, limit)
	}
}

// RSV1 is a protocol error where it means nothing: with no extension
// agreed, on a continuation frame and on a control frame.
func TestDeflate_RSV1WhereItMeansNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opts   Options
		frames [][2]any
	}{
		{"not agreed", Options{}, [][2]any{{byte(0xc1), []byte{0xf2, 0x48, 0xcd, 0xc9, 0xc9, 0x07, 0x00}}}},
		{"on a continuation", Options{Deflate: true}, [][2]any{
			{byte(0x41), []byte{0xf2, 0x48, 0xcd}},
			{byte(0xc0), []byte{0xc9, 0xc9, 0x07, 0x00}},
		}},
		{"on a ping", Options{Deflate: true}, [][2]any{{byte(0xc9), []byte{}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, raw := serverUnderTest(t, tc.opts)
			go func() {
				for _, f := range tc.frames {
					raw.send(f[0].(byte), f[1].([]byte))
				}
			}()
			_, _, err := s.Receive(context.Background())
			var pe *ProtocolError
			if !errors.As(err, &pe) || pe.Code != CloseProtocolError {
				t.Fatalf("err = %v, want a 1002", err)
			}
		})
	}
}

// What a server answers each offer with, and which responses a client
// takes for its own offer.
func TestDeflate_Negotiation(t *testing.T) {
	for _, tc := range []struct {
		offers []string
		want   string
	}{
		{[]string{"permessage-deflate; client_max_window_bits"}, deflateResponse}, // a browser's
		{[]string{DeflateOffer}, deflateResponse},
		{[]string{"permessage-deflate; server_max_window_bits=10, permessage-deflate"}, deflateResponse},
		{[]string{"permessage-deflate; server_max_window_bits=10"}, ""},
		{[]string{"permessage-deflate; server_max_window_bits=15; client_max_window_bits=12"}, deflateResponse},
		{[]string{"permessage-deflate; mystery"}, ""},
		{[]string{"permessage-deflate; server_no_context_takeover; server_no_context_takeover"}, ""},
		{[]string{"x-webkit-deflate-frame"}, ""},
		{nil, ""},
	} {
		if got := AcceptDeflate(tc.offers); got != tc.want {
			t.Errorf("AcceptDeflate(%q) = %q, want %q", tc.offers, got, tc.want)
		}
	}
	for _, tc := range []struct {
		response string
		ok       bool
	}{
		{deflateResponse, true},
		{"permessage-deflate; server_no_context_takeover", true},
		{"permessage-deflate; server_no_context_takeover; server_max_window_bits=9", true},
		{"permessage-deflate", false},
		{"permessage-deflate; server_no_context_takeover; client_max_window_bits=10", false},
		{"permessage-deflate; server_no_context_takeover; server_max_window_bits=16", false},
		{"permessage-deflate; server_no_context_takeover, permessage-deflate; server_no_context_takeover", false},
		{"x-other", false},
	} {
		if err := checkDeflateResponse([]string{tc.response}); (err == nil) != tc.ok {
			t.Errorf("checkDeflateResponse(%q) = %v, want ok %v", tc.response, err, tc.ok)
		}
	}

	// A client that asks for compression offers it, and a response that
	// takes it turns it on.
	a, b := Pipe()
	seen := fakeServer(t, b, func(r *http.Request) (int, http.Header) {
		h := http.Header{}
		h.Set("Upgrade", "websocket")
		h.Set("Connection", "Upgrade")
		h.Set("Sec-WebSocket-Accept", AcceptKey(r.Header.Get("Sec-WebSocket-Key")))
		h.Set("Sec-WebSocket-Extensions", AcceptDeflate(r.Header.Values("Sec-WebSocket-Extensions")))
		return 101, h
	})
	c, _, err := Client(context.Background(), a, Request{Host: "h", Compress: true}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Abort()
	if r := <-seen; r.Header.Get("Sec-WebSocket-Extensions") != DeflateOffer || !c.opts.Deflate {
		t.Fatalf("offered %q, agreed %v", r.Header.Get("Sec-WebSocket-Extensions"), c.opts.Deflate)
	}
}

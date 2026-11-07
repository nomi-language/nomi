package websocket

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// RFC 6455 section 1.3's example key and its accept value.
func TestAcceptKey_TheRFCExample(t *testing.T) {
	if got := AcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("AcceptKey = %q", got)
	}
	if !ValidKey("dGhlIHNhbXBsZSBub25jZQ==") || ValidKey("c2hvcnQ=") || ValidKey("not base64!") {
		t.Fatal("ValidKey accepts the wrong keys")
	}
	if !ValidKey(NewKey()) {
		t.Fatal("NewKey is not a valid key")
	}
}

// rawPeer writes and reads frames by hand, for the end under test to meet.
type rawPeer struct {
	t      *testing.T
	c      net.Conn
	br     *bufio.Reader
	masked bool
}

func newRaw(t *testing.T, c net.Conn, masked bool) *rawPeer {
	return &rawPeer{t: t, c: c, br: bufio.NewReader(c), masked: masked}
}

// frame builds a frame; b0 is the first byte whole (FIN, RSV, opcode).
func frameBytes(b0 byte, payload []byte, masked bool) []byte {
	out := []byte{b0}
	mbit := byte(0)
	if masked {
		mbit = 0x80
	}
	n := len(payload)
	switch {
	case n <= 125:
		out = append(out, mbit|byte(n))
	case n <= 0xFFFF:
		out = append(out, mbit|126, byte(n>>8), byte(n))
	default:
		out = append(out, mbit|127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	if masked {
		key := [4]byte{0x37, 0xfa, 0x21, 0x3d}
		out = append(out, key[:]...)
		p := append([]byte(nil), payload...)
		mask(key, p)
		out = append(out, p...)
	} else {
		out = append(out, payload...)
	}
	return out
}

func (p *rawPeer) send(b0 byte, payload []byte) {
	p.t.Helper()
	if _, err := p.c.Write(frameBytes(b0, payload, p.masked)); err != nil {
		p.t.Fatalf("raw write: %v", err)
	}
}

type rawFrame struct {
	fin     bool
	op      byte
	masked  bool
	payload []byte
}

func (p *rawPeer) read() (rawFrame, error) {
	_ = p.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var head [2]byte
	if _, err := io.ReadFull(p.br, head[:]); err != nil {
		return rawFrame{}, err
	}
	f := rawFrame{fin: head[0]&0x80 != 0, op: head[0] & 0x0F, masked: head[1]&0x80 != 0}
	n := uint64(head[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(p.br, ext[:]); err != nil {
			return rawFrame{}, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(p.br, ext[:]); err != nil {
			return rawFrame{}, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	var key [4]byte
	if f.masked {
		if _, err := io.ReadFull(p.br, key[:]); err != nil {
			return rawFrame{}, err
		}
	}
	f.payload = make([]byte, n)
	if _, err := io.ReadFull(p.br, f.payload); err != nil {
		return rawFrame{}, err
	}
	if f.masked {
		mask(key, f.payload)
	}
	return f, nil
}

func (p *rawPeer) mustRead() rawFrame {
	p.t.Helper()
	f, err := p.read()
	if err != nil {
		p.t.Fatalf("raw read: %v", err)
	}
	return f
}

func closeCode(f rawFrame) int {
	if len(f.payload) < 2 {
		return CloseNoStatus
	}
	return int(binary.BigEndian.Uint16(f.payload))
}

// serverUnderTest is a server Conn over a pipe, with a raw client peer.
func serverUnderTest(t *testing.T, opts Options) (*Conn, *rawPeer) {
	a, b := Pipe()
	opts.Client = false
	s := New(a, nil, opts)
	t.Cleanup(s.Abort)
	return s, newRaw(t, b, true)
}

func pair(t *testing.T, server, client Options) (*Conn, *Conn) {
	a, b := Pipe()
	server.Client = false
	client.Client = true
	s, c := New(a, nil, server), New(b, nil, client)
	t.Cleanup(func() { s.Abort(); c.Abort() })
	return s, c
}

func TestEcho_TextAndBinaryBothWays(t *testing.T) {
	s, c := pair(t, Options{}, Options{})
	ctx := context.Background()
	if err := c.Send(ctx, true, []byte("héllo")); err != nil {
		t.Fatal(err)
	}
	m, ok, err := s.Receive(ctx)
	if err != nil || !ok || !m.Text || string(m.Data) != "héllo" {
		t.Fatalf("server got %+v %v %v", m, ok, err)
	}
	big := bytes.Repeat([]byte{0, 1, 2, 255}, 40000)
	if err := s.Send(ctx, false, big); err != nil {
		t.Fatal(err)
	}
	m, ok, err = c.Receive(ctx)
	if err != nil || !ok || m.Text || !bytes.Equal(m.Data, big) {
		t.Fatalf("client got %d bytes text=%v %v %v", len(m.Data), m.Text, ok, err)
	}
	if err := c.Send(ctx, true, []byte{0xff}); err == nil {
		t.Fatal("a text message that is not UTF-8 was sent")
	}
}

// A client masks every frame and a server masks none.
func TestMasking_ClientFramesAreMaskedServerFramesAreNot(t *testing.T) {
	a, b := Pipe()
	client := New(a, nil, Options{Client: true})
	server := New(b, nil, Options{})
	defer client.Abort()
	defer server.Abort()
	rawFromClient := newRaw(t, b, false)
	if err := client.Send(context.Background(), true, []byte("masked")); err != nil {
		t.Fatal(err)
	}
	f := rawFromClient.mustRead()
	if !f.masked || string(f.payload) != "masked" {
		t.Fatalf("client frame masked=%v payload=%q", f.masked, f.payload)
	}

	a2, b2 := Pipe()
	srv := New(a2, nil, Options{})
	defer srv.Abort()
	rawFromServer := newRaw(t, b2, false)
	if err := srv.Send(context.Background(), true, []byte("plain")); err != nil {
		t.Fatal(err)
	}
	f = rawFromServer.mustRead()
	if f.masked || string(f.payload) != "plain" {
		t.Fatalf("server frame masked=%v payload=%q", f.masked, f.payload)
	}
}

// A message in fragments, with a ping between them, arrives whole, and the
// ping is answered with a pong carrying its payload.
func TestFragments_JoinedWithControlFramesBetween(t *testing.T) {
	s, raw := serverUnderTest(t, Options{})
	go func() {
		raw.send(opText, []byte("frag"))
		raw.send(0x80|opPing, []byte("are you there"))
		raw.send(opContinuation, []byte("ment"))
		raw.send(0x80|opPong, []byte("unsolicited"))
		raw.send(0x80|opContinuation, []byte("ed é"))
	}()
	m, ok, err := s.Receive(context.Background())
	if err != nil || !ok || string(m.Data) != "fragmented é" || !m.Text {
		t.Fatalf("got %q %v %v", m.Data, ok, err)
	}
	pong := raw.mustRead()
	if pong.op != opPong || string(pong.payload) != "are you there" {
		t.Fatalf("pong = %+v", pong)
	}
}

// A character split across two fragments is valid UTF-8 once joined.
func TestFragments_ACharacterSplitAcrossFragments(t *testing.T) {
	s, raw := serverUnderTest(t, Options{})
	euro := []byte("€")
	go func() {
		raw.send(opText, euro[:1])
		raw.send(0x80|opContinuation, euro[1:])
	}()
	m, ok, err := s.Receive(context.Background())
	if err != nil || !ok || string(m.Data) != "€" {
		t.Fatalf("got %q %v %v", m.Data, ok, err)
	}
}

// expectClose reads frames until a close and checks its code.
func expectClose(t *testing.T, raw *rawPeer, code int) {
	t.Helper()
	for {
		f, err := raw.read()
		if err != nil {
			t.Fatalf("no close frame: %v", err)
		}
		if f.op == opClose {
			if got := closeCode(f); got != code {
				t.Fatalf("closed with %d (%q), want %d", got, f.payload[min(2, len(f.payload)):], code)
			}
			return
		}
	}
}

// Autobahn-style cases: frames a client sends, by hand, and what the
// server must do: echo messages, or close with a code.
func TestAutobahnStyleCases(t *testing.T) {
	type step struct {
		b0      byte
		payload []byte
	}
	text := func(s string) step { return step{0x80 | opText, []byte(s)} }
	cases := []struct {
		name  string
		max   int64
		steps []step
		echo  []string // messages the server receives, in order
		close int      // the code it closes with; 0 for none
		err   int      // the Receive error's code; 0 for a clean end
	}{
		{name: "1.1 empty text", steps: []step{text("")}, echo: []string{""}},
		{name: "1.1 text 125", steps: []step{text(strings.Repeat("*", 125))}, echo: []string{strings.Repeat("*", 125)}},
		{name: "1.1 text 126", steps: []step{text(strings.Repeat("*", 126))}, echo: []string{strings.Repeat("*", 126)}},
		{name: "1.1 text 65536", steps: []step{text(strings.Repeat("*", 65536))}, echo: []string{strings.Repeat("*", 65536)}},
		{name: "2.2 ping with payload", steps: []step{{0x80 | opPing, []byte("hi")}, text("after")}, echo: []string{"after"}},
		{name: "2.5 ping over 125", steps: []step{{0x80 | opPing, bytes.Repeat([]byte{1}, 126)}}, close: 1002, err: 1002},
		{name: "3.1 rsv1", steps: []step{{0x80 | 0x40 | opText, []byte("x")}}, close: 1002, err: 1002},
		{name: "3.2 rsv2 after a good message", steps: []step{text("ok"), {0x80 | 0x20 | opText, []byte("x")}}, echo: []string{"ok"}, close: 1002, err: 1002},
		{name: "4.1 opcode 3", steps: []step{{0x80 | 3, nil}}, close: 1002, err: 1002},
		{name: "4.2 opcode 11", steps: []step{{0x80 | 11, nil}}, close: 1002, err: 1002},
		{name: "5.1 fragmented ping", steps: []step{{opPing, []byte("a")}, {0x80 | opContinuation, []byte("b")}}, close: 1002, err: 1002},
		{name: "5.3 fragments", steps: []step{{opText, []byte("a")}, {opContinuation, []byte("b")}, {0x80 | opContinuation, []byte("c")}}, echo: []string{"abc"}},
		{name: "5.9 continuation first", steps: []step{{0x80 | opContinuation, []byte("x")}}, close: 1002, err: 1002},
		{name: "5.18 text inside a fragmented text", steps: []step{{opText, []byte("a")}, text("b")}, close: 1002, err: 1002},
		{name: "6.3 invalid UTF-8", steps: []step{text("\xce\xba\xe1\xbd\xb9\xcf\x83\xce\xbc\xce\xb5\xed\xa0\x80edited")}, close: 1007, err: 1007},
		{name: "6.4 invalid UTF-8 across fragments", steps: []step{{opText, []byte("\xce")}, {0x80 | opContinuation, []byte("x")}}, close: 1007, err: 1007},
		{name: "6.x surrogate", steps: []step{text("\xed\xa0\x80")}, close: 1007, err: 1007},
		{name: "7.1 close 1000", steps: []step{{0x80 | opClose, []byte{0x03, 0xe8}}}, close: 1000},
		{name: "7.3 close with no code", steps: []step{{0x80 | opClose, nil}}, close: 1005},
		{name: "7.3 close of one byte", steps: []step{{0x80 | opClose, []byte{0x03}}}, close: 1002, err: 1002},
		{name: "7.5 close reason not UTF-8", steps: []step{{0x80 | opClose, []byte{0x03, 0xe8, 0xff}}}, close: 1007, err: 1007},
		{name: "7.9 close code 999", steps: []step{{0x80 | opClose, []byte{0x03, 0xe7}}}, close: 1002, err: 1002},
		{name: "7.9 close code 1004", steps: []step{{0x80 | opClose, []byte{0x03, 0xec}}}, close: 1002, err: 1002},
		{name: "7.9 close code 1005", steps: []step{{0x80 | opClose, []byte{0x03, 0xed}}}, close: 1002, err: 1002},
		{name: "7.9 close code 2999", steps: []step{{0x80 | opClose, []byte{0x0b, 0xb7}}}, close: 1002, err: 1002},
		{name: "7.7 close code 4000", steps: []step{{0x80 | opClose, []byte{0x0f, 0xa0, 'o', 'k'}}}, close: 4000, err: 4000},
		{name: "9 message over the limit", max: 10, steps: []step{text(strings.Repeat("x", 11))}, close: 1009, err: 1009},
		{name: "9 fragments over the limit", max: 10, steps: []step{{opText, []byte("123456")}, {0x80 | opContinuation, []byte("789012")}}, close: 1009, err: 1009},
		{name: "9 exactly the limit", max: 10, steps: []step{text("0123456789")}, echo: []string{"0123456789"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, raw := serverUnderTest(t, Options{MaxMessage: tc.max})
			go func() {
				for _, st := range tc.steps {
					if _, err := raw.c.Write(frameBytes(st.b0, st.payload, true)); err != nil {
						return
					}
				}
			}()
			ctx := context.Background()
			for _, want := range tc.echo {
				m, ok, err := s.Receive(ctx)
				if err != nil || !ok || string(m.Data) != want {
					t.Fatalf("received %q %v %v, want %q", m.Data, ok, err, want)
				}
			}
			if tc.close == 0 {
				return
			}
			_, ok, err := s.Receive(ctx)
			if ok {
				t.Fatal("received a message, want the connection to end")
			}
			var code int
			var pe *ProtocolError
			var ce *CloseError
			switch {
			case errors.As(err, &pe):
				code = pe.Code
			case errors.As(err, &ce):
				code = ce.Code
			case err != nil:
				t.Fatalf("error %v", err)
			}
			if code != tc.err {
				t.Fatalf("Receive ended with code %d (%v), want %d", code, err, tc.err)
			}
			expectClose(t, raw, tc.close)
			if _, err := raw.read(); err == nil {
				t.Fatal("the server sent more after its close frame")
			}
		})
	}
}

// Close sends a close frame, waits for the peer's, and both ends report a
// clean end.
func TestClose_TheClosingHandshake(t *testing.T) {
	s, c := pair(t, Options{}, Options{})
	ctx := context.Background()
	got := make(chan error, 1)
	go func() {
		_, ok, err := s.Receive(ctx)
		if ok {
			err = errors.New("a message arrived")
		}
		got <- err
	}()
	if err := c.Close(CloseNormal, "bye"); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != nil {
		t.Fatalf("server Receive = %v, want a clean end", err)
	}
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("the client did not close after the server's close frame")
	}
	if _, ok, err := c.Receive(ctx); ok || err != nil {
		t.Fatalf("a Receive after a clean close = %v %v", ok, err)
	}
	if err := c.Send(ctx, true, []byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatalf("a Send after close = %v", err)
	}
	if err := c.Close(CloseNormal, ""); err != nil {
		t.Fatalf("a second Close = %v", err)
	}
	if err := c.Close(1005, ""); err == nil {
		t.Fatal("Close sent 1005")
	}
}

// Close while another goroutine is receiving: the receiver sees the end.
func TestClose_WhileAnotherIsReceiving(t *testing.T) {
	s, c := pair(t, Options{}, Options{})
	ctx := context.Background()
	got := make(chan error, 2)
	go func() {
		_, ok, err := s.Receive(ctx)
		if ok {
			err = errors.New("a message arrived")
		}
		got <- err
	}()
	go func() {
		_, ok, err := c.Receive(ctx)
		if ok {
			err = errors.New("a message arrived")
		}
		got <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := c.Close(CloseGoingAway, "leaving"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-got; err != nil {
			t.Fatalf("Receive = %v, want a clean end", err)
		}
	}
}

// The peer closing the connection with no close frame is 1006.
func TestAbnormalEnd(t *testing.T) {
	s, raw := serverUnderTest(t, Options{})
	_ = raw.c.Close()
	_, ok, err := s.Receive(context.Background())
	var ce *CloseError
	if ok || !errors.As(err, &ce) || ce.Code != CloseAbnormal {
		t.Fatalf("Receive = %v %v, want 1006", ok, err)
	}
}

// Many goroutines sending at once each get their message through whole.
func TestSend_ConcurrentSendersAreSerialized(t *testing.T) {
	s, c := pair(t, Options{}, Options{})
	ctx := context.Background()
	const senders, each = 8, 50
	var wg sync.WaitGroup
	for i := range senders {
		wg.Go(func() {
			for j := range each {
				msg := fmt.Sprintf("%d-%d-%s", i, j, strings.Repeat("x", 200+i*37))
				if err := s.Send(ctx, true, []byte(msg)); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	seen := map[string]bool{}
	for range senders * each {
		m, ok, err := c.Receive(ctx)
		if err != nil || !ok {
			t.Fatalf("Receive = %v %v", ok, err)
		}
		seen[string(m.Data)] = true
	}
	wg.Wait()
	if len(seen) != senders*each {
		t.Fatalf("%d distinct messages, want %d", len(seen), senders*each)
	}
}

func TestIdleTimeout(t *testing.T) {
	s, raw := serverUnderTest(t, Options{IdleTimeout: 30 * time.Millisecond})
	_, ok, err := s.Receive(context.Background())
	if ok || !errors.Is(err, ErrIdle) {
		t.Fatalf("Receive = %v %v, want ErrIdle", ok, err)
	}
	expectClose(t, raw, CloseGoingAway)
}

// With a ping interval the server pings, the client's pongs keep an idle
// timeout from firing, and the client answers without being asked.
func TestPingInterval_PongsKeepTheConnectionAlive(t *testing.T) {
	s, c := pair(t, Options{PingInterval: 10 * time.Millisecond, IdleTimeout: 60 * time.Millisecond}, Options{})
	ctx := context.Background()
	go func() {
		// The client reads, so it answers the pings.
		_, _, _ = c.Receive(ctx)
	}()
	got := make(chan error, 1)
	go func() {
		_, _, err := s.Receive(ctx)
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("the server's Receive ended at %v while pongs came", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := c.Send(ctx, true, []byte("still here")); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != nil {
		t.Fatalf("Receive = %v", err)
	}
}

func TestReceive_StopsWhenTheContextEnds(t *testing.T) {
	s, _ := serverUnderTest(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan error, 1)
	go func() {
		_, _, err := s.Receive(ctx)
		got <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-got:
		var ce *CloseError
		if !errors.As(err, &ce) || ce.Code != CloseAbnormal {
			t.Fatalf("Receive = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not stop")
	}
}

// fakeServer reads a handshake request from c and answers it with
// respond's status and headers.
func fakeServer(t *testing.T, c net.Conn, respond func(r *http.Request) (int, http.Header)) <-chan *http.Request {
	seen := make(chan *http.Request, 1)
	go func() {
		br := bufio.NewReader(c)
		r, err := http.ReadRequest(br)
		if err != nil {
			t.Error(err)
			return
		}
		seen <- r
		status, h := respond(r)
		var head strings.Builder
		fmt.Fprintf(&head, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
		_ = h.Write(&head)
		head.WriteString("\r\n")
		if status != 101 {
			head.WriteString("go away")
		}
		_, _ = io.WriteString(c, head.String())
		if status != 101 {
			_ = c.Close()
		}
		if status == 101 {
			// One frame right behind the response, which the client must
			// not lose to its handshake reader.
			_, _ = c.Write(frameBytes(0x80|opText, []byte("welcome"), false))
		}
	}()
	return seen
}

func TestClient_Handshake(t *testing.T) {
	a, b := Pipe()
	seen := fakeServer(t, b, func(r *http.Request) (int, http.Header) {
		h := http.Header{}
		h.Set("Upgrade", "websocket")
		h.Set("Connection", "Upgrade")
		h.Set("Sec-WebSocket-Accept", AcceptKey(r.Header.Get("Sec-WebSocket-Key")))
		h.Set("Sec-WebSocket-Protocol", "chat")
		return 101, h
	})
	extra := http.Header{"Authorization": {"Bearer t"}, "Upgrade": {"not this"}}
	c, resp, err := Client(context.Background(), a, Request{Host: "example.com", Target: "/ws?x=1", Header: extra, Protocols: []string{"v2", "chat"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Abort()
	r := <-seen
	if r.Host != "example.com" || r.URL.String() != "/ws?x=1" || r.Header.Get("Upgrade") != "websocket" ||
		r.Header.Get("Sec-WebSocket-Version") != "13" || r.Header.Get("Authorization") != "Bearer t" ||
		r.Header.Get("Sec-WebSocket-Protocol") != "v2, chat" || !ValidKey(r.Header.Get("Sec-WebSocket-Key")) {
		t.Fatalf("handshake request: %s %v", r.URL, r.Header)
	}
	if resp.Header.Get("Sec-WebSocket-Protocol") != "chat" {
		t.Fatalf("response %v", resp.Header)
	}
	m, ok, err := c.Receive(context.Background())
	if err != nil || !ok || string(m.Data) != "welcome" {
		t.Fatalf("first message %q %v %v", m.Data, ok, err)
	}
}

func TestClient_HandshakeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		respond func(r *http.Request) (int, http.Header)
		want    string
	}{
		{"forbidden", func(*http.Request) (int, http.Header) { return 403, http.Header{} }, "403 Forbidden: go away"},
		{"wrong accept", func(*http.Request) (int, http.Header) {
			return 101, http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"}, "Sec-Websocket-Accept": {"nope"}}
		}, "does not match"},
		{"unoffered protocol", func(r *http.Request) (int, http.Header) {
			return 101, http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"},
				"Sec-Websocket-Accept": {AcceptKey(r.Header.Get("Sec-WebSocket-Key"))}, "Sec-Websocket-Protocol": {"other"}}
		}, "not offered"},
		{"extension", func(r *http.Request) (int, http.Header) {
			return 101, http.Header{"Upgrade": {"websocket"}, "Connection": {"Upgrade"},
				"Sec-Websocket-Accept": {AcceptKey(r.Header.Get("Sec-WebSocket-Key"))}, "Sec-Websocket-Extensions": {"permessage-deflate"}}
		}, "extension"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := Pipe()
			fakeServer(t, b, tc.respond)
			_, _, err := Client(context.Background(), a, Request{Host: "h"}, Options{})
			var he *HandshakeError
			if !errors.As(err, &he) || !strings.Contains(he.Reason, tc.want) {
				t.Fatalf("err = %v, want a HandshakeError with %q", err, tc.want)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	if !HasToken([]string{"keep-alive, Upgrade"}, "upgrade") || HasToken([]string{"keep-alive"}, "upgrade") {
		t.Fatal("HasToken")
	}
	if got := Tokens([]string{"a, b", " ,c"}); strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("Tokens = %v", got)
	}
}

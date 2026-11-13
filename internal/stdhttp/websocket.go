package stdhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/websocket"
	"github.com/nomi-language/nomi/rt"
)

// std/websocket's Go side: the protocol in internal/websocket over a
// std/net connection, held as a Handle. A connection's blocking calls stop
// when the calling task's context ends. When the program starts draining,
// each open connection starts closing with 1001, so the task receiving on
// it sees the end and returns. An Err is a websocket.Error variant's name
// and fields, or "Network" and a net.Error's parts.

type wsConn struct {
	c        *websocket.Conn
	id       int64
	protocol string
}

var wsIDs atomic.Int64

func wsOf(h rt.HTTPHandle) *wsConn {
	w, ok := h.Value().(*wsConn)
	if !ok {
		rt.Trap("websocket: a Handle holds no WebSocket")
	}
	return w
}

func wsOptions(client bool, max, ping, idle int64) websocket.Options {
	return websocket.Options{Client: client, MaxMessage: max, PingInterval: dur(ping), IdleTimeout: dur(idle)}
}

func wsHandle(c *websocket.Conn, protocol string) rt.HTTPHandle {
	unregister := rt.OnDrain(c.GoingAway)
	c.OnClosed(func() { unregister() })
	return rt.NewHTTPHandle(&wsConn{c: c, id: wsIDs.Add(1), protocol: protocol})
}

// WsAcceptKey is `std::http::host.ws_accept_key`: Sec-WebSocket-Accept for a key.
func WsAcceptKey(key string) string { return websocket.AcceptKey(key) }

// WsValidKey is `std::http::host.ws_valid_key`.
func WsValidKey(key string) bool { return websocket.ValidKey(key) }

// WsOpen is `std::http::host.ws_open`: a WebSocket over a connection whose
// handshake is done. Deadlines a server left on a hijacked connection are
// cleared. The connection is the WebSocket's from here on.
func WsOpen(conn rt.NetConn, client bool, max, ping, idle int64) rt.HTTPHandle {
	x := connOf(conn)
	_ = x.c.SetDeadline(time.Time{})
	return wsHandle(websocket.New(x.c, nil, wsOptions(client, max, ping, idle)), "")
}

// WsConnect is `std::http::host.ws_connect`: the client's opening handshake over
// conn, its request target and headers given, offering protocols.
func WsConnect(fr *rt.Frame, conn rt.NetConn, host, target string, headers, protocols *rt.List[string], max, ping, idle int64) rt.Result[rt.HTTPHandle, *rt.List[string]] {
	x := connOf(conn)
	h := http.Header{}
	for l := headers; l != nil && l.Tail != nil; l = l.Tail.Tail {
		h.Add(l.Head, l.Tail.Head)
	}
	req := websocket.Request{Host: host, Target: target, Header: h, Protocols: strsOf(protocols)}
	c, resp, err := websocket.Client(frameContext(fr), x.c, req, wsOptions(true, max, ping, idle))
	if err != nil {
		_ = x.c.Close()
		return rt.Err[rt.HTTPHandle](listOf(wsErrParts(err, host)))
	}
	return rt.Ok[rt.HTTPHandle, *rt.List[string]](wsHandle(c, resp.Header.Get("Sec-WebSocket-Protocol")))
}

// WsProtocol is `std::http::host.ws_protocol`: the subprotocol the server chose
// in a client's handshake, "" for none.
func WsProtocol(h rt.HTTPHandle) string { return wsOf(h).protocol }

// WsID is `std::http::host.ws_id`: a number no other WebSocket of the process has.
func WsID(h rt.HTTPHandle) int64 { return wsOf(h).id }

// WsReceive is `std::http::host.ws_receive`: the next message, its first byte 1
// for text and 2 for binary before the payload, or None after a clean
// close.
func WsReceive(fr *rt.Frame, h rt.HTTPHandle) rt.Result[rt.Maybe[rt.Bytes], *rt.List[string]] {
	m, ok, err := wsOf(h).c.Receive(frameContext(fr))
	if err != nil {
		return rt.Err[rt.Maybe[rt.Bytes]](listOf(wsErrParts(err, "")))
	}
	if !ok {
		return rt.Ok[rt.Maybe[rt.Bytes], *rt.List[string]](rt.None[rt.Bytes]())
	}
	kind := "\x02"
	if m.Text {
		kind = "\x01"
	}
	return rt.Ok[rt.Maybe[rt.Bytes], *rt.List[string]](rt.Some(rt.Bytes(kind + string(m.Data))))
}

// WsSend is `std::http::host.ws_send`.
func WsSend(fr *rt.Frame, h rt.HTTPHandle, text bool, data rt.Bytes) rt.Result[rt.Unit, *rt.List[string]] {
	return wsDone(wsOf(h).c.Send(frameContext(fr), text, []byte(data)))
}

// WsPing is `std::http::host.ws_ping`.
func WsPing(fr *rt.Frame, h rt.HTTPHandle, data rt.Bytes) rt.Result[rt.Unit, *rt.List[string]] {
	return wsDone(wsOf(h).c.Ping(frameContext(fr), []byte(data)))
}

// WsClose is `std::http::host.ws_close`: the closing handshake with code and
// reason.
func WsClose(h rt.HTTPHandle, code int64, reason string) rt.Result[rt.Unit, *rt.List[string]] {
	return wsDone(wsOf(h).c.Close(int(code), reason))
}

// WsPipe is `std::http::host.ws_pipe`: two buffered connections in memory, for
// testing a handler with no socket.
func WsPipe() *rt.List[rt.NetConn] {
	a, b := websocket.Pipe()
	return rt.Cons(newConn(context.Background(), a), rt.Cons(newConn(context.Background(), b), (*rt.List[rt.NetConn])(nil)))
}

func wsDone(err error) rt.Result[rt.Unit, *rt.List[string]] {
	if err != nil {
		return rt.Err[rt.Unit](listOf(wsErrParts(err, "")))
	}
	return rt.Ok[rt.Unit, *rt.List[string]](rt.Unit{})
}

// wsErrParts is err as a websocket.Error variant's parts.
func wsErrParts(err error, address string) []string {
	var he *websocket.HandshakeError
	var pe *websocket.ProtocolError
	var ce *websocket.CloseError
	switch {
	case errors.As(err, &he):
		return []string{"Handshake", strconv.Itoa(he.Status), he.Reason}
	case errors.As(err, &pe):
		return []string{"Protocol", strconv.Itoa(pe.Code), pe.Reason}
	case errors.As(err, &ce):
		return []string{"Closed", strconv.Itoa(ce.Code), ce.Reason}
	case errors.Is(err, websocket.ErrClosed):
		return []string{"Closed", "1006", "the connection is closed"}
	case errors.Is(err, websocket.ErrIdle):
		return []string{"Timeout"}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return []string{"Closed", "1006", "the task was stopped"}
	}
	var netErr0 interface{ Timeout() bool }
	if address != "" || errors.As(err, &netErr0) {
		return append([]string{"Network"}, strsOf(netErr("websocket", address, err))...)
	}
	return []string{"Invalid", err.Error()}
}

package rt

import "context"

// The values std/http reaches outside the machine, as slots rt carries and
// never opens, as processes.go does for std/process: internal/stdhttp holds
// Go's client and the responses it answers. `http_testing.run` installs a
// fake for the dynamic extent of one call (EnterNetwork): the function that
// answers each request in place of the network, and the requests it
// answered. Tasks spawned inside inherit it with the rest of the frame.

// HTTPClient is std/http_host's `Client`: a client internal/stdhttp made.
type HTTPClient struct{ v any }

// NewHTTPClient wraps v as a value.
func NewHTTPClient(v any) HTTPClient { return HTTPClient{v: v} }

// Value is what the value wraps.
func (c HTTPClient) Value() any { return c.v }

// HTTPExchange is std/http_host's `Exchange`: a response a client answered.
type HTTPExchange struct{ v any }

// NewHTTPExchange wraps v as a value.
func NewHTTPExchange(v any) HTTPExchange { return HTTPExchange{v: v} }

// Value is what the value wraps.
func (e HTTPExchange) Value() any { return e.v }

// NetworkFake is what `http_testing.run` installs: the Nomi function that
// answers each request, and the requests it answered, in order.
type NetworkFake struct {
	Answer   any
	Answered *[]any
}

// EnterNetwork returns the frame a call runs on with fake answering its
// std/http requests: a child of parent that carries it, as every frame and
// task derived from it does.
func EnterNetwork(parent *Frame, fake NetworkFake) *Frame {
	child := *parent
	child.network = &fake
	return &child
}

// NetworkOf is the fake the frame's std/http requests reach (the innermost
// EnterNetwork), or nil for the network.
func NetworkOf(fr *Frame) *NetworkFake {
	if fr == nil {
		return nil
	}
	f, _ := fr.network.(*NetworkFake)
	return f
}

// HTTPListener is std/http_host's `Listener`: a server internal/stdhttp
// started, listening and handing its requests to the program.
type HTTPListener struct{ v any }

// NewHTTPListener wraps v as a value.
func NewHTTPListener(v any) HTTPListener { return HTTPListener{v: v} }

// Value is what the value wraps.
func (l HTTPListener) Value() any { return l.v }

// HTTPJar is std/http_host's `Jar`: a cookie jar.
type HTTPJar struct{ v any }

// NewHTTPJar wraps v as a value.
func NewHTTPJar(v any) HTTPJar { return HTTPJar{v: v} }

// Value is what the value wraps.
func (j HTTPJar) Value() any { return j.v }

// HTTPHandle is std/http_host's `Handle`: one of the Go values std/http
// keeps for a program, such as a TLS ClientHello, a request tracer or a
// streaming multipart reader, behind a std struct of its own.
type HTTPHandle struct{ v any }

// NewHTTPHandle wraps v as a value.
func NewHTTPHandle(v any) HTTPHandle { return HTTPHandle{v: v} }

// Value is what the value wraps.
func (h HTTPHandle) Value() any { return h.v }

// HTTPMux is std/http_host's `Mux`: the Go ServeMux a Router matches with.
type HTTPMux struct{ v any }

// NewHTTPMux wraps v as a value.
func NewHTTPMux(v any) HTTPMux { return HTTPMux{v: v} }

// Value is what the value wraps.
func (m HTTPMux) Value() any { return m.v }

// HTTPIncoming is std/http_host's `Incoming`: one request a server received,
// waiting for the program's response. Done is the request's own context,
// which ends when the client goes away.
type HTTPIncoming struct {
	v    any
	done context.Context
}

// NewHTTPIncoming wraps v, a request whose context is done.
func NewHTTPIncoming(v any, done context.Context) HTTPIncoming {
	return HTTPIncoming{v: v, done: done}
}

// Value is what the value wraps.
func (in HTTPIncoming) Value() any { return in.v }

// Done is the request's context.
func (in HTTPIncoming) Done() context.Context { return in.done }

// EnterRequest returns the frame a request's handler runs on: a child of
// parent under the scoped execution context c (the app's Context field reads
// it, as after a `with` of that field), floored by the deadline in force,
// and stopped when done ends as when the task is cancelled. release ends it.
func EnterRequest(parent *Frame, done context.Context, c Context) (*Frame, func()) {
	next := ContextWithFloor(ActiveContext(parent), c)
	fr := EnterContext(parent, next)
	fr, releaseDeadline := EnterDeadline(fr, next)
	ctx, cancel := context.WithCancel(fr.ctx)
	stop := context.AfterFunc(done, cancel)
	child := *fr
	child.ctx = ctx
	child.stop = stopFlag(ctx)
	return &child, func() {
		stop()
		cancel()
		releaseDeadline()
	}
}

// TLSCertificate is std/http_host's `Certificate`: a certificate chain and
// its private key, which internal/stdhttp holds and never hands back.
type TLSCertificate struct{ v any }

// NewTLSCertificate wraps v as a value.
func NewTLSCertificate(v any) TLSCertificate { return TLSCertificate{v: v} }

// Value is what the value wraps.
func (c TLSCertificate) Value() any { return c.v }

// TLSConfig is std/http_host's `TlsConfig`: the Go TLS configuration
// std/tls builds from a ClientConfig or ServerConfig for one use.
type TLSConfig struct{ v any }

// NewTLSConfig wraps v as a value.
func NewTLSConfig(v any) TLSConfig { return TLSConfig{v: v} }

// Value is what the value wraps.
func (c TLSConfig) Value() any { return c.v }

// NetConn is std/http_host's `Conn`: a network connection, TCP or TLS over
// TCP, that std/net reads and writes as a Reader and a Writer.
type NetConn struct{ v any }

// NewNetConn wraps v as a value.
func NewNetConn(v any) NetConn { return NetConn{v: v} }

// Value is what the value wraps.
func (c NetConn) Value() any { return c.v }

// NetListener is std/http_host's `TcpListener`: a listening socket whose
// connections std/net accepts.
type NetListener struct{ v any }

// NewNetListener wraps v as a value.
func NewNetListener(v any) NetListener { return NetListener{v: v} }

// Value is what the value wraps.
func (l NetListener) Value() any { return l.v }

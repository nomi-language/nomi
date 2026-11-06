package stdhttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/internal/osreach"
	"github.com/nomi-language/nomi/rt"
)

// The server side of std/http. Go's http.Server accepts connections and
// calls a handler on a goroutine of its own per request; that goroutine
// hands the request to the program (Next) and waits until the program has
// answered it (Finish). The program runs each handler as a task of its own
// (std/http's `serve`), so no Nomi code runs on Go's goroutines, and the
// task's frame ends when the client goes away (rt.EnterRequest).

// listener is what std/http_host's `Listener` wraps.
type listener struct {
	srv      *http.Server
	ln       net.Listener
	incoming chan *incoming
	closed   chan struct{}
	once     sync.Once
	// b runs the server's hooks on the task waiting in Next.
	b *broker
	// open is each connection the server holds, for CloseConnections.
	mu   sync.Mutex
	open map[net.Conn]struct{}
	// unhook removes the drain hook Listen registered.
	unhook func()
}

func listenerOf(l rt.HTTPListener) *listener {
	ln, ok := l.Value().(*listener)
	if !ok {
		rt.Trap("http: a Listener holds no server")
	}
	return ln
}

// incoming is what std/http_host's `Incoming` wraps: one request and the
// writer its response goes to. Every write holds mu, and once the Go handler
// has returned (gone) a write fails rather than touching a finished
// ResponseWriter.
type incoming struct {
	w    http.ResponseWriter
	r    *http.Request
	done chan struct{}

	mu       sync.Mutex
	wrote    bool
	finished bool
	gone     bool
}

func incomingOf(in rt.HTTPIncoming) *incoming {
	i, ok := in.Value().(*incoming)
	if !ok {
		rt.Trap("http: an Incoming holds no request")
	}
	return i
}

func (l *listener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	in := &incoming{w: w, r: r, done: make(chan struct{})}
	select {
	case l.incoming <- in:
	case <-l.closed:
		http.Error(w, "the server is shutting down", http.StatusServiceUnavailable)
		return
	case <-r.Context().Done():
		return
	}
	select {
	case <-in.done:
	case <-l.closed:
		// Shutdown's budget ran out with this request unanswered: return,
		// and refuse the program's later writes.
	}
	in.mu.Lock()
	in.gone = true
	in.mu.Unlock()
}

// Hooks is a server's or transport's hooks into the program, each run on
// the task serving its broker; a nil one is not set.
type Hooks struct {
	OnState      func(*rt.Frame, string, int64) rt.Unit
	OnError      func(*rt.Frame, string) rt.Unit
	OnShutdown   func(*rt.Frame) rt.Unit
	OnCountError func(*rt.Frame, string) rt.Unit
}

// Listen is `http_host.listen`: bind address and start serving on it.
// durations are the read, read-header, write, idle and shutdown timeouts
// in nanoseconds, 0 for Go's default (none); limits are the most header
// bytes and the most values one header may hold, 0 for Go's defaults.
// flags are keep-alives, answering `OPTIONS *` itself, and honouring
// client priority. protocols is HTTP/1, HTTP/2 and unencrypted HTTP/2,
// used when hasProtocols; http2 and http2Flags are an Http2Options' parts
// (http2Config). An Err is an http.Error variant's name and fields.
//
// When the program starts draining its supervisors, as at SIGTERM, the
// server shuts down as Shutdown does with the shutdown timeout: it stops
// accepting at once, and the requests it took finish inside the
// supervisors' grace period (rt.OnDrain).
func Listen(address string, durations *rt.List[int64], limits *rt.List[int64], flags *rt.List[bool],
	protocols *rt.List[bool], hasProtocols bool, http2 *rt.List[int64], http2Flags *rt.List[bool],
	onState func(*rt.Frame, string, int64) rt.Unit, hasOnState bool,
	onError func(*rt.Frame, string) rt.Unit, hasOnError bool,
	onShutdown func(*rt.Frame) rt.Unit, hasOnShutdown bool,
	onCountError func(*rt.Frame, string) rt.Unit, hasOnCountError bool) rt.Result[rt.HTTPListener, *rt.List[string]] {
	return listen(address, durations, limits, flags, protocols, hasProtocols, http2, http2Flags,
		hooks(onState, hasOnState, onError, hasOnError, onShutdown, hasOnShutdown, onCountError, hasOnCountError), nil)
}

// ListenTLS is `http_host.listen_tls`: Listen, serving HTTPS with cfg, as
// Go's ListenAndServeTLS does (HTTP/2 included when cfg allows it).
func ListenTLS(address string, durations *rt.List[int64], limits *rt.List[int64], flags *rt.List[bool],
	protocols *rt.List[bool], hasProtocols bool, http2 *rt.List[int64], http2Flags *rt.List[bool],
	onState func(*rt.Frame, string, int64) rt.Unit, hasOnState bool,
	onError func(*rt.Frame, string) rt.Unit, hasOnError bool,
	onShutdown func(*rt.Frame) rt.Unit, hasOnShutdown bool,
	onCountError func(*rt.Frame, string) rt.Unit, hasOnCountError bool,
	cfg rt.TLSConfig) rt.Result[rt.HTTPListener, *rt.List[string]] {
	return listen(address, durations, limits, flags, protocols, hasProtocols, http2, http2Flags,
		hooks(onState, hasOnState, onError, hasOnError, onShutdown, hasOnShutdown, onCountError, hasOnCountError), confOf(cfg))
}

func hooks(onState func(*rt.Frame, string, int64) rt.Unit, hasOnState bool,
	onError func(*rt.Frame, string) rt.Unit, hasOnError bool,
	onShutdown func(*rt.Frame) rt.Unit, hasOnShutdown bool,
	onCountError func(*rt.Frame, string) rt.Unit, hasOnCountError bool) Hooks {
	var h Hooks
	if hasOnState {
		h.OnState = onState
	}
	if hasOnError {
		h.OnError = onError
	}
	if hasOnShutdown {
		h.OnShutdown = onShutdown
	}
	if hasOnCountError {
		h.OnCountError = onCountError
	}
	return h
}

func ints(l *rt.List[int64]) []int64 {
	var out []int64
	for ; l != nil; l = l.Tail {
		out = append(out, l.Head)
	}
	return out
}

func bools(l *rt.List[bool]) []bool {
	var out []bool
	for ; l != nil; l = l.Tail {
		out = append(out, l.Head)
	}
	return out
}

// at is xs[i], or zero past its end.
func at[T any](xs []T, i int) T {
	var zero T
	if i < len(xs) {
		return xs[i]
	}
	return zero
}

// protocolsOf is Go's Protocols for HTTP/1, HTTP/2 and unencrypted HTTP/2.
func protocolsOf(flags *rt.List[bool]) *http.Protocols {
	fs := bools(flags)
	p := &http.Protocols{}
	p.SetHTTP1(at(fs, 0))
	p.SetHTTP2(at(fs, 1))
	p.SetUnencryptedHTTP2(at(fs, 2))
	return p
}

// http2Config is Go's HTTP2Config for an Http2Options' parts: the most
// concurrent streams, decoder and encoder header table sizes, read frame
// size, receive buffers per connection and per stream, then the send-ping,
// ping and write-byte timeouts in nanoseconds (0 for Go's default each);
// flags are strict_max_concurrent_requests and
// permit_prohibited_cipher_suites. countError, when set, runs through b.
func http2Config(nums *rt.List[int64], flags *rt.List[bool], countError func(*rt.Frame, string) rt.Unit, b *broker) *http.HTTP2Config {
	ns, fs := ints(nums), bools(flags)
	c := &http.HTTP2Config{
		MaxConcurrentStreams:          int(at(ns, 0)),
		MaxDecoderHeaderTableSize:     int(at(ns, 1)),
		MaxEncoderHeaderTableSize:     int(at(ns, 2)),
		MaxReadFrameSize:              int(at(ns, 3)),
		MaxReceiveBufferPerConnection: int(at(ns, 4)),
		MaxReceiveBufferPerStream:     int(at(ns, 5)),
		SendPingTimeout:               dur(at(ns, 6)),
		PingTimeout:                   dur(at(ns, 7)),
		WriteByteTimeout:              dur(at(ns, 8)),
		StrictMaxConcurrentRequests:   at(fs, 0),
		PermitProhibitedCipherSuites:  at(fs, 1),
	}
	if countError != nil {
		c.CountError = func(kind string) { b.post(func(fr *rt.Frame) { countError(fr, kind) }) }
	}
	return c
}

// lineWriter hands each line Go's log writes to onError through b.
type lineWriter struct {
	b       *broker
	onError func(*rt.Frame, string) rt.Unit
}

func (w lineWriter) Write(p []byte) (int, error) {
	line := strings.TrimSuffix(string(p), "\n")
	w.b.post(func(fr *rt.Frame) { w.onError(fr, line) })
	return len(p), nil
}

func listen(address string, durations, limits *rt.List[int64], flags *rt.List[bool], protocols *rt.List[bool],
	hasProtocols bool, h2 *rt.List[int64], h2Flags *rt.List[bool], h Hooks, cfg *tlsConf) rt.Result[rt.HTTPListener, *rt.List[string]] {
	if !hasSockets {
		return rt.Err[rt.HTTPListener](listOf([]string{"Listen", address, noSocketsMessage}))
	}
	osreach.Note("listen")
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return rt.Err[rt.HTTPListener](listOf([]string{"Listen", address, err.Error()}))
	}
	ds, ls, fs := ints(durations), ints(limits), bools(flags)
	l := &listener{ln: ln, incoming: make(chan *incoming), closed: make(chan struct{}), b: newBroker()}
	l.srv = &http.Server{
		Handler:                      l,
		ReadTimeout:                  dur(at(ds, 0)),
		ReadHeaderTimeout:            dur(at(ds, 1)),
		WriteTimeout:                 dur(at(ds, 2)),
		IdleTimeout:                  dur(at(ds, 3)),
		MaxHeaderBytes:               int(at(ls, 0)),
		MaxHeaderValueCount:          int(at(ls, 1)),
		DisableGeneralOptionsHandler: !at(fs, 1),
		DisableClientPriority:        !at(fs, 2),
		HTTP2:                        http2Config(h2, h2Flags, h.OnCountError, l.b),
	}
	if hasProtocols {
		l.srv.Protocols = protocolsOf(protocols)
	}
	l.srv.SetKeepAlivesEnabled(at(fs, 0))
	l.open = map[net.Conn]struct{}{}
	l.srv.ConnState = func(c net.Conn, s http.ConnState) {
		l.mu.Lock()
		switch s {
		case http.StateNew:
			l.open[c] = struct{}{}
		case http.StateHijacked, http.StateClosed:
			delete(l.open, c)
		}
		l.mu.Unlock()
		if h.OnState != nil {
			remote := c.RemoteAddr().String()
			l.b.post(func(fr *rt.Frame) { h.OnState(fr, remote, int64(s)) })
		}
	}
	if h.OnError != nil {
		l.srv.ErrorLog = log.New(lineWriter{b: l.b, onError: h.OnError}, "", 0)
	}
	if h.OnShutdown != nil {
		l.srv.RegisterOnShutdown(func() { l.b.post(func(fr *rt.Frame) { h.OnShutdown(fr) }) })
	}
	shutdownTimeout := at(ds, 4)
	if cfg != nil {
		cfg.b = l.b
		l.srv.TLSConfig = cfg.c
		go func() { _ = l.srv.ServeTLS(ln, "", "") }()
	} else {
		go func() { _ = l.srv.Serve(ln) }()
	}
	l.unhook = rt.OnDrain(func() { go l.shutdown(shutdownTimeout) })
	return rt.Ok[rt.HTTPListener, *rt.List[string]](rt.NewHTTPListener(l))
}

// Address is `http_host.address`: the address the server is bound to, its
// port chosen when the address asked for port 0.
func Address(l rt.HTTPListener) string { return listenerOf(l).ln.Addr().String() }

// Next is `http_host.next`: the next request, or None once the server has
// shut down or the calling task is stopped.
func Next(fr *rt.Frame, l rt.HTTPListener) rt.Maybe[rt.HTTPIncoming] {
	ln := listenerOf(l)
	ctx := context.Background()
	if fr != nil && fr.Context() != nil {
		ctx = fr.Context()
	}
	for {
		select {
		case in := <-ln.incoming:
			return rt.Some(rt.NewHTTPIncoming(in, in.r.Context()))
		case <-ln.b.ready:
			ln.b.run(fr)
			continue
		case <-ln.closed:
		case <-ctx.Done():
		}
		// The hooks Go called on the way down (on_shutdown, the last
		// connection states) still run.
		ln.b.run(fr)
		return rt.None[rt.HTTPIncoming]()
	}
}

// Shutdown is `http_host.shutdown`: Go's Server.Shutdown. The server stops
// accepting and waits up to timeout (nanoseconds) for the requests it is
// answering; then it closes every connection, and requests still
// unanswered are abandoned. A second call does nothing.
func Shutdown(l rt.HTTPListener, timeout int64) rt.Unit {
	listenerOf(l).shutdown(timeout)
	return rt.Unit{}
}

func (ln *listener) shutdown(timeout int64) {
	if !hasSockets {
		return // no listener exists here
	}
	ln.once.Do(func() {
		if ln.unhook != nil {
			ln.unhook()
		}
		ctx, cancel := context.WithTimeout(context.Background(), dur(timeout))
		defer cancel()
		if err := ln.srv.Shutdown(ctx); err != nil {
			_ = ln.srv.Close()
		}
		close(ln.closed)
	})
}

// The parts of a received request.

func InMethod(in rt.HTTPIncoming) string { return incomingOf(in).r.Method }
func InURL(in rt.HTTPIncoming) string    { return incomingOf(in).r.URL.String() }
func InHost(in rt.HTTPIncoming) string   { return incomingOf(in).r.Host }
func InRemote(in rt.HTTPIncoming) string { return incomingOf(in).r.RemoteAddr }
func InTarget(in rt.HTTPIncoming) string { return incomingOf(in).r.RequestURI }
func InProtocol(in rt.HTTPIncoming) *rt.List[int64] {
	r := incomingOf(in).r
	return rt.Cons(int64(r.ProtoMajor), rt.Cons(int64(r.ProtoMinor), (*rt.List[int64])(nil)))
}
func InHeaders(in rt.HTTPIncoming) *rt.List[string]  { return headerList(incomingOf(in).r.Header) }
func InTrailers(in rt.HTTPIncoming) *rt.List[string] { return headerList(incomingOf(in).r.Trailer) }
func InContentLength(in rt.HTTPIncoming) int64       { return incomingOf(in).r.ContentLength }
func InTransferEncoding(in rt.HTTPIncoming) *rt.List[string] {
	return listOf(incomingOf(in).r.TransferEncoding)
}
func InClose(in rt.HTTPIncoming) bool { return incomingOf(in).r.Close }

// InBody is `http_host.in_body`: the request's body as a Reader.
func InBody(in rt.HTTPIncoming) rt.Reader {
	r := incomingOf(in).r
	return rt.NewReader(rt.ReadSource{Name: "<request>", R: r.Body, Close: r.Body.Close})
}

// writeHead sets the response's headers and announced trailers and writes
// its status. It holds in.mu.
func (in *incoming) writeHead(status int64, headers, trailerNames *rt.List[string]) bool {
	if in.gone || in.wrote {
		return false
	}
	h := in.w.Header()
	for l := headers; l != nil && l.Tail != nil; l = l.Tail.Tail {
		h.Add(l.Head, l.Tail.Head)
	}
	for l := trailerNames; l != nil; l = l.Tail {
		h.Add("Trailer", l.Head)
	}
	in.wrote = true
	in.w.WriteHeader(int(status))
	return true
}

func (in *incoming) setTrailers(trailers *rt.List[string]) {
	h := in.w.Header()
	for l := trailers; l != nil && l.Tail != nil; l = l.Tail.Tail {
		h.Set(l.Head, l.Tail.Head)
	}
}

func trailerNames(trailers *rt.List[string]) *rt.List[string] {
	var names []string
	for l := trailers; l != nil && l.Tail != nil; l = l.Tail.Tail {
		names = append(names, l.Head)
	}
	return listOf(names)
}

// Respond is `http_host.respond`: write the whole response. kind is 0 for no
// body, 1 for data, 2 for input; a negative contentLength is unknown. An Err
// is an http.Error variant's name and fields.
func Respond(fr *rt.Frame, i rt.HTTPIncoming, status int64, headers, trailers *rt.List[string], kind int64,
	data rt.Bytes, input rt.Reader, contentLength, writeTimeout int64, fullDuplex bool) rt.Result[rt.Unit, *rt.List[string]] {
	in := incomingOf(i)
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.gone {
		return rt.Err[rt.Unit](listOf([]string{"Canceled"}))
	}
	if in.wrote {
		return rt.Err[rt.Unit](listOf([]string{"Protocol", "the response was already written"}))
	}
	in.control(writeTimeout, fullDuplex)
	if contentLength >= 0 {
		in.w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	} else if kind == bodyBytes {
		in.w.Header().Set("Content-Length", strconv.FormatInt(int64(len(data)), 10))
	}
	in.writeHead(status, headers, trailerNames(trailers))
	var err error
	switch kind {
	case bodyBytes:
		_, err = in.w.Write([]byte(data))
	case bodyStream:
		_, err = io.Copy(in.w, readerInput{fr: fr, r: input})
	}
	in.setTrailers(trailers)
	if err != nil {
		return rt.Err[rt.Unit](listOf([]string{"Protocol", err.Error()}))
	}
	return rt.Ok[rt.Unit, *rt.List[string]](rt.Unit{})
}

// Produce is `http_host.produce`: write the status and headers, announcing
// the trailers named, and answer a Writer over the body. Each time the
// writer passes bytes on (when its buffer fills, or at Writer.flush), they
// are sent to the client at once.
func Produce(i rt.HTTPIncoming, status int64, headers, trailerNames *rt.List[string], writeTimeout int64, fullDuplex bool) rt.Writer {
	in := incomingOf(i)
	in.mu.Lock()
	if !in.gone && !in.wrote {
		in.control(writeTimeout, fullDuplex)
	}
	in.writeHead(status, headers, trailerNames)
	in.mu.Unlock()
	return rt.NewWriter(rt.WriteSink{Name: "<response>", W: responseWriter{in: in}})
}

type responseWriter struct{ in *incoming }

func (w responseWriter) Write(p []byte) (int, error) {
	in := w.in
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.gone || in.finished {
		return 0, errors.New("the response is finished")
	}
	n, err := in.w.Write(p)
	if err == nil {
		err = http.NewResponseController(in.w).Flush()
	}
	return n, err
}

// Finish is `http_host.finish`: the program has answered. It sets trailers,
// and a request answered with nothing written gets a 500. A second call does
// nothing.
func Finish(i rt.HTTPIncoming, trailers *rt.List[string]) rt.Unit {
	in := incomingOf(i)
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.finished {
		return rt.Unit{}
	}
	in.finished = true
	if !in.gone {
		if !in.wrote {
			in.wrote = true
			http.Error(in.w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		in.setTrailers(trailers)
	}
	close(in.done)
	return rt.Unit{}
}

// Answered is `http_host.answered_already`: whether Finish has run, so a
// handler task a supervisor restarts does not answer twice.
func Answered(i rt.HTTPIncoming) bool {
	in := incomingOf(i)
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.finished
}

// DetectContentType is `http_host.detect_content_type`: Go's
// DetectContentType over at most the first 512 bytes.
func DetectContentType(data rt.Bytes) string { return http.DetectContentType([]byte(data)) }

// mux is what std/http_host's `Mux` wraps: Go's ServeMux over a router's
// patterns, each with a handler that records which pattern matched.
type mux struct {
	m        *http.ServeMux
	patterns []string
}

func muxOf(m rt.HTTPMux) *mux {
	x, ok := m.Value().(*mux)
	if !ok {
		rt.Trap("http: a Mux holds no router")
	}
	return x
}

// matched is what a mux's recording handler writes into the matchRecorder.
type matched struct {
	index  int
	values []string
}

type matchRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	match  *matched
}

func (r *matchRecorder) Header() http.Header         { return r.header }
func (r *matchRecorder) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *matchRecorder) WriteHeader(status int)      { r.status = status }

var wildcard = regexp.MustCompile(`\{([^}.$]+)(\.\.\.)?\}`)

// NewMux is `http_host.new_mux`: Go's ServeMux with patterns registered in
// order. A pattern Go refuses, or one that conflicts with an earlier one, is
// an Err of ["Pattern", pattern, reason], where Go panics.
func NewMux(patterns *rt.List[string]) (res rt.Result[rt.HTTPMux, *rt.List[string]]) {
	x := &mux{m: http.NewServeMux()}
	for l := patterns; l != nil; l = l.Tail {
		pattern, index := l.Head, len(x.patterns)
		var names []string
		for _, m := range wildcard.FindAllStringSubmatch(pattern, -1) {
			names = append(names, m[1])
		}
		failed := func() (reason string) {
			defer func() {
				if r := recover(); r != nil {
					reason = fmt.Sprint(r)
				}
			}()
			x.m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				m := &matched{index: index}
				for _, n := range names {
					m.values = append(m.values, n, r.PathValue(n))
				}
				w.(*matchRecorder).match = m
			})
			return ""
		}()
		if failed != "" {
			return rt.Err[rt.HTTPMux](listOf([]string{"Pattern", pattern, patternReason(pattern, failed)}))
		}
		x.patterns = append(x.patterns, pattern)
	}
	return rt.Ok[rt.HTTPMux, *rt.List[string]](rt.NewHTTPMux(x))
}

// CheckPattern is `http_host.check_pattern`: why Go's ServeMux refuses
// pattern, or "" when it takes it. It registers the pattern in a mux of its
// own, so it reads and changes nothing else, and a typed literal may call it.
func CheckPattern(pattern string) (reason string) {
	defer func() {
		if r := recover(); r != nil {
			reason = patternReason(pattern, fmt.Sprint(r))
		}
	}()
	http.NewServeMux().HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	return ""
}

// patternReason is Go's panic message for pattern without what the
// error around it already says: the `http: ` package prefix and the
// `parsing "<pattern>": ` that names the pattern again.
func patternReason(pattern, panicked string) string {
	reason := strings.TrimPrefix(panicked, "http: ")
	return strings.TrimPrefix(reason, fmt.Sprintf("parsing %q: ", pattern))
}

// Route is `http_host.route`: match a request against the mux as its
// ServeHTTP does. A match answers ["match", index, pattern, name, value,
// ...]; anything else the mux answers itself (a redirect to the cleaned or
// slashed path, a 405 with Allow, a 404) is ["answer", status, body, name,
// value, ...] with its headers.
func Route(m rt.HTTPMux, method, host, target string) *rt.List[string] {
	x := muxOf(m)
	u, err := url.Parse(target)
	if err != nil {
		return listOf([]string{"answer", "400", "invalid request URL\n"})
	}
	req := &http.Request{Method: method, Host: host, URL: u, RequestURI: u.RequestURI(), Header: http.Header{},
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
	if req.Host == "" {
		req.Host = u.Host
	}
	rec := &matchRecorder{header: http.Header{}, status: http.StatusOK}
	x.m.ServeHTTP(rec, req)
	if rec.match != nil {
		out := []string{"match", strconv.Itoa(rec.match.index), x.patterns[rec.match.index]}
		return listOf(append(out, rec.match.values...))
	}
	out := []string{"answer", strconv.Itoa(rec.status), rec.body.String()}
	for name, values := range rec.header {
		for _, v := range values {
			out = append(out, name, v)
		}
	}
	return listOf(out)
}

// synthetic is a request Go's helpers can answer: the method, target and
// headers of a Nomi Request.
func synthetic(method, target string, headers *rt.List[string]) (*http.Request, bool) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, false
	}
	req := &http.Request{Method: method, URL: u, Host: u.Host, RequestURI: u.RequestURI(), Header: http.Header{},
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
	for l := headers; l != nil && l.Tail != nil; l = l.Tail.Tail {
		req.Header.Add(l.Head, l.Tail.Head)
	}
	return req, true
}

// answered is what a Go helper wrote: ["answer", status, body, name, value, ...].
func answered(rec *matchRecorder) *rt.List[string] {
	out := []string{"answer", strconv.Itoa(rec.status), rec.body.String()}
	for name, values := range rec.header {
		for _, v := range values {
			out = append(out, name, v)
		}
	}
	return listOf(out)
}

func badTarget() *rt.List[string] { return listOf([]string{"answer", "400", "invalid request URL\n"}) }

// AnswerContent is `http_host.answer_content`: Go's ServeContent for data
// named name, modified at modified nanoseconds (negative for unknown),
// answering the ranges and conditional headers of the request. set is
// the response's headers before ServeContent runs, name and value
// alternating: an ETag there is what If-None-Match and If-Range are
// checked against, and a Content-Type there is kept.
func AnswerContent(method, target string, headers *rt.List[string], name string, modified int64, set *rt.List[string], data rt.Bytes) *rt.List[string] {
	req, ok := synthetic(method, target, headers)
	if !ok {
		return badTarget()
	}
	var mod time.Time
	if modified >= 0 {
		mod = time.Unix(0, modified)
	}
	rec := &matchRecorder{header: http.Header{}, status: http.StatusOK}
	for l := set; l != nil && l.Tail != nil; l = l.Tail.Tail {
		rec.header.Add(l.Head, l.Tail.Head)
	}
	http.ServeContent(rec, req, name, mod, bytes.NewReader([]byte(data)))
	return answered(rec)
}

// AnswerRedirect is `http_host.answer_redirect`: Go's Redirect to to, which
// may be relative to the request's path.
func AnswerRedirect(method, target string, headers *rt.List[string], to string, status int64) *rt.List[string] {
	req, ok := synthetic(method, target, headers)
	if !ok {
		return badTarget()
	}
	rec := &matchRecorder{header: http.Header{}, status: http.StatusOK}
	http.Redirect(rec, req, to, int(status))
	return answered(rec)
}

// FormatTime is `http_host.format_time`: Go's TimeFormat in UTC.
func FormatTime(nanos int64) string { return time.Unix(0, nanos).UTC().Format(http.TimeFormat) }

// ParseTime is `http_host.parse_time`: Go's ParseTime, in nanoseconds.
func ParseTime(text string) rt.Result[int64, string] {
	t, err := http.ParseTime(text)
	if err != nil {
		return rt.Err[int64](err.Error())
	}
	return rt.Ok[int64, string](t.UnixNano())
}

// LimitBody is `http_host.limit_body`: a reader over r that fails once more
// than max bytes are read, as Go's MaxBytesReader does.
func LimitBody(fr *rt.Frame, r rt.Reader, max int64) rt.Reader {
	return rt.NewReader(rt.ReadSource{Name: "<request>", R: &limited{r: readerInput{fr: fr, r: r}, left: max}})
}

type limited struct {
	r    io.Reader
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, errors.New("http: request body too large")
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n + int(l.left), errors.New("http: request body too large")
	}
	return n, err
}

// InLocal is `http_host.in_local`: the server address the request came in
// on, Go's LocalAddrContextKey.
func InLocal(in rt.HTTPIncoming) string {
	if a, ok := incomingOf(in).r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		return a.String()
	}
	return ""
}

// InTLS is `http_host.in_tls`: the TLS state of the connection the request
// came in on, as stateParts lists it; None over plain HTTP.
func InTLS(in rt.HTTPIncoming) rt.Maybe[*rt.List[*rt.List[string]]] {
	if s := incomingOf(in).r.TLS; s != nil {
		return rt.Some(stateParts(*s))
	}
	return rt.None[*rt.List[*rt.List[string]]]()
}

// ExtendReadDeadline is `http_host.extend_read_deadline`: Go's
// ResponseController.SetReadDeadline, by nanoseconds from now (0 for
// none). An Err is the reason Go gives, as when the protocol cannot.
func ExtendReadDeadline(i rt.HTTPIncoming, by int64) rt.Result[rt.Unit, string] {
	in := incomingOf(i)
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.gone {
		return rt.Err[rt.Unit]("the request is finished")
	}
	var at time.Time
	if by > 0 {
		at = time.Now().Add(dur(by))
	}
	if err := http.NewResponseController(in.w).SetReadDeadline(at); err != nil {
		return rt.Err[rt.Unit](err.Error())
	}
	return rt.Ok[rt.Unit, string](rt.Unit{})
}

// control applies a response's write timeout (nanoseconds, 0 for none) and
// full duplex before its head is written. It holds in.mu.
func (in *incoming) control(writeTimeout int64, fullDuplex bool) {
	rc := http.NewResponseController(in.w)
	if writeTimeout > 0 {
		_ = rc.SetWriteDeadline(time.Now().Add(dur(writeTimeout)))
	}
	if fullDuplex {
		_ = rc.EnableFullDuplex()
	}
}

// Upgrade is `http_host.upgrade`: answer with status and headers, then
// take the connection from the server, as Go's Hijack does, for the
// handler to speak another protocol over. What the client sent after the
// request is read first. An Err is an http.Error variant's name and fields.
func Upgrade(i rt.HTTPIncoming, status int64, headers *rt.List[string]) rt.Result[rt.NetConn, *rt.List[string]] {
	in := incomingOf(i)
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.gone || in.wrote {
		return rt.Err[rt.NetConn](listOf([]string{"Protocol", "the response was already written"}))
	}
	c, brw, err := http.NewResponseController(in.w).Hijack()
	if err != nil {
		return rt.Err[rt.NetConn](listOf([]string{"Protocol", err.Error()}))
	}
	in.wrote = true
	head := &bytes.Buffer{}
	fmt.Fprintf(head, "HTTP/1.1 %03d %s\r\n", status, http.StatusText(int(status)))
	h := http.Header{}
	for l := headers; l != nil && l.Tail != nil; l = l.Tail.Tail {
		h.Add(l.Head, l.Tail.Head)
	}
	_ = h.Write(head)
	head.WriteString("\r\n")
	if _, err := c.Write(head.Bytes()); err != nil {
		_ = c.Close()
		return rt.Err[rt.NetConn](listOf([]string{"Protocol", err.Error()}))
	}
	return rt.Ok[rt.NetConn, *rt.List[string]](newConn(context.Background(), bufferedConn{Conn: c, r: brw.Reader}))
}

// bufferedConn is a hijacked connection whose reads take what Go's server
// had already read first.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// CloseConnections is `http_host.close_connections`: close every
// connection the server holds, idle or busy, as httptest's
// CloseClientConnections does. The server goes on accepting.
func CloseConnections(l rt.HTTPListener) rt.Unit {
	ln := listenerOf(l)
	ln.mu.Lock()
	open := make([]net.Conn, 0, len(ln.open))
	for c := range ln.open {
		open = append(open, c)
	}
	ln.mu.Unlock()
	for _, c := range open {
		_ = c.Close()
	}
	return rt.Unit{}
}

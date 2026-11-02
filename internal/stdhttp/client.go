package stdhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// client is what std/http_host's `Client` wraps: Go's client, which owns
// the connection pool, made once from the options std/http's ClientOptions
// gave.
type client struct {
	c *http.Client
	// b runs the transport's hooks, and its TLS configuration's, on the
	// task waiting in Send.
	b *broker
}

func clientOf(c rt.HTTPClient) *client {
	cl, ok := c.Value().(*client)
	if !ok {
		rt.Trap("http: a Client holds no client")
	}
	return cl
}

func dur(ns int64) time.Duration { return time.Duration(ns) }

// NewClient is `http_host.new_client`. A duration or count of 0 is Go's
// default (none); redirects is -1 for never following one, else the most
// it follows. proxy is 0 for the environment's, 1 for none, 2 for proxyURL.
//
// protocols is HTTP/1, HTTP/2 and unencrypted HTTP/2, used when
// hasProtocols; http2 and http2Flags are an Http2Options' parts
// (http2Config); proxyHeaders alternate name and value, sent with each
// CONNECT to a proxy. onProxyConnect, when set, sees each CONNECT's answer
// (the proxy URL, status and headers) and answers "" to go on or why not.
func NewClient(timeout, redirects, proxy int64, proxyURL string, tlsHandshakeTimeout int64,
	keepAlives, compression bool, maxIdle, maxIdlePerHost, maxPerHost, idleTimeout,
	responseHeaderTimeout, expectContinueTimeout, maxResponseHeaderBytes, writeBufferSize,
	readBufferSize int64, forceHTTP2 bool, protocols *rt.List[bool], hasProtocols bool,
	http2 *rt.List[int64], http2Flags *rt.List[bool], proxyHeaders *rt.List[string],
	onProxyConnect func(*rt.Frame, string, int64, *rt.List[string]) string, hasOnProxyConnect bool,
	onCountError func(*rt.Frame, string) rt.Unit, hasOnCountError bool) rt.HTTPClient {
	b := newBroker()
	tr := &http.Transport{
		DialContext:            (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:    dur(tlsHandshakeTimeout),
		DisableKeepAlives:      !keepAlives,
		DisableCompression:     !compression,
		MaxIdleConns:           int(maxIdle),
		MaxIdleConnsPerHost:    int(maxIdlePerHost),
		MaxConnsPerHost:        int(maxPerHost),
		IdleConnTimeout:        dur(idleTimeout),
		ResponseHeaderTimeout:  dur(responseHeaderTimeout),
		ExpectContinueTimeout:  dur(expectContinueTimeout),
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		WriteBufferSize:        int(writeBufferSize),
		ReadBufferSize:         int(readBufferSize),
		ForceAttemptHTTP2:      forceHTTP2,
	}
	if hasProtocols {
		tr.Protocols = protocolsOf(protocols)
	}
	var countError func(*rt.Frame, string) rt.Unit
	if hasOnCountError {
		countError = onCountError
	}
	tr.HTTP2 = http2Config(http2, http2Flags, countError, b)
	if proxyHeaders != nil {
		tr.ProxyConnectHeader = http.Header{}
		for l := proxyHeaders; l != nil && l.Tail != nil; l = l.Tail.Tail {
			tr.ProxyConnectHeader.Add(l.Head, l.Tail.Head)
		}
	}
	if hasOnProxyConnect {
		tr.OnProxyConnectResponse = func(ctx context.Context, proxyURL *url.URL, _ *http.Request, res *http.Response) error {
			reason := ""
			if !b.call(ctx.Done(), func(fr *rt.Frame) {
				reason = onProxyConnect(fr, proxyURL.String(), int64(res.StatusCode), headerList(res.Header))
			}) {
				return errors.New("http: no task ran on_proxy_connect")
			}
			if reason != "" {
				return errors.New(reason)
			}
			return nil
		}
	}
	switch proxy {
	case 0:
		tr.Proxy = http.ProxyFromEnvironment
	case 2:
		u, err := url.Parse(proxyURL)
		if err != nil {
			rt.Trap("http: the proxy URL is invalid: " + err.Error())
		}
		tr.Proxy = http.ProxyURL(u)
	}
	browserTransport(tr)
	c := &http.Client{Transport: tr, Timeout: dur(timeout)}
	switch {
	case redirects < 0:
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	default:
		max := int(redirects)
		c.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
			if len(via) > max {
				return tooManyRedirects{count: len(via)}
			}
			return nil
		}
	}
	return rt.NewHTTPClient(&client{c: c, b: b})
}

var defaultClient = sync.OnceValue(func() rt.HTTPClient {
	return NewClient(0, 10, 0, "", int64(10*time.Second), true, true, 100, 2, 0, int64(90*time.Second), 0, int64(time.Second), 0, 0, 0, true,
		nil, false, nil, nil, nil, nil, false, nil, false)
})

// DefaultClient is `http_host.default_client`: one client for the process,
// with std/http's default options, as Go's DefaultClient is one.
func DefaultClient() rt.HTTPClient { return defaultClient() }

type tooManyRedirects struct{ count int }

func (e tooManyRedirects) Error() string { return fmt.Sprintf("stopped after %d redirects", e.count) }

// CloseIdle is `http_host.close_idle`.
func CloseIdle(c rt.HTTPClient) rt.Unit {
	clientOf(c).c.CloseIdleConnections()
	return rt.Unit{}
}

// exchange is what std/http_host's `Exchange` wraps: a response Go answered,
// its body read whole (fetch) or open (with_response).
type exchange struct {
	resp *http.Response
	body []byte
	read bool
}

func exchangeOf(e rt.HTTPExchange) *exchange {
	ex, ok := e.Value().(*exchange)
	if !ok {
		rt.Trap("http: an Exchange holds no response")
	}
	return ex
}

// The body kinds std/http's Body crosses as.
const (
	bodyEmpty = iota
	bodyBytes
	bodyStream
)

// Send is `http_host.send`: one request on c under the frame's context. An
// Err is the http.Error's variant name and its fields (errorParts). With
// stream false the body is read whole, refused past maxBody bytes when
// maxBody is not negative; with stream true it stays open for the reader.
func Send(fr *rt.Frame, c rt.HTTPClient, method, target string, headers *rt.List[string], bodyKind int64,
	data rt.Bytes, input rt.Reader, contentLength int64, host string, closes bool, trailers *rt.List[string],
	stream bool, maxBody int64, tracer rt.HTTPHandle, traced bool) rt.Result[rt.HTTPExchange, *rt.List[string]] {
	fail := func(parts ...string) rt.Result[rt.HTTPExchange, *rt.List[string]] {
		return rt.Err[rt.HTTPExchange](listOf(parts))
	}
	ctx := context.Background()
	if fr != nil && fr.Context() != nil {
		ctx = fr.Context()
	}
	if traced {
		ctx = httptrace.WithClientTrace(ctx, tracerOf(tracer).trace())
	}
	var body io.Reader
	switch bodyKind {
	case bodyBytes:
		body = bytes.NewReader([]byte(data))
	case bodyStream:
		body = readerInput{fr: fr, r: input}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fail("InvalidUrl", target, ue.Err.Error())
		}
		return fail("InvalidRequest", err.Error())
	}
	if bodyKind == bodyBytes {
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader([]byte(data))), nil }
	}
	for l := headers; l != nil && l.Tail != nil; l = l.Tail.Tail {
		req.Header.Add(l.Head, l.Tail.Head)
	}
	if contentLength >= 0 {
		req.ContentLength = contentLength
	} else if bodyKind == bodyStream {
		req.ContentLength = -1
	}
	if host != "" {
		req.Host = host
	}
	req.Close = closes
	if trailers != nil {
		req.Trailer = http.Header{}
		for l := trailers; l != nil && l.Tail != nil; l = l.Tail.Tail {
			req.Trailer.Add(l.Head, l.Tail.Head)
		}
	}
	cl := clientOf(c)
	var resp *http.Response
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err = cl.c.Do(req)
	}()
	if cl.b != nil {
		cl.b.wait(fr, done)
	} else {
		<-done
	}
	if err != nil {
		return fail(errorParts(ctx, req, err)...)
	}
	ex := &exchange{resp: resp}
	if !stream {
		defer resp.Body.Close()
		r := io.Reader(resp.Body)
		if maxBody >= 0 {
			r = io.LimitReader(resp.Body, maxBody+1)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return fail(errorParts(ctx, req, err)...)
		}
		if maxBody >= 0 && int64(len(b)) > maxBody {
			return fail("BodyTooLarge", fmt.Sprint(maxBody))
		}
		ex.body, ex.read = b, true
	}
	return rt.Ok[rt.HTTPExchange, *rt.List[string]](rt.NewHTTPExchange(ex))
}

// errorParts names the http.Error variant a failed request is, and its
// fields as text, in declaration order.
func errorParts(ctx context.Context, req *http.Request, err error) []string {
	var redirects tooManyRedirects
	var dnsErr *net.DNSError
	var opErr *net.OpError
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	var netErr net.Error
	switch {
	case errors.As(err, &redirects):
		return []string{"TooManyRedirects", fmt.Sprint(redirects.count)}
	case errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled:
		return []string{"Canceled"}
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
		return []string{"Timeout", "request"}
	case errors.As(err, &netErr) && netErr.Timeout():
		return []string{"Timeout", "request"}
	case errors.As(err, &dnsErr):
		return []string{"Dns", dnsErr.Name, dnsErr.Err}
	case errors.As(err, &certErr), errors.As(err, &recordErr):
		return []string{"Tls", unwrapURL(err).Error()}
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return []string{"Connect", req.URL.Host, unwrapURL(err).Error()}
	case errors.Is(err, http.ErrSchemeMismatch):
		return []string{"Protocol", unwrapURL(err).Error()}
	}
	var ue *url.Error
	if errors.As(err, &ue) && strings.Contains(ue.Err.Error(), "unsupported protocol scheme") {
		return []string{"InvalidUrl", req.URL.String(), ue.Err.Error()}
	}
	return []string{"Protocol", unwrapURL(err).Error()}
}

func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func listOf(xs []string) *rt.List[string] {
	var out *rt.List[string]
	for i := len(xs) - 1; i >= 0; i-- {
		out = rt.Cons(xs[i], out)
	}
	return out
}

func headerList(h http.Header) *rt.List[string] {
	var flat []string
	for k, vs := range h {
		for _, v := range vs {
			flat = append(flat, k, v)
		}
	}
	return listOf(sortedPairs(flat))
}

// sortedPairs orders header pairs by name, keeping each name's values in
// order: Go's Header is a map, so its order is not the wire's.
func sortedPairs(flat []string) []string {
	type pair struct{ k, v string }
	var ps []pair
	for i := 0; i+1 < len(flat); i += 2 {
		ps = append(ps, pair{flat[i], flat[i+1]})
	}
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j-1].k > ps[j].k; j-- {
			ps[j-1], ps[j] = ps[j], ps[j-1]
		}
	}
	out := make([]string, 0, len(flat))
	for _, p := range ps {
		out = append(out, p.k, p.v)
	}
	return out
}

// Status is `http_host.status`.
func Status(e rt.HTTPExchange) int64 { return int64(exchangeOf(e).resp.StatusCode) }

// Protocol is `http_host.protocol`: [major, minor].
func Protocol(e rt.HTTPExchange) *rt.List[int64] {
	r := exchangeOf(e).resp
	return rt.Cons(int64(r.ProtoMajor), rt.Cons(int64(r.ProtoMinor), (*rt.List[int64])(nil)))
}

// Headers is `http_host.headers`: name and value, alternating, by name.
func Headers(e rt.HTTPExchange) *rt.List[string] { return headerList(exchangeOf(e).resp.Header) }

// Trailers is `http_host.trailers`, once the body is read.
func Trailers(e rt.HTTPExchange) *rt.List[string] { return headerList(exchangeOf(e).resp.Trailer) }

// BodyBytes is `http_host.body_bytes`: the body fetch read.
func BodyBytes(e rt.HTTPExchange) rt.Bytes { return rt.Bytes(exchangeOf(e).body) }

// BodyReader is `http_host.body_reader`: the open body as a Reader.
func BodyReader(e rt.HTTPExchange) rt.Reader {
	ex := exchangeOf(e)
	if ex.read {
		return rt.NewReader(rt.ReadSource{Name: "<response>", R: bytes.NewReader(ex.body)})
	}
	return rt.NewReader(rt.ReadSource{Name: "<response>", R: ex.resp.Body, Close: ex.resp.Body.Close})
}

// CloseBody is `http_host.close_body`.
func CloseBody(e rt.HTTPExchange) rt.Unit {
	_ = exchangeOf(e).resp.Body.Close()
	return rt.Unit{}
}

// ContentLength is `http_host.content_length`: -1 when unknown.
func ContentLength(e rt.HTTPExchange) int64 { return exchangeOf(e).resp.ContentLength }

// TransferEncoding is `http_host.transfer_encoding`.
func TransferEncoding(e rt.HTTPExchange) *rt.List[string] {
	return listOf(exchangeOf(e).resp.TransferEncoding)
}

// Closes is `http_host.closes`: the server asked to close the connection.
func Closes(e rt.HTTPExchange) bool { return exchangeOf(e).resp.Close }

// Uncompressed is `http_host.uncompressed`.
func Uncompressed(e rt.HTTPExchange) bool { return exchangeOf(e).resp.Uncompressed }

// FinalURL is `http_host.final_url`: the URL the last request of any
// redirects went to.
func FinalURL(e rt.HTTPExchange) string {
	if r := exchangeOf(e).resp.Request; r != nil && r.URL != nil {
		return r.URL.String()
	}
	return ""
}

// CanonicalKey is `http_host.canonical_key`: Go's CanonicalHeaderKey.
func CanonicalKey(name string) string { return http.CanonicalHeaderKey(name) }

// StatusText is `http_host.status_text`: Go's StatusText, "" for a code it
// does not name.
func StatusText(code int64) string { return http.StatusText(int(code)) }

// readerInput is a Reader as an io.Reader, for a request body.
type readerInput struct {
	fr *rt.Frame
	r  rt.Reader
}

func (in readerInput) Read(p []byte) (n int, err error) {
	defer func() {
		if recover() != nil {
			n, err = 0, errors.New("http: the request body's read was cancelled")
		}
	}()
	chunk := rt.ReaderRead(in.fr, in.r, int64(len(p)))
	if chunk.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: chunk.Err}
	}
	if chunk.Ok.Tag != rt.TagSome {
		return 0, io.EOF
	}
	return copy(p, []byte(chunk.Ok.Some)), nil
}

// ExchangeTLS is `http_host.exchange_tls`: the TLS state of the
// connection the response came over, as stateParts lists it; None over
// plain HTTP.
func ExchangeTLS(e rt.HTTPExchange) rt.Maybe[*rt.List[*rt.List[string]]] {
	if st := exchangeOf(e).resp.TLS; st != nil {
		return rt.Some(stateParts(*st))
	}
	return rt.None[*rt.List[*rt.List[string]]]()
}

// tracer is what a std/http Tracer's handle wraps: the events Go's
// httptrace reported for the requests sent with it, in order. Go reports
// them on its own goroutines, so each is kept under mu and read later.
type tracer struct {
	mu     sync.Mutex
	events []*rt.List[*rt.List[string]]
}

func tracerOf(h rt.HTTPHandle) *tracer {
	t, ok := h.Value().(*tracer)
	if !ok {
		rt.Trap("http: a Tracer holds no tracer")
	}
	return t
}

// NewTracer is `http_host.new_tracer`.
func NewTracer() rt.HTTPHandle { return rt.NewHTTPHandle(&tracer{}) }

// TracerEvents is `http_host.tracer_events`: each event's name and fields,
// then for a finished TLS handshake its state as stateParts lists it.
func TracerEvents(h rt.HTTPHandle) *rt.List[*rt.List[*rt.List[string]]] {
	t := tracerOf(h)
	t.mu.Lock()
	defer t.mu.Unlock()
	var out *rt.List[*rt.List[*rt.List[string]]]
	for i := len(t.events) - 1; i >= 0; i-- {
		out = rt.Cons(t.events[i], out)
	}
	return out
}

func (t *tracer) record(fields []string, rest *rt.List[*rt.List[string]]) {
	t.mu.Lock()
	t.events = append(t.events, rt.Cons(listOf(fields), rest))
	t.mu.Unlock()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// trace is the httptrace.ClientTrace that records into t.
func (t *tracer) trace() *httptrace.ClientTrace {
	rec := func(fields ...string) { t.record(fields, nil) }
	return &httptrace.ClientTrace{
		GetConn: func(hostPort string) { rec("GetConnection", hostPort) },
		GotConn: func(info httptrace.GotConnInfo) {
			rec("GotConnection", strconv.FormatBool(info.Reused), strconv.FormatBool(info.WasIdle), strconv.FormatInt(int64(info.IdleTime), 10))
		},
		PutIdleConn:          func(err error) { rec("PutIdleConnection", errText(err)) },
		GotFirstResponseByte: func() { rec("FirstResponseByte") },
		Got100Continue:       func() { rec("Continue") },
		Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
			rec(append([]string{"Informational", strconv.Itoa(code)}, sortedPairs(flatHeader(http.Header(header)))...)...)
			return nil
		},
		DNSStart: func(info httptrace.DNSStartInfo) { rec("DnsStart", info.Host) },
		DNSDone: func(info httptrace.DNSDoneInfo) {
			fields := []string{"DnsDone", strconv.FormatBool(info.Coalesced), errText(info.Err)}
			for _, a := range info.Addrs {
				fields = append(fields, a.String())
			}
			rec(fields...)
		},
		ConnectStart:      func(network, addr string) { rec("ConnectStart", network, addr) },
		ConnectDone:       func(network, addr string, err error) { rec("ConnectDone", network, addr, errText(err)) },
		TLSHandshakeStart: func() { rec("TlsStart") },
		TLSHandshakeDone: func(st tls.ConnectionState, err error) {
			t.record([]string{"TlsDone", errText(err)}, stateParts(st))
		},
		WroteHeaderField: func(key string, value []string) { rec(append([]string{"WroteHeader", key}, value...)...) },
		WroteHeaders:     func() { rec("WroteHeaders") },
		Wait100Continue:  func() { rec("WaitContinue") },
		WroteRequest:     func(info httptrace.WroteRequestInfo) { rec("WroteRequest", errText(info.Err)) },
	}
}

func flatHeader(h http.Header) []string {
	var flat []string
	for k, vs := range h {
		for _, v := range vs {
			flat = append(flat, k, v)
		}
	}
	return flat
}

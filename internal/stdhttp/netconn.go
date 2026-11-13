package stdhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nomi-language/nomi/internal/osreach"
	"github.com/nomi-language/nomi/rt"
)

// std/net's Go side: TCP connections and listeners, and TLS over them. A
// connection is read and written through one Reader and one Writer, made
// when it opens. Its reads and writes stop when the task that opened or
// accepted it is cancelled (the frame's context sets a past deadline), so a
// cancelled task never stays blocked on the network. An Err is a net.Error
// or tls.Error variant's name and its fields.

func asError[T any](err error, target *T) bool { return errors.As(err, target) }

// conn is what std/http/host's `Conn` wraps.
type conn struct {
	c    net.Conn
	r    rt.Reader
	w    rt.Writer
	stop func() bool
}

func connOf(c rt.NetConn) *conn {
	x, ok := c.Value().(*conn)
	if !ok {
		rt.Trap("net: a Conn holds no connection")
	}
	return x
}

func frameContext(fr *rt.Frame) context.Context {
	if fr != nil && fr.Context() != nil {
		return fr.Context()
	}
	return context.Background()
}

// newConn wraps c, its reads and writes stopped when ctx ends.
func newConn(ctx context.Context, c net.Conn) rt.NetConn {
	x := &conn{c: c}
	x.r = rt.NewReader(rt.ReadSource{Name: "<connection>", R: c})
	x.w = rt.NewWriter(rt.WriteSink{Name: "<connection>", W: c, Unbuffered: true})
	x.stop = context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Unix(1, 0)) })
	return rt.NewNetConn(x)
}

// netErr is err, from operation op on address, as a net.Error variant's
// parts: a TLS failure as tls.Error's, else Go's error sorted by what
// happened.
func netErr(op, address string, err error) *rt.List[string] {
	if isTLSError(err) {
		return listOf(tlsErrorParts(err))
	}
	var dns *net.DNSError
	var addr *net.AddrError
	var parse *net.ParseError
	var timeout interface{ Timeout() bool }
	switch {
	case errors.As(err, &dns):
		return listOf([]string{"Dns", dns.Name, dns.Err, dns.Server, strconv.FormatBool(dns.IsNotFound), strconv.FormatBool(dns.IsTimeout)})
	case errors.As(err, &addr):
		return listOf([]string{"InvalidAddress", addr.Addr, addr.Err})
	case errors.As(err, &parse):
		return listOf([]string{"InvalidAddress", parse.Text, parse.Type})
	case errors.Is(err, net.ErrClosed):
		return listOf([]string{"Closed"})
	case errors.Is(err, syscall.ECONNREFUSED):
		return listOf([]string{"Refused", address})
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return listOf([]string{"Unreachable", address})
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return listOf([]string{"Reset", address})
	case errors.Is(err, syscall.EADDRINUSE):
		return listOf([]string{"AddressInUse", address})
	case errors.As(err, &timeout) && timeout.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return listOf([]string{"Timeout", address})
	}
	return listOf([]string{"Other", op, address, err.Error()})
}

func isTLSError(err error) bool {
	var alert tls.AlertError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	return errors.As(err, &alert) || errors.As(err, &verify) || errors.As(err, &record)
}

func dialer(timeout, keepAlive int64, local string) (*net.Dialer, []string) {
	if !hasSockets {
		return nil, strsOf(netErr("dial", local, errors.New(noSocketsMessage)))
	}
	osreach.Note("connect")
	d := &net.Dialer{Timeout: dur(timeout), KeepAlive: dur(keepAlive)}
	if local != "" {
		a, err := net.ResolveTCPAddr("tcp", local)
		if err != nil {
			return nil, strsOf(netErr("dial", local, err))
		}
		d.LocalAddr = a
	}
	return d, nil
}

// keepAliveConfig is Go's KeepAliveConfig for std/net's KeepAlive: a
// duration in nanoseconds, a count, 0 for Go's default.
func keepAliveConfig(enabled bool, idle, interval, count int64) net.KeepAliveConfig {
	return net.KeepAliveConfig{Enable: enabled, Idle: dur(idle), Interval: dur(interval), Count: int(count)}
}

// DialWith is `std::http::host.dial_with`: Dial with every option std/net's
// DialOptions has.
func DialWith(fr *rt.Frame, address string, timeout int64, local string, fallback int64, kaEnabled bool,
	kaIdle, kaInterval, kaCount int64, multipath bool) rt.Result[rt.NetConn, *rt.List[string]] {
	if !hasSockets {
		return rt.Err[rt.NetConn](netErr("dial", address, errors.New(noSocketsMessage)))
	}
	d, fail := dialer(timeout, 0, local)
	if fail != nil {
		return rt.Err[rt.NetConn](listOf(fail))
	}
	d.FallbackDelay = dur(fallback)
	d.KeepAliveConfig = keepAliveConfig(kaEnabled, kaIdle, kaInterval, kaCount)
	if !kaEnabled {
		d.KeepAlive = -1
	}
	d.SetMultipathTCP(multipath)
	ctx := frameContext(fr)
	c, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return rt.Err[rt.NetConn](netErr("dial", address, err))
	}
	return rt.Ok[rt.NetConn, *rt.List[string]](newConn(ctx, c))
}

// ListenWith is `std::http::host.net_listen_with`: NetListen with std/net's
// ListenOptions.
func ListenWith(fr *rt.Frame, address string, kaEnabled bool, kaIdle, kaInterval, kaCount int64, multipath bool) rt.Result[rt.NetListener, *rt.List[string]] {
	if !hasSockets {
		return rt.Err[rt.NetListener](netErr("listen", address, errors.New(noSocketsMessage)))
	}
	osreach.Note("listen")
	lc := &net.ListenConfig{KeepAliveConfig: keepAliveConfig(kaEnabled, kaIdle, kaInterval, kaCount)}
	if !kaEnabled {
		lc.KeepAlive = -1
	}
	lc.SetMultipathTCP(multipath)
	ln, err := lc.Listen(frameContext(fr), "tcp", address)
	if err != nil {
		return rt.Err[rt.NetListener](netErr("listen", address, err))
	}
	return rt.Ok[rt.NetListener, *rt.List[string]](rt.NewNetListener(&listenerView{netListener: serveListener(ln)}))
}

func tcpOf(c rt.NetConn) *net.TCPConn {
	switch x := connOf(c).c.(type) {
	case *net.TCPConn:
		return x
	case *tls.Conn:
		t, _ := x.NetConn().(*net.TCPConn)
		return t
	}
	return nil
}

// ConnCloseRead is `std::http::host.conn_close_read`: shuts the receiving half of
// a TCP connection.
func ConnCloseRead(c rt.NetConn) rt.Unit {
	if t := tcpOf(c); t != nil {
		_ = t.CloseRead()
	}
	return rt.Unit{}
}

// ConnNoDelay is `std::http::host.conn_no_delay`: Go's SetNoDelay.
func ConnNoDelay(c rt.NetConn, on bool) rt.Unit {
	if t := tcpOf(c); t != nil {
		_ = t.SetNoDelay(on)
	}
	return rt.Unit{}
}

// ConnLinger is `std::http::host.conn_linger`: Go's SetLinger, seconds, -1 for
// the system's default.
func ConnLinger(c rt.NetConn, seconds int64) rt.Unit {
	if t := tcpOf(c); t != nil {
		_ = t.SetLinger(int(seconds))
	}
	return rt.Unit{}
}

// ConnKeepAlive is `std::http::host.conn_keep_alive`: Go's SetKeepAliveConfig.
func ConnKeepAlive(c rt.NetConn, enabled bool, idle, interval, count int64) rt.Unit {
	if t := tcpOf(c); t != nil {
		_ = t.SetKeepAliveConfig(keepAliveConfig(enabled, idle, interval, count))
	}
	return rt.Unit{}
}

// ConnMultipath is `std::http::host.conn_multipath`: whether the connection runs
// Multipath TCP.
func ConnMultipath(c rt.NetConn) bool {
	if t := tcpOf(c); t != nil {
		on, err := t.MultipathTCP()
		return err == nil && on
	}
	return false
}

// Pipe is `std::http::host.pipe`: Go's net.Pipe, two connected ends in memory.
func Pipe() *rt.List[rt.NetConn] {
	a, b := net.Pipe()
	return rt.Cons(newConn(context.Background(), a), rt.Cons(newConn(context.Background(), b), (*rt.List[rt.NetConn])(nil)))
}

// SplitHostPort is `std::http::host.split_host_port`: [host, port] or an Err's parts.
func SplitHostPort(address string) rt.Result[*rt.List[string], *rt.List[string]] {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return rt.Err[*rt.List[string]](netErr("split", address, err))
	}
	return rt.Ok[*rt.List[string], *rt.List[string]](listOf([]string{host, port}))
}

// JoinHostPort is `std::http::host.join_host_port`: Go's JoinHostPort.
func JoinHostPort(host, port string) string { return net.JoinHostPort(host, port) }

func lookup[T any](fr *rt.Frame, name string, f func(context.Context) (T, error), parts func(T) *rt.List[string]) rt.Result[*rt.List[string], *rt.List[string]] {
	v, err := f(frameContext(fr))
	if err != nil {
		return rt.Err[*rt.List[string]](netErr("lookup", name, err))
	}
	return rt.Ok[*rt.List[string], *rt.List[string]](parts(v))
}

// Lookup is `std::http::host.lookup`: the system resolver's answer for kind
// (host, ip, address, cname, ns, txt, mx, port, srv) about name, flat:
// mx as host and preference pairs, srv as its cname then target, port,
// priority and weight quads, port as one number. For srv, name is
// "service protocol domain".
func Lookup(fr *rt.Frame, kind, name string) rt.Result[*rt.List[string], *rt.List[string]] {
	osreach.Note("connect")
	r := net.DefaultResolver
	same := func(xs []string) *rt.List[string] { return listOf(xs) }
	switch kind {
	case "host":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) { return r.LookupHost(ctx, name) }, same)
	case "ip":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) {
			ips, err := r.LookupIP(ctx, "ip", name)
			out := make([]string, len(ips))
			for i, ip := range ips {
				out[i] = ip.String()
			}
			return out, err
		}, same)
	case "address":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) { return r.LookupAddr(ctx, name) }, same)
	case "cname":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) {
			c, err := r.LookupCNAME(ctx, name)
			return []string{c}, err
		}, same)
	case "txt":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) { return r.LookupTXT(ctx, name) }, same)
	case "ns":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) {
			ns, err := r.LookupNS(ctx, name)
			out := make([]string, len(ns))
			for i, n := range ns {
				out[i] = n.Host
			}
			return out, err
		}, same)
	case "mx":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) {
			mx, err := r.LookupMX(ctx, name)
			var out []string
			for _, m := range mx {
				out = append(out, m.Host, strconv.Itoa(int(m.Pref)))
			}
			return out, err
		}, same)
	case "port":
		return lookup(fr, name, func(ctx context.Context) ([]string, error) {
			p, err := r.LookupPort(ctx, "tcp", name)
			return []string{strconv.Itoa(p)}, err
		}, same)
	case "srv":
		var service, proto, domain string
		if parts := strings.Fields(name); len(parts) == 3 {
			service, proto, domain = parts[0], parts[1], parts[2]
		}
		return lookup(fr, domain, func(ctx context.Context) ([]string, error) {
			cname, srvs, err := r.LookupSRV(ctx, service, proto, domain)
			out := []string{cname}
			for _, s := range srvs {
				out = append(out, s.Target, strconv.Itoa(int(s.Port)), strconv.Itoa(int(s.Priority)), strconv.Itoa(int(s.Weight)))
			}
			return out, err
		}, same)
	}
	rt.Trap("net: no lookup of kind " + kind)
	return rt.Result[*rt.List[string], *rt.List[string]]{}
}

// TLSDial is `std::http::host.tls_dial`: Dial, then a TLS client handshake with
// cfg, its server name the address's host when cfg names none.
func TLSDial(fr *rt.Frame, address string, timeout, keepAlive int64, local string, cfg rt.TLSConfig) rt.Result[rt.NetConn, *rt.List[string]] {
	if !hasSockets {
		return rt.Err[rt.NetConn](netErr("dial", address, errors.New(noSocketsMessage)))
	}
	d, fail := dialer(timeout, keepAlive, local)
	if fail != nil {
		return rt.Err[rt.NetConn](listOf(fail))
	}
	ctx := frameContext(fr)
	conf := confOf(cfg)
	conf.b = newBroker()
	td := &tls.Dialer{NetDialer: d, Config: conf.c}
	var c net.Conn
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err = td.DialContext(ctx, "tcp", address)
	}()
	conf.b.wait(fr, done)
	if err != nil {
		return rt.Err[rt.NetConn](netErr("dial", address, err))
	}
	return rt.Ok[rt.NetConn, *rt.List[string]](newConn(ctx, c))
}

// TLSClient is `std::http::host.tls_client`: a TLS client over an open connection
// (STARTTLS), its handshake finished before it answers.
func TLSClient(fr *rt.Frame, c rt.NetConn, cfg rt.TLSConfig) rt.Result[rt.NetConn, *rt.List[string]] {
	conf := confOf(cfg)
	return handshake(fr, conf, tls.Client(connOf(c).c, conf.c))
}

// TLSServer is `std::http::host.tls_server`: the server side of TLSClient.
func TLSServer(fr *rt.Frame, c rt.NetConn, cfg rt.TLSConfig) rt.Result[rt.NetConn, *rt.List[string]] {
	conf := confOf(cfg)
	return handshake(fr, conf, tls.Server(connOf(c).c, conf.c))
}

// handshake runs t's handshake while the calling task serves the
// configuration's hooks.
func handshake(fr *rt.Frame, conf *tlsConf, t *tls.Conn) rt.Result[rt.NetConn, *rt.List[string]] {
	ctx := frameContext(fr)
	if conf.b == nil {
		conf.b = newBroker()
	}
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		err = t.HandshakeContext(ctx)
	}()
	conf.b.wait(fr, done)
	if err != nil {
		return rt.Err[rt.NetConn](listOf(tlsErrorParts(err)))
	}
	return rt.Ok[rt.NetConn, *rt.List[string]](newConn(ctx, t))
}

// ConnReader is `std::http::host.conn_reader`: the connection's Reader.
func ConnReader(c rt.NetConn) rt.Reader { return connOf(c).r }

// ConnWriter is `std::http::host.conn_writer`: the connection's Writer, which
// sends each write as it is made.
func ConnWriter(c rt.NetConn) rt.Writer { return connOf(c).w }

// ConnClose is `std::http::host.conn_close`.
func ConnClose(c rt.NetConn) rt.Unit {
	x := connOf(c)
	x.stop()
	_ = x.c.Close()
	return rt.Unit{}
}

// ConnCloseWrite is `std::http::host.conn_close_write`: shuts the sending half,
// so the peer reads the end of its input, when the connection has halves.
func ConnCloseWrite(c rt.NetConn) rt.Unit {
	switch x := connOf(c).c.(type) {
	case *net.TCPConn:
		_ = x.CloseWrite()
	case *tls.Conn:
		_ = x.CloseWrite()
	}
	return rt.Unit{}
}

// ConnLocal is `std::http::host.conn_local`: the local address.
func ConnLocal(c rt.NetConn) string { return connOf(c).c.LocalAddr().String() }

// ConnRemote is `std::http::host.conn_remote`: the peer's address.
func ConnRemote(c rt.NetConn) string { return connOf(c).c.RemoteAddr().String() }

// ConnDeadline is `std::http::host.conn_deadline`: which is 0 for both reads and
// writes, 1 for reads, 2 for writes; at is Unix nanoseconds, or 0 for none.
func ConnDeadline(c rt.NetConn, which, at int64) rt.Unit {
	t := time.Time{}
	if at != 0 {
		t = time.Unix(0, at)
	}
	x := connOf(c).c
	switch which {
	case 1:
		_ = x.SetReadDeadline(t)
	case 2:
		_ = x.SetWriteDeadline(t)
	default:
		_ = x.SetDeadline(t)
	}
	return rt.Unit{}
}

// TLSStateParts is `std::http::host.tls_state_parts`: a TLS connection's state
// as stateParts lists it, or None for a connection that is not TLS.
func TLSStateParts(c rt.NetConn) rt.Maybe[*rt.List[*rt.List[string]]] {
	t, ok := connOf(c).c.(*tls.Conn)
	if !ok {
		return rt.None[*rt.List[*rt.List[string]]]()
	}
	return rt.Some(stateParts(t.ConnectionState()))
}

// TLSVerifyHostname is `std::http::host.tls_verify_hostname`: Go's
// Conn.VerifyHostname, "" when host matches the peer's certificate.
func TLSVerifyHostname(c rt.NetConn, host string) string {
	t, ok := connOf(c).c.(*tls.Conn)
	if !ok {
		return "the connection is not TLS"
	}
	if err := t.VerifyHostname(host); err != nil {
		return err.Error()
	}
	return ""
}

// netListener is what std/http/host's `TcpListener` wraps. A goroutine
// accepts connections into conns, so an accept can stop when the task
// waiting in it is cancelled; the program's drain closes the socket.
type netListener struct {
	ln     net.Listener
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	unhook func()

	mu       sync.Mutex
	deadline time.Time
}

// listenerView is what a TcpListener value holds: the listener, and the
// TLS configuration its accepted connections take when it is a TLS one.
type listenerView struct {
	*netListener
	tls *tlsConf
}

func netListenerOf(l rt.NetListener) *netListener { return viewOf(l).netListener }

func viewOf(l rt.NetListener) *listenerView {
	x, ok := l.Value().(*listenerView)
	if !ok {
		rt.Trap("net: a TcpListener holds no listener")
	}
	return x
}

// TLSListen is `std::http::host.tls_listen`: NetListen, its connections TLS
// servers with cfg.
func TLSListen(address string, cfg rt.TLSConfig) rt.Result[rt.NetListener, *rt.List[string]] {
	return startListener(address, confOf(cfg))
}

func startListener(address string, cfg *tlsConf) rt.Result[rt.NetListener, *rt.List[string]] {
	if !hasSockets {
		return rt.Err[rt.NetListener](netErr("listen", address, errors.New(noSocketsMessage)))
	}
	osreach.Note("listen")
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return rt.Err[rt.NetListener](netErr("listen", address, err))
	}
	return rt.Ok[rt.NetListener, *rt.List[string]](rt.NewNetListener(&listenerView{netListener: serveListener(ln), tls: cfg}))
}

// TLSWrapListener is `std::http::host.tls_wrap_listener`: the listener's
// connections as TLS servers with cfg, as Go's tls.NewListener makes them.
// It shares the listener: closing either closes both.
func TLSWrapListener(l rt.NetListener, cfg rt.TLSConfig) rt.NetListener {
	return rt.NewNetListener(&listenerView{netListener: netListenerOf(l), tls: confOf(cfg)})
}

// serveListener starts accepting ln's connections into a netListener.
func serveListener(ln net.Listener) *netListener {
	x := &netListener{ln: ln, conns: make(chan net.Conn), closed: make(chan struct{})}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				x.close()
				return
			}
			select {
			case x.conns <- c:
			case <-x.closed:
				_ = c.Close()
				return
			}
		}
	}()
	x.unhook = rt.OnDrain(x.close)
	return x
}

func (x *netListener) close() {
	x.once.Do(func() {
		close(x.closed)
		_ = x.ln.Close()
	})
}

// NetListenerAddress is `std::http::host.net_listener_address`.
func NetListenerAddress(l rt.NetListener) string { return netListenerOf(l).ln.Addr().String() }

// NetAccept is `std::http::host.net_accept`: the next connection, Err Closed once
// the listener is closed, or once the calling task is stopped.
func NetAccept(fr *rt.Frame, l rt.NetListener) rt.Result[rt.NetConn, *rt.List[string]] {
	v := viewOf(l)
	x := v.netListener
	ctx := frameContext(fr)
	select {
	case c := <-x.conns:
		if hasSockets && v.tls != nil {
			// The handshake runs here, so the configuration's hooks run on
			// the accepting task; a failed one closes the connection, and
			// the accept waits for the next.
			t := tls.Server(c, v.tls.c)
			r := handshake(fr, v.tls, t)
			if r.Tag != rt.TagOk {
				_ = c.Close()
				return NetAccept(fr, l)
			}
			return r
		}
		return rt.Ok[rt.NetConn, *rt.List[string]](newConn(context.Background(), c))
	case <-x.closed:
	case <-ctx.Done():
	case <-x.deadlineC():
		return rt.Err[rt.NetConn](listOf([]string{"Timeout", x.ln.Addr().String()}))
	}
	return rt.Err[rt.NetConn](listOf([]string{"Closed"}))
}

// deadlineC fires at the listener's accept deadline, or never.
func (x *netListener) deadlineC() <-chan time.Time {
	x.mu.Lock()
	at := x.deadline
	x.mu.Unlock()
	if at.IsZero() {
		return nil
	}
	return time.After(time.Until(at))
}

// NetListenerDeadline is `std::http::host.net_listener_deadline`: an accept
// waiting past at (Unix nanoseconds; 0 for none) fails with a timeout.
func NetListenerDeadline(l rt.NetListener, at int64) rt.Unit {
	x := netListenerOf(l)
	x.mu.Lock()
	x.deadline = time.Time{}
	if at != 0 {
		x.deadline = time.Unix(0, at)
	}
	x.mu.Unlock()
	return rt.Unit{}
}

// NetListenerClose is `std::http::host.net_listener_close`.
func NetListenerClose(l rt.NetListener) rt.Unit {
	x := netListenerOf(l)
	x.unhook()
	x.close()
	return rt.Unit{}
}

// WithTLS is `std::http::host.with_tls`: a client sharing c's options whose
// transport uses cfg for its TLS connections.
func WithTLS(c rt.HTTPClient, cfg rt.TLSConfig) rt.HTTPClient {
	cl := *clientOf(c).c
	tr, ok := cl.Transport.(*http.Transport)
	if !ok {
		rt.Trap("http: the client has no transport to configure")
	}
	tr = tr.Clone()
	conf := confOf(cfg)
	b := clientOf(c).b
	conf.b = b
	tr.TLSClientConfig = conf.c
	cl.Transport = tr
	return rt.NewHTTPClient(&client{c: &cl, b: b})
}

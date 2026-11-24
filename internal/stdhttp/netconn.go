package stdhttp

import (
	"context"
	"crypto"
	_ "crypto/sha256" // the hashes tls-server-end-point names
	_ "crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nomi-language/nomi/internal/osreach"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/rt"
)

// std/net's and std/udp's Go side: TCP and Unix stream connections and
// listeners, TLS over them, and datagram sockets (datagram.go). A
// connection is read and written through one Reader and one Writer, made
// when it opens. Its reads and writes stop when the task that opened it is
// cancelled (the frame's context sets a past deadline), so a cancelled task
// never stays blocked on the network. An Err is a net.Error or tls.Error
// variant's name and its fields.

func asError[T any](err error, target *T) bool { return errors.As(err, target) }

// conn is what std/http/host's `Conn` wraps: a stream connection or a
// datagram socket.
type conn struct {
	c    net.Conn
	r    rt.Reader
	w    rt.Writer
	stop func() bool
	// release runs when the socket closes, or is nil: a bound datagram
	// socket's drain hook comes off and its Unix socket file goes.
	release func()

	// readDeadline is the read deadline the program set (ConnDeadline),
	// which a receive stopped with its task gives back to the socket.
	mu           sync.Mutex
	readDeadline time.Time

	// tlsServer is whether c is the server side of a TLS connection, which
	// its channel binding's certificate is (TLSServerEndPoint).
	tlsServer bool
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
func newConn(ctx context.Context, c net.Conn) rt.NetConn { return rt.NewNetConn(connWith(ctx, c)) }

func connWith(ctx context.Context, c net.Conn) *conn {
	x := &conn{c: c}
	x.r = rt.NewReader(rt.ReadSource{Name: "<connection>", R: c, Fail: streamFailure("<connection>")})
	x.w = rt.NewWriter(rt.WriteSink{Name: "<connection>", W: c, Unbuffered: true, Fail: streamFailure("<connection>")})
	x.stop = context.AfterFunc(ctx, func() {
		x.mu.Lock()
		x.readDeadline = time.Unix(1, 0)
		x.mu.Unlock()
		_ = c.SetDeadline(time.Unix(1, 0))
	})
	return x
}

// streamFailure is the io.Error a read or write of the stream named name
// answers for err, for every stream this package makes: a connection, a
// request or response body, a chunked body, a multipart part. The errors
// of net/http and net, which internal/stdio does not link, are named
// here: a body the server discarded and closed, a hijacked response and a
// closed connection are `Closed`, and a chunk size line too long to read
// is `Corrupt`. Every other error is a file's or a process's stream's, as
// stdio.Classify sorts it: a read past the deadline `set_read_deadline`
// set is `TimedOut`.
func streamFailure(name string) func(op string, err error) rt.IOError {
	return func(op string, err error) rt.IOError {
		switch {
		case errors.Is(err, http.ErrBodyReadAfterClose), errors.Is(err, http.ErrHijacked), errors.Is(err, net.ErrClosed):
			return rt.IOError{Tag: rt.TagIOClosed}
		case errors.Is(err, http.ErrLineTooLong):
			return rt.IOError{Tag: rt.TagIOCorrupt, Path: name, Reason: "a chunk size line is too long"}
		}
		return stdio.Classify(op, name, err)
	}
}

// netErr is err, from operation op on address, as a net.Error variant's
// parts: a TLS failure as tls.Error's, else Go's error sorted by what
// happened, a system's failure by its errno as stdio.Errno reads it on
// every system.
func netErr(op, address string, err error) *rt.List[string] {
	if isTLSError(err) {
		return listOf(tlsErrorParts(err))
	}
	var dns *net.DNSError
	var addr *net.AddrError
	var parse *net.ParseError
	var timeout interface{ Timeout() bool }
	var lacks unsupported
	switch errno := stdio.Errno(err); {
	case errors.As(err, &lacks):
		return listOf([]string{"Unsupported", op, string(lacks)})
	case errors.As(err, &dns):
		return listOf([]string{"Dns", dns.Name, dns.Err, dns.Server, strconv.FormatBool(dns.IsNotFound), strconv.FormatBool(dns.IsTimeout)})
	case errors.As(err, &addr):
		return listOf([]string{"InvalidAddress", addr.Addr, addr.Err})
	case errors.As(err, &parse):
		return listOf([]string{"InvalidAddress", parse.Text, parse.Type})
	case errors.Is(err, net.ErrClosed):
		return listOf([]string{"Closed"})
	case errno == syscall.ECONNREFUSED:
		return listOf([]string{"Refused", address})
	case errno == syscall.ENETUNREACH, errno == syscall.EHOSTUNREACH:
		return listOf([]string{"Unreachable", address})
	case errno == syscall.ECONNRESET, errno == syscall.ECONNABORTED, errno == syscall.EPIPE:
		return listOf([]string{"Reset", address})
	case errno == syscall.EADDRINUSE:
		return listOf([]string{"AddressInUse", address})
	case errno == syscall.ETIMEDOUT, errors.As(err, &timeout) && timeout.Timeout(), errors.Is(err, context.DeadlineExceeded):
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

// dialer is a net.Dialer on network ("tcp", "unix", "udp", "unixgram")
// from the local address local ("" for any), setting SO_REUSEADDR and
// SO_REUSEPORT before it binds when asked.
func dialer(network string, timeout, keepAlive int64, local string, reuseAddress, reusePort bool) (*net.Dialer, []string) {
	if !hasSockets {
		return nil, strsOf(netErr("dial", local, errors.New(noSocketsMessage)))
	}
	osreach.Note("connect")
	d := &net.Dialer{Timeout: dur(timeout), KeepAlive: dur(keepAlive), Control: bindControl(reuseAddress, reusePort)}
	if local != "" {
		a, err := resolveAddr(network, local)
		if err != nil {
			return nil, strsOf(netErr("dial", local, err))
		}
		d.LocalAddr = a
	}
	return d, nil
}

// resolveAddr is address on network: a path for a Unix network, else
// host:port with the host resolved.
func resolveAddr(network, address string) (net.Addr, error) {
	switch network {
	case "unix", "unixgram":
		return &net.UnixAddr{Name: address, Net: network}, nil
	case "udp", "udp4", "udp6":
		return net.ResolveUDPAddr(network, address)
	}
	return net.ResolveTCPAddr(network, address)
}

// keepAliveConfig is Go's KeepAliveConfig for std/net's KeepAlive: a
// duration in nanoseconds, a count, 0 for Go's default.
func keepAliveConfig(enabled bool, idle, interval, count int64) net.KeepAliveConfig {
	return net.KeepAliveConfig{Enable: enabled, Idle: dur(idle), Interval: dur(interval), Count: int(count)}
}

// DialWith is `std::http::host.dial_with`: a stream connection on network
// ("tcp", or "unix" for a Unix socket's path) with every option std/net's
// DialOptions has. A Unix dial reads the timeout, the local address and
// the reuse options; Go's dialer reads the rest only for TCP.
func DialWith(
	fr *rt.Frame,
	network string,
	address string,
	timeoutArg rt.Int,
	local string,
	fallbackArg rt.Int,
	kaEnabled bool,
	kaIdleArg, kaIntervalArg, kaCountArg rt.Int,
	multipath bool,
	reuseAddress, reusePort bool,
) rt.Result[rt.NetConn, *rt.List[string]] {
	timeout := timeoutArg.Saturated()
	fallback := fallbackArg.Saturated()
	kaIdle := kaIdleArg.Saturated()
	kaInterval := kaIntervalArg.Saturated()
	kaCount := kaCountArg.Saturated()
	if !hasSockets {
		return rt.Err[rt.NetConn](netErr("dial", address, errors.New(noSocketsMessage)))
	}
	d, fail := dialer(network, timeout, 0, local, reuseAddress, reusePort)
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
	c, err := d.DialContext(ctx, network, address)
	if err != nil {
		return rt.Err[rt.NetConn](netErr("dial", address, err))
	}
	return rt.Ok[rt.NetConn, *rt.List[string]](newConn(ctx, c))
}

// ListenWith is `std::http::host.net_listen_with`: a listener on network
// ("tcp", or "unix" for a Unix socket's path) with std/net's
// ListenOptions. A Unix listener removes its socket file when it closes,
// as Go's does; a Linux abstract name (`@...`) has no file.
func ListenWith(fr *rt.Frame, network, address string, kaEnabled bool, kaIdleArg, kaIntervalArg, kaCountArg rt.Int, multipath, reuseAddress, reusePort bool) rt.Result[rt.NetListener, *rt.List[string]] {
	kaIdle := kaIdleArg.Saturated()
	kaInterval := kaIntervalArg.Saturated()
	kaCount := kaCountArg.Saturated()
	if !hasSockets {
		return rt.Err[rt.NetListener](netErr("listen", address, errors.New(noSocketsMessage)))
	}
	osreach.Note("listen")
	lc := &net.ListenConfig{
		KeepAliveConfig: keepAliveConfig(kaEnabled, kaIdle, kaInterval, kaCount),
		Control:         bindControl(reuseAddress, reusePort),
	}
	if !kaEnabled {
		lc.KeepAlive = -1
	}
	lc.SetMultipathTCP(multipath)
	ln, err := lc.Listen(frameContext(fr), network, address)
	if err != nil {
		return rt.Err[rt.NetListener](netErr("listen", address, err))
	}
	return rt.Ok[rt.NetListener, *rt.List[string]](rt.NewNetListener(&listenerView{netListener: serveListener(ln)}))
}

func tcpOf(c rt.NetConn) *net.TCPConn {
	t, _ := socketOf(c).(*net.TCPConn)
	return t
}

// ConnCloseRead is `std::http::host.conn_close_read`: shuts the receiving half of
// a TCP or Unix connection.
func ConnCloseRead(c rt.NetConn) rt.Unit {
	if h, ok := socketOf(c).(interface{ CloseRead() error }); ok {
		_ = h.CloseRead()
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
func ConnLinger(c rt.NetConn, secondsArg rt.Int) rt.Unit {
	seconds := secondsArg.Saturated()
	if t := tcpOf(c); t != nil {
		_ = t.SetLinger(int(seconds))
	}
	return rt.Unit{}
}

// ConnKeepAlive is `std::http::host.conn_keep_alive`: Go's SetKeepAliveConfig.
func ConnKeepAlive(c rt.NetConn, enabled bool, idleArg, intervalArg, countArg rt.Int) rt.Unit {
	idle := idleArg.Saturated()
	interval := intervalArg.Saturated()
	count := countArg.Saturated()
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
func TLSDial(fr *rt.Frame, address string, timeoutArg, keepAliveArg rt.Int, local string, cfg rt.TLSConfig) rt.Result[rt.NetConn, *rt.List[string]] {
	timeout := timeoutArg.Saturated()
	keepAlive := keepAliveArg.Saturated()
	if !hasSockets {
		return rt.Err[rt.NetConn](netErr("dial", address, errors.New(noSocketsMessage)))
	}
	d, fail := dialer("tcp", timeout, keepAlive, local, false, false)
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
	return handshake(fr, conf, tls.Client(connOf(c).c, conf.c), false)
}

// TLSServer is `std::http::host.tls_server`: the server side of TLSClient.
func TLSServer(fr *rt.Frame, c rt.NetConn, cfg rt.TLSConfig) rt.Result[rt.NetConn, *rt.List[string]] {
	conf := confOf(cfg)
	return handshake(fr, conf, tls.Server(connOf(c).c, conf.c), true)
}

// handshake runs t's handshake while the calling task serves the
// configuration's hooks. server is whether t is the server side.
func handshake(fr *rt.Frame, conf *tlsConf, t *tls.Conn, server bool) rt.Result[rt.NetConn, *rt.List[string]] {
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
	x := connWith(ctx, t)
	x.tlsServer = server
	return rt.Ok[rt.NetConn, *rt.List[string]](rt.NewNetConn(x))
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
	if x.release != nil {
		x.release()
	}
	return rt.Unit{}
}

// ConnCloseWrite is `std::http::host.conn_close_write`: shuts the sending half,
// so the peer reads the end of its input, when the connection has halves.
func ConnCloseWrite(c rt.NetConn) rt.Unit {
	if h, ok := connOf(c).c.(interface{ CloseWrite() error }); ok {
		_ = h.CloseWrite()
	}
	return rt.Unit{}
}

// ConnLocal is `std::http::host.conn_local`: the local address, or a Unix
// socket's path, "" for an unbound one.
func ConnLocal(c rt.NetConn) string { return addrString(connOf(c).c.LocalAddr()) }

// ConnRemote is `std::http::host.conn_remote`: the peer's address.
func ConnRemote(c rt.NetConn) string { return addrString(connOf(c).c.RemoteAddr()) }

// ConnDeadline is `std::http::host.conn_deadline`: which is 0 for both reads and
// writes, 1 for reads, 2 for writes; at is Unix nanoseconds, or 0 for none.
func ConnDeadline(c rt.NetConn, whichArg, atArg rt.Int) rt.Unit {
	which := whichArg.Saturated()
	at := atArg.Saturated()
	t := time.Time{}
	if at != 0 {
		t = time.Unix(0, at)
	}
	x := connOf(c)
	if which != 2 {
		x.mu.Lock()
		x.readDeadline = t
		x.mu.Unlock()
	}
	switch which {
	case 1:
		_ = x.c.SetReadDeadline(t)
	case 2:
		_ = x.c.SetWriteDeadline(t)
	default:
		_ = x.c.SetDeadline(t)
	}
	return rt.Unit{}
}

// TLSStateParts is `std::http::host.tls_state_parts`: a TLS connection's state
// as stateParts lists it, or None for a connection that is not TLS.
func TLSStateParts(c rt.NetConn) rt.Option[*rt.List[*rt.List[string]]] {
	t, ok := connOf(c).c.(*tls.Conn)
	if !ok {
		return rt.None[*rt.List[*rt.List[string]]]()
	}
	return rt.Some(stateParts(t.ConnectionState()))
}

// TLSExportKeyingMaterial is `std::http::host.tls_export_keying_material`:
// length bytes of keying material exported from the connection's TLS
// session under label and, when hasContext, context (RFC 5705, and RFC
// 8446 section 7.5 for TLS 1.3), Go's ConnectionState.ExportKeyingMaterial;
// or why not: the connection is not TLS, or Go refuses the label or the
// length.
func TLSExportKeyingMaterial(c rt.NetConn, label string, context rt.Bytes, hasContext bool, length rt.Int) rt.Result[rt.Bytes, string] {
	t, ok := connOf(c).c.(*tls.Conn)
	if !ok {
		return rt.Err[rt.Bytes]("the connection is not TLS")
	}
	n := length.Saturated()
	if n > maxExported {
		return rt.Err[rt.Bytes](fmt.Sprintf("at most %d bytes of keying material are exported, got %s", maxExported, length))
	}
	var ctx []byte
	if hasContext {
		ctx = append([]byte{}, context...)
	}
	state := t.ConnectionState()
	out, err := state.ExportKeyingMaterial(label, ctx, int(n))
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	return rt.Ok[rt.Bytes, string](rt.Bytes(out))
}

// maxExported bounds what TLSExportKeyingMaterial answers: TLS 1.3's
// exporter gives at most 255 hashes' worth, and no protocol asks for more
// than a few hundred bytes.
const maxExported = 1 << 16

// TLSServerEndPoint is `std::http::host.tls_server_end_point`: the
// `tls-server-end-point` channel binding of RFC 5929, the hash of the
// server's certificate (the peer's on a client, this side's on a server)
// by the hash its signature algorithm names, SHA-256 for MD5 and SHA-1; or
// why there is none: the connection is not TLS, the server presented no
// certificate, or its signature names no hash (Ed25519).
func TLSServerEndPoint(c rt.NetConn) rt.Result[rt.Bytes, string] {
	x := connOf(c)
	t, ok := x.c.(*tls.Conn)
	if !ok {
		return rt.Err[rt.Bytes]("the connection is not TLS")
	}
	state := t.ConnectionState()
	var raw []byte
	switch {
	case x.tlsServer && len(state.LocalCertificate) > 0:
		raw = state.LocalCertificate[0]
	case !x.tlsServer && len(state.PeerCertificates) > 0:
		raw = state.PeerCertificates[0].Raw
	default:
		return rt.Err[rt.Bytes]("the server presented no certificate")
	}
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	var h crypto.Hash
	switch cert.SignatureAlgorithm {
	case x509.MD5WithRSA, x509.SHA1WithRSA, x509.ECDSAWithSHA1, x509.DSAWithSHA1,
		x509.SHA256WithRSA, x509.ECDSAWithSHA256, x509.SHA256WithRSAPSS, x509.DSAWithSHA256:
		h = crypto.SHA256
	case x509.SHA384WithRSA, x509.ECDSAWithSHA384, x509.SHA384WithRSAPSS:
		h = crypto.SHA384
	case x509.SHA512WithRSA, x509.ECDSAWithSHA512, x509.SHA512WithRSAPSS:
		h = crypto.SHA512
	default:
		return rt.Err[rt.Bytes](fmt.Sprintf("the server's certificate is signed with %s, which names no hash for tls-server-end-point", cert.SignatureAlgorithm))
	}
	d := h.New()
	d.Write(raw)
	return rt.Ok[rt.Bytes, string](rt.Bytes(d.Sum(nil)))
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

// A netListener is a net.Listener over the connections its goroutine
// accepts, so an HTTP server serves on a std::net Listener (`Server.listener`)
// as `Listener.accept` does. Closing it takes its drain hook off.
func (x *netListener) Accept() (net.Conn, error) {
	select {
	case c := <-x.conns:
		return c, nil
	case <-x.closed:
		return nil, net.ErrClosed
	}
}

func (x *netListener) Close() error {
	x.unhook()
	x.close()
	return nil
}

func (x *netListener) Addr() net.Addr { return x.ln.Addr() }

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
			r := handshake(fr, v.tls, t, true)
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
func NetListenerDeadline(l rt.NetListener, atArg rt.Int) rt.Unit {
	at := atArg.Saturated()
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
	_ = netListenerOf(l).Close()
	return rt.Unit{}
}

// The sockets a service manager starts the process with (stdhttp package
// doc, "TLS and sockets"). A descriptor that is neither a stream listener
// nor a datagram socket is left open and unlisted.

// listenFDsStart is the first descriptor the protocol passes.
const listenFDsStart = 3

// inheritedSocket is one passed descriptor and the name LISTEN_FDNAMES
// gives it: a listener, a datagram socket, or a file held open, being
// neither.
type inheritedSocket struct {
	name     string
	listener *rt.NetListener
	socket   *rt.NetConn
	file     *os.File
}

var inherited struct {
	once    sync.Once
	sockets []inheritedSocket
	err     error
}

// inheritedSockets is every socket the process was started with, wrapped
// on the first call.
func inheritedSockets() ([]inheritedSocket, error) {
	inherited.once.Do(func() {
		if runtime.GOOS == "windows" {
			return
		}
		var names []string
		if names, inherited.err = listenFDNames(os.Getenv, os.Getpid()); inherited.err != nil {
			return
		}
		for i, name := range names {
			inherited.sockets = append(inherited.sockets, wrapInherited(os.NewFile(uintptr(listenFDsStart+i), name), name))
		}
	})
	return inherited.sockets, inherited.err
}

// listenFDNames is the name of each descriptor the environment getenv
// reads passes to the process pid: none when LISTEN_PID is unset or names
// another process, and "unknown" for each when LISTEN_FDNAMES is unset or
// empty, as systemd's sd_listen_fds_with_names answers.
func listenFDNames(getenv func(string) string, pid int) ([]string, error) {
	listenPID := getenv("LISTEN_PID")
	if listenPID == "" {
		return nil, nil
	}
	p, err := strconv.Atoi(listenPID)
	if err != nil {
		return nil, errors.New("LISTEN_PID is not a process id: " + listenPID)
	}
	if p != pid {
		return nil, nil
	}
	n, err := strconv.Atoi(getenv("LISTEN_FDS"))
	if err != nil || n < 0 {
		return nil, errors.New("LISTEN_FDS is not a count: " + getenv("LISTEN_FDS"))
	}
	names := make([]string, n)
	for i := range names {
		names[i] = "unknown"
	}
	if given := getenv("LISTEN_FDNAMES"); given != "" {
		parts := strings.Split(given, ":")
		if len(parts) != n {
			return nil, errors.New("LISTEN_FDNAMES names " + strconv.Itoa(len(parts)) + " descriptors and LISTEN_FDS passes " + strconv.Itoa(n))
		}
		names = parts
	}
	return names, nil
}

// wrapInherited is the socket f holds: a stream listener, a datagram
// socket, or f itself, held open, for anything else. A Unix datagram
// socket reads to net.FileListener as a Unix listener, so the network its
// address names decides.
func wrapInherited(f *os.File, name string) inheritedSocket {
	s := inheritedSocket{name: name}
	if ln, err := net.FileListener(f); err == nil {
		if network := ln.Addr().Network(); network == "tcp" || network == "unix" {
			_ = f.Close()
			l := rt.NewNetListener(&listenerView{netListener: serveListener(ln)})
			s.listener = &l
			return s
		}
		_ = ln.Close()
	}
	if pc, err := net.FilePacketConn(f); err == nil {
		_ = f.Close()
		c := rt.NewNetConn(boundSocket(pc.(net.Conn), ""))
		s.socket = &c
		return s
	}
	s.file = f
	return s
}

// inheritedOf is each inherited socket that pick answers, among those
// named name, or every one for None.
func inheritedOf[T any](name rt.Option[string], pick func(inheritedSocket) *T) rt.Result[*rt.List[T], *rt.List[string]] {
	if !hasSockets {
		return rt.Err[*rt.List[T]](netErr("inherit", "", errors.New(noSocketsMessage)))
	}
	osreach.Note("listen")
	all, err := inheritedSockets()
	if err != nil {
		return rt.Err[*rt.List[T]](listOf([]string{"Other", "inherit", "LISTEN_FDS", err.Error()}))
	}
	var out *rt.List[T]
	for i := len(all) - 1; i >= 0; i-- {
		if v := pick(all[i]); v != nil && (name.Tag != rt.TagSome || all[i].name == name.Some) {
			out = rt.Cons(*v, out)
		}
	}
	return rt.Ok[*rt.List[T], *rt.List[string]](out)
}

// InheritedListeners is `std::http::host.inherited_listeners`: the
// listening stream sockets the process was started with, in the order
// passed, those named name alone when it is given.
func InheritedListeners(name rt.Option[string]) rt.Result[*rt.List[rt.NetListener], *rt.List[string]] {
	return inheritedOf(name, func(s inheritedSocket) *rt.NetListener { return s.listener })
}

// InheritedSockets is `std::http::host.inherited_sockets`: the datagram
// sockets the process was started with, as InheritedListeners.
func InheritedSockets(name rt.Option[string]) rt.Result[*rt.List[rt.NetConn], *rt.List[string]] {
	return inheritedOf(name, func(s inheritedSocket) *rt.NetConn { return s.socket })
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

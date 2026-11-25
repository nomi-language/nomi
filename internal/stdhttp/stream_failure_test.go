package stdhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// A handler that starts its response without full_duplex and then reads
// the request body reads a body the server has discarded and closed: Go
// answers http.ErrBodyReadAfterClose. The read is io.Error's Closed. It
// was Other with the code "unknown", which printed as an operating system
// error, since the body's reader passed Go's error to no classification.
func TestInBody_AReadAfterTheServerClosedTheBodyIsClosed(t *testing.T) {
	defer rt.ResetSupervisors()
	durations := rt.Cons(rt.SmallInt(0), rt.Cons(rt.SmallInt(0), rt.Cons(rt.SmallInt(0), rt.Cons(rt.SmallInt(0), rt.Cons(rt.SmallInt(int64(5*time.Second)), (*rt.List[rt.Int])(nil))))))
	flags := rt.Cons(true, rt.Cons(true, rt.Cons(true, (*rt.List[bool])(nil))))
	res := Listen("127.0.0.1:0", rt.None[rt.NetListener](), durations, nil, flags, nil, false, nil, nil, nil, false, nil, false, nil, false, nil, false)
	if res.Tag != 1 {
		t.Fatalf("Listen failed: %v", res.Err)
	}
	l := res.Ok
	read := make(chan rt.Result[rt.Option[rt.Bytes], rt.IOError], 1)
	go func() {
		next := Next(nil, l)
		if next.Tag != 1 {
			close(read)
			return
		}
		w := Produce(next.Some, rt.SmallInt(200), nil, nil, rt.SmallInt(0), false)
		// Sending the head is where an HTTP/1 server discards the body.
		if r := rt.WriterWriteText(nil, w, "answered"); r.Tag != rt.TagOk {
			t.Errorf("writing the response: %v", r.Err)
		}
		if r := rt.WriterFlush(nil, w); r.Tag != rt.TagOk {
			t.Errorf("flushing the response: %v", r.Err)
		}
		read <- rt.ReaderRead(nil, InBody(next.Some), rt.SmallInt(16))
		_ = rt.WriterClose(nil, w)
		Finish(next.Some, nil)
	}()

	resp, err := http.Post("http://"+Address(l)+"/", "text/plain", bytes.NewReader([]byte("a short body")))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	got, ok := <-read
	if !ok {
		t.Fatal("the server took no request")
	}
	if got.Tag == rt.TagOk {
		t.Fatalf("the read after the server closed the body succeeded: %v", got.Ok)
	}
	if want := (rt.IOError{Tag: rt.TagIOClosed}); got.Err != want {
		t.Errorf("the read after the server closed the body = %+v, want %+v", got.Err, want)
	}
}

// The errors of net/http and net that this package's streams return are
// each the io.Error variant that names what happened, and every other
// error is sorted as a file's stream's is. Each was Other with the code
// "unknown", except the closed connection, which went to stdio alone.
func TestStreamFailure_NetAndHTTPErrorsAreTheirVariants(t *testing.T) {
	closedConn := &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}
	for _, c := range []struct {
		name string
		err  error
		want rt.IOError
	}{
		{"a body the server closed", http.ErrBodyReadAfterClose, rt.IOError{Tag: rt.TagIOClosed}},
		{"a hijacked response", http.ErrHijacked, rt.IOError{Tag: rt.TagIOClosed}},
		{"a closed connection", closedConn, rt.IOError{Tag: rt.TagIOClosed}},
		{"a chunk size line too long", http.ErrLineTooLong, rt.IOError{Tag: rt.TagIOCorrupt, Path: "<request>", Reason: "a chunk size line is too long"}},
		{"a body shorter than its length", io.ErrUnexpectedEOF, rt.IOError{Tag: rt.TagIOTruncated, Path: "<request>"}},
		{"a read timeout", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}, rt.IOError{Tag: rt.TagIOTimedOut}},
		{"an io.Error a nested reader returned", rt.IOFailure{E: rt.IOError{Tag: rt.TagIOBrokenPipe}}, rt.IOError{Tag: rt.TagIOBrokenPipe}},
	} {
		if got := streamFailure("<request>")("read", c.err); got != c.want {
			t.Errorf("%s: streamFailure(%v) = %+v, want %+v", c.name, c.err, got, c.want)
		}
	}

	// A write to a response the handler has finished is Closed.
	w := rt.NewWriter(rt.WriteSink{Name: "<response>", W: responseWriter{in: &incoming{finished: true}}, Fail: streamFailure("<response>")})
	_ = rt.WriterWriteText(nil, w, "late")
	if got, want := rt.WriterFlush(nil, w), (rt.IOError{Tag: rt.TagIOClosed}); got.Tag == rt.TagOk || got.Err != want {
		t.Errorf("a write after the response finished = %+v, want Err(%+v)", got, want)
	}
}

// The errors that went to the fallback, which printed `read <stream>:
// failed`, are each the variant that names what happened: a TLS alert the
// peer sent and a reset connection are Reset, a TLS record that does not
// decode is Corrupt, a body written past its length or for a status that
// takes none is Closed, a cancelled wait is Closed, and what a chunked or
// multipart decoder refuses is Corrupt.
func TestStreamFailure_EachFailureIsItsVariant(t *testing.T) {
	const name = "<connection>"
	reset := rt.IOError{Tag: rt.TagIOReset, Path: name}
	closed := rt.IOError{Tag: rt.TagIOClosed}
	for _, c := range []struct {
		what string
		fail func(op string, err error) rt.IOError
		err  error
		want rt.IOError
	}{
		{"a TLS alert the peer sent", streamFailure(name),
			&net.OpError{Op: "remote error", Err: tls.AlertError(40)}, reset},
		{"a TLS record that does not authenticate", streamFailure(name),
			&net.OpError{Op: "local error", Err: tls.AlertError(20)},
			rt.IOError{Tag: rt.TagIOCorrupt, Path: name, Reason: "a TLS record is not valid: bad record MAC"}},
		{"a TLS record header that does not parse", streamFailure(name),
			tls.RecordHeaderError{Msg: "oversized record received with length 20000"},
			rt.IOError{Tag: rt.TagIOCorrupt, Path: name, Reason: "a TLS record is not valid: oversized record received with length 20000"}},
		{"a reset connection", streamFailure(name),
			&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, reset},
		{"an aborted connection", streamFailure(name),
			&net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.ECONNABORTED)}, reset},
		{"a body for a status that takes none", streamFailure(name), http.ErrBodyNotAllowed, closed},
		{"a body past its Content-Length", streamFailure(name), http.ErrContentLength, closed},
		{"a cancelled wait", streamFailure(name), context.Canceled, closed},
		{"a cancelled read of a request body", streamFailure(name), func() error {
			// A read that unwinds, as one under a stopped task's frame does.
			_, err := readerInput{}.Read(make([]byte, 1))
			return err
		}(), closed},
		{"a chunked encoding that does not decode", decodedFailure("<chunked>", "the chunked encoding is malformed"),
			errors.New("malformed chunked encoding"),
			rt.IOError{Tag: rt.TagIOCorrupt, Path: "<chunked>", Reason: "the chunked encoding is malformed"}},
		{"a part that does not decode", decodedFailure("<part>", "the part's encoding is malformed"),
			fmt.Errorf("quotedprintable: invalid hex byte 0x3d"),
			rt.IOError{Tag: rt.TagIOCorrupt, Path: "<part>", Reason: "the part's encoding is malformed"}},
		{"a decoded stream cut short", decodedFailure("<part>", "the part's encoding is malformed"),
			io.ErrUnexpectedEOF, rt.IOError{Tag: rt.TagIOTruncated, Path: "<part>"}},
	} {
		if got := c.fail("read", c.err); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.what, got, c.want)
		}
	}
}

// A TLS connection whose record is changed in flight fails at both ends, as
// Go writes the errors: the end that reads the record finds it does not
// authenticate (Corrupt) and sends the peer an alert, and the peer reads
// the alert (Reset). It holds tlsPeerAlert and tlsOwnAlert to crypto/tls.
func TestStreamFailure_TLSAlertsOnAConnection(t *testing.T) {
	pair := strsOf(TestCertificate())
	cert, err := tls.X509KeyPair([]byte(pair[0]), []byte(pair[1]))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(pair[0]))
	near, far := net.Pipe()
	wire := &flipping{Conn: near}
	client := tls.Client(wire, &tls.Config{RootCAs: roots, ServerName: "localhost"})
	server := tls.Server(far, &tls.Config{Certificates: []tls.Certificate{cert}, SessionTicketsDisabled: true})
	// The pipe's ends, not the TLS connections: a TLS close sends an alert
	// no one reads, and waits five seconds for it.
	defer near.Close()
	defer far.Close()
	shaken := make(chan error, 1)
	go func() { shaken <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-shaken; err != nil {
		t.Fatalf("server handshake: %v", err)
	}

	read := make(chan rt.Result[rt.Option[rt.Bytes], rt.IOError], 1)
	go func() { read <- rt.ReaderRead(nil, connWith(context.Background(), server).r, rt.SmallInt(16)) }()
	wire.flip.Store(true)
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	peer := rt.ReaderRead(nil, connWith(context.Background(), client).r, rt.SmallInt(16))
	own := <-read

	corrupt := rt.IOError{Tag: rt.TagIOCorrupt, Path: "<connection>", Reason: "a TLS record is not valid: bad record MAC"}
	if own.Tag == rt.TagOk || own.Err != corrupt {
		t.Errorf("reading a changed record = %+v, want Err(%+v)", own, corrupt)
	}
	if reset := (rt.IOError{Tag: rt.TagIOReset, Path: "<connection>"}); peer.Tag == rt.TagOk || peer.Err != reset {
		t.Errorf("reading the peer's alert = %+v, want Err(%+v)", peer, reset)
	}
}

// flipping is a connection that, once flip is set, changes the last byte
// of each write.
type flipping struct {
	net.Conn
	flip atomic.Bool
}

func (f *flipping) Write(p []byte) (int, error) {
	if f.flip.Load() && len(p) > 0 {
		q := append([]byte(nil), p...)
		q[len(q)-1] ^= 0xff
		return f.Conn.Write(q)
	}
	return f.Conn.Write(p)
}

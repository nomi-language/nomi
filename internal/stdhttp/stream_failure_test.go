package stdhttp

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
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

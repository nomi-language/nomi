package hostadapt

import (
	"fmt"
	"io"

	"github.com/nomi-language/nomi/rt"
)

// A Go io.Reader or io.Writer crosses as std/io's Reader or Writer, and
// back (spec: "Go binding signatures project").
//
// Go to Nomi, the Go value becomes a handle's source or sink (rt.NewReader,
// rt.NewWriter), named "<go>" in an error, closed through its own Close
// when it has one. Nomi to Go, the handle is read or written through its
// own calls (rt.ReaderRead, rt.WriterWrite), so its buffer and its lock
// are the ones the program uses: what Go reads is what the program would
// have read next, and what Go writes lands in order with the program's
// writes. Those calls run on the frame of the binding's call; a Go value
// that keeps the stream past the call (a gzip.Reader over it) reads it
// later on that frame.

// goStreamName is the name an error about a stream Go made names.
const goStreamName = "<go>"

// ReaderFromGo is a Reader over r, a Go function's io.Reader result.
func ReaderFromGo(r io.Reader) (rt.Reader, error) {
	if r == nil {
		return rt.Reader{}, fmt.Errorf("the Go function returned a nil io.Reader")
	}
	src := rt.ReadSource{Name: goStreamName, R: r}
	if c, ok := r.(io.Closer); ok {
		src.Close = c.Close
	}
	return rt.NewReader(src), nil
}

// WriterFromGo is a Writer over w, a Go function's io.Writer result. It
// does not buffer: each write reaches w as it is made, and w buffers as it
// chooses (a gzip.Writer does until it is closed). Writer.close closes w
// when w has a Close.
func WriterFromGo(w io.Writer) (rt.Writer, error) {
	if w == nil {
		return rt.Writer{}, fmt.Errorf("the Go function returned a nil io.Writer")
	}
	sink := rt.WriteSink{Name: goStreamName, W: w, Unbuffered: true}
	if c, ok := w.(io.Closer); ok {
		sink.Close = c.Close
	}
	return rt.NewWriter(sink), nil
}

// GoReader is the io.ReadCloser a Go parameter typed io.Reader or
// io.ReadCloser receives for r. Close closes r.
func GoReader(fr *rt.Frame, r rt.Reader) io.ReadCloser { return &goReader{fr: fr, r: r} }

// GoWriter is the io.WriteCloser a Go parameter typed io.Writer or
// io.WriteCloser receives for w. Close flushes and closes w.
func GoWriter(fr *rt.Frame, w rt.Writer) io.WriteCloser { return &goWriter{fr: fr, w: w} }

type goReader struct {
	fr *rt.Frame
	r  rt.Reader
}

func (g *goReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	got := rt.ReaderRead(g.fr, g.r, int64(len(p)))
	if got.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: got.Err}
	}
	if got.Ok.Tag != rt.TagSome {
		return 0, io.EOF
	}
	return copy(p, got.Ok.Some), nil
}

func (g *goReader) Close() error {
	if res := rt.ReaderClose(g.r); res.Tag != rt.TagOk {
		return rt.IOFailure{E: res.Err}
	}
	return nil
}

type goWriter struct {
	fr *rt.Frame
	w  rt.Writer
}

func (g *goWriter) Write(p []byte) (int, error) {
	if res := rt.WriterWrite(g.fr, g.w, rt.Bytes(p)); res.Tag != rt.TagOk {
		return 0, rt.IOFailure{E: res.Err}
	}
	return len(p), nil
}

func (g *goWriter) Close() error {
	if res := rt.WriterClose(g.fr, g.w); res.Tag != rt.TagOk {
		return rt.IOFailure{E: res.Err}
	}
	return nil
}

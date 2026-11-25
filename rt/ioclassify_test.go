package rt

import (
	"context"
	"io"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

// failing is a source whose every read fails with err.
type failing struct{ err error }

func (f failing) Read([]byte) (int, error)  { return 0, f.err }
func (f failing) Write([]byte) (int, error) { return 0, f.err }

// A stream with no Fail hook and standard input fail through IOErrorOf, the
// table every stream's failure is sorted by: each of these was Other.
func TestIOErrorOf_StreamsWithNoHookAndStandardInputAreSorted(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want func(path string) IOError
	}{
		{"a closed file", fs.ErrClosed, func(string) IOError { return IOError{Tag: TagIOClosed} }},
		{"a stream cut short", io.ErrUnexpectedEOF, func(p string) IOError { return IOError{Tag: TagIOTruncated, Path: p} }},
		{"a broken pipe", syscall.EPIPE, func(string) IOError { return IOError{Tag: TagIOBrokenPipe} }},
		{"a deadline", os.ErrDeadlineExceeded, func(string) IOError { return IOError{Tag: TagIOTimedOut} }},
		{"a full device", &fs.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}, func(p string) IOError { return IOError{Tag: TagIONoSpace, Path: p} }},
	}
	fr := NewFrame(context.Background())
	for _, c := range cases {
		r := NewReader(ReadSource{Name: "<source>", R: failing{c.err}})
		if got := ReaderRead(fr, r, SmallInt(8)); got.Tag != TagErr || got.Err != c.want("<source>") {
			t.Errorf("%s, read with no hook: %+v, want %+v", c.name, got.Err, c.want("<source>"))
		}
		w := NewWriter(WriteSink{Name: "<sink>", W: failing{c.err}, Unbuffered: true})
		if got := WriterWrite(fr, w, Bytes("x")); got.Tag != TagErr || got.Err != c.want("<sink>") {
			t.Errorf("%s, write with no hook: %+v, want %+v", c.name, got.Err, c.want("<sink>"))
		}
		in := NewInput(failing{c.err})
		if got := in.readLine(); got.Tag != TagErr || got.Err != c.want(StdinPath) {
			t.Errorf("%s, standard input: %+v, want %+v", c.name, got.Err, c.want(StdinPath))
		}
	}
}

package stdio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// timeoutError is an error that says it is a timeout as net.Error does,
// as net/http's client timeout and a context's deadline do.
type timeoutError struct{}

func (timeoutError) Error() string { return "timed out" }
func (timeoutError) Timeout() bool { return true }

// The sentinel errors of io, os and context that a stream's read or write
// returns are each the io.Error variant that names what happened. Each of
// io.ErrClosedPipe, io.ErrUnexpectedEOF and a timeout that is not an
// os deadline was Other with the code "unknown".
func TestClassify_SentinelErrorsAreTheirVariants(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want rt.IOError
	}{
		{"a closed file", fs.ErrClosed, rt.IOError{Tag: rt.TagIOClosed}},
		{"a closed pipe", io.ErrClosedPipe, rt.IOError{Tag: rt.TagIOClosed}},
		{"a closed pipe, wrapped", &fs.PathError{Op: "write", Path: "p", Err: io.ErrClosedPipe}, rt.IOError{Tag: rt.TagIOClosed}},
		{"a stream that ends early", io.ErrUnexpectedEOF, rt.IOError{Tag: rt.TagIOTruncated, Path: "<request>"}},
		{"a stream that ends early, wrapped", fmt.Errorf("reading: %w", io.ErrUnexpectedEOF), rt.IOError{Tag: rt.TagIOTruncated, Path: "<request>"}},
		{"a stream's deadline", os.ErrDeadlineExceeded, rt.IOError{Tag: rt.TagIOTimedOut}},
		{"a context's deadline", context.DeadlineExceeded, rt.IOError{Tag: rt.TagIOTimedOut}},
		{"a timeout of another package", fmt.Errorf("get: %w", timeoutError{}), rt.IOError{Tag: rt.TagIOTimedOut}},
		{"an errno Go calls a timeout", syscall.EAGAIN, rt.IOError{Tag: rt.TagIOOther, Operation: "read", Path: "<request>", Code: "EAGAIN"}},
		{"an error with no code", errors.New("something else"), rt.IOError{Tag: rt.TagIOOther, Operation: "read", Path: "<request>", Code: "unknown"}},
	} {
		if got := Classify("read", "<request>", c.err); got != c.want {
			t.Errorf("%s: Classify(%v) = %+v, want %+v", c.name, c.err, got, c.want)
		}
	}
}

package rt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Input is a program's standard input: the reader `io.read_line` reads a line
// at a time. One Input is shared by every frame a machine builds, so its
// buffer is too: a line read in one task is not read again in another.
//
// # Cancellation
//
// A read of standard input may wait for input that never comes, and an
// operating system read of a pipe or terminal cannot be interrupted. So the
// reads of anything that may block (every source but data already in memory)
// happen on a goroutine of the Input's own (pump), one at a time when a read
// asks, and the read that asked waits for the answer or for its frame's
// context to end, whichever is first. A read whose task is cancelled, whose
// `concurrent` scope is torn down, or whose case runs out of time unwinds as
// a cancelled channel receive does (raiseCanceled), and the bytes the pump
// reads later are kept for the next read. Nothing read is lost: a line is
// taken out of the buffer only once it is whole. The goroutine left blocked
// in the operating system read keeps no program waiting: a process exits
// without waiting for it.
type Input struct {
	// lock is held by the one read in progress; it is a channel so a read
	// waiting for it can give up when its frame is cancelled.
	lock chan struct{}
	// buf holds what was read and not yet answered.
	buf []byte
	// end is the error that ended the source (io.EOF, or the failure), once
	// buf holds everything before it.
	end error
	// src answers the next chunk of the source.
	src func(ctx context.Context) ([]byte, error)
	// file is the source when it is a file, for a question about its
	// device (StandardFile), or nil.
	file *os.File
}

// NewInput wraps r as a program's input.
func NewInput(r io.Reader) *Input {
	in := &Input{lock: make(chan struct{}, 1)}
	in.file, _ = r.(*os.File)
	switch r.(type) {
	case *strings.Reader, *bytes.Reader:
		// Data in memory never blocks, so it is read where it is asked for.
		in.src = func(context.Context) ([]byte, error) {
			chunk := make([]byte, 64<<10)
			n, err := r.Read(chunk)
			return chunk[:n], err
		}
	default:
		in.src = pump(r)
	}
	return in
}

// pump answers each ask for a chunk of r from a goroutine that reads r, so
// the asker can stop waiting. An answer that arrives after its asker gave up
// is the next ask's.
func pump(r io.Reader) func(ctx context.Context) ([]byte, error) {
	type answer struct {
		chunk []byte
		err   error
	}
	asks := make(chan struct{})
	answers := make(chan answer, 1)
	started, waiting := false, false
	return func(ctx context.Context) ([]byte, error) {
		if !started {
			started = true
			go func() {
				for range asks {
					chunk := make([]byte, 64<<10)
					n, err := r.Read(chunk)
					answers <- answer{chunk[:n], err}
				}
			}()
		}
		if !waiting {
			asks <- struct{}{}
			waiting = true
		}
		select {
		case a := <-answers:
			waiting = false
			return a.chunk, a.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type inputKey struct{}

// WithInput answers ctx carrying in, for every frame built over it. A frame
// over a context with no Input reads end of input at once.
func WithInput(ctx context.Context, in *Input) context.Context {
	return context.WithValue(ctx, inputKey{}, in)
}

// StdinPath is the path an io.Error about standard input names.
const StdinPath = "<stdin>"

// ReadLine is `io.read_line`: the next line of the frame's input with its line
// ending stripped, Ok(None) when no input remains, or the io.Error that
// stopped the read: InvalidText for a line that is not UTF-8. A final line
// with no newline is still a line. Inside `io.capture` the input is the
// capture's. A read whose frame is cancelled while it waits unwinds as the
// cancellation.
func ReadLine(fr *Frame) Result[Option[string], IOError] {
	var in *Input
	if fr != nil {
		if c := captureFor(fr); c != nil {
			return c.readLine()
		}
		if fr.ctx != nil {
			in, _ = fr.ctx.Value(inputKey{}).(*Input)
		}
	}
	if in == nil {
		return Ok[Option[string], IOError](None[string]())
	}
	return in.readLineOn(fr)
}

// ctxOf is the context a read on fr waits under.
func ctxOf(fr *Frame) context.Context {
	if fr == nil || fr.ctx == nil {
		return context.Background()
	}
	return fr.ctx
}

// acquire takes in's lock, or raises fr's cancellation if it ends first.
func (in *Input) acquire(fr *Frame) {
	select {
	case in.lock <- struct{}{}:
	case <-ctxOf(fr).Done():
		raiseCanceled(fr)
	}
}

func (in *Input) release() { <-in.lock }

// fill reads the next chunk into buf, or records the end. A cancelled wait
// raises fr's cancellation with buf as it was. The lock is held.
func (in *Input) fill(fr *Frame) { in.fillWithin(fr, -1) }

// fillWithin is fill waiting at most within, or as long as it takes when
// within is negative. It reports false when nothing arrived in time; what
// arrives later is the next fill's. The lock is held.
func (in *Input) fillWithin(fr *Frame, within time.Duration) bool {
	ctx := ctxOf(fr)
	wait := ctx
	if within >= 0 {
		var cancel context.CancelFunc
		wait, cancel = context.WithTimeout(ctx, within)
		defer cancel()
	}
	chunk, err := in.src(wait)
	if err != nil && wait.Err() != nil && errors.Is(err, wait.Err()) {
		if ctx.Err() != nil {
			raiseCanceled(fr)
		}
		return false
	}
	in.buf = append(in.buf, chunk...)
	if err != nil {
		in.end = err
	}
	return true
}

// failure is the io.Error for the source's end, or nil at its end of input.
func (in *Input) failure(op string) *IOError {
	if in.end == nil || errors.Is(in.end, io.EOF) {
		return nil
	}
	return &IOError{Tag: TagIOOther, Operation: op, Path: StdinPath, Code: ErrnoName(in.end)}
}

// readLine reads the next line of in, waiting under no frame: a capture's
// input, which is in memory.
func (in *Input) readLine() Result[Option[string], IOError] { return in.readLineOn(nil) }

// readLineOn reads the next line of in on fr.
func (in *Input) readLineOn(fr *Frame) Result[Option[string], IOError] {
	in.acquire(fr)
	defer in.release()
	for {
		if i := bytes.IndexByte(in.buf, '\n'); i >= 0 {
			line := string(in.buf[:i])
			in.buf = in.buf[i+1:]
			return lineRead(strings.TrimSuffix(line, "\r"))
		}
		if in.end != nil {
			if f := in.failure("read"); f != nil {
				return Err[Option[string]](*f)
			}
			if len(in.buf) == 0 {
				return Ok[Option[string], IOError](None[string]())
			}
			line := string(in.buf)
			in.buf = nil
			return lineRead(strings.TrimSuffix(line, "\r"))
		}
		in.fill(fr)
	}
}

// readSome answers up to max bytes of in on fr: what is buffered, or one
// chunk's worth when nothing is, or Ok(None) at the end.
func (in *Input) readSome(fr *Frame, max int64) Result[Option[Bytes], IOError] {
	in.acquire(fr)
	defer in.release()
	for len(in.buf) == 0 {
		if in.end != nil {
			if f := in.failure("read"); f != nil {
				return Err[Option[Bytes]](*f)
			}
			return Ok[Option[Bytes], IOError](None[Bytes]())
		}
		in.fill(fr)
	}
	n := len(in.buf)
	if int64(n) > max {
		n = int(max)
	}
	out := Bytes(in.buf[:n])
	in.buf = in.buf[n:]
	return Ok[Option[Bytes], IOError](Some(out))
}

// StdinReadByte is the next byte of the frame's standard input, read from
// the buffer `io.read_line` reads, for std::terminal's keys: the byte, -1
// when none arrived within `within` (a negative within waits as long as it
// takes), or -2 at the end of the input. A frame with no input is at its
// end. Inside `io.capture` it reads the capture's input.
func StdinReadByte(fr *Frame, within time.Duration) Result[int64, IOError] {
	in, replay := stdinInput(fr)
	if replay != nil || in == nil {
		return Ok[int64, IOError](-2)
	}
	in.acquire(fr)
	defer in.release()
	for len(in.buf) == 0 {
		if in.end != nil {
			if f := in.failure("read"); f != nil {
				return Err[int64](*f)
			}
			return Ok[int64, IOError](-2)
		}
		if !in.fillWithin(fr, within) {
			return Ok[int64, IOError](-1)
		}
	}
	b := in.buf[0]
	in.buf = in.buf[1:]
	return Ok[int64, IOError](int64(b))
}

// lineRead is a line read from standard input as `io.read_line` answers it:
// the line, or InvalidText when it is not UTF-8.
func lineRead(line string) Result[Option[string], IOError] {
	if !utf8.ValidString(line) {
		return Err[Option[string]](IOError{Tag: TagIOInvalidText, Path: StdinPath, Offset: SmallInt(int64(InvalidUTF8Offset(line)))})
	}
	return Ok[Option[string], IOError](Some(line))
}

// InvalidUTF8Offset is the byte offset of the first sequence in s that is
// not UTF-8, or -1 when s is UTF-8.
func InvalidUTF8Offset(s string) int {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return -1
}

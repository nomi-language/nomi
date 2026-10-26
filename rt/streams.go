package rt

import (
	"bufio"
	"context"
	"errors"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// std/io's `Reader` and `Writer`: the standard streams, and streams over
// files and over data the program holds.
//
// A machine puts its output and error writers on the context its frames are
// built over (WithStreams), so a write from an adapter (`Writer.write`) and a
// write from the VM's own `io.print` reach one serialized writer and keep
// their order. A write goes to the `io.capture` in force first
// (CaptureWrite, CaptureErrorWrite).
//
// A stream over anything else is a handle (NewReader, NewWriter): rt holds
// its buffer and its state, and the thing it reads or writes is an
// io.Reader or io.Writer its maker hands in, so rt makes no file system call
// itself (internal/stdio opens the files). The handle's value never
// changes; reading or writing it moves the stream it names on. Each call
// holds the handle's lock, so two tasks sharing one handle never interleave
// inside a call.
//
// # Closing
//
// `close` answers a Result, since a failed close of a written file can lose
// data. A handle nobody closes is closed when the program no longer holds
// it (a cleanup the Go runtime runs), and a writer's buffer is flushed when
// the program ends (FlushOpenWriters), as a process exit flushes Go's and
// C's buffered output.

// Reader is std/io's `Reader`: a stream bytes are read from. The value names
// the stream and never changes.
type Reader struct {
	stream stream
	h      *readHandle
}

// Writer is std/io's `Writer`: a stream bytes are written to.
type Writer struct {
	stream stream
	h      *writeHandle
}

type stream uint8

const (
	streamStdin stream = iota + 1
	streamStdout
	streamStderr
	// streamHandle is a Reader or Writer over a handle (NewReader,
	// NewWriter).
	streamHandle
)

// IOFailure is an IOError as a Go error, so a source or sink a handle reads
// or writes (an in-memory file) can fail with the io.Error it means, which
// the handle answers as it is.
type IOFailure struct{ E IOError }

func (f IOFailure) Error() string { return "io.Error " + f.E.Operation + " " + f.E.Path }

// ReadSource is what a Reader that is not standard input reads.
type ReadSource struct {
	// Name is the path an error about the stream names.
	Name string
	// R is the bytes.
	R io.Reader
	// Close releases what R reads, or is nil when nothing needs releasing.
	Close func() error
	// Fail is the io.Error for err, a failure of operation op (`read`,
	// `close`), when err is not an IOFailure. Nil makes such a failure Other.
	Fail func(op string, err error) IOError
}

// WriteSink is what a Writer that is not standard output or error writes.
type WriteSink struct {
	// Name is the path an error about the stream names.
	Name string
	// W receives the bytes, buffered unless Unbuffered is set.
	W io.Writer
	// Close commits what was written once the buffer is flushed (an atomic
	// write renames its temporary file here), or is nil.
	Close func() error
	// Abort gives the write up instead of committing it: an atomic write
	// removes its temporary file. Nil makes an abort a close.
	Abort func()
	// Fail is as ReadSource's.
	Fail func(op string, err error) IOError
	// Unbuffered sends each write to W as it is made.
	Unbuffered bool
}

// readHandle is a Reader's state behind the value. The cleanup the Go
// runtime runs once no Reader holds it closes state.
type readHandle struct{ st *readState }

type readState struct {
	mu     sync.Mutex
	src    ReadSource
	r      *bufio.Reader
	closed bool
	// held marks a reader over data the program holds (from_bytes,
	// from_text), which reads nothing outside the machine.
	held bool
	// pull is the source of a reader whose chunks the program makes as
	// they are read (from_fn, from_chunks), or nil.
	pull *pullReader
}

// writeHandle is a Writer's state behind the value, as readHandle is.
type writeHandle struct{ st *writeState }

type writeState struct {
	mu     sync.Mutex
	sink   WriteSink
	w      *bufio.Writer
	closed bool
}

// openWriters holds every writer state not yet closed, for FlushOpenWriters.
// It holds the state, not the handle, so it keeps no Writer alive.
var openWriters sync.Map // *writeState -> struct{}

// NewReader is a Reader over src.
func NewReader(src ReadSource) Reader {
	st := &readState{src: src, r: bufio.NewReaderSize(src.R, 64<<10)}
	h := &readHandle{st: st}
	runtime.AddCleanup(h, func(st *readState) { st.close() }, st)
	return Reader{stream: streamHandle, h: h}
}

// NewWriter is a Writer over sink.
func NewWriter(sink WriteSink) Writer {
	st := &writeState{sink: sink}
	if !sink.Unbuffered {
		st.w = bufio.NewWriterSize(sink.W, 64<<10)
	}
	h := &writeHandle{st: st}
	openWriters.Store(st, struct{}{})
	runtime.AddCleanup(h, func(st *writeState) { st.discard() }, st)
	return Writer{stream: streamHandle, h: h}
}

// FlushOpenWriters flushes every writer the program has not closed, so what
// it wrote reaches the file when the program ends. The machine calls it as a
// program finishes.
func FlushOpenWriters() {
	openWriters.Range(func(k, _ any) bool {
		st := k.(*writeState)
		st.mu.Lock()
		if !st.closed && st.w != nil {
			_ = st.w.Flush()
		}
		st.mu.Unlock()
		return true
	})
}

// fail is the io.Error a handle answers for err in operation op.
func fail(name, op string, err error, classify func(string, error) IOError) IOError {
	var f IOFailure
	if errors.As(err, &f) {
		return f.E
	}
	if classify != nil {
		return classify(op, err)
	}
	return IOError{Tag: TagIOOther, Operation: op, Path: name, Code: ErrnoName(err)}
}

func (st *readState) fail(op string, err error) IOError {
	return fail(st.src.Name, op, err, st.src.Fail)
}

func (st *writeState) fail(op string, err error) IOError {
	return fail(st.sink.Name, op, err, st.sink.Fail)
}

// close closes the source; the lock is taken here.
func (st *readState) close() Result[Unit, IOError] {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return Err[Unit](IOError{Tag: TagIOClosed})
	}
	st.closed = true
	if st.src.Close != nil {
		if err := st.src.Close(); err != nil {
			return Err[Unit](st.fail("close", err))
		}
	}
	return Ok[Unit, IOError](Unit{})
}

// close flushes and commits the sink.
func (st *writeState) close() Result[Unit, IOError] {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return Err[Unit](IOError{Tag: TagIOClosed})
	}
	st.closed = true
	openWriters.Delete(st)
	if st.w != nil {
		if err := st.w.Flush(); err != nil {
			if st.sink.Abort != nil {
				st.sink.Abort()
			} else if st.sink.Close != nil {
				_ = st.sink.Close()
			}
			return Err[Unit](st.fail("write", err))
		}
	}
	if st.sink.Close != nil {
		if err := st.sink.Close(); err != nil {
			return Err[Unit](st.fail("close", err))
		}
	}
	return Ok[Unit, IOError](Unit{})
}

// discard ends a write the program gave up: an atomic write leaves the old
// file as it was. Any other write is closed. A closed writer is left alone.
func (st *writeState) discard() {
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		return
	}
	if st.sink.Abort == nil {
		st.mu.Unlock()
		_ = st.close()
		return
	}
	st.closed = true
	openWriters.Delete(st)
	st.sink.Abort()
	st.mu.Unlock()
}

type streamsKey struct{}

type streams struct{ out, err io.Writer }

// WithStreams answers ctx carrying the program's output and error writers,
// for every frame built over it. Either may be nil: output then goes nowhere
// and errors to the diagnostic output (WithDiagnosticOutput) or os.Stderr.
func WithStreams(ctx context.Context, out, err io.Writer) context.Context {
	return context.WithValue(ctx, streamsKey{}, streams{out: out, err: err})
}

func streamsOf(fr *Frame) streams {
	if fr != nil && fr.ctx != nil {
		if s, ok := fr.ctx.Value(streamsKey{}).(streams); ok {
			return s
		}
	}
	return streams{}
}

// WriteError writes s to the frame's standard error: the `io.capture` in
// force, else the program's error writer. It is `io.eprint` and `io.ewrite`
// once rendered.
func WriteError(fr *Frame, s string) {
	if CaptureErrorWrite(fr, s) {
		return
	}
	w := streamsOf(fr).err
	if w == nil {
		var ctx context.Context
		if fr != nil {
			ctx = fr.ctx
		}
		w = diagnosticOutput(ctx)
	}
	_, _ = io.WriteString(w, s)
}

// writeOutput writes s to the frame's standard output: the `io.capture` in
// force, else the program's output writer.
func writeOutput(fr *Frame, s string) {
	if CaptureWrite(fr, s) {
		return
	}
	if w := streamsOf(fr).out; w != nil {
		_, _ = io.WriteString(w, s)
	}
}

// IOStdin is `io.stdin`, a marker that names standard input: a read finds
// the frame's input (an `io.capture` in force, else the program's) when it
// is made, not when the value was made.
func IOStdin() Reader { return Reader{stream: streamStdin} }

// IOStdout is `io.stdout`, a marker as IOStdin is.
func IOStdout() Writer { return Writer{stream: streamStdout} }

// IOStderr is `io.stderr`, a marker as IOStdin is.
func IOStderr() Writer { return Writer{stream: streamStderr} }

// ReaderFromBytes is `Reader.from_bytes`: a Reader over data the program
// holds.
func ReaderFromBytes(data Bytes) Reader {
	r := NewReader(ReadSource{Name: "<bytes>", R: strings.NewReader(string(data))})
	r.h.st.held = true
	return r
}

// ReaderFromText is `Reader.from_text`.
func ReaderFromText(text string) Reader {
	r := NewReader(ReadSource{Name: "<text>", R: strings.NewReader(text)})
	r.h.st.held = true
	return r
}

// Standard reports whether r is standard input.
func (r Reader) Standard() bool { return r.stream == streamStdin }

// Held reports whether r reads data the program holds (Reader.from_bytes,
// Reader.from_text) rather than something outside the machine.
func (r Reader) Held() bool { return r.stream == streamHandle && r.h.st.held }

// Standard reports whether w is standard output or standard error.
func (w Writer) Standard() bool { return w.stream == streamStdout || w.stream == streamStderr }

// ReaderReadLine is `Reader.read_line`: the next line without its `\n` and a
// `\r` before it, or Ok(None) at the end. On standard input it reads what
// `io.read_line` reads.
func ReaderReadLine(fr *Frame, r Reader) Result[Maybe[string], IOError] {
	switch r.stream {
	case streamStdin:
		return ReadLine(fr)
	case streamHandle:
		st := r.h.st
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.closed {
			return Err[Maybe[string]](IOError{Tag: TagIOClosed})
		}
		st.at(fr)
		line, err := st.r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return Err[Maybe[string]](st.fail("read", err))
		}
		if err != nil && line == "" {
			return Ok[Maybe[string], IOError](None[string]())
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if !utf8.ValidString(line) {
			return Err[Maybe[string]](IOError{Tag: TagIOInvalidText, Path: st.src.Name, Offset: int64(InvalidUTF8Offset(line))})
		}
		return Ok[Maybe[string], IOError](Some(line))
	}
	return Err[Maybe[string]](IOError{Tag: TagIOClosed})
}

// ReaderRead is `Reader.read`: up to max bytes, at least one unless the
// stream is at its end, where it answers Ok(None).
func ReaderRead(fr *Frame, r Reader, max int64) Result[Maybe[Bytes], IOError] {
	if max <= 0 {
		Trap("Reader.read: max is " + strconv.FormatInt(max, 10) + "; it must be at least 1")
	}
	switch r.stream {
	case streamStdin:
		return stdinRead(fr, max)
	case streamHandle:
		st := r.h.st
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.closed {
			return Err[Maybe[Bytes]](IOError{Tag: TagIOClosed})
		}
		st.at(fr)
		return readSome(st.r, max, st.fail)
	}
	return Err[Maybe[Bytes]](IOError{Tag: TagIOClosed})
}

// readSome reads up to max bytes from r: what is buffered, or one read's
// worth when nothing is.
func readSome(r *bufio.Reader, max int64, failure func(string, error) IOError) Result[Maybe[Bytes], IOError] {
	n := int(max)
	if b := r.Buffered(); b > 0 && b < n {
		n = b
	}
	buf := make([]byte, n)
	for {
		got, err := r.Read(buf)
		if got > 0 {
			return Ok[Maybe[Bytes], IOError](Some(Bytes(buf[:got])))
		}
		if errors.Is(err, io.EOF) {
			return Ok[Maybe[Bytes], IOError](None[Bytes]())
		}
		if err != nil {
			return Err[Maybe[Bytes]](failure("read", err))
		}
	}
}

// ReaderReadAll is `Reader.read_all`: everything left.
func ReaderReadAll(fr *Frame, r Reader) Result[Bytes, IOError] {
	switch r.stream {
	case streamStdin:
		return stdinReadAll(fr)
	case streamHandle:
		st := r.h.st
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.closed {
			return Err[Bytes](IOError{Tag: TagIOClosed})
		}
		st.at(fr)
		data, err := io.ReadAll(st.r)
		if err != nil {
			return Err[Bytes](st.fail("read", err))
		}
		return Ok[Bytes, IOError](Bytes(data))
	}
	return Err[Bytes](IOError{Tag: TagIOClosed})
}

// ReaderReadAllText is `Reader.read_all_text`: everything left as text, or
// InvalidText with the offset of the first byte that is not UTF-8, counted
// from where the read began.
func ReaderReadAllText(fr *Frame, r Reader) Result[string, IOError] {
	data := ReaderReadAll(fr, r)
	if data.Tag != TagOk {
		return Err[string](data.Err)
	}
	if !utf8.ValidString(string(data.Ok)) {
		return Err[string](IOError{Tag: TagIOInvalidText, Path: r.name(), Offset: int64(InvalidUTF8Offset(string(data.Ok)))})
	}
	return Ok[string, IOError](string(data.Ok))
}

// name is the path an error about r names.
func (r Reader) name() string {
	if r.stream == streamHandle {
		return r.h.st.src.Name
	}
	return StdinPath
}

// ReaderCopyTo is `Reader.copy_to`: everything left in r written to w, and
// the count of bytes copied.
func ReaderCopyTo(fr *Frame, r Reader, w Writer) Result[int64, IOError] {
	var total int64
	for {
		chunk := ReaderRead(fr, r, 64<<10)
		if chunk.Tag != TagOk {
			return Err[int64](chunk.Err)
		}
		if chunk.Ok.Tag != TagSome {
			return Ok[int64, IOError](total)
		}
		if res := WriterWrite(fr, w, chunk.Ok.Some); res.Tag != TagOk {
			return Err[int64](res.Err)
		}
		total += int64(len(chunk.Ok.Some))
	}
}

// ReaderClose is `Reader.close`. Closing standard input does nothing; a
// handle closed twice answers Closed.
func ReaderClose(r Reader) Result[Unit, IOError] {
	if r.stream == streamHandle {
		return r.h.st.close()
	}
	return Ok[Unit, IOError](Unit{})
}

// WriterWrite is `Writer.write`: data as it is.
func WriterWrite(fr *Frame, w Writer, data Bytes) Result[Unit, IOError] {
	return WriterWriteText(fr, w, string(data))
}

// WriterWriteText is `Writer.write_text`, which `Writer.print` writes
// through.
func WriterWriteText(fr *Frame, w Writer, text string) Result[Unit, IOError] {
	switch w.stream {
	case streamStdout:
		writeOutput(fr, text)
	case streamStderr:
		WriteError(fr, text)
	case streamHandle:
		st := w.h.st
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.closed {
			return Err[Unit](IOError{Tag: TagIOClosed})
		}
		var err error
		if st.w != nil {
			_, err = st.w.WriteString(text)
		} else {
			_, err = io.WriteString(st.sink.W, text)
		}
		if err != nil {
			return Err[Unit](st.fail("write", err))
		}
	default:
		return Err[Unit](IOError{Tag: TagIOClosed})
	}
	return Ok[Unit, IOError](Unit{})
}

// WriterFlush is `Writer.flush`: what the writer buffered reaches what it
// writes to. The standard streams are never buffered here.
func WriterFlush(w Writer) Result[Unit, IOError] {
	if w.stream != streamHandle {
		return Ok[Unit, IOError](Unit{})
	}
	st := w.h.st
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return Err[Unit](IOError{Tag: TagIOClosed})
	}
	if st.w != nil {
		if err := st.w.Flush(); err != nil {
			return Err[Unit](st.fail("write", err))
		}
	}
	return Ok[Unit, IOError](Unit{})
}

// WriterClose is `Writer.close`: flush, then commit. Closing a standard
// stream does nothing.
func WriterClose(w Writer) Result[Unit, IOError] {
	if w.stream == streamHandle {
		return w.h.st.close()
	}
	return Ok[Unit, IOError](Unit{})
}

// WriterDiscard is std/io's private `discard`, which std/fs's `with_writer`
// defers: a writer still open when its body ends without an Ok is given up,
// so an atomic write leaves the old file as it was. A closed writer, and a
// standard stream, is left alone.
func WriterDiscard(w Writer) Unit {
	if w.stream == streamHandle {
		w.h.st.discard()
	}
	return Unit{}
}

// stdinInput is the input a byte read of standard input reads on fr: the
// open `io.capture`'s, or the program's. A replay's script is read a line at
// a time instead (stdinReplay).
func stdinInput(fr *Frame) (in *Input, replay *Capture) {
	if fr == nil {
		return nil, nil
	}
	if c := captureFor(fr); c != nil {
		if c.replay {
			return nil, c
		}
		return c.in, nil
	}
	if fr.ctx != nil {
		in, _ = fr.ctx.Value(inputKey{}).(*Input)
	}
	return in, nil
}

// stdinRead is `Reader.read` on standard input. Inside `io.replay` each read
// answers the script's next input line and its newline, which the transcript
// records as a line read.
func stdinRead(fr *Frame, max int64) Result[Maybe[Bytes], IOError] {
	in, replay := stdinInput(fr)
	if replay != nil {
		line := replay.readLine()
		if line.Tag != TagOk {
			return Err[Maybe[Bytes]](line.Err)
		}
		if line.Ok.Tag != TagSome {
			return Ok[Maybe[Bytes], IOError](None[Bytes]())
		}
		return Ok[Maybe[Bytes], IOError](Some(Bytes(line.Ok.Some + "\n")))
	}
	if in == nil {
		return Ok[Maybe[Bytes], IOError](None[Bytes]())
	}
	return in.readSome(fr, max)
}

// stdinReadAll is `Reader.read_all` on standard input.
func stdinReadAll(fr *Frame) Result[Bytes, IOError] {
	var out strings.Builder
	for {
		chunk := stdinRead(fr, 64<<10)
		if chunk.Tag != TagOk {
			return Err[Bytes](chunk.Err)
		}
		if chunk.Ok.Tag != TagSome {
			return Ok[Bytes, IOError](Bytes(out.String()))
		}
		out.WriteString(string(chunk.Ok.Some))
	}
}

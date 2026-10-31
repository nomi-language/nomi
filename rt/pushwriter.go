package rt

import "bytes"

// Writers whose bytes the program takes as they are written:
// `Writer.from_fn` (a function the writer hands each chunk to) and the
// in-memory writer `Writer.collect` gives its body.
//
// A from_fn writer's function is program code. It runs on the frame of the
// operation that sends the bytes on (pushWriter.fr, set under the handle's
// lock before each write, flush and close), so a chunk written inside
// `io.capture` prints into that capture, as a pulled reader's chunks do. A
// buffered writer sends bytes on when its buffer fills, on flush and on
// close; an unbuffered one on each write.
//
// The function must not write to the writer it is fed by: the write holds
// that writer's lock, so it would wait for itself. Writing to another
// writer is fine; each holds its own lock.
//
// Nothing runs program code once the program has stopped holding the
// writer or has ended: the runtime's cleanup and FlushOpenWriters leave a
// from_fn writer alone, so what it still buffers then is dropped. Close it.

// pushWriter is the io.Writer under a from_fn writer.
type pushWriter struct {
	push func(fr *Frame, chunk Bytes) Result[Unit, IOError]
	// fr is the frame of the operation being answered.
	fr *Frame
}

func (p *pushWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	got := p.push(p.fr, Bytes(b))
	if got.Tag != TagOk {
		return 0, IOFailure{E: got.Err}
	}
	return len(b), nil
}

// WriterFromFn is `Writer.from_fn`: a Writer that hands what is written to
// write, a chunk at a time, and calls closeFn when it is closed, after the
// last chunk. Buffered, write receives the buffer's contents as it fills and
// on flush and close, so the boundaries of the writes are not kept;
// unbuffered, it receives each write as it is made. An Err from either is
// the io.Error the operation answers.
func WriterFromFn(write func(fr *Frame, chunk Bytes) Result[Unit, IOError], closeFn func(fr *Frame) Result[Unit, IOError], unbuffered bool) Writer {
	p := &pushWriter{push: write}
	w := NewWriter(WriteSink{
		Name: "<fn>",
		W:    p,
		Close: func() error {
			if res := closeFn(p.fr); res.Tag != TagOk {
				return IOFailure{E: res.Err}
			}
			return nil
		},
		Unbuffered: unbuffered,
	})
	w.h.st.push = p
	return w
}

// WriterToMemory is the writer `Writer.collect` hands its body: an
// unbuffered writer into memory, which WriterCollected reads.
func WriterToMemory() Writer {
	w := NewWriter(WriteSink{Name: "<memory>", W: &bytes.Buffer{}, Unbuffered: true})
	w.h.st.held = true
	return w
}

// Held reports whether w writes into memory the program holds
// (WriterToMemory) rather than to something outside the machine.
func (w Writer) Held() bool { return w.stream == streamHandle && w.h.st.held }

// WriterCollected is everything written to w, a WriterToMemory writer, so
// far. Any other writer has nothing to answer.
func WriterCollected(w Writer) Bytes {
	if w.stream != streamHandle {
		return ""
	}
	st := w.h.st
	st.mu.Lock()
	defer st.mu.Unlock()
	if buf, ok := st.sink.W.(*bytes.Buffer); ok && st.held {
		return Bytes(buf.String())
	}
	return ""
}

// at points a from_fn writer's function at fr, the frame of the operation
// about to write through it. st.mu is held.
func (st *writeState) at(fr *Frame) {
	if st.push != nil {
		st.push.fr = fr
	}
}

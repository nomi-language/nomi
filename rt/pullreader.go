package rt

import (
	"io"
	"iter"
)

// Readers whose bytes the program makes as they are read: `Reader.from_fn`
// (a function the reader calls for each chunk) and `Reader.from_chunks` (an
// Iter of chunks the reader pulls one at a time).
//
// Each read of such a reader runs program code: the function, or the
// iterator's pipeline up to its next chunk. It runs on the frame of the read
// that needs the chunk (pullReader.fr, set under the handle's lock before
// each operation), so a chunk made inside `io.capture` prints into that
// capture. An iterator is pulled through a coroutine (iter.Pull), as `zip`
// pulls its right side; the coroutine keeps the frame of the read that
// started it.
//
// A pulled function must not read the reader it feeds: the read holds the
// reader's lock, so it would wait for itself.

// Pull is what a pulled reader calls for its next chunk on frame fr: Some
// chunk, None at the end, or the io.Error the read answers.
type Pull func(fr *Frame) Result[Maybe[Bytes], IOError]

// pullReader is the io.Reader under a pulled reader's buffer.
type pullReader struct {
	pull Pull
	// fr is the frame of the read being answered.
	fr   *Frame
	rest []byte
	done bool
}

func (p *pullReader) Read(b []byte) (int, error) {
	for len(p.rest) == 0 {
		if p.done {
			return 0, io.EOF
		}
		got := p.pull(p.fr)
		if got.Tag != TagOk {
			return 0, IOFailure{E: got.Err}
		}
		if got.Ok.Tag != TagSome {
			p.done = true
			return 0, io.EOF
		}
		p.rest = []byte(got.Ok.Some)
	}
	n := copy(b, p.rest)
	p.rest = p.rest[n:]
	return n, nil
}

// newPullReader is a Reader named name over pull, whose Close is closeFn
// (nil for none).
func newPullReader(name string, pull Pull, closeFn func() error) Reader {
	p := &pullReader{pull: pull}
	r := NewReader(ReadSource{Name: name, R: p, Close: closeFn})
	r.h.st.pull = p
	return r
}

// ReaderFromFn is `Reader.from_fn`: a Reader that calls next for each chunk
// it needs, on the frame of the read.
func ReaderFromFn(next func(fr *Frame) Result[Maybe[Bytes], IOError]) Reader {
	return newPullReader("<fn>", next, nil)
}

// ReaderFromEach is `Reader.from_chunks` once its Iter is a push loop: run
// drives the chunks into yield until yield answers false. The reader pulls
// one chunk per read that needs one; closing it stops the loop.
func ReaderFromEach(run func(fr *Frame, yield func(chunk Bytes) bool)) Reader {
	var next func() (Bytes, bool)
	var stop func()
	pull := func(fr *Frame) Result[Maybe[Bytes], IOError] {
		if next == nil {
			next, stop = iter.Pull(func(yield func(Bytes) bool) { run(fr, yield) })
		}
		chunk, ok := next()
		if !ok {
			return Ok[Maybe[Bytes], IOError](None[Bytes]())
		}
		return Ok[Maybe[Bytes], IOError](Some(chunk))
	}
	return newPullReader("<chunks>", pull, func() error {
		if stop != nil {
			stop()
		}
		return nil
	})
}

// at points a pulled reader's source at fr, the frame of the operation
// about to read it. st.mu is held.
func (st *readState) at(fr *Frame) {
	if st.pull != nil {
		st.pull.fr = fr
	}
}

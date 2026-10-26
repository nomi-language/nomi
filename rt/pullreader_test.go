package rt

import (
	"context"
	"runtime"
	"testing"
)

// A from_fn reader calls its function on the frame of each read that needs a
// chunk, skips empty chunks, and answers the function's io.Error as the
// read's.
func TestPullReaders_FromFnCallsBackOnTheReadsFrame(t *testing.T) {
	chunks := []Result[Maybe[Bytes], IOError]{
		Ok[Maybe[Bytes], IOError](Some(Bytes("on"))),
		Ok[Maybe[Bytes], IOError](Some(Bytes(""))),
		Ok[Maybe[Bytes], IOError](Some(Bytes("e\n"))),
		Err[Maybe[Bytes]](IOError{Tag: TagIOTimedOut}),
		Ok[Maybe[Bytes], IOError](Some(Bytes("two"))),
		Ok[Maybe[Bytes], IOError](None[Bytes]()),
	}
	var frames []*Frame
	r := ReaderFromFn(func(fr *Frame) Result[Maybe[Bytes], IOError] {
		frames = append(frames, fr)
		next := chunks[0]
		chunks = chunks[1:]
		return next
	})
	first, second := NewFrame(context.Background()), NewFrame(context.Background())
	if got := ReaderReadLine(first, r); got.Tag != TagOk || got.Ok.Some != "one" {
		t.Fatalf("first line: %+v", got)
	}
	if got := ReaderReadLine(second, r); got.Tag != TagErr || got.Err.Tag != TagIOTimedOut {
		t.Fatalf("second read: %+v, want TimedOut", got)
	}
	if got := ReaderReadAll(second, r); got.Tag != TagOk || string(got.Ok) != "two" {
		t.Fatalf("after the failure: %+v, want the chunks after it", got)
	}
	if len(frames) != 6 || frames[0] != first || frames[2] != first || frames[3] != second || frames[5] != second {
		t.Fatalf("called on the wrong frames: %v", frames)
	}
}

// A from_chunks reader pulls one chunk per read that needs one, so an
// endless source works, and closing it stops the source's coroutine.
func TestPullReaders_FromEachPullsLazilyAndCloseStopsTheSource(t *testing.T) {
	base := goroutinesSettled(t)
	for range 20 {
		pulled := 0
		stopped := false
		r := ReaderFromEach(func(fr *Frame, yield func(Bytes) bool) {
			for {
				pulled++
				if !yield(Bytes("y\n")) {
					stopped = true
					return
				}
			}
		})
		fr := NewFrame(context.Background())
		if got := ReaderReadLine(fr, r); got.Tag != TagOk || got.Ok.Some != "y" {
			t.Fatalf("read: %+v", got)
		}
		if got := ReaderClose(r); got.Tag != TagOk {
			t.Fatalf("close: %+v", got)
		}
		if !stopped || pulled > 64<<10/2+1 {
			t.Fatalf("stopped %v after %d chunks", stopped, pulled)
		}
	}
	runtime.GC()
	if after := goroutinesSettled(t); after > base+2 {
		t.Fatalf("goroutines went from %d to %d: a closed reader left its source parked", base, after)
	}
}

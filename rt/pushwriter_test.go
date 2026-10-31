package rt

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func okUnit() Result[Unit, IOError] { return Ok[Unit, IOError](Unit{}) }

// A from_fn writer calls its function on the frame of the operation that
// sends the bytes on: a buffered one on flush and close, with what
// accumulated; an unbuffered one on each write. close runs last.
func TestPushWriters_FromFnCallsBackOnTheOperationsFrame(t *testing.T) {
	type call struct {
		fr   *Frame
		what string
	}
	var calls []call
	write := func(fr *Frame, chunk Bytes) Result[Unit, IOError] {
		calls = append(calls, call{fr, string(chunk)})
		return okUnit()
	}
	closeFn := func(fr *Frame) Result[Unit, IOError] {
		calls = append(calls, call{fr, "close"})
		return okUnit()
	}
	first, second := NewFrame(context.Background()), NewFrame(context.Background())

	w := WriterFromFn(write, closeFn, false)
	WriterWriteText(first, w, "a")
	WriterWriteText(first, w, "b")
	if len(calls) != 0 {
		t.Fatalf("a buffered writer called back before a flush: %v", calls)
	}
	WriterFlush(second, w)
	WriterWriteText(first, w, "c")
	if got := WriterClose(first, w); got.Tag != TagOk {
		t.Fatalf("close: %+v", got)
	}
	want := []call{{second, "ab"}, {first, "c"}, {first, "close"}}
	if len(calls) != len(want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d is %v, want %v", i, calls[i], want[i])
		}
	}

	calls = nil
	u := WriterFromFn(write, closeFn, true)
	WriterWriteText(first, u, "x")
	WriterWriteText(second, u, "y")
	if len(calls) != 2 || calls[0] != (call{first, "x"}) || calls[1] != (call{second, "y"}) {
		t.Fatalf("unbuffered calls %v", calls)
	}
}

// An Err from the function is the io.Error the operation answers, and from
// close is close's. A buffered writer keeps a failed send's error, as a
// writer over a file does.
func TestPushWriters_AFailureIsTheOperationsError(t *testing.T) {
	fr := NewFrame(context.Background())
	failing := func(*Frame, Bytes) Result[Unit, IOError] {
		return Err[Unit](IOError{Tag: TagIOBrokenPipe})
	}
	closes := func(*Frame) Result[Unit, IOError] { return Err[Unit](IOError{Tag: TagIOTimedOut}) }

	u := WriterFromFn(failing, closes, true)
	if got := WriterWriteText(fr, u, "x"); got.Tag != TagErr || got.Err.Tag != TagIOBrokenPipe {
		t.Fatalf("unbuffered write: %+v, want BrokenPipe", got)
	}

	b := WriterFromFn(failing, closes, false)
	if got := WriterWriteText(fr, b, "x"); got.Tag != TagOk {
		t.Fatalf("a buffered write sends nothing yet: %+v", got)
	}
	if got := WriterFlush(fr, b); got.Tag != TagErr || got.Err.Tag != TagIOBrokenPipe {
		t.Fatalf("flush: %+v, want BrokenPipe", got)
	}
	if got := WriterWriteText(fr, b, "y"); got.Tag != TagErr || got.Err.Tag != TagIOBrokenPipe {
		t.Fatalf("a write after a failed send: %+v, want BrokenPipe", got)
	}

	ok := WriterFromFn(func(*Frame, Bytes) Result[Unit, IOError] { return okUnit() }, closes, false)
	if got := WriterClose(fr, ok); got.Tag != TagErr || got.Err.Tag != TagIOTimedOut {
		t.Fatalf("close: %+v, want TimedOut", got)
	}
	if got := WriterWriteText(fr, ok, "z"); got.Tag != TagErr || got.Err.Tag != TagIOClosed {
		t.Fatalf("a write after close: %+v, want Closed", got)
	}
}

// A from_fn writer's function may write to another writer, here one that
// is itself a from_fn writer, from several tasks at once; each holds only
// its own lock.
func TestPushWriters_AFunctionWritesToAnotherWriter(t *testing.T) {
	sink := WriterToMemory()
	relay := WriterFromFn(func(fr *Frame, chunk Bytes) Result[Unit, IOError] {
		return WriterWrite(fr, sink, chunk)
	}, func(*Frame) Result[Unit, IOError] { return okUnit() }, true)
	outer := WriterFromFn(func(fr *Frame, chunk Bytes) Result[Unit, IOError] {
		return WriterWrite(fr, relay, chunk)
	}, func(*Frame) Result[Unit, IOError] { return okUnit() }, false)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fr := NewFrame(context.Background())
			for range 100 {
				WriterWriteText(fr, outer, "ab")
			}
			WriterFlush(fr, outer)
		}()
	}
	wg.Wait()
	got := string(WriterCollected(sink))
	if len(got) != 8*100*2 || strings.Count(got, "ab") != 800 {
		t.Fatalf("collected %d bytes, %d pairs", len(got), strings.Count(got, "ab"))
	}
}

// Nothing runs a from_fn writer's function once the program is done with
// it: FlushOpenWriters and the cleanup leave its buffer alone.
func TestPushWriters_NoCallbackAfterTheProgram(t *testing.T) {
	called := false
	w := WriterFromFn(func(*Frame, Bytes) Result[Unit, IOError] {
		called = true
		return okUnit()
	}, func(*Frame) Result[Unit, IOError] {
		called = true
		return okUnit()
	}, false)
	WriterWriteText(NewFrame(context.Background()), w, "held")
	FlushOpenWriters()
	w.h.st.discard()
	if called {
		t.Fatal("the function ran after the program")
	}
}

// The memory writer holds what was written, and keeps it after close.
func TestPushWriters_MemoryWriterCollects(t *testing.T) {
	fr := NewFrame(context.Background())
	w := WriterToMemory()
	WriterWriteText(fr, w, "one ")
	WriterWrite(fr, w, Bytes("two"))
	WriterClose(fr, w)
	if got := string(WriterCollected(w)); got != "one two" {
		t.Fatalf("collected %q", got)
	}
	if got := WriterWriteText(fr, w, "late"); got.Tag != TagErr || got.Err.Tag != TagIOClosed {
		t.Fatalf("a write after close: %+v", got)
	}
}

package vm

import (
	"errors"
	"testing"
)

// playHeap makes readHeap answer read's heap for the rest of the test.
func playHeap(t *testing.T, read func() heap) {
	saved := readHeap
	t.Cleanup(func() { readHeap = saved })
	readHeap = read
}

// A run that doubles what it holds every step is stopped near its memory
// bound even when a collection frees, mid-run, garbage the heap held when
// the run started. The heap's growth then reads below zero, and a read
// scheduled from the growth comes 26 steps later (a 64 MiB bound over one
// byte), by which time the run holds 2^26 times what it held: the
// vmhost macro sandbox's `grow(s + s)` reached gigabytes that way.
// The heap is played here, so a failure costs no memory.
func TestMemoryLimit_StopsADoublingRunAfterACollectionFreesGarbage(t *testing.T) {
	const (
		bound   = 64 << 20
		garbage = 256 << 20
		freedAt = 3
	)
	// held is what the run holds, all of it allocated by the run.
	var step, held, allocated int64
	playHeap(t, func() heap {
		objects := held
		if step < freedAt {
			objects += garbage
		}
		return heap{objects: objects, allocated: allocated}
	})
	f := newFuel(Limits{Steps: 1 << 40, Memory: bound})
	held, allocated = 1<<10, 1<<10
	var err error
	for ; step < 64; step++ {
		if err = f.burn(); err != nil {
			break
		}
		allocated += 2 * held
		held *= 2
	}
	if !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("after %d steps the run holds %d bytes and burn answers %v, want ErrMemoryLimit", step, held, err)
	}
	if most := int64(2 * (bound + garbage)); held > most {
		t.Errorf("the run was stopped holding %d bytes, more than twice the bound and the freed garbage (%d)", held, most)
	}
}

// A run that makes garbage every step, which collections free, reads the
// heap as seldom as what it holds allows, however much it allocated in
// all and however large the rest of the heap is: a read costs a few
// hundred nanoseconds, more than a step.
func TestMemoryLimit_GarbageDoesNotMakeEveryStepReadTheHeap(t *testing.T) {
	const (
		bound = 64 << 20
		live  = 512 << 20
		held  = 1 << 10
		steps = 10_000
	)
	var allocated, reads int64
	playHeap(t, func() heap {
		reads++
		return heap{objects: live + held, allocated: allocated, live: live}
	})
	f := newFuel(Limits{Steps: 1 << 40, Memory: bound})
	for range steps {
		if err := f.burn(); err != nil {
			t.Fatalf("burn answers %v for a run that holds %d bytes", err, held)
		}
		allocated += 1 << 20
	}
	if reads > steps/8 {
		t.Errorf("%d steps that each made 1 MiB of garbage read the heap %d times", steps, reads)
	}
}

// A read of the heap allocates nothing: a run that reads it every few
// steps would otherwise grow the heap with its reads, and be stopped for
// memory the reads took.
func TestMemoryLimit_ReadingTheHeapAllocatesNothing(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { readHeap() }); n != 0 {
		t.Errorf("a read of the heap allocates %v times", n)
	}
}

//go:build !js

package stdwasm

import "testing"

// refusedGrowthTrap is the trap of a call that asked memory to grow past
// the limit and then trapped: wazero's allocator sees the refusal.
var refusedGrowthTrap = []string{"memory_limit"}

// emptySpare drops every buffer spare holds and answers how many it has
// handed out.
func emptySpare() uint64 {
	spare.mu.Lock()
	defer spare.mu.Unlock()
	spare.bufs, spare.held = nil, 0
	return spare.reused
}

func reusedSoFar() uint64 {
	spare.mu.Lock()
	defer spare.mu.Unlock()
	return spare.reused
}

// A run's memory is the buffer an earlier run's was, and it sees nothing
// that run wrote: not its global, not its first page, and not the page it
// grew, which the buffer held already.
func TestRun_AReusedMemoryHoldsNothingAnEarlierRunLeft(t *testing.T) {
	m := mustCompile(t, "residue")
	before := emptySpare()
	if got := scribble(m); got != fresh {
		t.Fatalf("the first run found %q left, want nothing", got)
	}
	if n := reusedSoFar(); n != before {
		t.Fatalf("the first run took %d buffers from an empty spare", n-before)
	}
	if got := scribble(m); got != fresh {
		t.Errorf("the second run found %q left, want nothing", got)
	}
	if n := reusedSoFar(); n != before+1 {
		t.Errorf("the second run took %d buffers from spare, want the first run's", n-before)
	}
}

// spare holds no more than spareBytes, and takes the smallest buffer that
// fits.
func TestSpare_HoldsAtMostItsCap(t *testing.T) {
	var b buffers
	b.put(make([]byte, 3, 32<<20))
	b.put(make([]byte, 5, 16<<20))
	if b.put(make([]byte, 0, 1<<20)); b.held != 49<<20 || len(b.bufs) != 3 {
		t.Fatalf("spare holds %d buffers of %d bytes, want 3 of %d", len(b.bufs), b.held, 49<<20)
	}
	if b.put(make([]byte, 0, 16<<20)); b.held != 49<<20 || len(b.bufs) != 3 {
		t.Errorf("spare took a buffer past its cap of %d: %d buffers of %d bytes", spareBytes, len(b.bufs), b.held)
	}
	buf, zero := b.take(2 << 20)
	if cap(buf) != 16<<20 || len(buf) != 0 || zero != 5 {
		t.Errorf("take(2 MiB) = a buffer of %d bytes, zero from %d; want the 16 MiB one, zero from 5", cap(buf), zero)
	}
	if buf, _ := b.take(40 << 20); cap(buf) != 40<<20 || b.reused != 1 || b.held != 33<<20 {
		t.Errorf("take(40 MiB) = a buffer of %d bytes, %d reused, %d held; want a new one", cap(buf), b.reused, b.held)
	}
}

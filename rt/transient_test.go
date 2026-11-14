package rt

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// frozenVector is a vector a test kept, with the elements it must still
// hold.
type frozenVector struct {
	v    Vector[int64]
	want []int64
}

// TestVectorOwned_MatchesPersistent drives one vector through random sets
// and pushes in their Owned forms and checks each result against a slice.
// Now and then it keeps the current vector, as a caller that does not give
// its value up does, and goes on from a persistent update of it, the only
// way such a value goes on; every kept vector must end as it was kept, so no
// Owned operation wrote a node a kept vector reaches.
func TestVectorOwned_MatchesPersistent(t *testing.T) {
	for seed := uint64(0); seed < 40; seed++ {
		r := rand.New(rand.NewPCG(seed, 7))
		var v Vector[int64]
		var model []int64
		if seed%2 == 1 {
			// A vector a literal built, whose leaves are windows of one array.
			model = make([]int64, r.IntN(3000))
			for i := range model {
				model[i] = int64(i)
			}
			v = VectorOf(model)
			model = slices.Clone(model)
		}
		var frozen []frozenVector
		for step := range 3000 {
			x := int64(seed*100000 + uint64(step))
			switch k := r.IntN(10); {
			case k < 5 || len(model) == 0:
				v = VectorPushOwned(v, x)
				model = append(model, x)
			case k < 9:
				i := r.IntN(len(model))
				v = VectorSetOwned(v, int64(i), x)
				model[i] = x
			default:
				frozen = append(frozen, frozenVector{v, slices.Clone(model)})
				if r.IntN(2) == 0 {
					v = VectorPush(v, x)
					model = append(model, x)
				} else {
					i := r.IntN(len(model))
					v = VectorSet(v, int64(i), x)
					model[i] = x
				}
			}
			if got := VectorToSlice(v); !slices.Equal(got, model) {
				t.Fatalf("seed %d step %d: vector holds %d elements differing from the model", seed, step, len(got))
			}
		}
		for i, f := range frozen {
			if got := VectorToSlice(f.v); !slices.Equal(got, f.want) {
				t.Fatalf("seed %d: kept vector %d changed after it was kept", seed, i)
			}
		}
	}
}

// TestVectorOwned_AWindowIsCopied: an Owned operation on a window over a
// trie another vector reaches (a tail) answers the persistent result and
// leaves the other vector whole.
func TestVectorOwned_AWindowIsCopied(t *testing.T) {
	var v Vector[int64]
	for i := range 100 {
		v = VectorPushOwned(v, int64(i))
	}
	rest := VectorTail(v)
	rest = VectorSetOwned(rest, 0, -1)
	rest = VectorPushOwned(rest, -2)
	if got := VectorAt(v, 1); got != 1 {
		t.Fatalf("the vector a window was taken from reads %d at 1, want 1", got)
	}
	if got := VectorAt(rest, 0); got != -1 || VectorLength(rest) != 100 {
		t.Fatalf("the window reads %d at 0 and has %d elements, want -1 and 100", got, VectorLength(rest))
	}
}

// TestVectorOwned_WritesInPlace: once a vector's lineage owns its nodes, a
// set allocates nothing and a push allocates only when its tail fills.
func TestVectorOwned_WritesInPlace(t *testing.T) {
	var v Vector[int64]
	for i := range 1 << 12 {
		v = VectorPushOwned(v, int64(i))
	}
	for i := range 1 << 12 {
		v = VectorSetOwned(v, int64(i), 0)
	}
	i := int64(0)
	if got := testing.AllocsPerRun(1000, func() {
		v = VectorSetOwned(v, i%(1<<12), i)
		i++
	}); got != 0 {
		t.Errorf("a set on an owned vector allocates %v times, want 0", got)
	}
	if got := testing.AllocsPerRun(32*40, func() { v = VectorPushOwned(v, 1) }); got > 0.2 {
		t.Errorf("a push on an owned vector allocates %v times, want under 0.2 (a leaf and a tail per 32)", got)
	}
}

// frozenMap is a map a test kept, with the entries it must still hold, in
// order.
type frozenMap struct {
	m    Map[int64, int64]
	want []MapEntry[int64, int64]
}

// TestMapOwned_MatchesPersistent is TestVectorOwned_MatchesPersistent for
// MapPutOwned and MapRemoveOwned, under a full hash and under one that
// collides, which builds deep paths and collision buckets.
func TestMapOwned_MatchesPersistent(t *testing.T) {
	eq := func(a, b int64) bool { return a == b }
	for _, hash := range []struct {
		name string
		h    func(int64) uint64
	}{{"HashInt", HashInt}, {"colliding", func(k int64) uint64 { return uint64(k) & 0x3f }}} {
		for seed := uint64(0); seed < 20; seed++ {
			r := rand.New(rand.NewPCG(seed, 11))
			var m Map[int64, int64]
			var model []MapEntry[int64, int64]
			find := func(k int64) int {
				return slices.IndexFunc(model, func(e MapEntry[int64, int64]) bool { return e.Key == k })
			}
			put := func(k, v int64) {
				if i := find(k); i >= 0 {
					model[i].Val = v
				} else {
					model = append(model, MapEntry[int64, int64]{k, v})
				}
			}
			remove := func(k int64) {
				if i := find(k); i >= 0 {
					model = slices.Delete(model, i, i+1)
				}
			}
			var frozen []frozenMap
			for step := range 2000 {
				k, v := int64(r.IntN(400)), int64(step)
				switch op := r.IntN(10); {
				case op < 6:
					m = MapPutOwned(m, hash.h, eq, k, v)
					put(k, v)
				case op < 9:
					m = MapRemoveOwned(m, hash.h, eq, k)
					remove(k)
				default:
					frozen = append(frozen, frozenMap{m, slices.Clone(model)})
					m = MapPut(m, hash.h, eq, k, v)
					put(k, v)
				}
				if got := MapEntries(m); !slices.Equal(got, model) {
					t.Fatalf("%s seed %d step %d: the map's entries differ from the model", hash.name, seed, step)
				}
			}
			for i, f := range frozen {
				if got := MapEntries(f.m); !slices.Equal(got, f.want) {
					t.Fatalf("%s seed %d: kept map %d changed after it was kept", hash.name, seed, i)
				}
			}
		}
	}
}

// TestMapOwned_WritesInPlace: once a map's lineage owns its nodes, putting
// an existing key allocates nothing.
func TestMapOwned_WritesInPlace(t *testing.T) {
	eq := func(a, b int64) bool { return a == b }
	var m Map[int64, int64]
	for i := range int64(1000) {
		m = MapPutOwned(m, HashInt, eq, i, i)
	}
	for i := range int64(1000) {
		m = MapPutOwned(m, HashInt, eq, i, 0)
	}
	i := int64(0)
	if got := testing.AllocsPerRun(1000, func() {
		m = MapPutOwned(m, HashInt, eq, i%1000, i)
		i++
	}); got != 0 {
		t.Errorf("a put of an existing key on an owned map allocates %v times, want 0", got)
	}
}

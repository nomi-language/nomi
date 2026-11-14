package rt

import (
	"fmt"
	"math/rand/v2"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// vecVersion is one Vector the property test made, beside the plain slice it
// must equal for as long as the test holds it.
type vecVersion struct {
	v     Vector[int64]
	model []int64
}

// TestVectorMatchesASliceModel runs random sequences of push, set, tail,
// slice, concat and of, each applied to a random version made earlier, and
// checks the result against a plain-slice model. Every version is kept and
// checked again at the end, so an update that changed a node another version
// shares fails here. The sizes cross a leaf (32), a root of leaves (1056) and
// a second level (32800 elements is past 32*32*32 + 32).
func TestVectorMatchesASliceModel(t *testing.T) {
	for seed := range uint64(12) {
		r := rand.New(rand.NewPCG(seed, 7))
		steps := 400
		if seed%4 == 3 {
			steps = 40
		}
		versions := []vecVersion{{}}
		next := int64(0)
		pick := func() vecVersion { return versions[r.IntN(len(versions))] }
		latest := func() vecVersion { return versions[len(versions)-1] }
		add := func(op string, v Vector[int64], model []int64) {
			checkVector(t, fmt.Sprintf("seed %d step %d (%s)", seed, len(versions), op), v, model)
			versions = append(versions, vecVersion{v, model})
		}
		for range steps {
			// Mostly build on the newest version, so the vectors grow past a
			// level, and sometimes branch from an old one.
			base := latest()
			if r.IntN(4) == 0 {
				base = pick()
			}
			switch op := r.IntN(10); {
			case op < 4:
				k := 1 + r.IntN(80)
				if seed%4 == 3 {
					k = 1000 + r.IntN(1000)
				}
				v, model := base.v, slices.Clone(base.model)
				for range k {
					next++
					v = VectorPush(v, next)
					model = append(model, next)
				}
				add("push", v, model)
			case op < 6 && len(base.model) > 0:
				v, model := base.v, slices.Clone(base.model)
				for range 1 + r.IntN(20) {
					i := r.IntN(len(model))
					next++
					v = VectorSet(v, int64(i), next)
					model[i] = next
				}
				add("set", v, model)
			case op < 7:
				add("tail", VectorTail(base.v), modelTail(base.model))
			case op < 8:
				from, drop := r.IntN(len(base.model)+2)-1, r.IntN(len(base.model)+2)-1
				add("slice", VectorSlice(base.v, from, drop), modelSlice(base.model, from, drop))
			case op < 9:
				other := pick()
				add("concat", VectorConcat(base.v, other.v), slices.Concat(base.model, other.model))
			default:
				add("of", VectorOf(base.model), slices.Clone(base.model))
			}
		}
		for i, ver := range versions {
			checkVector(t, fmt.Sprintf("seed %d: retained version %d", seed, i), ver.v, ver.model)
		}
	}
}

func modelTail(xs []int64) []int64 {
	if len(xs) <= 1 {
		return nil
	}
	return slices.Clone(xs[1:])
}

func modelSlice(xs []int64, from, drop int) []int64 {
	if from < 0 || drop < 0 || len(xs)-from-drop <= 0 {
		return nil
	}
	return slices.Clone(xs[from : len(xs)-drop])
}

// checkVector holds v to model through every read: length, get at every index
// and out of range, the walk, equality and hashing against a fresh vector, and
// rendering.
func checkVector(t *testing.T, what string, v Vector[int64], model []int64) {
	t.Helper()
	if VectorLength(v) != int64(len(model)) {
		t.Fatalf("%s: length %d, want %d", what, VectorLength(v), len(model))
	}
	for i, want := range model {
		if got := VectorAt(v, int64(i)); got != want {
			t.Fatalf("%s: At(%d) = %d, want %d", what, i, got, want)
		}
	}
	if VectorGet(v, -1).Tag != TagNone || VectorGet(v, int64(len(model))).Tag != TagNone {
		t.Fatalf("%s: get out of range answered Some", what)
	}
	if got := VectorToSlice(v); !slices.Equal(got, model) {
		t.Fatalf("%s: walk = %v, want %v", what, got, model)
	}
	eq := func(a, b int64) bool { return a == b }
	fresh := VectorOf(model)
	if !VectorEqual(v, fresh, eq) || !VectorEqual(fresh, v, eq) {
		t.Fatalf("%s: not equal to a fresh vector of the same elements", what)
	}
	h := func(x int64) uint64 { return uint64(x) }
	if HashVector(v, h) != HashVector(fresh, h) {
		t.Fatalf("%s: hash differs from a fresh vector's", what)
	}
	if len(model) < 200 {
		render := func(x int64) string { return fmt.Sprint(x) }
		if got, want := FormatVector(v, render), FormatVector(fresh, render); got != want {
			t.Fatalf("%s: renders %s, want %s", what, got, want)
		}
	}
}

// Rendering and equality agree with the slice they were built from, a view
// included.
func TestVectorRenderingAndEquality(t *testing.T) {
	render := func(x int64) string { return fmt.Sprint(x) }
	if got := FormatVector(Vector[int64]{}, render); got != "Vector[]" {
		t.Errorf("empty renders %q", got)
	}
	v := VectorOf([]int64{1, 2, 3, 4})
	if got := FormatVector(VectorSlice(v, 1, 1), render); got != "Vector[2, 3]" {
		t.Errorf("slice renders %q", got)
	}
	eq := func(a, b int64) bool { return a == b }
	if VectorEqual(VectorTail(v), VectorOf([]int64{2, 3, 5}), eq) {
		t.Error("vectors differing in the last element are equal")
	}
	if !VectorContains(v, 3, eq) || VectorContains(v, 9, eq) {
		t.Error("contains disagrees with the elements")
	}
}

// TestMeasureVectorCosts asserts the header's cost table for push, set, At
// and length by counting the work they do: nodes built, elements and child
// pointers copied, and nodes visited. Nothing here reads a clock.
//
// The counters are compiled out of a normal build (vectorcount_off.go), so
// under a plain `go test` this test re-runs itself with `-tags rtvectorcount`
// and fails if the child fails.
//
// The bounds, derived from vector.go. A trie of n elements has
// d = log32ceil(n/32) branch levels above its leaves. A push copies at most
// the 32-element tail, and when the tail is full builds at most d+1 nodes
// (a new root and its path, or a copied path) copying at most 32 child
// pointers each. A set copies one leaf or the tail and the d branches above
// it. So both are bounded by 32*(d+2) copies and d+2 nodes: a constant times
// log32 n, where the old window copy was n.
func TestMeasureVectorCosts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds vectors up to 2^20 elements; -short")
	}
	if !vecCounting {
		runCountingBuild(t, "rtvectorcount", "TestMeasureVectorCosts")
		return
	}
	for _, n := range []int{1 << 4, 1 << 8, 1 << 12, 1 << 16, 1 << 20} {
		d := log32ceil(max(n/vecWidth, 1))
		copyBound, nodeBound := vecWidth*(d+2), d+2

		v := Vector[int64]{}
		worstPush := vecCost{}
		for i := range int64(n) {
			vecCountReset()
			v = VectorPush(v, i)
			c := vecCountRead()
			worstPush.copies, worstPush.nodes = max(worstPush.copies, c.copies), max(worstPush.nodes, c.nodes)
			if c.copies > copyBound || c.nodes > nodeBound {
				t.Fatalf("n = %d: push %d copied %d and built %d nodes; the bounds are %d and %d", n, i, c.copies, c.nodes, copyBound, nodeBound)
			}
		}

		worstSet, worstVisit := vecCost{}, 0
		r := rand.New(rand.NewPCG(uint64(n), 1))
		s := v
		for range 1000 {
			i := int64(r.IntN(n))
			vecCountReset()
			s = VectorSet(s, i, -i)
			c := vecCountRead()
			worstSet.copies, worstSet.nodes = max(worstSet.copies, c.copies), max(worstSet.nodes, c.nodes)
			if c.copies > copyBound || c.nodes > nodeBound {
				t.Fatalf("n = %d: set(%d) copied %d and built %d nodes; the bounds are %d and %d", n, i, c.copies, c.nodes, copyBound, nodeBound)
			}
			vecCountReset()
			if got := VectorAt(s, i); got != -i {
				t.Fatalf("n = %d: At(%d) after set = %d", n, i, got)
			}
			c = vecCountRead()
			worstVisit = max(worstVisit, c.visits)
			if c.visits > d+1 || c.copies != 0 || c.nodes != 0 {
				t.Fatalf("n = %d: At(%d) visited %d nodes, copied %d, built %d; a read visits at most %d and builds nothing", n, i, c.visits, c.copies, c.nodes, d+1)
			}
		}
		// The original is unchanged by the sets.
		for i := range int64(n) {
			if VectorAt(v, i) != i {
				t.Fatalf("n = %d: set changed the original at %d", n, i)
			}
		}

		vecCountReset()
		VectorLength(v)
		VectorTail(v)
		VectorSlice(v, 3, 3)
		if c := vecCountRead(); c != (vecCost{}) {
			t.Errorf("n = %d: length, tail and slice did %+v; each is a field read or a new window", n, c)
		}
		t.Logf("n = %7d   levels %d   push worst %d copies %d nodes   set worst %d copies %d nodes   At worst %d visits",
			n, d, worstPush.copies, worstPush.nodes, worstSet.copies, worstSet.nodes, worstVisit)
	}

	// A queue made by tail and push keeps a bounded trie: the dropped prefix
	// is shed once it outgrows the window.
	q := Vector[int64]{}
	for i := range int64(10) {
		q = VectorPush(q, i)
	}
	for i := range int64(100_000) {
		q = VectorPush(VectorTail(q), 10+i)
	}
	if q.t.cnt > 4*q.n || VectorAt(q, 0) != 100_000 {
		t.Errorf("a queue of %d elements holds a trie of %d and starts at %d", q.n, q.t.cnt, VectorAt(q, 0))
	}
}

// runCountingBuild runs the test named test in a child `go test` built with
// tag, where a structure's work counters are live. It requires the child to
// report a PASS for that test by name, because `go test -run` that matches
// nothing also exits 0.
func runCountingBuild(t *testing.T, tag, test string) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the counting build needs the go command: %v", err)
	}
	out, err := exec.Command(goBin, "test", "-tags="+tag, "-run=^"+test+"$", "-count=1", "-v", ".").CombinedOutput()
	t.Logf("go test -tags=%s -run=^%s$ .\n%s", tag, test, out)
	if err != nil {
		t.Fatalf("counting build failed: %v", err)
	}
	if !strings.Contains(string(out), "--- PASS: "+test) {
		t.Fatalf("counting build did not report %s as passed", test)
	}
}

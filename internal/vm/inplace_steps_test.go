package vm_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// stepBytes answers the bytes one step of a program allocates: files hold
// the program, main.nomi its entry, and each %d in a file is the number of
// steps. It runs the program at two step counts, the least allocation of
// several runs at each, and divides the difference by the difference of the
// counts, so what a run allocates once (loading, printing) cancels out.
func stepBytes(t *testing.T, files map[string]string) float64 {
	t.Helper()
	const low, high = 2000, 12000
	at := func(n int) uint64 {
		dir := t.TempDir()
		for name, src := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(src, "%d", fmt.Sprint(n))), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		p, err := vmhost.Load(filepath.Join(dir, "main.nomi"))
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		least := ^uint64(0)
		for range 4 {
			out.Reset()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			least = min(least, after.TotalAlloc-before.TotalAlloc)
		}
		if got := strings.TrimSpace(out.String()); got == "" {
			t.Fatalf("the program printed nothing:\n%s", files["main.nomi"])
		}
		return least
	}
	lo, hi := at(low), at(high)
	if hi < lo {
		return 0
	}
	return float64(hi-lo) / float64(high-low)
}

// oneFile is a program of one file.
func oneFile(src string) map[string]string { return map[string]string{"main.nomi": src} }

// stepSlack is how many bytes per step one way of writing a loop may
// allocate beyond another that writes the same values in place: a boxed
// Int or a header, never a copy of a path of the collection's tree (some
// hundreds of bytes).
const stepSlack = 48

// requireNear fails unless got allocates at most stepSlack bytes a step
// more than base.
func requireNear(t *testing.T, what string, got, base float64, baseWhat string) {
	t.Helper()
	t.Logf("%s: %.0f B/step; %s: %.0f B/step", what, got, baseWhat, base)
	if got > base+stepSlack {
		t.Errorf("%s allocates %.0f bytes a step, more than %.0f past %s's %.0f", what, got, float64(stepSlack), baseWhat, base)
	}
}

// requireCopyingCostsMore fails unless the copying machine allocates at
// least 200 bytes a step more than the in-place one on files: the counter
// tells a step that copies from one that does not.
func requireCopyingCostsMore(t *testing.T, what string, files map[string]string, inPlace float64) {
	t.Helper()
	t.Setenv(vm.NoInPlaceEnv, "1")
	copying := stepBytes(t, files)
	t.Logf("%s copying: %.0f B/step", what, copying)
	if copying < inPlace+200 {
		t.Errorf("%s allocates %.0f bytes a step copying and %.0f in place: the step does not copy, or the in-place one does", what, copying, inPlace)
	}
}

const vecTail = `import std::io

fn fill(v: Vector<Int>, i: Int, n: Int) -> Vector<Int>
    if i >= n then v else fill(Vector.push(v, i), i + 1, n)
end

fn main()
    io.print(Vector.length(fill(SEED, 0, %d)))
end
`

const vecFold = `import std::io

fn main()
    v = Iter.reduce(0..%d, SEED, STEP)
    io.print(Vector.length(v))
end
`

const setTail = `import std::io

fn fill(s: Set<Int>, i: Int, n: Int) -> Set<Int>
    if i >= n then s else fill(Set.insert(s, i), i + 1, n)
end

fn main()
    io.print(Set.size(fill(SEED, 0, %d)))
end
`

const setFold = `import std::io

fn main()
    s = Iter.reduce(0..%d, SEED, fn acc, x -> Set.insert(acc, x))
    io.print(Set.size(s))
end
`

const lambdaPush = "fn acc, x -> Vector.push(acc, x)"

func seeded(src, seed string) map[string]string {
	return oneFile(strings.ReplaceAll(src, "SEED", seed))
}

func folded(seed, step string) map[string]string {
	return oneFile(strings.ReplaceAll(strings.ReplaceAll(vecFold, "SEED", seed), "STEP", step))
}

// TestInPlace_AnEmptyLiteralStartsAsANonEmptyOneDoes: a Vector or Set
// built up from `Vector[]` or `Set[]`, by tail calls or by a fold, is
// written in place from its first update, as one built up from a literal
// with an element is.
func TestInPlace_AnEmptyLiteralStartsAsANonEmptyOneDoes(t *testing.T) {
	cases := []struct {
		name         string
		empty, fresh map[string]string
	}{
		{"a vector by tail calls", seeded(vecTail, "Vector[]"), seeded(vecTail, "Vector[-1]")},
		{"a vector by a fold", folded("Vector[]", lambdaPush), folded("Vector[-1]", lambdaPush)},
		{"a set by tail calls", seeded(setTail, "Set[]"), seeded(setTail, "Set[-1]")},
		{"a set by a fold", seeded(setFold, "Set[]"), seeded(setFold, "Set[-1]")},
	}
	for _, c := range cases {
		requireNear(t, c.name+" from an empty literal", stepBytes(t, c.empty), stepBytes(t, c.fresh), "from a literal with an element")
	}
	requireCopyingCostsMore(t, "a vector by a fold from Vector[]", cases[1].empty, stepBytes(t, cases[1].empty))
}

// TestInPlace_ANamedStepFoldsAsALambdaDoes: a fold whose step is a
// top-level function, a type's function, a capture or a function another
// file declares writes its accumulator in place, as a lambda does.
func TestInPlace_ANamedStepFoldsAsALambdaDoes(t *testing.T) {
	const decls = `
fn push_one(acc: Vector<Int>, x: Int) -> Vector<Int>
    Vector.push(acc, x)
end

struct Bag
    size: Int
end

impl Bag
    fn add(acc: Vector<Int>, x: Int) -> Vector<Int>
        Vector.push(acc, x)
    end
end
`
	lambda := stepBytes(t, folded("Vector[-1]", lambdaPush))
	for _, step := range []string{"push_one", "Bag.add", "&Vector.push(&1, &2)"} {
		files := folded("Vector[-1]", step)
		files["main.nomi"] += decls
		requireNear(t, "a fold whose step is "+step, stepBytes(t, files), lambda, "a lambda step")
	}
	other := folded("Vector[-1]", "steps.push_one")
	other["main.nomi"] = strings.Replace(other["main.nomi"], "import std::io\n", "import\n    std::io\n    steps\nend\n", 1)
	other["steps.nomi"] = "pub fn push_one(acc: Vector<Int>, x: Int) -> Vector<Int>\n    Vector.push(acc, x)\nend\n"
	named := stepBytes(t, other)
	requireNear(t, "a fold whose step another file declares", named, lambda, "a lambda step")
	requireCopyingCostsMore(t, "a fold whose step another file declares", other, named)
}

// TestInPlace_AFoldStepCostsWhatATailCallDoes: a push or a put in a fold's
// step allocates what the same update does in a tail call, give or take a
// boxed value: running the step as a callback costs no copy of the
// machine per step.
func TestInPlace_AFoldStepCostsWhatATailCallDoes(t *testing.T) {
	const mapTail = `import std::io

fn fill(m: Map<Int, Int>, i: Int, n: Int) -> Map<Int, Int>
    if i >= n then m else fill(Map.put(m, i, i), i + 1, n)
end

fn main()
    io.print(Map.size(fill({}, 0, %d)))
end
`
	const mapFold = `import std::io

fn main()
    m = Iter.reduce(0..%d, {}, fn acc, x -> Map.put(acc, x, x))
    io.print(Map.size(m))
end
`
	requireNear(t, "a push in a fold", stepBytes(t, folded("Vector[-1]", lambdaPush)), stepBytes(t, seeded(vecTail, "Vector[-1]")), "a push in a tail call")
	requireNear(t, "a put in a fold", stepBytes(t, oneFile(mapFold)), stepBytes(t, oneFile(mapTail)), "a put in a tail call")
}

// TestInPlace_ABorrowedCollectionIsCopiedOnce: a Vector or Map whose
// caller keeps it is copied by the first update in the callee, and the
// copy is the callee's own: each later update, in the callee and in the
// calls it hands the copy to, writes in place.
func TestInPlace_ABorrowedCollectionIsCopiedOnce(t *testing.T) {
	const vecKept = `import std::io

fn fill(v: Vector<Int>, i: Int, n: Int) -> Vector<Int>
    if i >= n then v else fill(Vector.push(v, i), i + 1, n)
end

fn main()
    seed = Vector[-1]
    v = fill(seed, 0, %d)
    io.print(Vector.length(v) + Vector.length(seed))
end
`
	const mapKept = `import std::io

fn fill(m: Map<Int, Int>, i: Int, n: Int) -> Map<Int, Int>
    if i >= n then m else fill(Map.put(m, i, i), i + 1, n)
end

fn main()
    seed = {-1 => -1}
    m = fill(seed, 0, %d)
    io.print(Map.size(m) + Map.size(seed))
end
`
	const mapFresh = `import std::io

fn fill(m: Map<Int, Int>, i: Int, n: Int) -> Map<Int, Int>
    if i >= n then m else fill(Map.put(m, i, i), i + 1, n)
end

fn main()
    io.print(Map.size(fill({-1 => -1}, 0, %d)))
end
`
	const foldKept = `import std::io

fn main()
    seed = Vector[-1]
    v = Iter.reduce(0..%d, seed, fn acc, x -> Vector.push(acc, x))
    io.print(Vector.length(v) + Vector.length(seed))
end
`
	vec := stepBytes(t, oneFile(vecKept))
	requireNear(t, "a kept vector", vec, stepBytes(t, seeded(vecTail, "Vector[-1]")), "a vector handed over")
	requireNear(t, "a kept map", stepBytes(t, oneFile(mapKept)), stepBytes(t, oneFile(mapFresh)), "a map handed over")
	requireNear(t, "a fold over a kept seed", stepBytes(t, oneFile(foldKept)), stepBytes(t, folded("Vector[-1]", lambdaPush)), "a fold over a seed handed over")
	requireCopyingCostsMore(t, "a kept vector", oneFile(vecKept), vec)
}

// keptUpdates updates collections whose old values the program reads
// again: a map and a set a callee removes a missing key from (which
// answers the caller's own map) and then updates, an empty literal pushed
// to twice, a fold whose named step a caller's seed starts, a step that
// answers a `once`, a fold over a kept seed whose step sometimes answers
// its accumulator, an element of a list updated twice, and a fold whose
// step arrives as a parameter.
const keptUpdates = `import std::io

once base = Vector[100]

fn grow(m: Map<Int, Int>, n: Int) -> Map<Int, Int>
    if n < 1 then
        m
    else
        grow(Map.put(m, n, n * n), n - 1)
    end
end

fn mark(m: Map<Int, Int>) -> Map<Int, Int>
    Map.put(Map.remove(m, 99), 1, 0)
end

fn tag(s: Set<Int>) -> Set<Int>
    Set.insert(Set.remove(s, 99), 0)
end

fn push_one(acc: Vector<Int>, x: Int) -> Vector<Int>
    Vector.push(acc, x)
end

fn reset(acc: Vector<Int>, x: Int) -> Vector<Int>
    if x == 1 then base else Vector.push(acc, x)
end

fn fold_with(f: (Vector<Int>, Int) -> Vector<Int>) -> Vector<Int>
    Iter.reduce(0..3, Vector[], f)
end

fn twice(v: Vector<Int>) -> Vector<Int>
    Vector.push(Vector.push(v, 2), 3)
end

fn main()
    m = grow({}, 3)
    m2 = mark(m)
    io.print("${m} ${m2}")

    s = Iter.reduce(1..=3, Set[], fn acc, x -> Set.insert(acc, x))
    s2 = tag(s)
    io.print("${s} ${s2}")

    e: Vector<Int> = Vector[]
    a = Vector.push(e, 1)
    b = Vector.push(e, 2)
    io.print("${e} ${a} ${b}")

    seed = Vector[0]
    v = Iter.reduce(1..=3, seed, push_one)
    io.print("${seed} ${v}")

    r = Iter.reduce(0..4, Vector[0], reset)
    io.print("${base} ${r}")

    kept = Vector[9]
    w = Iter.reduce(0..6, kept, fn acc, x -> if x % 2 == 0 then acc else Vector.push(acc, x))
    io.print("${kept} ${w}")

    xs = [Vector[1]]

    case xs
        [first, ..] -> io.print("${twice(first)} ${first}")
        []          -> io.print("none")
    end

    io.inspect(xs)

    (state, outs) = Iter.map_reduce(0..3, Vector[], fn acc, i -> (Vector.push(acc, i), i))
    io.print("${state} ${outs}")
    io.print("${fold_with(push_one)}")
end
`

// TestInPlace_AnUpdatedCollectionKeepsWhatOthersRead: each collection
// keptUpdates reads again keeps its value, in place and copying; the
// first update of a borrowed collection copies it and later ones write
// the copy.
func TestInPlace_AnUpdatedCollectionKeepsWhatOthersRead(t *testing.T) {
	const want = "{3 => 9, 2 => 4, 1 => 1} {3 => 9, 2 => 4, 1 => 0}\n" +
		"Set[1, 2, 3] Set[1, 2, 3, 0]\n" +
		"Vector[] Vector[1] Vector[2]\n" +
		"Vector[0] Vector[0, 1, 2, 3]\n" +
		"Vector[100] Vector[100, 2, 3]\n" +
		"Vector[9] Vector[9, 1, 3, 5]\n" +
		"Vector[1, 2, 3] Vector[1]\n" +
		"[Vector[1]]\n" +
		"Vector[0, 1, 2] [0, 1, 2]\n" +
		"Vector[0, 1, 2]\n"
	runBothWays(t, keptUpdates, want)
}

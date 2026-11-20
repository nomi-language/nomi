package vm_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// The reach walk's table edges are typed (reach.go, heldTypes): an
// instruction that renders, compares or hashes a value, or reports it in an
// assertion's `values:` row, reaches the Display, Equatable, Hashable and
// hand-written Debug bodies of the declared types the value's type is or
// holds, at any depth, and every one only where the type does not bound
// them. Each program here runs on a counting machine and is held to the walk
// as TestReach_EveryEnteredFunctionIsReached holds the corpus (soundness):
// every body the run entered was reached. And each names the bodies the run
// must enter, so the program exercises the edge, and the bodies of a type it
// never renders, compares or hashes that the walk must not reach, or must
// reach where the value's type does not bound it.
func TestReach_TableEdgesFollowTheOperandsType(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers and runs programs; -short")
	}
	// Loud's and Other's bodies are never run by any program here.
	const decls = `import std::io

struct Point
    x: Int
end

impl Display for Point
    fn to_string(p: Point) -> String
        "p${p.x}"
    end
end

struct Plain
    x: Int
end

derive Display for Plain

struct Loud
    n: Int
end

impl Display for Loud
    fn to_string(l: Loud) -> String
        io.print("shown")
        "loud ${l.n}"
    end
end

struct Key
    n: Int
end

impl Equatable for Key
    fn equal(a: Key, b: Key) -> Bool
        a.n == b.n
    end
end

impl Hashable for Key
    fn hash(k: Key) -> Int
        k.n
    end
end

struct Outer
    key: Key
end

derive Equatable for Outer

derive Hashable for Outer

struct Other
    n: Int
end

impl Equatable for Other
    fn equal(a: Other, b: Other) -> Bool
        io.print("compared")
        a.n == b.n
    end
end

impl Hashable for Other
    fn hash(o: Other) -> Int
        io.print("hashed")
        o.n
    end
end

struct Shown
    n: Int
end

impl Debug for Shown
    fn inspect(s: Shown) -> String
        "shown ${s.n}"
    end
end

struct Quiet
    n: Int
end

impl Debug for Quiet
    fn inspect(q: Quiet) -> String
        io.print("inspected")
        "quiet ${q.n}"
    end
end

fn show(d: Display) -> String
    "${d}"
end

fn label<T>(x: T) -> String where T: Display
    "[${x}]"
end

`
	for _, tc := range []struct {
		name, main string
		// entered are bodies the run must enter; unreached are bodies the
		// walk must not reach; reached are bodies it must reach though the
		// run enters none of them.
		entered, unreached, reached []string
	}{
		{"a Display nested in a List in a Map, rendered structurally",
			"fn main()\n    m = {\"a\" => [Point{x: 1}]}\n    io.print(\"${(m, 0)}\")\nend\n",
			[]string{"to_string(main.Point)"}, []string{"to_string(main.Loud)"}, nil},
		{"a value held as an interface",
			"fn main()\n    io.print(show(Point{x: 2}))\nend\n",
			[]string{"to_string(main.Point)"}, nil, []string{"to_string(main.Loud)"}},
		{"a generic function rendering its T",
			"fn main()\n    io.print(label(Point{x: 3}))\nend\n",
			[]string{"to_string(main.Point)"}, []string{"to_string(main.Loud)"}, nil},
		{"a struct of another type interpolated",
			"fn main()\n    io.print(\"${Plain{x: 4}}\")\nend\n",
			[]string{"to_string(main.Plain)"}, []string{"to_string(main.Loud)", "to_string(main.Point)"}, nil},
		{"a key holding a key with hand-written impls",
			"fn main()\n    m = {Outer{key: Key{n: 1}} => \"one\"}\n    io.print(Map.get(m, Outer{key: Key{n: 1}}) |> Maybe.with_default(\"none\"))\nend\n",
			[]string{"equal(main.Key)", "hash(main.Key)"}, []string{"equal(main.Other)", "hash(main.Other)"}, nil},
		{"a tuple key",
			"fn main()\n    m = {(Key{n: 1}, 2) => \"one\"}\n    io.print(Map.get(m, (Key{n: 1}, 2)) |> Maybe.with_default(\"none\"))\nend\n",
			[]string{"equal(main.Key)", "hash(main.Key)"}, []string{"equal(main.Other)", "hash(main.Other)"}, nil},
		{"a key held as an interface",
			"fn main()\n    xs: List<Equatable> = [Key{n: 1}, Key{n: 1}]\n    io.inspect(xs == [Key{n: 1}])\nend\n",
			nil, nil, []string{"equal(main.Other)"}},
		{"an assertion's row of a type with a hand-written Debug",
			"fn main() -> Result<Unit, AssertionFailure>\n    assert Shown{n: 1} == Shown{n: 2}\n    Ok(Unit)\nend\n",
			[]string{"inspect(main.Shown)"}, []string{"inspect(main.Quiet)"}, nil},
		{"an assertion's row of a value held as an interface",
			"fn main() -> Result<Unit, AssertionFailure>\n    xs: List<Equatable> = [Key{n: 1}]\n    assert xs == [Key{n: 2}]\n    Ok(Unit)\nend\n",
			nil, nil, []string{"inspect(main.Quiet)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.nomi")
			src := decls + tc.main
			if strings.Contains(tc.main, "AssertionFailure") {
				// A main that asserts returns its failure, whose type is
				// imported only where it is named.
				src = strings.Replace(src, "import std::io\n",
					"import\n    std::io\n    std::assertions.AssertionFailure\nend\n", 1)
			}
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			s := &soundness{t: t}
			m, reached := s.program(tc.name, path)
			if m == nil {
				t.Fatalf("the program did not run: %v (%d blocked)", s.refused, s.blocked)
			}
			entered := map[string]bool{}
			for f, n := range vm.EntryCounts(m) {
				if n > 0 {
					entered[bodyName(f)] = true
				}
			}
			byName := map[string]bool{}
			for f := range reached {
				byName[bodyName(f)] = true
			}
			for _, name := range tc.entered {
				if !entered[name] {
					t.Errorf("the run did not enter %s, so this does not exercise its edge; entered: %s", name, sortedNames(entered))
				}
			}
			for _, name := range tc.unreached {
				if byName[name] {
					t.Errorf("the walk reaches %s, whose type the program never renders, compares or hashes", name)
				}
			}
			for _, name := range tc.reached {
				if !byName[name] {
					t.Errorf("the walk does not reach %s, which a value whose type does not bound it may run", name)
				}
			}
		})
	}
}

// bodyName names f by its own name, an impl body's from its block
// (ir.Symbol.Impl), and its first parameter's type, which tells one type's
// impl body from another's: `equal(main.Key)`.
func bodyName(f *ir.Func) string {
	name := f.Name()
	if impl, ok := f.Sym().Impl(); ok {
		name = impl.Func
	}
	if len(f.Params()) == 0 {
		return name
	}
	return name + "(" + f.TempType(f.Params()[0].Temp).String() + ")"
}

// sortedNames is set's members, sorted and joined.
func sortedNames(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

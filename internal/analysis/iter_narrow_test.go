package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A List is an Iter, so a List stands wherever an Iter is expected. An Iter
// is not a List: it is lazy, and the IR builder has no conversion from one to
// the other. Every position below once accepted the Iter, and the builder then
// declined the program, so the user saw "not supported yet" instead of a type
// error.
const iterNarrowDecls = `struct Box {
  xs: List<Int>
}

fn it(): Iter<Int> {
  [1, 2]
}

fn take(xs: List<Int>): Int {
  Iter.count(xs)
}

fn gen<T>(xs: List<T>): Int {
  Iter.count(xs)
}

`

func iterHint(fn string) string {
	return "\nhelp: an Iter is not a collection; materialize it with `|> " + fn + "()`"
}

// expectDiagAt fails unless one error reads exactly want and spans the last
// occurrence of at in src.
func expectDiagAt(t *testing.T, src string, errs []analysis.TypeError, want, at string) {
	t.Helper()
	i := strings.LastIndex(src, at)
	if i < 0 {
		t.Fatalf("%q is not in the source", at)
	}
	line := strings.Count(src[:i], "\n") + 1
	col := i - strings.LastIndex(src[:i], "\n")
	msgs := make([]string, len(errs))
	for k, e := range errs {
		if diagText(e) == want {
			if e.Line != line || e.Col != col || e.EndLine != line || e.EndCol != col+len(at) {
				t.Fatalf("%s\nis at %d:%d-%d:%d, want %d:%d-%d:%d (%q)", want, e.Line, e.Col, e.EndLine, e.EndCol, line, col, line, col+len(at), at)
			}
			return
		}
		msgs[k] = diagText(e)
	}
	t.Fatalf("expected the error\n  %s\ngot %d errors:\n  %s", want, len(errs), strings.Join(msgs, "\n  "))
}

func TestIterNarrow_EveryPositionRefusesTheIter(t *testing.T) {
	for _, tc := range []struct {
		name, src, want, at string
	}{
		{"a once annotation",
			"once xs: List<Int> = [1, 2] |> Iter.map(|x| x + 1)\n",
			"type mismatch: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"[1, 2] |> Iter.map(|x| x + 1)"},
		{"an annotated binding",
			"fn main() {\n  a: List<Int> = it()\n  _ = a\n}\n",
			"type mismatch: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"an argument",
			"fn main() {\n  _ = take(it())\n}\n",
			"argument 1: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"a pipe argument",
			"fn main() {\n  _ = it() |> take()\n}\n",
			"pipe argument type mismatch: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"a generic argument",
			"fn main() {\n  _ = gen(it())\n}\n",
			"argument 1: expected List<T>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"a generic pipe argument",
			"fn main() {\n  _ = it() |> gen()\n}\n",
			"argument 1: expected List<T>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"a struct field",
			"fn main() {\n  _ = Box{xs: it()}\n}\n",
			"field 'xs' of Box: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"a list element",
			"fn main() {\n  xs: List<List<Int>> = [it()]\n  _ = xs\n}\n",
			"list element type mismatch: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"a function's tail",
			"fn get(): List<Int> {\n  [1, 2] |> Iter.map(|x| x + 1)\n}\n",
			"return type mismatch: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"[1, 2] |> Iter.map(|x| x + 1)"},
		{"a return",
			"fn get(): List<Int> {\n  return it()\n}\n",
			"return type mismatch: expected List<Int>, got Iter<Int>" + iterHint("Iter.to_list"),
			"it()"},
		{"a Set",
			"fn get(): Set<Int> {\n  it()\n}\n",
			"return type mismatch: expected Set<Int>, got Iter<Int>" + iterHint("Iter.to_set"),
			"it()"},
		{"a Vector",
			"fn get(): Vector<Int> {\n  it()\n}\n",
			"return type mismatch: expected Vector<Int>, got Iter<Int>" + iterHint("Iter.to_vector"),
			"it()"},
		{"a Map",
			"fn get(): Map<Int, Int> {\n  [(1, 2)] |> Iter.map(|p| p)\n}\n",
			"return type mismatch: expected Map<Int, Int>, got Iter<(Int, Int)>" + iterHint("Iter.to_map"),
			"[(1, 2)] |> Iter.map(|p| p)"},
		{"a struct type",
			"fn main() {\n  n: Int = it()\n  _ = n\n}\n",
			"type mismatch: expected Int, got Iter<Int>",
			"it()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := iterNarrowDecls + tc.src
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}

// Any interface value, not only an Iter, is refused where one of its
// implementers is expected.
func TestIterNarrow_AnInterfaceIsNotItsImplementer(t *testing.T) {
	src := `interface Area {
  fn area(a: self): Int
}

struct Sq {
  side: Int
}

impl Area for Sq {
  fn area(s: Sq): Int {
    s.side
  }
}

fn some(): Area {
  Sq{side: 1}
}

fn main() {
  s: Sq = some()
  _ = s
}
`
	_, errs := checkSourceWithStdlib(src)
	expectDiagAt(t, src, errs, "type mismatch: expected Sq, got Area", "some()")
}

// The mirror: a List, Set or Map still enters every Iter position, an
// Iter-bounded type parameter takes a List, an implementer enters its
// interface, and a join of a List and an Iter is an Iter.
func TestIterNarrow_ACollectionIsAnIter(t *testing.T) {
	src := `interface Area {
  fn area(a: self): Int
}

struct Sq {
  side: Int
}

impl Area for Sq {
  fn area(s: Sq): Int {
    s.side
  }
}

struct Holder {
  xs: Iter<Int>
}

fn total(xs: Iter<Int>): Int {
  Iter.count(xs)
}

fn bounded<I>(xs: I): Int where I: Iter<Int> {
  Iter.count(xs)
}

fn as_iter(): Iter<Int> {
  [1, 2, 3]
}

fn as_area(): Area {
  Sq{side: 2}
}

fn main() {
  _ = total([1, 2])
  _ = [1, 2] |> total()
  _ = total(#{1, 2})
  _ = bounded([3, 4])
  d: Iter<Int> = [5]
  _ = d
  _ = Holder{xs: [1]}
  areas: List<Area> = [Sq{side: 1}, Sq{side: 3}]
  _ = areas |> Iter.map(|a| Area.area(a)) |> Iter.to_list()
  _ = Area.area(as_area())
  xs: List<Int> = as_iter() |> Iter.to_list()
  _ = xs
  pick = if Iter.count(d) > 0 { as_iter() } else { [9] }
  _ = total(pick)
  ys: List<Iter<Int>> = [[1], as_iter()]
  _ = ys
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

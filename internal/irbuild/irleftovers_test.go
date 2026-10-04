package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// Range cardinality, `Iter.group_by`, field sub-patterns inside a variant
// pattern and nested variant patterns in `if`-patterns. Each program runs on
// the VM and must print the expected output.
func TestIRLeftovers_ProgramsRunOnTheVM(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
		retained        []string
	}{
		{"range count and known_count", `import std/io

fn count(n: Int): Int {
  Iter.count(1..=n)
}

fn known(n: Int): Maybe<Int> {
  Iter.known_count(1..n)
}

fn empty(n: Int): Bool {
  Iter.empty?(1..n)
}

fn full(n: Int): Bool {
  Iter.not_empty?(1..n)
}

fn main() {
  io.print(count(5))
  io.print(count(0))
  io.inspect(known(5))
  io.inspect(known(1))
  io.inspect(known(-3))
  io.inspect(Iter.known_count(1..=5))
  io.inspect(Iter.known_count(Range.from(1)))
  io.inspect(Iter.known_count(Range.naturals()))
  io.inspect(empty(1))
  io.inspect(full(3))
  io.print(Iter.count(Range.from(1) |> Iter.take(4)))
}
`, "5\n0\nSome(4)\nSome(0)\nSome(0)\nSome(5)\nNone\nNone\nTrue\nTrue\n4\n",
			[]string{"count", "known", "empty", "full", "main"}},
		{"group_by builds a Map of Lists", `import std/io

fn by_parity(xs: List<Int>): Map<Int, List<Int>> {
  xs |> Iter.group_by(|x| x % 2)
}

fn by_initial(words: List<String>): Map<String, List<String>> {
  Iter.group_by(words, |w: String| Int.to_string(String.length(w)))
}

fn main() {
  grouped = by_parity([5, 2, 3, 4, 1, 6])
  io.inspect(grouped)
  io.inspect(Map.get(grouped, 1))
  io.inspect(Map.size(grouped))
  io.inspect(by_parity([]))
  io.inspect(by_initial(["ab", "c", "de", "fgh"]))
  io.inspect(Iter.group_by(1..=6, |x| x > 3))
  io.inspect([1, 2, 3] |> Iter.map(|x| x * 10) |> Iter.group_by(|x| x < 15))
}
`, "{1 => [5, 3, 1], 0 => [2, 4, 6]}\nSome([5, 3, 1])\n2\n{=>}\n{\"2\" => [\"ab\", \"de\"], \"1\" => [\"c\"], \"3\" => [\"fgh\"]}\n{False => [1, 2, 3], True => [4, 5, 6]}\n{True => [10], False => [20, 30]}\n",
			[]string{"by_parity", "by_initial", "main"}},
		{"field sub-patterns inside a variant pattern", `import std/io

enum Shape {
  Box {w: Int, h: Int}
  Named {name: String, sides: Int}
  Dot
}

struct Circle {
  r: Int
  tag: String
}

enum Drawing {
  embeds Circle
  Blank
}

fn describe(s: Maybe<Shape>): String {
  case s {
    Some(Shape.Box{w: 2, h}) -> "narrow box of height ${h}"
    Some(Shape.Box{w, h: 0}) -> "flat box of width ${w}"
    Some(Shape.Named{name: "tri", sides: n}) -> "triangle-ish ${n}"
    Some(Shape.Named{name, sides: _}) -> "named ${name}"
    Some(_) -> "other"
    None -> "nothing"
  }
}

fn top(s: Shape): Int {
  case s {
    Shape.Box{w: 1, h: 1} -> 1
    Shape.Box{w, h} -> w * h
    _ -> 0
  }
}

fn drawn(d: Drawing): String {
  case d {
    Drawing.Circle{r: 0, tag} -> "point ${tag}"
    Drawing.Circle{r, tag: "big"} -> "big ${r}"
    Drawing.Circle{r, tag: _} -> "circle ${r}"
    .Blank -> "blank"
  }
}

fn main() {
  io.print(describe(Some(Shape.Box{w: 2, h: 5})))
  io.print(describe(Some(Shape.Box{w: 7, h: 0})))
  io.print(describe(Some(Shape.Box{w: 7, h: 3})))
  io.print(describe(Some(Shape.Named{name: "tri", sides: 3})))
  io.print(describe(Some(Shape.Named{name: "sq", sides: 4})))
  io.print(describe(Some(Shape.Dot)))
  io.print(describe(None))
  io.print(top(Shape.Box{w: 1, h: 1}))
  io.print(top(Shape.Box{w: 3, h: 4}))
  io.print(top(Shape.Dot))
  io.print(drawn(Circle{r: 0, tag: "o"}))
  io.print(drawn(Circle{r: 5, tag: "big"}))
  io.print(drawn(Circle{r: 2, tag: "c"}))
  io.print(drawn(Drawing.Blank))
}
`, "narrow box of height 5\nflat box of width 7\nother\ntriangle-ish 3\nnamed sq\nother\nnothing\n1\n12\n0\npoint o\nbig 5\ncircle 2\nblank\n",
			[]string{"describe", "top", "drawn", "main"}},
		{"nested variant patterns in if-patterns", `import std/io

enum Req {
  Get(Int)
  Put {key: String, value: Int}
  Stop
}

enum Code {
  Num(Int)
  Word(String)
}

fn get_id(m: Maybe<Req>): Int {
  if Some(Req.Get(n)) = m {
    n
  } else {
    -1
  }
}

fn put_key(m: Maybe<Req>): String {
  if Some(Req.Put{key, value: 0}) = m {
    "zero " + key
  } else {
    "no"
  }
}

fn stopped(m: Result<Req, String>): Bool {
  if Ok(.Stop) = m {
    True
  } else {
    False
  }
}

fn seven(c: Code): String {
  if Code.Num(7) = c {
    "lucky"
  } else if Code.Word("seven") = c {
    "spelled"
  } else {
    "plain"
  }
}

fn main() {
  io.print(get_id(Some(Req.Get(4))))
  io.print(get_id(Some(Req.Stop)))
  io.print(get_id(None))
  io.print(put_key(Some(Req.Put{key: "a", value: 0})))
  io.print(put_key(Some(Req.Put{key: "a", value: 1})))
  io.print(put_key(None))
  io.inspect(stopped(Ok(Req.Stop)))
  io.inspect(stopped(Ok(Req.Get(1))))
  io.inspect(stopped(Err("e")))
  io.print(seven(Code.Num(7)))
  io.print(seven(Code.Num(8)))
  io.print(seven(Code.Word("seven")))
  io.print(seven(Code.Word("six")))
}
`, "4\n-1\n-1\nzero a\nno\nno\nTrue\nFalse\nFalse\nlucky\nplain\nspelled\nplain\n",
			[]string{"get_id", "put_key", "stopped", "seven", "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			irLeftoversRetained(t, tc.src, tc.retained...)
			verifyLambdaProgram(t, tc.src, tc.want)
		})
	}
}

// irLeftoversRetained requires every named function of src to be retained,
// so a passing run is a run of the retained bodies.
func irLeftoversRetained(t *testing.T, src string, names ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	readBack := map[string]bool{}
	prev, prevF := IRDeclineObserved, irFuncObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	irFuncObserved = func(origin irFuncOrigin, name string, f *ir.Func, read bool) {
		if origin != irFromStd && f != nil && read {
			readBack[name] = true
		}
	}
	res, _, err := GenerateIR(p)
	IRDeclineObserved, irFuncObserved = prev, prevF
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			have[f.Name()] = true
		}
	}
	for _, name := range names {
		if !have[name] {
			t.Errorf("%s was not retained (first decline: %q)", name, declined[name])
		} else if !readBack[name] {
			t.Errorf("%s was retained but its Go is the walk's, so the Source comparison says nothing about it", name)
		}
	}
}

// Test bodies: a bare-name assertion over a pipe-valued binding prints the
// binding's `pipeline values:` block, a sequence-valued row renders as
// `Seq`, and Range cardinality and `group_by` run inside a
// body. Several cases fail on purpose so the report text itself is compared
// between the VM and the golden record.
const irLeftoversTestBodySource = `fn double(n: Int): Int {
  n * 2
}

fn big?(n: Int): Bool {
  n > 100
}

test "a piped binding asserted bare" {
  ok = 4 |> double() |> big?()
  assert ok
}

test "a piped binding refuted bare" {
  small = [1, 2, 3] |> Iter.map(|x| x * 2) |> Iter.any?(|x| x > 4)
  refute small
}

test "a piped binding that passes" {
  fine = 60 |> double() |> big?()
  assert fine
}

test "a piped binding over a pattern" {
  n = 3 |> double() |> double()
  assert 13 = n
}

test "a sequence row" {
  ys = [1, 2] |> Iter.map(|x| x + 1)
  assert Iter.count(ys) == 3
}

test "range counts" {
  assert Iter.count(1..=20) == 20
  n = Iter.count(1..4)
  assert n == 4
}
`

func TestIRLeftovers_TestBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leftovers_test.nomi")
	if err := os.WriteFile(path, []byte(irLeftoversTestBodySource), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	prevD := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prevD
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != 6 {
		t.Fatalf("retained %v; want all 6 cases (declines: %v)", module, declined)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	interp := goldenReference(t, path)
	if interp.exit == 0 || !strings.Contains(interp.stdout, "pipeline values:") || !strings.Contains(interp.stdout, "<iter>") {
		t.Fatalf("the failing reports lost the rows this compares:\n%s", interp.stdout)
	}
	if out.String() != interp.stdout || exit != interp.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			exit, out.String(), interp.exit, interp.stdout)
	}
}

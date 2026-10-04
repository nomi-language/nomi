package vmhost_test

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// THE CONSTRUCT SWEEP: ordinary constructs at a spread of element types, each
// case a self-checking assertion, all run on the VM. It exists to find the next
// BLOCKED function before a user does. One generated test file per element
// type, one case per construct, so a gap names both.
//
// A case the VM cannot run fails the test unless sweepKnown lists it with the
// start of its decline reason, and a listed case that runs fails it too, so the
// list only shrinks. A case that runs and fails its assertion is a wrong answer
// (or a sweep bug) and always fails the test. NOMI_SWEEP_LIST=1 logs every
// BLOCKED case, listed or not.

// sweepType is one element type: its spelling, two distinct values, and what
// it supports.
type sweepType struct {
	name     string
	spelling string
	a, b     string
	ordered  bool // a < b holds and the type is Comparable
	hashable bool // usable as a Map key and a Set element
	display  bool // has Display, so it interpolates
}

var sweepTypes = []sweepType{
	{"Int", "Int", "1", "2", true, true, true},
	{"Float", "Float", "1.5", "2.5", true, false, true},
	{"String", "String", `"a"`, `"b"`, true, true, true},
	{"Bool", "Bool", "False", "True", true, true, true},
	{"Decimal", "Decimal", "1.5d", "2.5d", true, true, true},
	{"Struct", "P", "P{a: 1}", "P{a: 2}", true, true, false},
	{"Enum", "Color", "Color.Red", "Color.Green", true, true, false},
	{"Tuple", "(Int, String)", `(1, "a")`, `(2, "b")`, false, true, false},
	{"Maybe", "Maybe<Int>", "Some(1)", "None", false, true, false},
	{"List", "List<Int>", "[1]", "[2]", true, true, false},
	{"Vector", "Vector<Int>", "#[1]", "#[2]", true, true, false},
	{"Map", "Map<String, Int>", `{"k" => 1}`, `{"k" => 2}`, false, true, false},
	{"Set", "Set<Int>", "#{1}", "#{2}", false, true, false},
	{"Record", "{x: Int}", "{x: 1}", "{x: 2}", false, false, false},
	// A struct whose hand-written Equatable and Hashable ignore its `note`.
	{"Hand", "H", `H{k: 1, note: "a"}`, `H{k: 2, note: "a"}`, false, true, false},
}

// sweepHand is true for the type whose equality is hand-written.
func sweepHand(t sweepType) bool { return t.name == "Hand" }

// sweepConstruct is one construct: a case body over $A, $B and $T (the
// type's spelling), and the capability it needs.
type sweepConstruct struct {
	name  string
	needs func(sweepType) bool
	body  string
}

func sweepAll(sweepType) bool { return true }

var sweepConstructs = []sweepConstruct{
	{"equality", sweepAll, "assert $A == $A\n  refute $A == $B"},
	{"binding and if", sweepAll, "v = if True { $A } else { $B }\n  assert v == $A"},
	{"list map", sweepAll, "xs = [$A, $B]\n  ys = xs |> Iter.map(|x| x) |> Iter.to_list()\n  assert ys == xs"},
	{"list filter count", sweepAll, "n = [$A, $B, $A] |> Iter.filter(|x| x == $A) |> Iter.count()\n  assert n == 2"},
	{"list pattern", sweepAll, "case [$A, $B] {\n    [h, .._] -> assert h == $A\n    [] -> assert False\n  }"},
	{"list head", sweepAll, "assert List.head([$A, $B]) == Some($A)"},
	{"maybe", sweepAll, "assert Maybe.with_default(Some($A), $B) == $A"},
	{"result", sweepAll, "r: Result<$T, String> = Ok($A)\n  assert r == Ok($A)"},
	{"tuple destructure", sweepAll, "(x, y) = ($A, $B)\n  assert x == $A\n  assert y == $B"},
	{"generic struct field", sweepAll, "box = Box{inner: $A}\n  assert box.inner == $A"},
	{"generic fn", sweepAll, "assert same($A) == $A"},
	{"generic list fn", sweepAll, "assert first_or([$B, $A], $A) == $B"},
	{"nested fn recursion", sweepAll, "fn pick(n: Int): $T {\n    if n == 0 { $A } else { pick(n - 1) }\n  }\n  assert pick(3) == $A"},
	{"lambda capture", sweepAll, "f = || $A\n  assert f() == $A"},
	{"reduce", sweepAll, "n = Iter.reduce([$A, $B], |acc = 0, _x| acc + 1)\n  assert n == 2"},
	{"debug", sweepAll, "s = Debug.inspect($A)\n  assert String.length(s) > 0"},
	{"io inspect", sweepAll, "io.inspect($A)\n  assert True"},
	{"once", sweepAll, "assert shared == $A"},
	{"concurrent", sweepAll, "v = concurrent {\n    t = Task.spawn(|| $A)\n    Task.await(t)\n  }\n  assert v == $A"},
	{"map value", sweepAll, "m = {\"k\" => $A}\n  assert Map.get(m, \"k\") == Some($A)"},
	{"vector", sweepAll, "v = #[$A, $B]\n  assert Vector.at(v, 1) == Some($B)"},
	{"struct holding", sweepAll, "h = Holder{item: $A}\n  assert h.item == $A"},
	{"field default none", sweepAll, "o = Opt{}\n  assert o.v == None\n  assert Opt{v: Some($A)}.v == Some($A)"},
	{"enum payload", sweepAll, "w = Wrap.One($A)\n  case w {\n    .One(x) -> assert x == $A\n    .Zero -> assert False\n  }"},
	{"fn roundtrip", sweepAll, "assert echo($A) == $A"},
	{"fn case to maybe", sweepAll, "assert pick_first([$A, $B]) == Some($A)\n  assert pick_first([]) == None"},
	{"fn result", sweepAll, "assert wrap_ok($A) == Ok($A)"},
	{"fn reduce with equality", sweepAll, "assert count_eq([$A, $B, $A], $A) == 2"},
	{"fn tail recursion", sweepAll, "assert tail_find([$A, $B], $B, 0) == 1"},
	{"interpolation", func(t sweepType) bool { return t.display }, "s = \"${$A}\"\n  assert String.length(s) > 0"},
	{"map key", func(t sweepType) bool { return t.hashable }, "m = {$A => 1}\n  assert Map.get(m, $A) == Some(1)"},
	{"set", func(t sweepType) bool { return t.hashable }, "s = #{$A, $B, $A}\n  assert Set.size(s) == 2"},
	{"hash", func(t sweepType) bool { return t.hashable }, "assert Hashable.hash($A) == Hashable.hash($A)"},
	{"ordering", func(t sweepType) bool { return t.ordered }, "assert $A < $B\n  refute $B < $A"},
	{"compare", func(t sweepType) bool { return t.ordered }, "assert Comparable.compare($A, $B) == Ordering.Less"},
	{"sort", func(t sweepType) bool { return t.ordered }, "assert Iter.sort([$B, $A]) == [$A, $B]"},
	{"list compare", func(t sweepType) bool { return t.ordered }, "assert List.compare([$A], [$B]) == Ordering.Less"},
	// Two values the hand-written `equal?` calls equal are one element and
	// one key, at the top and nested.
	{"hand set", sweepHand, "s = Iter.to_set([H{k: 1, note: \"x\"}, H{k: 1, note: \"y\"}, $B])\n  assert Set.size(s) == 2\n  assert Set.contains?(s, H{k: 2, note: \"z\"})"},
	{"hand map key", sweepHand, "m = {H{k: 1, note: \"x\"} => 1}\n  assert Map.get(m, H{k: 1, note: \"y\"}) == Some(1)\n  assert Map.size(Map.put(m, H{k: 1, note: \"z\"}, 2)) == 1"},
	{"hand nested", sweepHand, "s = Iter.to_set([[H{k: 1, note: \"x\"}], [H{k: 1, note: \"y\"}]])\n  assert Set.size(s) == 1\n  assert #{$A} == Iter.to_set([H{k: 1, note: \"q\"}])"},
	{"hand derived holder", sweepHand, "assert Set.size(Iter.to_set([Keep{h: H{k: 1, note: \"x\"}}, Keep{h: H{k: 1, note: \"y\"}}])) == 1"},
	{"hand group by", sweepHand, "g = [H{k: 1, note: \"x\"}, H{k: 1, note: \"y\"}] |> Iter.group_by(|h| h)\n  assert Map.size(g) == 1"},
}

const sweepPrelude = `import std/io
import std/tasks.{Task}

struct P {
  a: Int
}

derive Equatable for P

derive Hashable for P

derive Comparable for P

enum Color {
  Red
  Green
}

derive Equatable for Color

derive Hashable for Color

derive Comparable for Color

struct H {
  k: Int
  note: String
}

impl Equatable for H {
  fn equal?(a: H, b: H): Bool {
    a.k == b.k
  }
}

impl Hashable for H {
  fn hash(h: H): Int {
    Hashable.hash(h.k)
  }
}

struct Keep {
  h: H
}

derive Equatable for Keep

derive Hashable for Keep

struct Box<X> {
  inner: X
}

fn same<X>(x: X): X {
  x
}

fn first_or<X>(xs: List<X>, d: X): X {
  case xs {
    [h, .._] -> h
    [] -> d
  }
}

struct Holder {
  item: $T
}

struct Opt {
  v: Maybe<$T> = Maybe.None
}

enum Wrap {
  One $T
  Zero
}

once shared: $T = $A

fn echo(x: $T): $T {
  x
}

fn pick_first(xs: List<$T>): Maybe<$T> {
  case xs {
    [h, .._] -> Some(h)
    [] -> None
  }
}

fn wrap_ok(x: $T): Result<$T, String> {
  Ok(x)
}

fn count_eq(xs: List<$T>, y: $T): Int {
  Iter.reduce(xs, |acc = 0, x| if x == y { acc + 1 } else { acc })
}

fn tail_find(xs: List<$T>, y: $T, i: Int): Int {
  case xs {
    [] -> -1
    [h, ..rest] -> if h == y { i } else { tail_find(rest, y, i + 1) }
  }
}
`

// sweepProgram is one element type's test file.
func sweepProgram(ty sweepType) string {
	sub := strings.NewReplacer("$A", ty.a, "$B", ty.b, "$T", ty.spelling)
	var sb strings.Builder
	sb.WriteString(sub.Replace(sweepPrelude))
	for _, c := range sweepConstructs {
		if !c.needs(ty) {
			continue
		}
		fmt.Fprintf(&sb, "\ntest %q {\n  %s\n}\n", c.name, sub.Replace(c.body))
	}
	return sb.String()
}

// sweepKnown is every case the VM cannot run today, keyed "Type / construct",
// with the start of its decline reason.
var sweepKnown = map[string]string{}

func TestSweep_OrdinaryConstructsRunOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	var blocked, failed []string
	seen := map[string]bool{}
	for _, ty := range sweepTypes {
		src := sweepProgram(ty)
		p, err := vmhost.LoadSource("main", src)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: the program does not load: %v", ty.name, err))
			continue
		}
		var out bytes.Buffer
		for _, c := range p.Cases(&out, vmhost.TestOptions{}) {
			key := ty.name + " / " + c.Name
			seen[key] = true
			switch {
			case c.Blocked != nil:
				reason := strings.Join(c.Blocked, "; ")
				want, known := sweepKnown[key]
				if !known || !strings.Contains(reason, want) {
					blocked = append(blocked, key+": "+reason)
				}
			case c.Err != nil:
				failed = append(failed, key+": "+c.Err.Error())
			default:
				if _, known := sweepKnown[key]; known {
					failed = append(failed, key+": runs now; remove it from sweepKnown")
				}
			}
		}
	}
	for key := range sweepKnown {
		if !seen[key] {
			failed = append(failed, key+": listed in sweepKnown but the sweep has no such case")
		}
	}
	sort.Strings(blocked)
	sort.Strings(failed)
	if os.Getenv("NOMI_SWEEP_LIST") != "" {
		for _, b := range blocked {
			t.Log("BLOCKED " + b)
		}
	}
	if len(blocked) > 0 || len(failed) > 0 {
		t.Fatalf("%d case(s) BLOCKED that sweepKnown does not list, %d failing:\n%s\n%s",
			len(blocked), len(failed), strings.Join(blocked, "\n"), strings.Join(failed, "\n"))
	}
}

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

const irIterStageSource = `import std/io

fn main() {
  io.inspect(Iter.iterate(1, |x| x * 3) |> Iter.take(4) |> Iter.to_list())
  io.print(Iter.repeat("ab") |> Iter.take(2) |> String.join())
  io.inspect([1, 2, 3, 4] |> Iter.drop(2) |> Iter.to_list())
  io.inspect(Iter.from(0) |> Iter.drop_while(|x| x < 2) |> Iter.take_while(|x| x < 5) |> Iter.to_list())
  io.inspect([1, 2] |> Iter.cycle() |> Iter.take(5) |> Iter.to_list())
  [7, 8] |> Iter.each(|x| io.print(x))
  io.inspect(Set.size([3, 1, 3, 2] |> Iter.to_set()))
  io.inspect([("a", 1), ("b", 2), ("a", 3)] |> Iter.to_map())
  io.inspect([1, 2] |> Iter.flat_map(|x| [x, x * 10]) |> Iter.to_list())
  io.inspect([[1], [2, 3]] |> Iter.flatten())
  io.inspect([1, 2, 3, 4, 5] |> Iter.chunks(2) |> Iter.to_list())
  io.inspect([1, 1, 2, 1] |> Iter.chunk_by(|x| x) |> Iter.to_list())
  io.inspect(Iter.empty?([]))
}
`

// std/iter's remaining adapters, materializers and constructors are ir.Iter
// nodes the VM drives through rt's Seq functions of the same names.
func TestIRIterStage_AdaptersAndMaterializers(t *testing.T) {
	verifyLambdaProgram(t, irIterStageSource,
		"[1, 3, 9, 27]\nabab\n[3, 4]\n[2, 3, 4]\n[1, 2, 1, 2, 1]\n7\n8\n3\n"+
			"{\"a\" => 3, \"b\" => 2}\n[1, 10, 2, 20]\n[1, 2, 3]\n[[1, 2], [3, 4], [5]]\n[[1, 1], [2], [1]]\nTrue\n")
}

const irIterToVectorSource = `import std/io

struct Countdown {
  from: Int
}

impl Iter for Countdown {
  fn each_while(c: Countdown, yield: (Int) -> Bool): Bool {
    if c.from <= 0 { True } else { if yield(c.from) { each_while(Countdown{from: c.from - 1}, yield) } else { False } }
  }
}

fn collect<T>(source: Iter<T>): Vector<T> { Iter.to_vector(source) }

fn main() {
  io.inspect([1, 2, 3] |> Iter.to_vector())
  io.inspect(#["a", "b"] |> Iter.to_vector())
  io.inspect(#{3, 1, 3} |> Iter.to_vector())
  io.inspect({"a" => 1, "b" => 2} |> Iter.to_vector())
  io.inspect("hé" |> Iter.to_vector())
  io.inspect(1..=3 |> Iter.to_vector())
  io.inspect(String.to_bytes("ab") |> Iter.to_vector() |> Vector.length())
  io.inspect(Countdown{from: 2} |> Iter.to_vector())
  io.inspect(Iter.from(1) |> Iter.filter(|x| x % 2 == 0) |> Iter.map(|x| x * 10) |> Iter.take(2) |> Iter.to_vector())
  empty: List<Int> = []
  io.inspect(Iter.to_vector(empty))
  io.inspect(collect(1..3))
  io.inspect(collect(Countdown{from: 1}))
  io.inspect(collect(["p", "q"]))
}
`

// Iter.to_vector is one ir.Iter node, rt.SeqToVector, over every source
// family to_list reads, and through a generic function's Iter<T> parameter.
func TestIRIterToVector_EverySourceFamily(t *testing.T) {
	verifyLambdaProgram(t, irIterToVectorSource,
		"#[1, 2, 3]\n#[\"a\", \"b\"]\n#[3, 1]\n#[(\"a\", 1), (\"b\", 2)]\n#[\"h\", \"é\"]\n#[1, 2, 3]\n2\n#[2, 1]\n"+
			"#[20, 40]\n#[]\n#[1, 2]\n#[1]\n#[\"p\", \"q\"]\n")
}

const irStringJoinSource = `import std/io

struct Countdown {
  from: Int
}

impl Iter for Countdown {
  fn each_while(c: Countdown, yield: (Int) -> Bool): Bool {
    if c.from <= 0 { True } else { if yield(c.from) { each_while(Countdown{from: c.from - 1}, yield) } else { False } }
  }
}

fn joined(parts: Iter<String>): String { String.join(parts, "/") }

fn main() {
  io.print(String.join(["a", "b", "c"], ", "))
  io.print(String.join(["he", "llo"]))
  io.print("[" + String.join([], "-") + "]")
  io.print(#{"solo"} |> String.join(", "))
  io.print(#["x", "y"] |> String.join("+"))
  io.print("héllo" |> String.join("."))
  io.print("héllo" |> Iter.map(String.to_upper) |> String.join())
  io.print(1..=3 |> Iter.map(Int.to_string) |> String.join("-"))
  io.print(Countdown{from: 3} |> Iter.map(Int.to_string) |> String.join(","))
  io.print(Iter.from(1) |> Iter.filter(|x| x % 2 == 0) |> Iter.map(Int.to_string) |> Iter.take(3) |> String.join(" "))
  io.print({"k" => 1} |> Iter.map(|(k, v)| k + "=" + Int.to_string(v)) |> String.join("&"))
  io.print(joined(["p", "q"]))
  io.print(joined(#{"r"}))
}
`

// String.join is one ir.Iter node, rt.SeqJoin, over every source family
// Iter reads, with the separator defaulting to "", and through a function's
// Iter<String> parameter.
func TestIRStringJoin_EverySourceFamily(t *testing.T) {
	verifyLambdaProgram(t, irStringJoinSource,
		"a, b, c\nhello\n[]\nsolo\nx+y\nh.é.l.l.o\nHÉLLO\n1-2-3\n3,2,1\n2 4 6\nk=1\np/q\nr\n")
}

const irSetAlgebraSource = `import std/io

fn main() {
  a = #{1, 2, 3}
  b = #{2, 3, 4}
  io.inspect(Set.union(a, b))
  io.inspect(Set.intersection(a, b))
  io.inspect(Set.difference(a, b))
  io.inspect(Set.subset?(#{1, 2}, a))
  io.inspect(Set.subset?(#{1, 9}, a))
  io.inspect(Iter.to_set([5, 4, 5]))
  io.inspect(List.compare([1, 2, 3], [1, 2, 4]))
  io.inspect(List.compare([1, 2], [1, 2]))
  io.inspect(Vector.compare(#[2], #[1, 9]))
}
`

// Set algebra and Iter.to_set are machine intrinsics keeping std's member
// order; List.compare and Vector.compare take the element's comparator.
func TestIRSetAlgebra_AndContainerCompare(t *testing.T) {
	verifyLambdaProgram(t, irSetAlgebraSource,
		"#{1, 2, 3, 4}\n#{2, 3}\n#{1}\nTrue\nFalse\n#{5, 4}\nLess\nEqual\nGreater\n")
}

// A bare `None` in a vector or set literal takes the other items' kind,
// before or after them, as it does in a list literal.
func TestIRVectorSetLiteral_BareNoneTakesTheItemsKind(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn main() {
  io.inspect(#[Some(1), None])
  io.inspect(#[None, Some("a")])
  io.inspect(#{Some(1), None, Some(1)})
  io.inspect(Set.size(#{None, Some(2), None}))
}
`, "#[Some(1), None]\n#[None, Some(\"a\")]\n#{Some(1), None}\n2\n")
}

const irLoopShapesSource = `test "a state tuple advances through an if tail" {
  (_, steps) = Iter.loop(|state = (27, 0)| {
    (n, count) = state
    if n == 1 { break state }
    if n % 2 == 0 {
      (n / 2, count + 1)
    } else {
      (n * 3 + 1, count + 1)
    }
  })

  assert steps == 111
}

test "return is the next state and an if-else tail guards" {
  a = Iter.loop(|n = 1| {
    if n % 3 == 0 { break n }
    return n + 1
  })
  b = Iter.loop(|n = 5| if n == 0 { break n } else { n - 1 })

  assert a == 3
  assert b == 0
}

test "a stateless loop answers its break value" {
  answer = Iter.loop(|| { break 42 })

  assert answer == 42
}
`

// In a VM-only test body Iter.loop takes a `return v` tail as the next
// state, an `if` tail with a leaving branch as a guard, an `if` tail of values
// as the next state, and a stateless callback that breaks as its break value.
func TestIRLoop_TestBodyTailShapesAndStateless(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop_test.nomi")
	if err := os.WriteFile(path, []byte(irLoopShapesSource), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var mod *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			mod = m
		}
	}
	if mod == nil || len(mod.Tests()) != 3 {
		t.Fatalf("the three cases were not all retained")
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(mod, res.IRModules(), &out).RunTests(path)
	if reason != "" || exit != 0 || !strings.Contains(out.String(), "3 passed, 0 failed") {
		t.Fatalf("VM exit %d, reason %q:\n%s", exit, reason, out.String())
	}
}

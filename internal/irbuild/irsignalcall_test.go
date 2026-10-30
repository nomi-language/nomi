package irbuild

import "testing"

// An iter-sensitive function acts on an iteration only as its callback
// itself, passed positionally, by name or piped; a seeded iteration takes
// its starting value as `from:`. Its `continue` skips the element, its bare
// `break` stops, and its `break v` answers v and stops, as the callback's own
// would. The checker rejects every call of it
// (internal/analysis/iter_sensitive_call_test.go); `Iter.loop` over a named
// step is the one call the builder lowers (irsignalcall.go).

// The function itself as the callback of every iteration function that
// takes one without a seed, positional, by name and piped.
func TestIRSignalCall_TheCallbackItself(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn skip(x: Int) -> Int {
  if x == 2 { continue }
  if x == 4 { break }
  x * 10
}

fn decide(x: Int) -> Bool {
  if x == 2 { continue }
  if x == 5 { break True }
  x % 2 == 1
}

fn grow(n: Int) -> Int {
  if n > 20 { break }
  n * 2
}

fn pair(x: Int) -> List<Int> {
  if x == 2 { continue }
  if x == 4 { break }
  [x, x]
}

fn show(x: Int) {
  if x == 2 { continue }
  if x == 4 { break }
  io.print("each ${x}")
}

fn main() {
  io.print(Debug.inspect(Iter.map([1, 2, 3, 4, 5], skip) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.filter([1, 2, 3, 4, 5, 6, 7], decide) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.take_while([1, 2, 3, 4, 5, 6, 7], decide) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.drop_while([1, 2, 3, 4, 5, 6], decide) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.flat_map([1, 2, 3, 4, 5], pair) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.iterate(1, grow) |> Iter.to_list()))
  Iter.each([1, 2, 3, 4, 5], show)
  io.print(Debug.inspect(Iter.find([1, 2, 3, 4, 5, 6], decide)))
  io.print(Debug.inspect(Iter.any([2, 4, 6, 5, 7], decide)))
  io.print(Debug.inspect(Iter.all([1, 2, 3, 5, 6], decide)))
  io.print(Debug.inspect(Map.map_values(%{"a" => 1, "b" => 2, "c" => 3, "d" => 4, "e" => 5}, skip)))
  io.print(Debug.inspect(Map.map_keys(%{1 => "a", 2 => "b", 3 => "c", 4 => "d", 5 => "e"}, skip)))
  io.print(Debug.inspect(Iter.find([1, 2, 3, 4, 5, 6], f: decide)))
  io.print(Debug.inspect([1, 2, 3, 4, 5] |> Iter.map(skip) |> Iter.to_list()))
}
`, "[10, 30]\n[1, 3, 5]\n[1, 3]\n[4, 5, 6]\n[1, 1, 3, 3]\n[1, 2, 4, 8, 16]\neach 1\neach 3\nSome(1)\nTrue\nTrue\n%{\"a\" => 10, \"c\" => 30}\n%{10 => \"a\", 30 => \"c\"}\nSome(1)\n[10, 30]\n")
}

// The function itself as the callback of every seeded iteration function,
// its starting value given as `from:`: positional, piped, by name, with
// `from:` written first, and `Iter.loop` over a named step whose `break`,
// `break v` and `continue` are the loop's.
func TestIRSignalCall_ASeededCallWithFrom(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn add(acc: Int, x: Int) -> Int {
  if x == 2 { continue }
  if x == 4 { break acc * 100 }
  acc + x
}

fn emit(s: Int, x: Int) -> #(Int, Int) {
  if x == 2 { continue }
  if x == 4 { break }
  #(x * 10, s + x)
}

fn tick(n: Int) -> Int {
  if n == 5 { break n * 10 }
  n + 1
}

fn halt(n: Int) -> Int {
  if n == 3 { break }
  n + 1
}

fn odd_steps(n: Int) -> Int {
  if n % 2 == 0 {
    return n + 3
  }
  if n > 10 { break n }
  n + 1
}

fn main() {
  io.print(Debug.inspect(Iter.reduce([1, 2, 3, 4, 5], add, from: 0)))
  io.print(Debug.inspect(Iter.reduce([1, 2, 3], add, from: 10)))
  io.print(Debug.inspect([1, 2, 3] |> Iter.reduce(add, from: 0)))
  io.print(Debug.inspect(Iter.reduce([1, 2, 3], f: add, from: 0)))
  io.print(Debug.inspect(Iter.reduce([1, 2, 3], from: 0, add)))
  io.print(Debug.inspect(Iter.scan([1, 2, 3, 4, 5], add, from: 0) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.map_reduce([1, 2, 3, 4, 5], emit, from: 0)))
  io.print(Debug.inspect(Iter.loop(tick, from: 0)))
  io.print(Debug.inspect(Iter.loop(halt, from: 0)))
  io.print(Debug.inspect(Iter.loop(odd_steps, from: 1)))
  io.print(Debug.inspect(Iter.loop(f: tick, from: 2)))
}
`, "400\n14\n4\n4\n4\n[1, 4, 400]\n#([10, 30], 4)\n50\n3\n13\n50\n")
}

// `from:` with a callback that is not an iter-sensitive function: an ordinary
// named function, an owner's function, a capture, and a lambda without a
// default whose own `break` and `continue` act on the fold or the loop.
func TestIRSignalCall_FromWithAnyCallback(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn plus(a: Int, b: Int) -> Int {
  a + b
}

fn main() {
  io.print(Debug.inspect(Iter.reduce([1, 2, 3], plus, from: 0)))
  io.print(Debug.inspect(Iter.reduce([1, 2, 3], Int.add, from: 100)))
  io.print(Debug.inspect(Iter.reduce([1, 2, 3], &(&1 * 10 + &2), from: 0)))
  io.print(Debug.inspect(Iter.reduce([1, 2, 3, 4], fn acc, x -> {
    if x == 2 { continue }
    if x == 4 { break acc * 10 }
    acc + x
  }, from: 0)))
  io.print(Debug.inspect(Iter.scan([1, 2, 3], plus, from: 1) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.map_reduce(["a", "bb"], fn n, s -> #(s + s, n + 1), from: 0)))
  io.print(Debug.inspect(Iter.loop(fn n -> {
    if n > 4 { break n }
    n + 2
  }, from: 1)))
  io.print(Debug.inspect(Iter.loop(&(if &1 > 3 { break } else { &1 + 1 }), from: 0)))
  io.print(Debug.inspect(Iter.reduce([1, 2, 3], &(if &2 == 2 { continue } else { &1 + &2 }), from: 0)))
  empty: List<Int> = []
  io.print(Debug.inspect(Iter.reduce(empty, plus, from: 7)))
}
`, "6\n106\n123\n40\n[2, 4, 7]\n#([\"aa\", \"bbbb\"], 2)\n5\n4\n4\n7\n")
}

// A loop over a function value that cannot signal never ends, and lowers as
// a loop with no `break` does: a partial capture, a struct field's function,
// a local function value and a named function.
func TestIRSignalCall_ALoopOverAnyFunctionValueLowers(t *testing.T) {
	const src = `import std/io

struct Cfg {
  step: (Int) -> Int
}

fn up(n: Int) -> Int {
  n + 1
}

fn bump(n: Int, by: Int) -> Int {
  n + by
}

fn partial() -> Int {
  Iter.loop(&bump(&1, 4), from: 1)
}

fn field() -> Int {
  cfg = #Cfg{step: up}
  Iter.loop(cfg.step, from: 0)
}

fn local() -> Int {
  f = fn n: Int -> n + 2
  Iter.loop(f, from: 0)
}

fn named() -> Int {
  Iter.loop(up, from: 0)
}

fn main() {
  io.print(partial() + field() + local() + named())
}
`
	if declined := irDeclinesOf(t, src); len(declined) != 0 {
		t.Fatalf("declined %v; want every body lowered", declined)
	}
}

// A pattern-bound starting value, a tuple and a struct, given as `from:`.
func TestIRSignalCall_PatternSeedFrom(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Acc {
  total: Int
  items: List<Int>
}

fn keep(acc: #(Int, List<Int>), x: Int) -> #(Int, List<Int>) {
  if x == 2 { continue }
  #(acc.0 + x, acc.1 + [x])
}

fn add(acc: Acc, x: Int) -> Acc {
  if x == 2 { continue }
  if x == 4 { break }
  #Acc{total: acc.total + x, items: acc.items + [x]}
}

fn main() {
  a = Iter.reduce([1, 2, 3], keep, from: #(0, []))
  io.print(Debug.inspect(a))
  b = Iter.reduce([1, 2, 3, 4, 5], add, from: #Acc{total: 0, items: []})
  io.print(Debug.inspect(b))
  c = Iter.reduce([1, 2, 3], fn #(n, xs), x -> #(n + x, xs + [x]), from: #(0, []))
  io.print(Debug.inspect(c))
}
`, "#(4, [1, 3])\n#Acc{total: 4, items: [1, 3]}\n#(6, [1, 2, 3])\n")
}

// A `defer` in the function runs as it leaves, whatever it leaves with, as
// an iteration's callback and as a loop's named step.
func TestIRSignalCall_DefersRunAsTheFunctionLeaves(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn inner(x: Int) -> Int {
  defer io.print("inner ${x}")
  if x == 2 { continue }
  if x == 3 { break x * 10 }
  x
}

fn step(n: Int) -> Int {
  defer io.print("step ${n}")
  if n == 1 { return 2 }
  if n == 3 { break n * 10 }
  n + 1
}

fn main() {
  out = Iter.map([1, 2, 3, 4], inner) |> Iter.to_list()
  io.print(Debug.inspect(out))
  io.print(Debug.inspect(Iter.loop(step, from: 0)))
}
`, "inner 1\ninner 2\ninner 3\n[1, 30]\nstep 0\nstep 1\nstep 2\nstep 3\n30\n")
}

// A generic iter-sensitive function as the callback.
func TestIRSignalCall_AGenericFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn head_or_skip<T>(xs: List<T>) -> T {
  case List.head(xs) {
    Some(x) -> x
    None -> continue
  }
}

fn main() {
  io.print(Debug.inspect(Iter.map([[1], [], [2]], head_or_skip) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.map([["a"], [], ["b"]], head_or_skip) |> Iter.to_list()))
}
`, "[1, 2]\n[\"a\", \"b\"]\n")
}

// Another file's iter-sensitive function: `pub`, passed qualified and by a
// selective import, to an unseeded iteration, a fold with `from:` and a
// loop with `from:`.
func TestIRSignalCall_AnotherFilesFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
import lib
import lib.{keep, add, step}

fn main() {
  io.print(Debug.inspect(Iter.map([3, -1, 4, 500, 6], lib.keep) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.map([3, -1, 4, 500, 6], keep) |> Iter.to_list()))
  io.print(Debug.inspect(Iter.reduce([1, -5, 2], lib.add, from: 0)))
  io.print(Debug.inspect(Iter.reduce([1, -5, 2], add, from: 0)))
  io.print(Debug.inspect(Iter.loop(lib.step, from: 0)))
  io.print(Debug.inspect(Iter.loop(step, from: 1)))
}
`, "[6, 8, 500]\n[6, 8, 500]\n3\n3\n300\n300\n", map[string]string{"lib.nomi": `pub fn keep(n: Int) -> Int {
  if n < 0 { continue }
  if n > 100 { break n }
  n * 2
}

pub fn add(acc: Int, n: Int) -> Int {
  if n < 0 { continue }
  acc + n
}

pub fn step(n: Int) -> Int {
  if n >= 3 { break n * 100 }
  n + 1
}
`})
}

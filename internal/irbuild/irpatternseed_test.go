package irbuild

import "testing"

// A seeded callback whose accumulator is bound by a pattern and which carries
// `break` or `continue`. The seed is lowered whole at the accumulator's kind
// before the callback, and `continue` or a bare `break` answers that whole
// value, so the seed's kind is the one the pattern parameter takes, never the
// kind of a plain parameter's inferred type.

// reduce: `continue` keeps the accumulator, a bare `break` stops with it, and
// `break v` stops with v.
func TestIRPatternSeed_ReduceSignals(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn main() {
  skipped = Iter.reduce([1, 2, 3], fn #(n, xs) = #(0, []), x -> {
    if x == 2 { continue }
    #(n + x, xs + [x])
  })
  io.print(Debug.inspect(skipped))
  stopped = Iter.reduce([1, 2, 3, 4], fn #(n, xs) = #(0, []), x -> {
    if x == 3 { break }
    #(n + x, xs + [x])
  })
  io.print(Debug.inspect(stopped))
  answered = Iter.reduce([1, 2, 3, 4], fn #(n, xs) = #(0, []), x -> {
    if x == 3 { break #(n * 100, xs) }
    #(n + x, xs + [x])
  })
  io.print(Debug.inspect(answered))
}
`, "#(4, [1, 3])\n#(3, [1, 2])\n#(300, [1, 2])\n")
}

// scan and map_reduce: `continue` yields or adds nothing and keeps the state,
// and a bare `break` ends the walk.
func TestIRPatternSeed_ScanAndMapReduceSignals(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn main() {
  states = Iter.scan([1, 2, 3, 4], fn #(n, k) = #(0, 0), x -> {
    if x == 2 { continue }
    if x == 4 { break }
    #(n + x, k + 1)
  }) |> Iter.to_list()
  io.print(Debug.inspect(states))
  outputs = Iter.map_reduce([1, 2, 3, 4], fn #(n, k) = #(0, 0), x -> {
    if x == 2 { continue }
    if x == 4 { break }
    #(x * 10, #(n + x, k + 1))
  })
  io.print(Debug.inspect(outputs))
}
`, "[#(1, 1), #(4, 2)]\n#([10, 30], #(4, 2))\n")
}

// A struct pattern and a nested tuple pattern bind the seed the same way.
func TestIRPatternSeed_StructAndNestedPatterns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Acc {
  total: Int
  items: List<Int>
}

fn main() {
  acc = Iter.reduce([1, 2, 3], fn #Acc{total, items} = #Acc{total: 0, items: []}, x -> {
    if x == 2 { continue }
    #Acc{total: total + x, items: items + [x]}
  })
  io.print(Debug.inspect(acc))
  bounds = Iter.reduce([1, 2, 3], fn #(n, #(lo, hi)) = #(0, #(100, 0)), x -> {
    if x == 2 { continue }
    #(n + x, #(Int.min(lo, x), Int.max(hi, x)))
  })
  io.print(Debug.inspect(bounds))
}
`, "#Acc{total: 4, items: [1, 3]}\n#(4, #(1, 3))\n")
}

// A named function whose own accumulator parameter is a pattern, passed as
// the callback with its starting value as `from:`.
func TestIRPatternSeed_NamedFunctionFrom(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn keep(#(n, xs): #(Int, List<Int>), x: Int) -> #(Int, List<Int>) {
  if x == 2 { continue }
  #(n + x, xs + [x])
}

fn main() {
  kept = Iter.reduce([1, 2, 3], keep, from: #(0, []))
  io.print(Debug.inspect(kept))
}
`, "#(4, [1, 3])\n")
}

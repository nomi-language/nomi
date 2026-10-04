package irbuild

import (
	"testing"
)

func TestIRSignal_TourBreakAndContinue(t *testing.T) {
	verifyLambdaProgram(t, `fn main(): List<Int> {
  // break in reduce — stop accumulating once total would exceed 60.
  partial_sum =
    [10, 20, 30, 40, 50]
    |> Iter.reduce(|acc = 0, x| {
      if acc + x > 60 { break }
      acc + x
    })

  dbg partial_sum

  // continue in map — skip odd numbers from the output.
  evens_doubled =
    [1, 2, 3, 4, 5]
    |> Iter.map(|x| {
      if x % 2 != 0 { continue }
      x * 2
    })
    |> Iter.to_list()

  dbg evens_doubled
}
`, "dbg line 10: partial_sum = 60\ndbg line 21: evens_doubled = [4, 8]\n")
}

func TestIRSignal_ReduceAndAdapterOutcomes(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn seed(): Int {
  io.print("seed")
  100
}
fn main() {
  capped = [5, 10, 15, 20] |> Iter.reduce(|acc = seed(), x| {
    if x == 15 { break acc * 2 }
    acc + x
  })
  io.print(capped)
  positives = [3, -1, 4, -5, 2] |> Iter.reduce(|acc = 0, x| {
    if x < 0 { continue }
    acc + x
  })
  io.print(positives)
  until = [1, 2, 3, 4, 5] |> Iter.map(|x| {
    if x == 4 { break }
    x * 10
  }) |> Iter.to_list()
  dbg until
  last = [1, 2, 3, 4, 5] |> Iter.map(|x| {
    if x == 3 { break 99 }
    x
  }) |> Iter.to_list()
  dbg last
  kept = [1, 2, 3, 4, 5, 6] |> Iter.filter(|x| {
    if x == 5 { break }
    x % 2 == 0
  }) |> Iter.to_list()
  dbg kept
  return
}
`, "seed\n230\n9\ndbg line 21: until = [10, 20, 30]\ndbg line 26: last = [1, 2, 99]\ndbg line 31: kept = [2, 4]\n")
}

// A signal ending a `case` arm of a signalling callback leaves the callback
// as one ending a guard arm does: `break` stops with the accumulator, `break
// v` with v, and `continue` keeps the accumulator.
func TestIRSignal_CaseArmSignalLeavesTheCallback(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  total = [1, 2, 3] |> Iter.reduce(|acc = 0, x| {
    case x {
      2 -> break
      _ -> acc + x
    }
  })
  io.print(total)
  kept = Iter.reduce([1, 2, 3], |acc = 0, n| {
    case n {
      2 -> break acc + 10
      _ -> acc + n
    }
  })
  io.print(kept)
  skip = Iter.reduce([1, 2, 3], |acc = 0, n| {
    case n {
      2 -> continue
      _ -> acc + n
    }
  })
  io.print(skip)
}
`, "1\n11\n4\n")
}

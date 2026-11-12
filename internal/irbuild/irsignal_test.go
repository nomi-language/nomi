package irbuild

import (
	"testing"
)

func TestIRSignal_TourBreakAndContinue(t *testing.T) {
	verifyLambdaProgram(t, `fn main()
  // break in reduce — stop accumulating once total would exceed 60.
  partial_sum =
    [10, 20, 30, 40, 50]
    |> Iter.reduce(0, fn acc, x ->
      if acc + x > 60 then break
      acc + x
    end)

  dbg partial_sum

  // continue in map — skip odd numbers from the output.
  evens_doubled =
    [1, 2, 3, 4, 5]
    |> Iter.map(fn x ->
      if x % 2 != 0 then continue
      x * 2
    end)
    |> Iter.to_list()

  dbg evens_doubled
end
`, "dbg line 10: partial_sum = 60\ndbg line 21: evens_doubled = [4, 8]\n")
}

func TestIRSignal_ReduceAndAdapterOutcomes(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn seed() -> Int
  io.print("seed")
  100
end
fn main()
  capped = [5, 10, 15, 20] |> Iter.reduce(seed(), fn acc, x ->
    if x == 15 then break acc * 2
    acc + x
  end)
  io.print(capped)
  positives = [3, -1, 4, -5, 2] |> Iter.reduce(0, fn acc, x ->
    if x < 0 then continue
    acc + x
  end)
  io.print(positives)
  until = [1, 2, 3, 4, 5] |> Iter.map(fn x ->
    if x == 4 then break
    x * 10
  end) |> Iter.to_list()
  dbg until
  last = [1, 2, 3, 4, 5] |> Iter.map(fn x ->
    if x == 3 then break 99
    x
  end) |> Iter.to_list()
  dbg last
  kept = [1, 2, 3, 4, 5, 6] |> Iter.filter(fn x ->
    if x == 5 then break
    x % 2 == 0
  end) |> Iter.to_list()
  dbg kept
  return
end
`, "seed\n230\n9\ndbg line 21: until = [10, 20, 30]\ndbg line 26: last = [1, 2, 99]\ndbg line 31: kept = [2, 4]\n")
}

// A signal ending a `case` arm of a signalling callback leaves the callback
// as one ending a guard arm does: `break` stops with the accumulator, `break
// v` with v, and `continue` keeps the accumulator.
func TestIRSignal_CaseArmSignalLeavesTheCallback(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  total = [1, 2, 3] |> Iter.reduce(0, fn acc, x ->
    case x
      2 -> break
      _ -> acc + x
    end
  end)
  io.print(total)
  kept = Iter.reduce([1, 2, 3], 0, fn acc, n ->
    case n
      2 -> break acc + 10
      _ -> acc + n
    end
  end)
  io.print(kept)
  skip = Iter.reduce([1, 2, 3], 0, fn acc, n ->
    case n
      2 -> continue
      _ -> acc + n
    end
  end)
  io.print(skip)
end
`, "1\n11\n4\n")
}

package irbuild

import "testing"

func TestIRInferredBranch_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"output operand", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
  io.print(if True then mark(4) else mark(9))
  io.print(case 2
      0 -> "zero"
      _ -> "other"
  end)
end`, "4\n4\nother\n"},
		{"lambda return", `import std::io
fn main()
  base = 7
  choose = fn flag: Bool ->
      return if flag then base else base + 1
  end
  choose(True) |> io.print()
  choose(False) |> io.print()
end`, "7\n8\n"},
		{"nested arithmetic", `import std::io
fn main()
  io.print((if True then 4 else 8) + (case 1
      0 -> 2
      _ -> 3
  end))
  io.print(if (if False then False else True) then 1 else 2)
end`, "7\n1\n"},
		{"nested value arms", `import std::io
fn choose(a: Bool, b: Bool) -> Int
  if a then
      if b then 1 else 2
  else
      case 1
          0 -> 3
          _ -> 4
      end
  end
end
fn main()
  choose(True, True) |> io.print()
  choose(True, False) |> io.print()
  choose(False, True) |> io.print()
end`, "1\n2\n4\n"},
		{"conditional case subject", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
  io.print(case (if True then mark(2) else mark(9))
    1 -> "one"
    _ -> "other"
  end)
end`, "2\nother\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// An `if` or `case` whose arms are a `List<Int>` and a generic call the
// expected `Iter<Int>` instantiated (`pick`'s T is the sequence there) is
// typed as the sequence, whichever arm comes first, and the list arm is
// viewed as it.
func TestIRInferredBranch_ListArmBesideASequenceArm(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn pick<T>(c: Bool, a: T, b: T) -> T
    if c then a else b
end

fn by_if(b: Bool, ys: List<Int>) -> List<Int>
    Iter.filter(if b then ys else pick(True, [3], [4]), fn x -> x > 1)
    |> Iter.to_list()
end

fn by_case(m: Maybe<List<Int>>) -> List<Int>
    Iter.filter(case m
        Some(o) -> o
        None -> pick(False, [7], [8])
    end, fn x -> x > 5)
    |> Iter.to_list()
end

fn main()
    io.inspect(by_if(True, [1, 2]))
    io.inspect(by_if(False, [1, 2]))
    io.inspect(by_case(Some([5, 6])))
    io.inspect(by_case(None))
end
`
	irRunSource(t, src, "[2]\n[3]\n[6]\n[8]\n")
}

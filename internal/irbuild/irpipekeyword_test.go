package irbuild

import "testing"

func TestIRPipeKeyword_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(s: String) -> String
    io.print(s)
    return s
end
fn main()
  label = mark("Ada")
    |> if String.contains("A") then
      "initialed"
    else
      "plain"
    end
  io.print(label)
  number = Some(mark("value"))
    |> case
      Some(s) -> "some ${s}"
      None -> "empty"
    end
  io.print(number)
end
`, "Ada\ninitialed\nvalue\nsome value\n")
}

func TestIRPipeKeyword_BareStagesAndFollowingCalls(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn choose(b: Bool) -> String
  b |> if then
      "yes"
  else
      "no"
  end |> String.lowercase()
end
fn describe(m: Option<Int>) -> Int
  m |> case
      Some(n) -> n + 1
      None -> 0
  end
end
fn main()
  io.print(choose(True))
  io.print(choose(False))
  io.print(describe(Some(41)))
  io.print(describe(None))
end
`, "yes\nno\n42\n0\n")
}

// A piped `if` or `case` is the stage itself, lowered with the piped value
// as its condition or scrutinee, so the region's type is the one the checker
// recorded for the stage. A region typed as an `Iter<T>` whose first arm is
// a List, Vector or Set, a region of an interface type whose arms are
// concrete, and a subtype beside its base each run piped as they do
// unpiped: bare and prefixed (`case id()`, `if is_zero() then`), bound,
// as a pipe's last stage, as a middle stage and as a statement.
func TestIRPipeKeyword_RegionsTakeTheStagesRecordedType(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn id(n: Int) -> Int
    n
end

fn is_zero(n: Int) -> Bool
    n == 0
end

fn by_case(n: Int) -> List<Int>
    region = n |> case
        0 -> [0]
        _ -> Iter.filter(0..n, fn x -> x % 2 == 0)
    end

    Iter.to_list(region)
end

fn by_if(n: Int) -> List<Int>
    region = (n == 0) |> if then
        [0]
    else
        Iter.filter(0..n, fn x -> x % 2 == 0)
    end

    Iter.to_list(region)
end

fn by_prefixed_case(n: Int) -> List<Int>
    region = n |> case id()
        0 -> Vector[0]
        _ -> Iter.filter(0..n, fn x -> x % 2 == 0)
    end

    Iter.to_list(region)
end

fn by_middle_if(n: Int) -> List<Int>
    n
    |> if is_zero() then
        Set[0]
    else
        Iter.filter(0..n, fn x -> x % 2 == 0)
    end
    |> Iter.to_list()
end

fn by_last_case(n: Int) -> List<Int>
    region = n |> id() |> case
        0 -> [0]
        _ -> Iter.filter(0..n, fn x -> x % 2 == 0)
    end

    Iter.to_list(region)
end

fn shown(n: Int) -> String
    d: Display = n |> case
        0 -> n
        _ -> "many"
    end

    Display.to_string(d)
end

fn shown_if(n: Int) -> String
    d: Display = n |> if is_zero() then n else "many"
    Display.to_string(d)
end

fn joined(n: Int) -> Int
    p: PositiveInt = 5

    region = n |> case
        0 -> p
        _ -> n
    end

    region + 1
end

fn each(n: Int)
    n |> case
        0 -> io.print(Iter.to_list([0]))
        _ -> io.print(Iter.to_list(Iter.filter(0..n, fn x -> x % 2 == 0)))
    end
end

fn main()
    for_each = fn n: Int ->
        io.print(by_case(n))
        io.print(by_if(n))
        io.print(by_prefixed_case(n))
        io.print(by_middle_if(n))
        io.print(by_last_case(n))
        io.print(shown(n))
        io.print(shown_if(n))
        io.print(joined(n))
        each(n)
    end

    for_each(0)
    for_each(7)
end
`, "[0]\n[0]\n[0]\n[0]\n[0]\n0\n0\n6\n[0]\n[0, 2, 4, 6]\n[0, 2, 4, 6]\n[0, 2, 4, 6]\n[0, 2, 4, 6]\n[0, 2, 4, 6]\nmany\nmany\n8\n[0, 2, 4, 6]\n")
}

package irbuild

import "testing"

func TestIRListPattern_RecursiveSum(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn sum(xs: List<Int>) -> Int
  case xs
    [] -> 0
    [head, ..tail] -> head + sum(tail)
  end
end
fn main()
  io.print(sum([1, 2, 3]))
  io.print(sum([]))
end
`, "6\n0\n")
}

func TestIRListPattern_ExactLengthsScopesAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn source() -> List<Int>
    io.print("subject")
    [1, 2, 3]
end
fn describe(xs: List<Int>) -> String
  case xs
    [] -> "empty"
    [_] -> "single"
    [_a, _b] -> "pair"
    [_, _, .._rest] -> "long"
  end
end
fn main()
  io.print(describe([]))
  io.print(describe([1]))
  io.print(describe([1, 2]))
  io.print(describe(source()))
  head = 9
  case source()
    [head, ..tail] ->
        io.print(head)
        io.inspect(tail)
    [] -> io.print("missing")
  end
  io.print(head)
end
`, "empty\nsingle\npair\nsubject\nlong\nsubject\n1\n[2, 3]\n9\n")
}

func TestIRListPattern_NestedListsAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  xs = [[1, 2], [3]]
  empty: List<Int> = []
  read = fn -> case xs
    [head, ..] -> head
    [] -> empty
  end
  io.inspect(read())
end
`, "[1, 2]\n")
}

func TestIRListPattern_MultilineBindings(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  xs = [1, 2, 3]
  case xs
    [
      head,
      _,
      ..tail
    ] ->
      io.print(head)
      io.inspect(tail)
    _ -> io.print("short")
  end
end
`, "1\n[3]\n")
}

// At the end of a list pattern `..` takes a pattern for the rest (spec §10
// *List Destructuring*): the suffix is matched against it as a nested
// pattern is against its value, and a failed match selects the next arm.
func TestIRListPattern_RestIsAPattern(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn f(xs: List<Int>) -> Int
  case xs
    [a, ..[b]] -> a + b
    [a, ..[]] -> a
    [a, ..[b, ..rest]] -> a + b + Iter.count(rest)
    _ -> 0
  end
end
fn g(xs: List<Int>) -> String
  case xs
    [..[]] -> "empty"
    [..[1, ..]] -> "starts with one"
    [_, ..([_] as one)] -> "two, ending ${one}"
    [.., last] -> "ends ${last}"
  end
end
fn main()
  io.print(f([1, 2]))
  io.print(f([1]))
  io.print(f([1, 2, 3, 4]))
  io.print(f([]))
  io.print(g([]))
  io.print(g([1, 5]))
  io.print(g([2, 5]))
  io.print(g([2, 5, 6]))
end
`, "3\n1\n5\n0\nempty\nstarts with one\ntwo, ending [5]\nends 6\n")
}

// A rest pattern inside the patterns that hold a list pattern: a variant's
// payload, a distinct type over a List, a struct's field, a tuple's
// component, and a destructuring binding with `else`.
func TestIRListPattern_RestIsAPatternInEveryPosition(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
enum Tok
  Num Int
  Items List<Int>
end
type Path List<String>
struct Box
  items: List<Int>
end
fn tok(t: Tok) -> Int
  case t
    .Items[a, ..[b]] -> a * b
    .Items[a, ..[b, ..rest]] -> a + b + Iter.count(rest)
    .Items(_) -> 0
    .Num(n) -> n
  end
end
fn toks(ts: List<Tok>) -> Int
  case ts
    [.Num(a), ..[.Num(b), ..[.Items[c, ..[d]]]]] -> a + b + c + d
    [_, ..rest] -> Iter.count(rest)
    [] -> -1
  end
end
fn path(p: Path) -> String
  case p
    Path[a, ..[b]] -> "${a}/${b}"
    Path[a, ..["x", ..rest]] -> "${a} x ${Iter.count(rest)}"
    _ -> "other"
  end
end
fn box(b: Box) -> Int
  case b
    Box{items: [a, ..[b]]} -> a - b
    Box{items: _} -> 0
  end
end
fn pair(p: (List<Int>, Maybe<List<String>>)) -> String
  case p
    ([_, ..[n]], Some(["a", ..[s]])) -> "${n} ${s}"
    _ -> "no"
  end
end
fn main()
  io.print(tok(.Items([3, 4])))
  io.print(tok(.Items([3, 4, 5, 6])))
  io.print(tok(.Items([3])))
  io.print(toks([.Num(1), .Num(2), .Items([3, 4])]))
  io.print(toks([.Num(1), .Num(2), .Items([3])]))
  io.print(path(Path(["a", "b"])))
  io.print(path(Path(["a", "x", "c", "d"])))
  io.print(path(Path(["a"])))
  io.print(box(Box{items: [9, 4]}))
  io.print(box(Box{items: [9]}))
  io.print(pair(([1, 2], Some(["a", "z"]))))
  io.print(pair(([1, 2, 3], Some(["a", "z"]))))
  [h, ..[i, ..tail]] = [10, 20, 30] else return
  io.print("${h} ${i} ${tail}")
  [j, ..[k]] = [10, 20, 30] else
    io.print("no match")
    return
  end
  io.print(j + k)
end
`, "12\n9\n0\n10\n2\na/b\na x 2\nother\n5\n0\n2 z\nno\n10 20 [30]\nno match\n")
}

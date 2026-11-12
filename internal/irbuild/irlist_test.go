package irbuild

import "testing"

func TestIRList_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"nested empty result and case", `fn nested() -> List<List<Int>>
    [[], [1]]
end
fn choose(n: Int) -> List<Int>
    case n
        0 -> []
        _ -> [n]
    end
end
fn main()
 dbg nested()
 dbg choose(0)
 dbg choose(2)
 Unit
end`, "dbg line 11: nested() = [[], [1]]\ndbg line 12: choose(0) = []\ndbg line 13: choose(2) = [2]\n"},
		{"empty-only spread", `import std::io
fn main()
 empty = fn ->
     io.print("empty")
     []
 end
 dbg [..empty()]
 Unit
end`, "empty\ndbg line 7: [..empty()] = []\n"},
		{"empty argument order", `import std::io
fn pair(xs: List<Int>, n: Int) -> List<Int>
    [n, ..xs]
end
fn mark() -> Int
    io.print("mark")
    2
end
fn main()
 empty = fn ->
     io.print("empty")
     []
 end
 dbg pair(empty(), mark())
 dbg pair(n: mark(), xs: empty())
 Unit
end`, "empty\nmark\ndbg line 14: pair(empty(), mark()) = [2]\nmark\nempty\ndbg line 15: pair(n: mark(), xs: empty()) = [2]\n"},
		{"empty values and contexts", `fn empty() -> List<Int>
    []
end
fn accept(xs: List<Int> = []) -> List<Int>
    xs
end
fn choose(n: Int) -> List<Int>
    if n == 0 then [] else [n]
end
fn main()
 xs = empty()
 dbg xs
 dbg empty()
 dbg accept()
 dbg accept(xs)
 dbg [1, ..[]]
 dbg [empty(), [1]]
 dbg [[2], empty()]
 dbg choose(0)
 Unit
end`, "dbg line 12: xs = []\ndbg line 13: empty() = []\ndbg line 14: accept() = []\ndbg line 15: accept(xs) = []\ndbg line 16: [1, ..[]] = [1]\ndbg line 17: [empty(), [1]] = [[], [1]]\ndbg line 18: [[2], empty()] = [[2], []]\ndbg line 19: choose(0) = []\n"},
		{"empty lambda effects", `import std::io
fn accept(xs: List<Int>) -> List<Int>
    xs
end
fn main()
 empty = fn ->
     io.print("empty")
     []
 end
 dbg accept(empty())
 defaulted = fn xs: List<Int> = [] -> xs
 dbg defaulted()
 dbg defaulted([])
 dbg [1, ..empty()]
 dbg [empty(), [2]]
 Unit
end`, "empty\ndbg line 10: accept(empty()) = []\ndbg line 12: defaulted() = []\ndbg line 13: defaulted([]) = []\nempty\ndbg line 14: [1, ..empty()] = [1]\nempty\ndbg line 15: [empty(), [2]] = [[], [2]]\n"},
		{"returned lists and spreads", `fn values() -> List<Int>
    [1, 2]
end
fn main()
 xs = values()
 dbg xs
 whole = [0, ..xs]
 dbg whole
 dbg xs
 Unit
end`, "dbg line 6: xs = [1, 2]\ndbg line 8: whole = [0, 1, 2]\ndbg line 9: xs = [1, 2]\n"},
		{"operand order", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn tail() -> List<Int>
    io.print(3)
    [3, 4]
end
fn main()
 xs = [mark(1), mark(2), ..tail()]
 dbg xs
 ys = [mark(5), mark(6)]
 dbg ys
 Unit
end`, "3\n1\n2\ndbg line 12: xs = [1, 2, 3, 4]\n5\n6\ndbg line 14: ys = [5, 6]\n"},
		{"nested lists and captured tails", `fn main()
 base = [1, 2]
 prepend = fn x: Int -> [x, ..base]
 dbg prepend(0)
 dbg [[1, 2], [3]]
 dbg [True, False]
 dbg [1.5, 2.5]
 Unit
end`, "dbg line 4: prepend(0) = [0, 1, 2]\ndbg line 5: [[1, 2], [3]] = [[1, 2], [3]]\ndbg line 6: [True, False] = [True, False]\ndbg line 7: [1.5, 2.5] = [1.5, 2.5]\n"},
		{"defaults and branch results", `fn choose(n: Int) -> List<Int>
    case n
        0 -> [1]
        _ -> [2]
    end
end
fn main()
 defaulted = fn xs = [3, 4] -> xs
 dbg defaulted()
 dbg defaulted([5])
 selected = if True then [6] else [7]
 dbg selected
 dbg choose(0)
 Unit
end`, "dbg line 9: defaulted() = [3, 4]\ndbg line 10: defaulted([5]) = [5]\ndbg line 12: selected = [6]\ndbg line 13: choose(0) = [1]\n"},
		{"string Debug escapes", `fn main()
 xs = ["a\nb\t\"\\z"]
 dbg xs
 Unit
end`, "dbg line 3: xs = [\"a\\nb\\t\\\"\\\\z\"]\n"},
		{"multiline elements", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
 xs = [
  mark(1),
  mark(2),
 ]
 dbg xs
 Unit
end`, "1\n2\ndbg line 11: xs = [1, 2]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

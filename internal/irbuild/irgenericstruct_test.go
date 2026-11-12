package irbuild

import (
	"testing"
)

// A generic struct's field read runs beside a struct whose field holds a
// Map of function values.
func TestIRGenericStruct_BesideAFunctionMapFieldRuns(t *testing.T) {
	verifyLambdaProgram(t, "import std::io\nstruct Box<T>\n    value: T\nend\nstruct Point\n    x: Int\nend\nstruct Outer\n    p: List<Point>\n f: Map<String, (Int) -> Int>\nend\nfn main()\n    box = Box{value: 42}\n    io.print(box.value)\n    io.inspect(Outer{p: [Point{x: 2}], f: {}})\nend\n",
		"42\nOuter{p: [Point{x: 2}], f: {}}\n")
}

func TestIRGenericStruct_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"generic factory and typed parameter", `import std::io
struct Box<T>
    value: T
end
fn wrap<T>(x: T) -> Box<T>
    Box{value: x}
end
fn read(box: Box<Int>) -> Int
    box.value
end
fn main()
  a = wrap(42)
  b = wrap("x")
  io.print(read(a))
  io.print(b.value)
end`, "42\nx\n"},
		{"box", `struct Box<T>
    value: T
end
fn main()
  box = Box{value: 42}
  dbg box.value
end`, "dbg line 6: box.value = 42\n"},
		{"distinct instances", `import std::io
struct Box<T>
    value: T
end
fn main()
  a = Box{value: 42}
  b = Box{value: "x"}
  c = Box{value: 7}
  io.print(a.value)
  io.print(b.value)
  io.print(c.value)
end`, "42\nx\n7\n"},
		{"source order", `import std::io
struct Pair<T, U>
  first: T
  second: U
end
fn mark(n: Int) -> Int
    io.print(n)
    return n
end
fn main()
  pair = Pair{second: mark(2), first: mark(1)}
  io.print(pair.first)
  io.print(pair.second)
end`, "2\n1\n1\n2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

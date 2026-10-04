package irbuild

import (
	"testing"
)

func TestIRLogicRetained_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"skipped effects and stable locals", `import std/io
fn mark(n: Int, value: Bool): Bool { io.print(n) value }
fn main() {
 a = False
 b = True
 x = a and b
 io.print(x)
 io.print(b)
 io.print(mark(1, False) and mark(2, True))
 io.print(mark(3, True) or mark(4, False))
 io.print(mark(5, True) and mark(6, False))
 io.print(mark(7, False) or mark(8, True))
}`, "False\nTrue\n1\nFalse\n3\nTrue\n5\n6\nFalse\n7\n8\nTrue\n"},
		{"nested operands and lambda captures", `import std/io
fn mark(n: Int, value: Bool): Bool { io.print(n) value }
fn main() {
 a = True
 choose = |b: Bool| a and (b or mark(1, True))
 io.print(choose(True))
 io.print(choose(False))
 io.print((mark(2, False) or mark(3, True)) and (mark(4, False) or mark(5, True)))
 io.print(False and (mark(6, True) or mark(7, True)))
 io.print(!mark(8, False))
 io.print(!!True)
}`, "True\n1\nTrue\n2\n3\n4\n5\nTrue\nFalse\n8\nTrue\nTrue\n"},
		{"branches and default calls", `import std/io
fn positive(n: Int = 1): Bool { n > 0 }
fn main() {
 select = |n: Int| if n > 0 and n < 100 { positive() and n < 4 } else { False or positive(n) }
 io.print(select(2))
 io.print(select(5))
 io.print(select(-1))
}`, "True\nFalse\nFalse\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

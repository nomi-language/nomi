package irbuild

import "testing"

func TestIRAnnotation_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"scalars and captures", `fn main() {
 n: Int = 20
 text: String = "answer"
 flag: Bool = True
 add: (Int) -> Int = |x: Int| x + n
 _ignored: Int = add(1)
 dbg text
 dbg flag
 dbg add(22)
 Unit
}`, "dbg line 7: text = \"answer\"\ndbg line 8: flag = True\ndbg line 9: add(22) = 42\n"},
		{"lists and annotated discards", `import std/io
fn main() {
 empty = || { io.print("empty") [] }
 xs: List<Int> = empty()
 _drop: List<Int> = empty()
 ys: List<List<Int>> = [[], [1]]
 dbg xs
 dbg ys
 Unit
}`, "empty\nempty\ndbg line 7: xs = []\ndbg line 8: ys = [[], [1]]\n"},
		{"branches and lexical blocks", `fn main() {
 xs: List<Int> = if True { [] } else { [1] }
 ys: List<Int> = case 0 { 0 -> []; _ -> [2] }
 zs: List<Int> = { amount: Int = 3 [amount] }
 empty: List<Int> = { xs }
 dbg xs
 dbg ys
 dbg zs
 dbg empty
 Unit
}`, "dbg line 6: xs = []\ndbg line 7: ys = []\ndbg line 8: zs = [3]\ndbg line 9: empty = []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

package irbuild

import "testing"

func TestIRAnnotation_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"scalars and captures", `fn main()
 n: Int = 20
 text: String = "answer"
 flag: Bool = True
 add: (Int) -> Int = fn x: Int -> x + n
 _ignored: Int = add(1)
 dbg text
 dbg flag
 dbg add(22)
 Unit
end`, "dbg line 7: text = \"answer\"\ndbg line 8: flag = True\ndbg line 9: add(22) = 42\n"},
		{"lists and annotated discards", `import std::io
fn main()
 empty = fn ->
     io.print("empty")
     []
 end
 xs: List<Int> = empty()
 _drop: List<Int> = empty()
 ys: List<List<Int>> = [[], [1]]
 dbg xs
 dbg ys
 Unit
end`, "empty\nempty\ndbg line 10: xs = []\ndbg line 11: ys = [[], [1]]\n"},
		{"branches and lexical blocks", `fn main()
 xs: List<Int> = if True then [] else [1]
 ys: List<Int> = case 0
     0 -> []
     _ -> [2]
 end
 zs: List<Int> = begin
     amount: Int = 3
     [amount]
 end
 empty: List<Int> = begin
     [..xs]
 end
 dbg xs
 dbg ys
 dbg zs
 dbg empty
 Unit
end`, "dbg line 14: xs = []\ndbg line 15: ys = []\ndbg line 16: zs = [3]\ndbg line 17: empty = []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

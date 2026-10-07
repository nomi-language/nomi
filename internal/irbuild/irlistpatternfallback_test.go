package irbuild

import (
	"testing"
)

// A literal head (`[1, .._]`) retains through listCaseTestNested;
// TestIRListPattern_LiteralHeadRetains runs one.
func TestIRListPattern_UnsupportedElementsPreserveFallback(t *testing.T) {
	for _, pattern := range []string{"[.._rest]"} {
		p, err := AnalyzeSource("main.nomi", "fn pick(xs: List<Int>): Int { case xs { "+pattern+" -> 1; _ -> 0 } }\nfn main() { _ = pick([1, 2]) }")
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range got.IR {
			for _, f := range mod.Funcs() {
				if f.Name() == "pick" {
					t.Fatal("unsupported pattern retained")
				}
			}
		}
	}
}

func TestIRListPattern_LiteralHeadRetains(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn pick(xs: List<Int>): Int { case xs { [1, .._] -> 1; _ -> 0 } }
fn main() {
  io.print(pick([1, 2]))
  io.print(pick([2, 1]))
  io.print(pick([]))
}
`, "1\n0\n0\n")
}

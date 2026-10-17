package irbuild

import (
	"testing"
)

// A pattern that is only a suffix (`[.._rest]`, `[..rest]`) matches every
// list, so it builds no length test; an arm after it, which the checker
// admits, still lowers. A literal head (`[1, .._]`) retains through
// listCaseTestNested; TestIRListPattern_LiteralHeadRetains runs one.
func TestIRListPattern_SuffixOnlyMatchesEveryList(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn pick(xs: List<Int>) -> Int { case xs { [.._rest] -> 1; _ -> 0 } }
fn all(xs: List<Int>) -> List<Int> { case xs { [..rest] -> rest } }
fn main() {
  io.print(pick([1, 2]))
  io.print(pick([]))
  io.inspect(all([3, 4]))
}
`, "1\n1\n[3, 4]\n")
}

func TestIRListPattern_LiteralHeadRetains(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn pick(xs: List<Int>) -> Int { case xs { [1, .._] -> 1; _ -> 0 } }
fn main() {
  io.print(pick([1, 2]))
  io.print(pick([2, 1]))
  io.print(pick([]))
}
`, "1\n0\n0\n")
}

package irbuild

import "testing"

func TestIRCaseAdHoc_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn below(n: Int, limit: Int): Bool { io.print(limit) n < limit }
fn classify(n: Int): String {
 case { below(n, 0) -> "negative" below(n, 10) -> "small" _ -> "large" }
}

fn main() {
 io.print(classify(-1))
 io.print(classify(5))
 io.print(classify(20))
 base = 10
 choose = |n: Int| case { n < base -> "low" _ -> "high" }
 io.print(choose(2))
 io.print(choose(12))
 x = 42
 size = if x > 50 { "big" } else { "small" }
 label = case x { 0 -> "zero" 1 -> "one" _ -> "many" }
 range = case { x < 0 -> "negative" x < 10 -> "small" x < 100 -> "medium" _ -> "large" }
 io.print(size)
 io.print(label)
 io.print(range)
}`, "0\nnegative\n0\n10\nsmall\n0\n10\nlarge\nlow\nhigh\nsmall\nmany\nmedium\n")
}

func TestIRCaseAdHoc_ConditionCursors(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn check(flag: Bool = False): Bool { flag }
fn choose(flag: Bool): String {
 case {
  check() -> "default"
  flag -> "flag"
  (1 <
   2) -> "comparison"
  _ -> "other"
 }
}
fn main() { io.print(choose(True)) io.print(choose(False)) }
`, "flag\ncomparison\n")
}

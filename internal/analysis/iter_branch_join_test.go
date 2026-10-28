package analysis_test

import "testing"

// The branches of an `if` or `case` that are two different Iter types join
// as `Iter<T>` where the value's type is written as one: a binding's
// annotation, a parameter's type, a function's declared result.
func TestIterBranchJoin_AWrittenIterJoinsTheBranches(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn in_order(values: Range<Int>, descending: Bool) -> Iter<Int> {
  if descending { Iter.reverse(values) } else { values }
}

fn total(xs: Iter<Int>) -> Int {
  Iter.count(xs)
}

fn main() {
  up = True
  a: Iter<Int> = if up { 1..4 } else { [3, 2, 1] }
  b: Iter<Int> = case up {
    True -> 1..=3
    False -> %[9, 8]
  }
  _ = #(Iter.count(a), Iter.count(b), total(if up { #[1, 2] } else { [1] }), in_order(1..3, up))
}
`)
	expectNoStdlibErrors(t, errs)
}

// With no written Iter, the mismatch stands, names both branch types, and
// its help names the annotation that joins them.
func TestIterBranchJoin_AnUnwrittenIterIsTheMismatch(t *testing.T) {
	const hint = "\nhelp: both are an Iter<Int>, and two kinds of Iter join only where the value's type is written as one: annotate it `: Iter<Int>`, or declare the function's result as `Iter<Int>`"
	for _, tc := range []struct{ name, body, want, at string }{
		{"an if", "  c = if up { 1..4 } else { [3, 2, 1] }\n  _ = c",
			"if/else branch type mismatch: then is Range<Int>, else is List<Int>" + hint, "[3, 2, 1]"},
		{"a case", "  c = case up {\n    True -> 1..4\n    False -> [3, 2, 1]\n  }\n  _ = c",
			"case branch type mismatch: expected Range<Int>, got List<Int>" + hint, "[3, 2, 1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main() {\n  up = True\n" + tc.body + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}

// A pipe's head is the first stage's first argument, so an `if` or `case`
// head of a stage whose first parameter is written as `Iter<T>` joins as a
// direct argument does.
func TestIterBranchJoin_APipeHeadJoinsAtTheStagesParameter(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
fn main() {
  up = True
  n = if up { 1..4 } else { [3, 2, 1] } |> Iter.count()
  xs = case up {
    True -> 1..=3
    False -> [9, 8]
  } |> Iter.map(fn x -> x * 2) |> Iter.to_list()
  _ = #(n, xs)
}
`)
	expectNoStdlibErrors(t, errs)
}

// The mirror: a stage whose first parameter is not an Iter gives the head
// nothing to join at, and the mismatch stands.
func TestIterBranchJoin_APipeHeadOfANonIterStageIsTheMismatch(t *testing.T) {
	src := "fn take(xs: List<Int>) -> Int {\n  Iter.count(xs)\n}\n\nfn main() {\n  up = True\n  _ = if up { 1..4 } else { [3, 2, 1] } |> take()\n}\n"
	_, errs := checkSourceWithStdlib(src)
	expectDiagAt(t, src, errs, "if/else branch type mismatch: then is Range<Int>, else is List<Int>\nhelp: both are an Iter<Int>, and two kinds of Iter join only where the value's type is written as one: annotate it `: Iter<Int>`, or declare the function's result as `Iter<Int>`", "[3, 2, 1]")
}

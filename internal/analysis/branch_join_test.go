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
  _ = (Iter.count(a), Iter.count(b), total(if up { #[1, 2] } else { [1] }), in_order(1..3, up))
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
  _ = (n, xs)
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

const shapeDecls = `interface Shape {
  fn area(shape: self) -> Float
}

struct Circle {
  r: Float
}

struct Square {
  s: Float
}

impl Shape for Circle {
  fn area(c: Circle) -> Float { c.r }
}

impl Shape for Square {
  fn area(q: Square) -> Float { q.s }
}
`

// Any interface joins branches of different types that implement it where
// the value's type is written as that interface: a program's interface at a
// binding's annotation, a function's declared result, a parameter and a pipe
// head's stage, over an `if` and a `case`, and std's Display and Hashable.
func TestInterfaceBranchJoin_AWrittenInterfaceJoinsTheBranches(t *testing.T) {
	_, errs := checkSourceWithStdlib(shapeDecls + `
fn pick(flag: Bool) -> Shape {
  if flag { Circle{r: 1.0} } else { Square{s: 2.0} }
}

fn describe(shape: Shape) -> Float {
  Shape.area(shape)
}

interface Source<T> {
  fn get(source: self) -> T
}

impl Source<Float> for Circle {
  fn get(c: Circle) -> Float { c.r }
}

impl Source<Float> for Square {
  fn get(q: Square) -> Float { q.s }
}

fn main() {
  up = True
  g: Source<Float> = if up { Circle{r: 1.0} } else { Square{s: 2.0} }
  _ = Source.get(g)
  n = 2
  s: Shape = if up { Circle{r: 1.0} } else { Square{s: 2.0} }
  u: Shape = case n {
    1 -> Circle{r: 1.0}
    2 -> Square{s: 2.0}
    _ -> pick(up)
  }
  d: Display = if up { 1 } else { "one" }
  h: Hashable = case n {
    1 -> 1
    _ -> "two"
  }
  a = describe(if up { Circle{r: 1.0} } else { Square{s: 2.0} })
  b = if up { Circle{r: 1.0} } else { Square{s: 2.0} } |> describe()
  _ = (s, u, d, h, a, b, pick(False))
}
`)
	expectNoStdlibErrors(t, errs)
}

// A written interface joins only branches that implement it: one that does
// not leaves the mismatch.
func TestInterfaceBranchJoin_ABranchOutsideTheInterfaceIsTheMismatch(t *testing.T) {
	src := shapeDecls + "\nfn main() {\n  up = True\n  s: Shape = if up { Circle{r: 1.0} } else { 3 }\n  _ = s\n}\n"
	_, errs := checkSourceWithStdlib(src)
	expectDiagAt(t, src, errs, "if/else branch type mismatch: then is Circle, else is Int", "3")
}

// With no written interface the mismatch stands. Its help names the
// interface when exactly one interface declared outside std is implemented
// by both types, and says nothing otherwise: std's Display, Equatable and
// the rest are shared by nearly any two types, and two program interfaces
// leave no one choice.
func TestInterfaceBranchJoin_AnUnwrittenInterfaceIsTheMismatch(t *testing.T) {
	const hint = "\nhelp: both implement Shape, and two types join as an interface only where the value's type is written as one: annotate it `: Shape`, or declare the function's result as `Shape`"
	const second = `
interface Named {
  fn name(value: self) -> String
}

impl Named for Circle {
  fn name(c: Circle) -> String { "circle" }
}

impl Named for Square {
  fn name(q: Square) -> String { "square" }
}
`
	for _, tc := range []struct{ name, decls, body, want, at string }{
		{"an if", shapeDecls, "  c = if up { Circle{r: 1.0} } else { Square{s: 2.0} }\n  _ = c",
			"if/else branch type mismatch: then is Circle, else is Square" + hint, "Square{s: 2.0}"},
		{"a case", shapeDecls, "  c = case up {\n    True -> Circle{r: 1.0}\n    False -> Square{s: 2.0}\n  }\n  _ = c",
			"case branch type mismatch: expected Circle, got Square" + hint, "Square{s: 2.0}"},
		{"std interfaces only", "", "  c = if up { 1 } else { \"one\" }\n  _ = c",
			"if/else branch type mismatch: then is Int, else is String", "\"one\""},
		{"two program interfaces", shapeDecls + second, "  c = if up { Circle{r: 1.0} } else { Square{s: 2.0} }\n  _ = c",
			"if/else branch type mismatch: then is Circle, else is Square", "Square{s: 2.0}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.decls + "\nfn main() {\n  up = True\n" + tc.body + "\n}\n"
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}

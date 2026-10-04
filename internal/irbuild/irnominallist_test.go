package irbuild

import "testing"

func TestIRNominalList_TourMoneySort(t *testing.T) {
	verifyLambdaProgram(t, `struct Money {
  amount: Int
}

derive Comparable for Money

fn main(): List<Money> {
  dbg Money{amount: 100} < Money{amount: 250}

  ms = [Money{amount: 3}, Money{amount: 1}, Money{amount: 2}]
  Iter.sort(ms) |> dbg
}
`, "dbg line 8: Money{amount: 100} < Money{amount: 250} = True\n"+
		"dbg line 11: Iter.sort(ms) = [Money{amount: 1}, Money{amount: 2}, Money{amount: 3}]\n")
}

// Container Debug calls each nominal element's own impl, custom or derived,
// through lists, tuples and records.
func TestIRNominalList_ContainerDebugCallsImpls(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct P {
  x: Int
}
enum Tag {
  Plain
  Count Int
}
impl Debug for Tag {
  fn inspect(t: Tag): String {
    case t {
      Tag.Plain -> "plain"
      Tag.Count(n) -> "count=${n}"
    }
  }
}
fn main() {
  ps = [P{x: 1}, P{x: 2}]
  io.inspect(ps)
  io.inspect([Tag.Plain, Tag.Count(4)])
  io.inspect((P{x: 3}, Tag.Count(5)))
  dbg {p: P{x: 4}, n: 1}
  io.print("done")
}
`, "[P{x: 1}, P{x: 2}]\n[plain, count=4]\n(P{x: 3}, count=5)\n"+
		"dbg line 22: {p: P{x: 4}, n: 1} = {n: 1, p: P{x: 4}}\ndone\n")
}

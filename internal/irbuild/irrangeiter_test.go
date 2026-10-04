package irbuild

import "testing"

func TestIRRangeIter_TourCollectionsRanges(t *testing.T) {
	verifyLambdaProgram(t, `fn main(): List<Int> {
  dbg Iter.to_list(1..5)
  dbg Iter.to_list(1..=5)

  // Unbounded ranges work too; bound them with Iter.take downstream.
  Range.naturals()
  |> Iter.take(3)
  |> Iter.to_list()
  |> dbg
}
`, "dbg line 2: Iter.to_list(1..5) = [1, 2, 3, 4]\n"+
		"dbg line 3: Iter.to_list(1..=5) = [1, 2, 3, 4, 5]\n"+
		"dbg line 9:\n  Range.naturals()\n  |> Iter.take(3)\n  |> Iter.to_list()\n  = [0, 1, 2]\n")
}

func TestIRRangeIter_BoundsAndStages(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn span(lo: Int, hi: Int): Range<Int> { lo..=hi }
fn main() {
  io.inspect(Iter.to_list(5..1))
  io.inspect(Iter.to_list(3..3))
  io.inspect(Iter.to_list(3..=3))
  io.inspect(Iter.to_list(-2..=2))
  io.inspect(span(4, 6) |> Iter.map(|n: Int| n * n) |> Iter.to_list())
  io.inspect(1..=10 |> Iter.filter(|n: Int| n % 3 == 0) |> Iter.to_list())
  io.print(1..=100 |> Iter.reduce(|acc = 0, n: Int| acc + n))
  io.inspect(Range.from(7) |> Iter.take(2) |> Iter.to_list())
  io.inspect(Iter.sort(3..=1))
}
`, "[]\n[]\n[3]\n[-2, -1, 0, 1, 2]\n[16, 25, 36]\n[3, 6, 9]\n5050\n[7, 8]\n[]\n")
}

func TestIRRangeIter_TheLastIntStopsTheWalk(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  top = Int.max_value
  io.inspect(Iter.to_list(top - 1..=top))
  io.inspect(Range.from(top - 1) |> Iter.take(5) |> Iter.to_list())
}
`, "[9223372036854775806, 9223372036854775807]\n[9223372036854775806, 9223372036854775807]\n")
}

// Cardinality over a Range walks the Range view and folds, as native lowers it
// (rt.SeqCount over rt.RangeSeq), so these bodies retain and agree.
func TestIRRangeIter_CardinalityAgrees(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  io.print(Iter.count(1..=4))
  io.print(Iter.empty?(3..3))
}
`, "4\nTrue\n")
}

func TestIRRangeIter_TourCodepointRange(t *testing.T) {
	verifyLambdaProgram(t, `fn main(): Maybe<List<String>> {
  a = try Codepoint.from_int(97)
  d = try Codepoint.from_int(100)

  letters =
    a..=d
    |> Iter.map(Codepoint.to_string)
    |> Iter.to_list()
    |> dbg

  Some(letters)
}
`, "dbg line 9:\n  a..=d\n  |> Iter.map(Codepoint.to_string)\n  |> Iter.to_list()\n  = [\"a\", \"b\", \"c\", \"d\"]\n")
}

package irbuild

import (
	"testing"
)

// A user-written interface-qualified stdlib call retains, and its Ordering
// compares as an enum.
func TestIRStdIfaceCall_UserSpellingRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  io.print(Comparable.compare(1, 2) == Ordering.Less)
}
`, "True\n")
}

func TestIRDerivedCompare_TourSeverityOrdering(t *testing.T) {
	verifyLambdaProgram(t, `enum Severity {
  Info
  Warning Int
  Error Int
}

derive Comparable for Severity

fn main() {
  dbg Severity.Info < Severity.Warning(1)
  dbg Severity.Warning(9) < Severity.Error(1)
  dbg Severity.Warning(1) < Severity.Warning(2)
}
`, "dbg line 10: Severity.Info < Severity.Warning(1) = True\n"+
		"dbg line 11: Severity.Warning(9) < Severity.Error(1) = True\n"+
		"dbg line 12: Severity.Warning(1) < Severity.Warning(2) = True\n")
}

func TestIRDerivedCompare_StructOrdering(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct User {
  first_name: String
  age: Int
}

derive Equatable for User
derive Comparable for User

fn main() {
  ann = User{first_name: "Ann", age: 41}
  cara = User{first_name: "Cara", age: 35}
  io.print(ann < cara)
  io.print(cara <= ann)
  io.print(User{first_name: "Ann", age: 50} > ann)
  io.print(ann >= User{first_name: "Ann", age: 41})
}
`, "True\nFalse\nTrue\nTrue\n")
}

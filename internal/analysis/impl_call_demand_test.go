package analysis_test

import (
	"strings"
	"testing"
)

// TestTypeQualifiedImplDemandDoesNotDoubleReport is the regression test for
// the guard in analysis/impl_call_demand.go, and it is written as a test
// rather than as a comment because a note that names its own expiry is not
// a check.
//
// Recording an impl demand at a type-qualified call closes a real hole: the
// demand must not depend on how the call is spelled. Recording it
// where a DECLARED bound already covers the conformance produces a second
// diagnostic for one fix, which is the anti-pattern derive_synthesis.go
// names in its own words. This is the exact source that caught it:
// 11-interfaces-and-impls/interface_defaults/interface_defaults_test.nomi
// :: "derive where clauses reject unsatisfied receiver arguments" asserts
// EXACTLY ONE diagnostic for it and went to two.
//
// The count is asserted, not just the text, because the failure mode is an
// EXTRA true diagnostic rather than a wrong one — a substring check would
// pass through it.
func TestTypeQualifiedImplDemandDoesNotDoubleReport(t *testing.T) {
	src := `struct DisplayBox<T> {
  value: T
}

struct Plain {
  tag: String
}

derive Display for DisplayBox<T> where T: Display

fn main() {
  _ = DisplayBox.to_string(DisplayBox{value: Plain{tag: "a"}})
}
`
	fa := buildWithStdlibForDerive(src)
	if len(fa.TypeErrors) != 1 {
		t.Fatalf("expected exactly 1 diagnostic, got %d:\n  %s",
			len(fa.TypeErrors), errMsgs(fa.TypeErrors))
	}
	// The surviving one must be the BOUND's, which names the where clause
	// that is the actual constraint, not DetectMissingImpls' call-site
	// restatement of the same fix.
	if got := fa.TypeErrors[0].Message; !strings.Contains(got, "does not implement Display") ||
		!strings.Contains(got, "where T: Display") {
		t.Errorf("surviving diagnostic is not the bound's: %q", got)
	}
}

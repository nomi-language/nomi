package irbuild

import (
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// `clock Clock.Virtual` lowers to a testing/synctest bubble. These tests hold
// the clause reader: the one arm that declines, and the single implementation
// both the front end and this package call.

// TestClock_UnreadableSpellingRefuses is the reachability witness for the one
// arm of this lowering that declines, and it carries BOTH halves a witness
// needs: a demonstrated FIRING, and a check that says what to do when its
// population empties.
//
// The firing is asserted directly on ast.TestClockVariant rather than inferred
// from a refusal that might have come from elsewhere.
func TestClock_UnreadableSpellingRefuses(t *testing.T) {
	// FIRING, half one: the reader really does decline something.
	if _, ok := ast.TestClockVariant(&ast.Ident{Name: "nope"}); ok {
		t.Fatal("ast.TestClockVariant accepted an *ast.Ident, so the refusal arm in " +
			"collectTestDecl can never be entered and this witness is vacuous")
	}
	if _, ok := ast.TestClockVariant(&ast.FieldAccess{Object: &ast.Ident{Name: "Clock"},
		Field: &ast.Ident{Name: "Wallclock"}}); ok {
		t.Fatal("ast.TestClockVariant accepted a variant name that is not Virtual or System")
	}
	// FIRING, half two: it accepts every legal spelling, so the decline above
	// is narrow rather than a blanket.
	for _, n := range []ast.Node{
		&ast.DotVariant{Name: "Virtual"},
		&ast.FieldAccess{Object: &ast.Ident{Name: "Clock"}, Field: &ast.Ident{Name: "Virtual"}},
		&ast.FieldAccess{Object: &ast.Ident{Name: "Clock"}, Field: &ast.Ident{Name: "System"}},
	} {
		if _, ok := ast.TestClockVariant(n); !ok {
			t.Errorf("ast.TestClockVariant declined %T, which the checker accepts", n)
		}
	}

	// THE POPULATION. checker.checkTestClock type-checks the clause against
	// `testing.Clock` before either backend reads it, so a spelling this
	// declines should be unreachable from legal source. If that ever stops
	// being true, the refusal is what a corpus file gets and this row is where
	// the reason is written down.
	src := `tests "g" {
  clock 1
  test "a" { assert 1 == 1 }
}
`
	if _, err := AnalyzeSource("m", src); err == nil {
		t.Fatal("the front end now ACCEPTS `clock 1`. That makes the refusal arm in " +
			"collectTestDecl reachable from user source, so it needs a corpus-level " +
			"assertion and an entry in the tally, not just this unit witness.")
	}
}

// TestClock_OneImplementationOfTheVariantRead is the expiry condition for the
// shared read, and it names the file and symbol whose change voids it.
//
// The rule decides observable behaviour: if `internal/frontend` and
// `internal/irbuild` read the clause differently, one group runs under two clocks and the two paths
// DIFFER on every timing assertion. So there is exactly one implementation, in
// `ast`, and this fails if a second appears in either caller.
func TestClock_OneImplementationOfTheVariantRead(t *testing.T) {
	for _, f := range []string{"tests.go", "testclock.go"} {
		src := mustReadSibling(t, f)
		if strings.Contains(src, "func clockVariantName") {
			t.Errorf("%s declares its own clause reader. There must be exactly one, "+
				"ast.TestClockVariant, because internal/frontend reads the same clause and a "+
				"disagreement is a wrong ANSWER about which clock a group runs "+
				"under — not a refusal.", f)
		}
	}
	// And the shared symbol is actually the one this package calls.
	src := mustReadSibling(t, "tests.go")
	if !strings.Contains(src, "ast.TestClockVariant(") {
		t.Error("tests.go no longer calls ast.TestClockVariant. If the read moved, " +
			"move this assertion with it; if it was inlined, the one-implementation " +
			"property is gone and internal/frontend/tests.go's caller has to be " +
			"reconciled by hand.")
	}
}

// mustReadSibling reads a source file in this package, so a property asserted
// about the implementation cannot drift from the file it describes.
func mustReadSibling(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

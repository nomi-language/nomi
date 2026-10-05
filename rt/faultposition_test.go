package rt

// Every positioned fault text, with the string a program must see.
//
// `internal/vm/traptext_test.go` already establishes that each of these texts
// has one home and that the VM spells no second copy. It says nothing about
// the line. A producer that passes no line makes these constructors print
// `line 0: integer overflow: 9223372036854775807 + 1`, and the repair for that
// belongs at the producer, not here.
//
// This file is the other half of that contract. `traptext_test.go` polices who
// may spell the text; this polices what the text is for a given line, at every
// constructor, so a change to any format string is a deliberate act with a
// diff rather than a silent move in observable output.
//
// # Why this is in `rt` and not beside `traptext_test.go`
//
// That file argues at length that it cannot live in `rt`, because it reaches
// into the compiler's source tree. This one does the opposite: it calls `rt`'s
// own constructors and reads nothing outside rt, so it belongs to the package
// that owns the strings.
//
// # `rt` has no absent position
//
// `internal/ir` refuses a line below 1 in two places: `ir.At` panics, and
// `ir.Pos.IsValid()` requires `line >= 1`. `rt` has no such rule — every
// constructor here formats whatever integer it is handed, so a producer that
// does not know the position renders `line 0:`, naming a line no file has.
//
// TestFaultPosition_ZeroRendersAsALineNoFileHas records that, and it is not an
// assertion that `line 0` is correct output. It is an assertion that these
// constructors are faithful to their argument — which is the property that
// makes the defect diagnosable, because it puts the fault squarely on the
// producers rather than leaving it ambiguous between producer and formatter.
// `arith.go`'s own header states the intended contract at the producer:
//
//	line is the source line of the operator, passed in as a constant at each
//	call site rather than recovered from the frame or the Go stack.
//
// So recovering the line from the frame or the Go stack is a rejected design
// here.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// positionedFault is one fault text, invoked at a line, with the exact string
// it must produce.
type positionedFault struct {
	// name is the constructor, so a failure names the function to look at.
	name string
	// at renders the text for one line.
	at func(line int) string
}

// positionedFaults is every fault text in this module that carries a Nomi
// source line into output a user sees.
//
// The list is checked against a derived one rather than trusted:
// TestFaultPosition_EveryOwnedTextIsListed re-derives the population from the
// `line %d` format strings in this module's own sources and fails if this
// table is missing one. So a new text is caught without
// anyone remembering to extend the table, which is the shape
// `knownTrapTextCopies` uses one layer up.
func positionedFaults() []positionedFault {
	return []positionedFault{
		{"DeferredCleanupError", func(l int) string { return DeferredCleanupError(l, "failure").Error() }},
		{"OverflowError", func(l int) string {
			return OverflowError(l, "+", 9223372036854775807, 1).Error()
		}},
		{"DivByZeroError", func(l int) string { return DivByZeroError(l).Error() }},
		{"FloatModuloText", FloatModuloText},
		{"DecimalDivByZeroText", DecimalDivByZeroText},
		{"DecimalNonTerminatingText", DecimalNonTerminatingText},
		{"DecimalModuloText", DecimalModuloText},
		{"AssertionFailure.Error", func(l int) string {
			return (&AssertionFailure{Line: l, Reason: "assertion failed"}).Error()
		}},
		{"EarlyReturnFailure.Error", func(l int) string {
			return (&EarlyReturnFailure{Line: l}).Error()
		}},
		{"NoCaseMatchError", func(l int) string { return NoCaseMatchError(l).Error() }},
		{"MapKeyMissingError", func(l int) string { return MapKeyMissingError(l, "z").Error() }},
		{"EnumFieldMissingError", func(l int) string { return EnumFieldMissingError(l, "Rect", "radius").Error() }},
		{"EnumFieldAccessError", func(l int) string { return EnumFieldAccessError(l, "Point", "height").Error() }},
		{"CallDepthError", func(l int) string { return CallDepthError(l).Error() }},
		{"UnreachableError", func(l int) string { return UnreachableError(l, "a reason").Error() }},
		{"DbgText", func(l int) string {
			// A plain buffer rather than a terminal, so ColorEnabledFor
			// answers false and the header is uncoloured — the shape the
			// committed expectations record.
			return DbgText(&bytes.Buffer{}, l, "41 + 1", "42")
		}},
	}
}

// wantAtLine7 is the exact string each constructor must produce for line 7.
//
// Seven rather than one, deliberately: line 1 is the first line of any file
// and a producer that accidentally passed a count, an index or a boolean could
// land on it. Seven is reachable only by carrying a real position.
var wantAtLine7 = map[string]string{
	"DeferredCleanupError":      "line 7: deferred cleanup failed: failure",
	"OverflowError":             "line 7: integer overflow: 9223372036854775807 + 1",
	"DivByZeroError":            "line 7: division by zero",
	"FloatModuloText":           "line 7: modulo not supported on floats",
	"DecimalDivByZeroText":      "line 7: decimal division by zero",
	"DecimalNonTerminatingText": "line 7: non-terminating decimal division; use Decimal.divide(a, b, scale, mode) for explicit rounding",
	"DecimalModuloText":         "line 7: modulo is not defined on Decimal",
	"AssertionFailure.Error":    "line 7: assertion failed",
	"EarlyReturnFailure.Error":  "line 7: test returned early",
	"NoCaseMatchError":          "line 7: no matching case branch",
	"MapKeyMissingError":        "line 7: key z not found in map",
	"EnumFieldMissingError":     "line 7: variant 'Rect' has no field 'radius'",
	"EnumFieldAccessError":      "line 7: cannot access field 'height' on variant 'Point'",
	"CallDepthError":            "line 7: stack overflow: more than 100000 nested calls",
	"UnreachableError":          "line 7: unreachable: a reason",
	// DbgText ends in a newline: it is a whole rendered line rather than a
	// message a caller wraps.
	"DbgText": "dbg line 7: 41 + 1 = 42\n",
}

// TestFaultPosition_TheAgreedStringAtARealLine is the pin. A program that
// reports one of these faults must print exactly this.
func TestFaultPosition_TheAgreedStringAtARealLine(t *testing.T) {
	faults := positionedFaults()
	if len(faults) != len(wantAtLine7) {
		t.Fatalf("the table and the expectations disagree in size: %d constructors, %d strings",
			len(faults), len(wantAtLine7))
	}
	for _, f := range faults {
		want, listed := wantAtLine7[f.name]
		if !listed {
			t.Errorf("%s has no expected string", f.name)
			continue
		}
		if got := f.at(7); got != want {
			t.Errorf("%s at line 7:\n got %q\nwant %q", f.name, got, want)
		}
	}
}

// TestFaultPosition_ZeroRendersAsALineNoFileHas records that this module has
// no absent-position form, so a producer that does not know the line puts
// `line 0` in front of a user.
//
// It is a reading of the formatter, not an endorsement of the output. These
// constructors print whatever line they are told, so a producer that passes 0
// yields `line 0: integer overflow`. The assertion keeps such a defect
// diagnosable at its producer rather than ambiguous between producer and
// formatter.
//
// EarlyReturnFailure is the one exception and it is the precedent for the
// alternative: its Error() guards on `e.Line > 0` and drops the prefix rather
// than printing a zero. That is what an absent form looks like when a type
// has one.
func TestFaultPosition_ZeroRendersAsALineNoFileHas(t *testing.T) {
	var faithful, absentForm []string
	for _, f := range positionedFaults() {
		got := f.at(0)
		switch {
		case strings.Contains(got, "line 0:"):
			faithful = append(faithful, f.name)
		default:
			absentForm = append(absentForm, f.name)
		}
	}
	if len(absentForm) != 1 || absentForm[0] != "EarlyReturnFailure.Error" {
		t.Errorf("exactly one constructor has an absent-position form and it is "+
			"EarlyReturnFailure.Error; got %v", absentForm)
	}
	if len(faithful) != len(positionedFaults())-1 {
		t.Errorf("every other constructor formats the line it is handed; got %v", faithful)
	}
	// The negative direction: a zero must not be silently turned into
	// something plausible. `line 1` would be the dangerous repair, because it
	// is a line every file has.
	if got := OverflowError(0, "+", 1, 1).Error(); !strings.HasPrefix(got, "line 0:") {
		t.Errorf("a zero must render as a zero rather than as a plausible line: %q", got)
	}
}

// TestFaultPosition_EveryOwnedTextIsListed re-derives the population from this
// module's own sources, so the table above cannot quietly fall behind.
//
// A new text is the failure this catches. A hand-written count of these texts
// goes stale the first time one lands without anyone re-deriving it, which is
// the reason the number is not written down anywhere here.
func TestFaultPosition_EveryOwnedTextIsListed(t *testing.T) {
	found := ownedPositionedTexts(t)
	if len(found) == 0 {
		t.Fatal("no positioned fault texts were found, so the scan is broken and " +
			"every count below is vacuous")
	}
	if len(found) != len(wantAtLine7) {
		t.Errorf("this module declares %d positioned fault texts and the table lists %d.\n"+
			"declared:\n  %s\nlisted:\n  %s",
			len(found), len(wantAtLine7),
			strings.Join(found, "\n  "), strings.Join(sortedTableKeys(), "\n  "))
	}
}

// ownedPositionedTexts is every `line %d` format string this module's non-test
// sources declare, sorted.
//
// It parses rather than greps, for the reason `internal/vm`'s scanner gives:
// every file here discusses these texts in prose, and a textual scan reports
// each discussion as a declaration. It also folds `+` over adjacent literals,
// because DecimalNonTerminatingText is spelled as two.
func ownedPositionedTexts(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading this module's sources: %v", err)
	}
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", n, err)
		}
		ast.Inspect(f, func(node ast.Node) bool {
			e, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			s, ok := foldedString(e)
			if !ok || !strings.Contains(s, "line %d") {
				return true
			}
			seen[s] = true
			return false
		})
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// foldedString is e's compile-time constant string value, folding `+` over
// adjacent literals.
func foldedString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, lok := foldedString(v.X)
		r, rok := foldedString(v.Y)
		if !lok || !rok {
			return "", false
		}
		return l + r, true
	}
	return "", false
}

func sortedTableKeys() []string {
	out := make([]string, 0, len(wantAtLine7))
	for k := range wantAtLine7 {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

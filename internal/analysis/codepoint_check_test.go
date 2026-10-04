package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A codepoint literal is std's Codepoint: it satisfies a Codepoint return,
// compares and orders against other Codepoints, and ranges over it iterate.
func TestCodepointLiteralTypesAsCodepoint(t *testing.T) {
	src := `fn first(): Codepoint {
  'a'
}

fn f(cp: Codepoint): Bool {
  same = cp == ','
  lower = cp >= 'a' and cp <= 'z'
  n = 'a'..='z' |> Iter.count()
  same or lower or n == 26
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// The literal needs no import: its type is std's declaration whatever the
// file has in scope.
func TestCodepointLiteralNeedsNoImport(t *testing.T) {
	src := `fn f(): Bool {
  'a' == 'b' or 'a' < 'b'
}
`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCodepointLiteralIsNotAString(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn f(): Bool {
  "a" == 'a'
}
`)
	expectStdlibError(t, errs, "equality type mismatch: String vs Codepoint")
}

func TestCodepointLiteralIsNotAnInt(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn f(): Int {
  'a'
}
`)
	expectStdlibError(t, errs, "Codepoint")
}

func TestCodepointLiteralRangeEndpointsMustAgree(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn f(): Int {
  'a'..=122 |> Iter.count()
}
`)
	expectStdlibError(t, errs, "range endpoints must have the same type, got Codepoint and Int")
}

func TestCodepointLiteralPatternNeedsACodepointScrutinee(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn f(s: String): Int {
  case s {
    'a' -> 1
    _ -> 2
  }
}
`)
	expectStdlibError(t, errs, "pattern 'a' is a Codepoint, but the value is a String")
}

func TestCodepointLiteralPatternsNeedACatchAll(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn f(cp: Codepoint): Int {
  case cp {
    'a' -> 1
    'b' -> 2
  }
}
`)
	expectStdlibError(t, errs, "non-exhaustive case")
}

func TestCodepointLiteralPatternsCheck(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn f(m: Maybe<Codepoint>, pair: (Codepoint, Int)): Int {
  a = case m {
    Some('a') -> 1
    _ -> 2
  }
  b = case pair {
    ('\n', n) -> n
    _ -> 0
  }
  a + b
}
`)
	expectNoStdlibErrors(t, errs)
}

// The literal's recorded type is what hover shows.
func TestCodepointLiteralRecordsItsType(t *testing.T) {
	fa, errs := checkSourceWithStdlib(`fn f(): Bool {
  x = 'q'
  x == 'r'
}
`)
	expectNoStdlibErrors(t, errs)
	var found bool
	for node, ty := range fa.ExprTypes {
		if node.NodeType() != "CodepointLit" {
			continue
		}
		found = true
		if !analysis.TypesEqual(ty, analysis.TypeCodepoint) || ty.String() != "Codepoint" {
			t.Errorf("codepoint literal recorded as %v, want Codepoint", ty)
		}
	}
	if !found {
		t.Fatal("no codepoint literal in ExprTypes")
	}
}

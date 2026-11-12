package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestClosedVariables_IntUnlessABoundRefusesIt holds the closing rule
// (closed_vars.go): a variable inference leaves unsolved is closed as Int,
// or as Unit when Int does not meet a bound the variable was checked
// against and Unit does. A demand an interface call places on the variable
// counts as a bound. The instance each call records reads the closed type
// through analysis.Elaborate, and the recorded type itself still displays
// the variable.
func TestClosedVariables_IntUnlessABoundRefusesIt(t *testing.T) {
	fa, errs := checkSourceWithStdlib(`fn present<T>(m: Maybe<T>) -> Bool
    case m
        Some(_) -> True
        None -> False
    end
end

fn shown<T>(m: Maybe<T>) -> String where T: Display
    case m
        Some(v) -> Display.to_string(v)
        None -> "none"
    end
end

interface Marker
    fn mark(m: self) -> String
end

impl Marker for Unit
    fn mark(_m: Unit) -> String
        "unit"
    end
end

fn marked<T>(m: Maybe<T>) -> String where T: Marker
    case m
        Some(v) -> Marker.mark(v)
        None -> "none"
    end
end

fn main()
    _ = present(None)
    _ = shown(None)
    _ = marked(None)
    _ = Display.to_string(Err("x"))
end
`)
	if len(errs) > 0 {
		t.Fatalf("the checker rejects the source: %v", errs)
	}
	want := map[string]string{
		"present":   "(Maybe<Int>) -> Bool",
		"shown":     "(Maybe<Int>) -> String",
		"marked":    "(Maybe<Unit>) -> String",
		"to_string": "(Result<Int, String>) -> String",
	}
	// The calls in main are the ones whose instance holds a variable; one
	// that displays none is in a body, at its declaration's own T. Each
	// call in main must be found, so the instance displays the variable.
	got := map[string]string{}
	for _, sym := range fa.References {
		if _, ok := want[sym.Name]; !ok || sym.CallType == nil || !strings.Contains(sym.CallType.String(), "?") {
			continue
		}
		got[sym.Name] = analysis.Elaborate(sym.CallType).String()
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s lowers at %q, want %q", name, got[name], w)
		}
	}
}

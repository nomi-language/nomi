package analysis_test

import (
	"strings"
	"testing"
)

// A `_` where a value goes is still the discard error, and its help says
// what type the place expects and which values in scope have it, nearest
// binding first: a typed hole.
func TestHole_NamesTheExpectedTypeAndWhatFits(t *testing.T) {
	src := "fn pick(xs: List<String>, fallback: String, count: Int) -> String\n  label = \"x\"\n  first = List.head(xs)\n  Maybe.with_default(first, _)\nend\n"
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		if !strings.Contains(e.Message, "`_` is a discard name, not a value") {
			continue
		}
		hints := strings.Join(e.Hints, "\n")
		for _, want := range []string{"as a hole, it stands for a value of type `String`", "in scope with that type: `label`, `fallback`"} {
			if !strings.Contains(hints, want) {
				t.Errorf("hints %q lack %q", hints, want)
			}
		}
		if strings.Contains(hints, "`count`") || strings.Contains(hints, "`xs`") {
			t.Errorf("hints %q list a value of another type", hints)
		}
		return
	}
	expectStdlibError(t, errs, "`_` is a discard name, not a value")
}

// With nothing of the type in scope, the hole names the type alone.
func TestHole_NamesTheTypeWhenNothingFits(t *testing.T) {
	_, errs := checkSourceWithStdlib("fn f(n: Int) -> String\n  x: String = _\n  x\nend\n")
	for _, e := range errs {
		if strings.Contains(e.Message, "`_` is a discard name") {
			hints := strings.Join(e.Hints, "\n")
			if !strings.Contains(hints, "a value of type `String`") || strings.Contains(hints, "in scope with that type") {
				t.Errorf("hints = %q, want the type and no candidates", hints)
			}
			return
		}
	}
	expectStdlibError(t, errs, "`_` is a discard name, not a value")
}

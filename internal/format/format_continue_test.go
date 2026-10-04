package format

import "testing"

// A value written after `continue` stays on its line. The checker rejects it
// (continue takes no value); splitting it onto a line of its own, as the
// formatter did while the parser dropped it, turned the mistake into an
// unreachable statement that the checker could not see.
func TestFormat_ContinueKeepsAWrittenValue(t *testing.T) {
	src := "fn f(p: (Int, Int)): (Int, Int) {\n    continue (1, 2)\n}\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
	bare := "fn f(n: Int): Int {\n    if n > 1 { continue }\n    n\n}\n"
	if got, _ := Format(bare); got != bare {
		t.Errorf("got:\n%s\nwant:\n%s", got, bare)
	}
}

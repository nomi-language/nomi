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

// A comment after a bare `return`, `break` or `continue` stays on its line.
func TestFormat_BareControlKeepsItsComment(t *testing.T) {
	src := "fn f(n: Int) {\n    if n > 1 {\n        return // done\n    }\n\n" +
		"    _ = Iter.reduce(1..=n, |acc = 0, x| {\n        if x > 3 {\n            break // stop\n        }\n\n" +
		"        if x == 2 {\n            continue // skip\n        }\n\n        acc + x\n    })\n}\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

package format

import "testing"

// A value written after `continue` stays on its line. The checker rejects it
// (continue takes no value); splitting it onto a line of its own, as the
// formatter did while the parser dropped it, turned the mistake into an
// unreachable statement that the checker could not see.
func TestFormat_ContinueKeepsAWrittenValue(t *testing.T) {
	src := "fn f(p: (Int, Int)) -> (Int, Int)\n    continue (1, 2)\nend\n"
	got, _ := Format(src)
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
	bare := "fn f(n: Int) -> Int\n    if n > 1 then continue\n    n\nend\n"
	if got, _ := Format(bare); got != bare {
		t.Errorf("got:\n%s\nwant:\n%s", got, bare)
	}
}

// A comment after a bare `return`, `break` or `continue` stays on its line.
func TestFormat_BareControlKeepsItsComment(t *testing.T) {
	src := "fn f(n: Int)\n" +
		"    if n > 1 then\n" +
		"        return // done\n" +
		"    end\n" +
		"\n" +
		"    _ = Iter.reduce(1..=n, 0, fn acc, x ->\n" +
		"        if x > 3 then\n" +
		"            break // stop\n" +
		"        end\n" +
		"\n" +
		"        if x == 2 then\n" +
		"            continue // skip\n" +
		"        end\n" +
		"\n" +
		"        acc + x\n" +
		"    end)\n" +
		"end\n"
	got, err := Format(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

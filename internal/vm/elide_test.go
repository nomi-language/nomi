package vm_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// runElideTests runs src's test cases and answers the uncoloured report.
func runElideTests(t *testing.T, src string) string {
	t.Helper()
	t.Setenv("NOMI_COLOR", "never")
	const name = "elide_test.nomi"
	p, err := vmhost.LoadSource(name, src)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	return buf.String()
}

// When a line diff shows a `"""` literal's lines, the echo of the assertion
// above it writes the literal as the source lines the diff numbers, for an
// `io.replay` script and for the expected side of `==` alike. A literal the
// diff shows without numbers is written `""" ... """`.
func TestElide_ADiffedLiteralIsCollapsedInTheEcho(t *testing.T) {
	out := runElideTests(t, `import std/io

fn main() {
    io.print("a")
    io.print("b")
}

test "replay" {
    assert io.replay(
        """
            a
            c
        """,
        main,
    )
}

test "equal" {
    out = "a\nb\n"
    assert out == """
        a
        c
        """
}

test "unnumbered" {
    out = "a\nb\nc\n"
    x = "q\nr"
    assert out == """
        a
        ${x}
        """
}
`)
	for _, want := range []string{
		"  line 9: the transcript differs from the script\n" +
			"    assert io.replay(\"\"\" lines 11-12 \"\"\", main)\n" +
			"    io.replay: the transcript (-) and what main printed (+) differ\n\n" +
			"        11   │     a\n",
		"  line 20: assertion failed\n" +
			"    assert out == \"\"\" lines 21-22 \"\"\"\n" +
			"    expected \"\"\" ... \"\"\" (-) and actual out (+) differ\n\n" +
			"        21   │ a\n",
		"  line 29: assertion failed\n" +
			"    assert out == \"\"\" ... \"\"\"\n" +
			"    expected \"\"\" ... \"\"\" (-) and actual out (+) differ\n\n" +
			"          │ a\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no\n%s\nin:\n%s", want, out)
		}
	}
}

// A `"""` literal no diff follows keeps its echo: two one-line Strings show
// their rows, and a comparison an `and` never reached has no diff.
func TestElide_ALiteralWithNoDiffKeepsItsEcho(t *testing.T) {
	out := runElideTests(t, `test "one line" {
    out = "a"
    assert out == """
        b
        """
}

test "not reached" {
    out = "a\nb\n"
    assert String.length(out) == 3 and out == """
        a
        c
        """
}
`)
	for _, want := range []string{
		"    assert out == \"\"\"\n        b\n        \"\"\"\n    values:\n",
		"    assert String.length(out) == 3 and out == \"\"\"\n        a\n        c\n        \"\"\"\n    values:\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no\n%s\nin:\n%s", want, out)
		}
	}
	if strings.Contains(out, "lines") || strings.Contains(out, `""" ... """`) {
		t.Errorf("a literal no diff shows was collapsed:\n%s", out)
	}
}

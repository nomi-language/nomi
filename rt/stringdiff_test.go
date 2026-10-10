package rt

import (
	"fmt"
	"strings"
	"testing"
)

// plainDiff is the diff block as FormatAssertionFailure prints it, without
// its header line and the blank line under it.
func plainDiff(t *testing.T, expected, actual string) string {
	t.Helper()
	d := StringDiff("actual", actual, "expected", expected)
	if d == nil {
		t.Fatalf("StringDiff(%q, %q) is nil", actual, expected)
	}
	return diffRows(t, d)
}

// diffRows is d's rows as FormatAssertionFailure prints them.
func diffRows(t *testing.T, d *AssertionStringDiff) string {
	t.Helper()
	got := FormatAssertionFailure(&AssertionFailure{
		Line: 1, Keyword: "assert", Expr: "actual == expected", Reason: "assertion failed", Diff: d,
	})
	head := "line 1: assertion failed\n" +
		"  assert actual == expected\n" +
		"  " + d.header() + "\n\n"
	if !strings.HasPrefix(got, head) {
		t.Fatalf("the report does not open with the diff header:\n%s", got)
	}
	return strings.TrimPrefix(got, head)
}

func TestStringDiffIsOnlyForLineBreaks(t *testing.T) {
	cases := []struct {
		actual, expected string
		want             bool
	}{
		{"a", "b", false},
		{"a ", "a", false},
		{"a\nb", "a\nb", false},
		{"a\nb", "a\nc", true},
		{"a", "a\n", true},
		{"a\r", "a", true},
	}
	for _, c := range cases {
		if got := StringDiff("x", c.actual, "y", c.expected) != nil; got != c.want {
			t.Errorf("StringDiff(%q, %q) present = %v, want %v", c.actual, c.expected, got, c.want)
		}
	}
}

func TestStringDiffChangedLine(t *testing.T) {
	got := plainDiff(t, "one\ntwo\nthree\n", "one\n2\nthree\n")
	want := "        │ one\n" +
		"      - │ two\n" +
		"      + │ 2\n" +
		"        │ three"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Unchanged runs longer than the context are one `...` line, before, between
// and after the changes; a run of one is shown, since its marker would take
// the same line.
func TestStringDiffCollapsesUnchangedRuns(t *testing.T) {
	var exp, act []string
	for i := 1; i <= 30; i++ {
		exp = append(exp, fmt.Sprint(i))
		switch i {
		case 10:
			act = append(act, "ten")
		case 18:
			act = append(act, "eighteen")
		default:
			act = append(act, fmt.Sprint(i))
		}
	}
	got := plainDiff(t, strings.Join(exp, "\n"), strings.Join(act, "\n"))
	want := "        │   ... 6 lines match ...\n" +
		"        │ 7\n        │ 8\n        │ 9\n" +
		"      - │ 10\n      + │ ten\n" +
		"        │ 11\n        │ 12\n        │ 13\n        │ 14\n        │ 15\n        │ 16\n        │ 17\n" +
		"      - │ 18\n      + │ eighteen\n" +
		"        │ 19\n        │ 20\n        │ 21\n" +
		"        │   ... 9 lines match ..."
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	exp[14], act[14] = "a", "b"
	got = plainDiff(t, strings.Join(exp[:22], "\n"), strings.Join(act[:22], "\n"))
	if strings.Contains(got, "... 1 lines match") {
		t.Fatalf("a run of one unchanged line was collapsed:\n%s", got)
	}
}

func TestStringDiffMarksTrailingWhitespace(t *testing.T) {
	got := plainDiff(t, "total: 5\nend\n", "total: 5 \t\nend\n")
	want := "      - │ total: 5\n" +
		"      + │ total: 5 \\t$\n" +
		"        │ end\n" +
		"        │ ($ ends a line that ends in whitespace)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStringDiffMarksAMissingFinalNewline(t *testing.T) {
	got := plainDiff(t, "a\nb\n", "a\nb")
	want := "        │ a\n" +
		"      - │ b\n" +
		"      + │ b\n" +
		"        │ \\ no newline at end"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// An extra final newline is the same marker under the expected side.
	got = plainDiff(t, "a\nb", "a\nb\n")
	want = "        │ a\n" +
		"      - │ b\n" +
		"        │ \\ no newline at end\n" +
		"      + │ b"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Two strings that differ only in a trailing newline on a one-line value: the
// diff is the only place the difference shows.
func TestStringDiffIdenticalButForTheNewline(t *testing.T) {
	got := plainDiff(t, "done", "done\n")
	want := "      - │ done\n" +
		"        │ \\ no newline at end\n" +
		"      + │ done"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStringDiffSpellsCarriageReturns(t *testing.T) {
	got := plainDiff(t, "a\nb\n", "a\r\nb\r\n")
	want := "      - │ a\n" +
		"      - │ b\n" +
		"      + │ a\\r$\n" +
		"      + │ b\\r$\n" +
		"        │ ($ ends a line that ends in whitespace)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("a raw carriage return reached the report: %q", got)
	}
}

// Past the comparison bound the diff shows the context before the first
// difference and that difference, and says it stopped.
func TestStringDiffBoundsLongInput(t *testing.T) {
	var exp, act strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&exp, "line %d\n", i)
		if i < 100 {
			fmt.Fprintf(&act, "line %d\n", i)
		} else {
			fmt.Fprintf(&act, "other %d\n", i)
		}
	}
	got := plainDiff(t, exp.String(), act.String())
	want := "        │   ... 97 lines match ...\n" +
		"        │ line 97\n        │ line 98\n        │ line 99\n" +
		"      - │ line 100\n" +
		"      + │ other 100\n" +
		"        │ ... too many lines to compare; this is the first difference"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The diff replaces the two operands' rows and leaves every other row.
func TestStringDiffReplacesTheOperandRows(t *testing.T) {
	got := FormatAssertionFailure(&AssertionFailure{
		Line: 4, Keyword: "assert", Expr: "render(x) == want", Reason: "assertion failed",
		Values: []AssertionValueContext{
			{Expr: "x", Value: "3"},
			{Expr: "render(x)", Value: "\"a\nb\""},
			{Expr: "want", Value: "\"a\nc\""},
		},
		Diff: StringDiff("render(x)", "a\nb", "want", "a\nc"),
	})
	want := "line 4: assertion failed\n" +
		"  assert render(x) == want\n" +
		"  values:\n" +
		"    x\n" +
		"      = 3\n" +
		"  expected want (-) and actual render(x) (+) differ\n" +
		"\n" +
		"        │ a\n" +
		"      - │ c\n" +
		"      + │ b"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An Assertable's diff hides the row that fed it a side, whatever the
// expression: the replay script is an interpolated `"""`, and its row matches
// the expected side line for line once trailing whitespace and the final
// newline the Assertable added are set aside. A row that differs keeps its
// place, and so does a String that is not either side.
func TestAssertableDiffHidesTheRowsItShows(t *testing.T) {
	got := FormatAssertionFailure(&AssertionFailure{
		Line: 9, Keyword: "assert", Expr: "replay(script(n), main)", Reason: "differs",
		Values: []AssertionValueContext{
			{Expr: "n", Value: "500"},
			{Expr: "label", Value: "\"> 500\""},
			{Expr: "script(n)", Value: "\"> 500  \r\nok\""},
			{Expr: "main", Value: "<func: main>"},
		},
		Diff: &AssertionStringDiff{Actual: "> 500\nno\n", Expected: "> 500\nok\n"},
	})
	want := "line 9: differs\n" +
		"  assert replay(script(n), main)\n" +
		"  values:\n" +
		"    n\n" +
		"      = 500\n" +
		"    label\n" +
		"      = \"> 500\"\n" +
		"  expected (-) and actual (+) differ\n" +
		"\n" +
		"        │ > 500\n" +
		"      - │ ok\n" +
		"      + │ no"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// With every row hidden the `values:` header goes too.
func TestAllRowsHiddenDropsTheValuesHeader(t *testing.T) {
	got := FormatAssertionFailure(&AssertionFailure{
		Line: 2, Keyword: "assert", Expr: "replay(\"a\", main)", Reason: "differs",
		Values: []AssertionValueContext{
			{Expr: "main", Value: "<func: main>"},
		},
		Diff: &AssertionStringDiff{Actual: "b\n", Expected: "a\n"},
	})
	want := "line 2: differs\n" +
		"  assert replay(\"a\", main)\n" +
		"  expected (-) and actual (+) differ\n" +
		"\n" +
		"      - │ a\n" +
		"      + │ b"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A function read through its own name is hidden with or without a diff, and
// a function read through anything else keeps the row that names it.
func TestFunctionRowNamingItselfIsHidden(t *testing.T) {
	cases := []struct {
		row    AssertionValueContext
		hidden bool
	}{
		{AssertionValueContext{Expr: "main", Value: "<func: main>"}, true},
		{AssertionValueContext{Expr: "util.double", Value: "<func: double>"}, true},
		{AssertionValueContext{Expr: "g", Value: "<func: double>"}, false},
		{AssertionValueContext{Expr: "f", Value: "<func: <lambda>>"}, false},
		{AssertionValueContext{Expr: "t", Value: "Computation(<func: t>)"}, false},
		{AssertionValueContext{Expr: "String.trim", Value: "<builtin: strings.String.trim>"}, false},
		{AssertionValueContext{Expr: "pick(main)", Value: "<func: main>"}, false},
	}
	for _, c := range cases {
		if got := (&AssertionFailure{}).rowHidden(c.row); got != c.hidden {
			t.Errorf("%s = %s: hidden %v, want %v", c.row.Expr, c.row.Value, got, c.hidden)
		}
	}
}

func TestDiffExprLabel(t *testing.T) {
	if got := diffExprLabel("run.output"); got != "run.output" {
		t.Fatalf("one line: %q", got)
	}
	if got := diffExprLabel("\"\"\"\n    a\n    \"\"\""); got != `""" ... """` {
		t.Fatalf("a triple-quoted literal: %q", got)
	}
}

// A line only one side has, and an empty line on either side: the empty
// one reads `(empty line)` when it is a difference and stays blank as
// context.
func TestStringDiffExtraMissingAndEmptyLines(t *testing.T) {
	got := plainDiff(t, "a\n\nb\nc\n", "\na\n\nb\n")
	want := "      + │ (empty line)\n" +
		"        │ a\n" +
		"        │\n" +
		"        │ b\n" +
		"      - │ c"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	got = plainDiff(t, "a\n\nb\n", "a\nb\n")
	want = "        │ a\n" +
		"      - │ (empty line)\n" +
		"        │ b"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// With the expected side's source line, each expected line is numbered,
// lines only the actual side has are not, a collapsed run moves the
// numbering past it, and the column is as wide as the widest number.
func TestStringDiffNumbersTheExpectedLines(t *testing.T) {
	var exp, act []string
	for i := 1; i <= 12; i++ {
		exp = append(exp, fmt.Sprint(i))
		act = append(act, fmt.Sprint(i))
	}
	act[9] = "ten"
	exp = append(exp, "")
	d := StringDiff("actual", strings.Join(act, "\n")+"\n", "expected", strings.Join(exp, "\n")+"\n")
	d.ExpectedLine = 95
	got := diffRows(t, d)
	want := "            │   ... 6 lines match ...\n" +
		"      101   │ 7\n" +
		"      102   │ 8\n" +
		"      103   │ 9\n" +
		"      104 - │ 10\n" +
		"          + │ ten\n" +
		"      105   │ 11\n" +
		"      106   │ 12\n" +
		"      107 - │ (empty line)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The echo writes a diffed literal as the source lines the diff numbers, a
// final newline ending the last line rather than starting another, and as
// the header does when the diff has no numbers.
func TestStringDiffElidedLiteral(t *testing.T) {
	for _, tc := range []struct {
		expected string
		line     int
		want     string
	}{
		{"a\nb\nc\n", 11, `""" lines 11-13 """`},
		{"a\nb\nc", 11, `""" lines 11-13 """`},
		{"a\n", 7, `""" line 7 """`},
		{"a\nb\n", 0, `""" ... """`},
	} {
		d := &AssertionStringDiff{Expected: tc.expected, Actual: "x\n", ExpectedLine: tc.line}
		if got := d.ElidedLiteral(); got != tc.want {
			t.Errorf("%q from line %d: got %s, want %s", tc.expected, tc.line, got, tc.want)
		}
	}
}

// io.replay's header names the function, and reads "the function" for a
// lambda.
func TestReplayDiffHeader(t *testing.T) {
	if got := ReplayDiffHeader("main"); got != "io.replay: the transcript (-) and what main printed (+) differ" {
		t.Fatalf("main: %q", got)
	}
	if got := ReplayDiffHeader("|| {\n    f()\n}"); got != "io.replay: the transcript (-) and what the function printed (+) differ" {
		t.Fatalf("a lambda: %q", got)
	}
	d := &AssertionStringDiff{Expected: "a\n", Actual: "b\n", Header: ReplayDiffHeader("bank.main")}
	got := diffRows(t, d)
	if got != "      - │ a\n      + │ b" {
		t.Fatalf("got:\n%s", got)
	}
}

// In colour, `-` rows are red, `+` rows green, and the numbers, the header,
// `(empty line)` and the notes dim; with the escapes removed the report is
// the plain one byte for byte.
func TestStringDiffColour(t *testing.T) {
	t.Setenv("NOMI_COLOR", "always")
	d := StringDiff("actual", "a\n\nb", "expected", "a\nc\n")
	d.ExpectedLine = 7
	failure := &AssertionFailure{Line: 1, Keyword: "assert", Expr: "actual == expected", Reason: "assertion failed", Diff: d}
	var b strings.Builder
	WriteAssertionFailure(&b, failure)
	got := b.String()
	for _, want := range []string{
		"\x1b[2m8\x1b[0m \x1b[31m-\x1b[0m │ \x1b[31mc\x1b[0m",
		"\x1b[32m+\x1b[0m │ \x1b[2m(empty line)\x1b[0m",
		"\x1b[32m+\x1b[0m │ \x1b[32mb\x1b[0m",
		"\x1b[2m\\ no newline at end\x1b[0m",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the coloured report lacks %q:\n%q", want, got)
		}
	}
	plain := "  " + strings.ReplaceAll(FormatAssertionFailure(failure), "\n", "\n  ") + "\n"
	plain = strings.ReplaceAll(plain, "  \n", "\n")
	if stripped := stripANSI(got); stripped != plain {
		t.Fatalf("colour changed more than the escape codes:\n%q\n%q", stripped, plain)
	}
}

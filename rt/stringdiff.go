package rt

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The line diff a failed `assert actual == expected` over two Strings prints
// in place of the two operands' `values:` rows, and the diff an Assertable
// that answered both sides shows (`io.replay`'s transcript and script).
//
// Two full multi-line values leave the comparison to the reader, who has to
// find the one line that differs, and cannot see a trailing space or a
// missing final newline at all. So when either String holds a line break the
// report shows a diff instead:
//
//	expected """ ... """ (-) and actual run.output (+) differ
//
//	    12   │ What is the starting balance?
//	    13 - │ Your starting balance: 500
//	       + │ Your starting balance is 500
//	         │   ... 4 lines match ...
//
// Each row is the expected line's source line, when the expected side is a
// literal written in the file (ExpectedLine), then the marker, then ` │ `
// and the line's text exactly as it is. A line only the actual side has
// carries no number. The marker has a column of its own so that text
// starting with spaces or `>>`, as a replay transcript's four-column gutter
// does, reads unchanged. A changed empty line reads `(empty line)`.
//
// # Which side is expected
//
// The left operand is the actual value and the right operand the expected
// one, the order every assertion in the tour, the spec and the stdlib writes.
// `-` lines are the expected side's and `+` lines the actual side's, as in a
// unified diff from what was wanted to what came out.
//
// # Invisible differences
//
// A `-` or `+` line that ends in a space, a tab or a carriage return ends in
// `$`, with a trailing tab spelled `\t`; a carriage return anywhere is spelled
// `\r`, because a raw one would move the terminal's cursor. When one string
// ends in a newline and the other does not, the changed last line of the one
// without is followed by `\ no newline at end`, git's spelling.
// All of it is ASCII, so it reads the same with colour off.

// AssertionStringDiff is the two sides of a failed String equality, with the
// source each was written as.
type AssertionStringDiff struct {
	ActualExpr   string
	Actual       string
	ExpectedExpr string
	Expected     string
	// ExpectedLine is the source line the expected side's first line is
	// written on, when the expected side is a String literal in the file
	// whose lines are the expected lines one for one, and 0 otherwise.
	ExpectedLine int
	// Header names the two sides in place of the one built from the two
	// expressions, or is empty. ReplayDiffHeader is `io.replay`'s.
	Header string
}

// ReplayDiffHeader is the diff header of a failed `io.replay`, whose run
// function was written as run: `main`, or a lambda, which reads as "the
// function".
func ReplayDiffHeader(run string) string {
	if run == "" || strings.ContainsAny(run, " \t\n(){}|") {
		run = "the function"
	}
	return "io.replay: the transcript (-) and what " + run + " printed (+) differ"
}

// ElidedLiteral is how a failure's echo of its assertion writes the `"""`
// literal this diff shows, in place of the literal's lines: `""" lines
// 11-23 """` when the diff numbers them (`""" line 11 """` for one), and
// `""" ... """`, as the header reads, when it does not.
func (d *AssertionStringDiff) ElidedLiteral() string {
	if d.ExpectedLine <= 0 {
		return `""" ... """`
	}
	last := d.ExpectedLine + max(0, len(splitDiffLines(d.Expected))-1)
	if last == d.ExpectedLine {
		return `""" line ` + strconv.Itoa(last) + ` """`
	}
	return `""" lines ` + strconv.Itoa(d.ExpectedLine) + "-" + strconv.Itoa(last) + ` """`
}

// StringDiff is the diff for a failed `actual == expected` over two Strings,
// or nil when the two `values:` rows read better: the strings are equal, or
// neither holds a line break. A one-line string's row already shows every
// character between its quotes, trailing spaces included.
func StringDiff(actualExpr, actual, expectedExpr, expected string) *AssertionStringDiff {
	if actual == expected {
		return nil
	}
	if !strings.ContainsAny(actual, "\n\r") && !strings.ContainsAny(expected, "\n\r") {
		return nil
	}
	return &AssertionStringDiff{
		ActualExpr: actualExpr, Actual: actual,
		ExpectedExpr: expectedExpr, Expected: expected,
	}
}

// diffContext is how many unchanged lines are kept on each side of a change.
const diffContext = 3

// diffMaxCells bounds the comparison: the lines left after the common prefix
// and suffix are compared by a table of (expected lines × actual lines)
// cells, and past this many the diff shows the first difference only.
const diffMaxCells = 1 << 20

// diffLine is one line of a string: its text without the "\n", and whether a
// "\n" ended it. Only the last line of a string can lack one.
type diffLine struct {
	text string
	nl   bool
}

func splitDiffLines(s string) []diffLine {
	var lines []diffLine
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, diffLine{text: s})
			break
		}
		lines = append(lines, diffLine{text: s[:i], nl: true})
		s = s[i+1:]
	}
	return lines
}

// diffOp is one line of the diff: ' ' unchanged, '-' expected only, '+'
// actual only.
type diffOp struct {
	kind byte
	line diffLine
}

// lineDiff compares expected with actual line by line. truncated reports that
// the lines were too many to compare in full, and the ops then end at the
// first difference.
func lineDiff(expected, actual []diffLine) (ops []diffOp, truncated bool) {
	prefix := 0
	for prefix < len(expected) && prefix < len(actual) && expected[prefix] == actual[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(expected)-prefix && suffix < len(actual)-prefix &&
		expected[len(expected)-1-suffix] == actual[len(actual)-1-suffix] {
		suffix++
	}
	for _, l := range expected[:prefix] {
		ops = append(ops, diffOp{' ', l})
	}
	a := expected[prefix : len(expected)-suffix]
	b := actual[prefix : len(actual)-suffix]
	if len(a) > 0 && len(b) > 0 && len(a)*len(b) > diffMaxCells {
		ops = append(ops, diffOp{'-', a[0]}, diffOp{'+', b[0]})
		return ops, true
	}
	ops = append(ops, lcsDiff(a, b)...)
	for _, l := range expected[len(expected)-suffix:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops, false
}

// lcsDiff is the longest-common-subsequence diff of a and b. At a choice it
// takes the expected side's line first, so a changed run reads as its `-`
// lines followed by its `+` lines.
func lcsDiff(a, b []diffLine) []diffOp {
	n, m := len(a), len(b)
	w := m + 1
	// lcs[i*w+j] is the length of the longest common subsequence of a[i:]
	// and b[j:]. Each fits in an int32 whatever the bound.
	lcs := make([]int32, (n+1)*w)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// diffExprLabel is how an operand's source reads in the diff header: as
// written when it is one line, and as its first and last lines otherwise, so
// a `"""` literal reads `""" ... """`. The whole of it is in the assertion
// line just above.
func diffExprLabel(expr string) string {
	if !strings.Contains(expr, "\n") {
		return expr
	}
	lines := strings.Split(expr, "\n")
	return strings.TrimSpace(lines[0]) + " ... " + strings.TrimSpace(lines[len(lines)-1])
}

// diffSide is one side's name in the diff header: the word, then the
// operand's source. Strings an `Assertable` answered have no source, and the
// header reads `expected (-) and actual (+) differ`.
func diffSide(word, expr string) string {
	if expr == "" {
		return word
	}
	return word + " " + diffExprLabel(expr)
}

// header is the line above the diff: what the two sides are.
func (d *AssertionStringDiff) header() string {
	if d.Header != "" {
		return d.Header
	}
	return diffSide("expected", d.ExpectedExpr) + " (-) and " + diffSide("actual", d.ActualExpr) + " (+) differ"
}

// visibleDiffText spells a line so a difference in it can be seen: a carriage
// return anywhere as `\r`, and, on a changed line (marked), trailing tabs as
// `\t` and the end of a line that ends in whitespace as `$`. It reports
// whether it wrote the `$`.
func visibleDiffText(text string, marked bool) (string, bool) {
	end := len(text)
	if marked {
		end = len(strings.TrimRight(text, " \t\r"))
	}
	body := strings.ReplaceAll(text[:end], "\r", `\r`)
	if end == len(text) {
		return body, false
	}
	tail := text[end:]
	tail = strings.ReplaceAll(tail, "\t", `\t`)
	tail = strings.ReplaceAll(tail, "\r", `\r`)
	return body + tail + "$", true
}

// writeStringDiff writes the diff block at indent: its header, a blank line,
// then one row per line at indent plus four spaces. del and ins colour a `-`
// and a `+` row; label dims the line numbers, `(empty line)` and the notes.
func (st assertionReportStyle) writeStringDiff(w io.Writer, indent string, d *AssertionStringDiff) {
	fmt.Fprintf(w, "%s%s\n\n", indent, st.label(d.header()))
	ops, truncated := lineDiff(splitDiffLines(d.Expected), splitDiffLines(d.Actual))
	shown := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		for k := max(0, i-diffContext); k <= min(len(ops)-1, i+diffContext); k++ {
			shown[k] = true
		}
	}
	// The number column is as wide as the last expected line's number, and
	// absent when the expected side is not a literal in the file.
	width := 0
	if d.ExpectedLine > 0 {
		width = len(strconv.Itoa(d.ExpectedLine + max(0, len(splitDiffLines(d.Expected))-1)))
	}
	row := func(line int, kind byte, text string) {
		var b strings.Builder
		b.WriteString(indent + "    ")
		if width > 0 {
			if line > 0 {
				num := strconv.Itoa(line)
				b.WriteString(strings.Repeat(" ", width-len(num)) + st.label(num))
			} else {
				b.WriteString(strings.Repeat(" ", width))
			}
			b.WriteString(" ")
		}
		switch kind {
		case '-':
			b.WriteString(st.del("-"))
		case '+':
			b.WriteString(st.ins("+"))
		default:
			b.WriteString(" ")
		}
		b.WriteString(" │")
		if text != "" {
			b.WriteString(" " + text)
		}
		fmt.Fprintln(w, b.String())
	}
	note := func(text string) { row(0, ' ', st.label(text)) }
	dollar := false
	// The marker is shown only when it is a difference: two strings that
	// both end without a newline need no note on either side.
	newlineDiffers := strings.HasSuffix(d.Expected, "\n") != strings.HasSuffix(d.Actual, "\n")
	// next is the source line of the next expected line, or 0.
	next := d.ExpectedLine
	advance := func(kind byte) int {
		if kind == '+' || next == 0 {
			return 0
		}
		next++
		return next - 1
	}
	for i := 0; i < len(ops); {
		if !shown[i] {
			run := i
			for run < len(ops) && !shown[run] {
				run++
			}
			// A run of one is shown: its note would take the same row.
			if run-i > 1 {
				for _, op := range ops[i:run] {
					advance(op.kind)
				}
				note(fmt.Sprintf("  ... %d lines match ...", run-i))
				i = run
				continue
			}
		}
		op := ops[i]
		i++
		line := advance(op.kind)
		text, marked := visibleDiffText(op.line.text, op.kind != ' ')
		dollar = dollar || marked
		switch {
		case op.kind == ' ':
			row(line, ' ', text)
			continue
		case text == "":
			row(line, op.kind, st.label("(empty line)"))
		case op.kind == '-':
			row(line, op.kind, st.del(text))
		default:
			row(line, op.kind, st.ins(text))
		}
		if !op.line.nl && newlineDiffers {
			note(`\ no newline at end`)
		}
	}
	if truncated {
		note("... too many lines to compare; this is the first difference")
	}
	if dollar {
		note("($ ends a line that ends in whitespace)")
	}
}

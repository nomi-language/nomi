package vmhost

import (
	"regexp"
	"testing"
)

// formatGap is a known break in the rule that formatting keeps meaning
// (format_meaning_test.go), too large to fix where it was found. Each is
// pinned by a minimal reproducer that TestKnownFormatGaps checks: formatting
// it must still fail with kind and a message reason matches. When a fix
// makes the reproducer pass the test fails; delete the entry in the same
// change.
type formatGap struct {
	name string
	// kind is the formatFailure kind the reproducer fails with.
	kind string
	// reason is a regular expression the failure's message matches. The
	// fuzz target and TestFormatKeepsMeaning excuse an input whose every
	// failure matches some gap's kind and reason.
	reason string
	src    string
}

var knownFormatGaps = []formatGap{
	{
		// A struct literal that breaks because it is too wide is written
		// across lines, and the next format reads that as the author's
		// choice to stack it. A stacked literal is LocalBroken, hidden from
		// the fits of the line it is on, so inside a call or literal it
		// opens on the call's line (`f(Point{`) instead of on a line of its
		// own. A fix makes one of the two layouts the other, and either
		// moves tracked files: breaking a too-wide literal in place hides
		// it from every enclosing fits check (an `if` stays on one line, a
		// list hugs its second element), and breaking a stacked one as
		// width does undoes the hug the corpus is written in. The source
		// cannot record which of the two broke a literal. A lambda body is
		// fixed this way already (TestFormat_LambdaStructLiteralBodyStaysOnItsLine).
		name:   "gapWidthBrokenLiteralHugsNextTime",
		kind:   "idempotence",
		reason: "first at line \\d+: `[^`]*[(\\[{]` became `[^`]*\\{`",
		src: `fn main() {
    m = same(Point{zed_long_long_long_long_long_long_long_long: 0, alpha_long_long_long_long_long_long_long_long_long: 3}, Point{zed: 0, alpha: 4})
}
`,
	},
	{
		// An arm too wide for its line puts every arm's body on the line
		// below its `->`. The next format finds the wide arm's literal
		// already broken, so the arm fits, and the bodies join their arrows
		// again. gapWidthBrokenLiteralHugsNextTime's cause, at a case arm.
		// Found by TestFormatKeepsMeaningRotating's first run.
		name:   "gapCaseArmsBrokenByWidthJoinNextTime",
		kind:   "idempotence",
		reason: "first at line \\d+: `[^`]*->` became `[^`]*-> [^`]+`",
		src: `struct Shape {
    path: List<Int>
    expected: String
    got_rather_long_rather_long_rather_long_rather_long: String
}

fn decode(r: Result<Int, String>): Result<Int, Shape> {
    case r {
        Ok(parsed) -> Ok(parsed)
        Err(_) -> Err(Shape{path: [], expected: "json", got_rather_long_rather_long_rather_long_rather_long: "invalid"})
    }
}
`,
	},
	{
		// A comment after the `{` of a top-level map destructure, on a
		// statement that also ends in a comment, moves below the statement
		// as the file's last line, and the output then ends without its
		// final newline, which the next format adds. Found by
		// TestFormatKeepsMeaning when a spec edit moved the spec's map
		// destructure block to a new line, which changed its layout
		// variant.
		name:   "gapMapPatternOpeningCommentEndsFileWithoutNewline",
		kind:   "idempotence",
		reason: "first at line \\d+: `` became ``",
		src: `x = 1

{ // t4
 "a" => y} = m // end
`,
	},
}

func (gap *formatGap) matches(f formatFailure) bool {
	return gap.kind == f.kind && regexp.MustCompile(gap.reason).MatchString(f.msg)
}

// knownFormatGapFor is the known gap every one of failures matches, or nil.
func knownFormatGapFor(failures []formatFailure) *formatGap {
	var found *formatGap
	for _, f := range failures {
		var match *formatGap
		for i := range knownFormatGaps {
			gap := &knownFormatGaps[i]
			if gap.matches(f) {
				match = gap
				break
			}
		}
		if match == nil {
			return nil
		}
		found = match
	}
	return found
}

// TestKnownFormatGaps holds each known gap's reproducer to its gap.
func TestKnownFormatGaps(t *testing.T) {
	for _, gap := range knownFormatGaps {
		t.Run(gap.name, func(t *testing.T) {
			o := checkFormat(t.TempDir(), gap.src, true)
			if o.parsed == 0 {
				t.Fatalf("the reproducer does not parse:\n%s", gap.src)
			}
			for _, f := range o.failures {
				if gap.matches(f) {
					return
				}
			}
			if len(o.failures) == 0 {
				t.Fatalf("formatting this reproducer keeps its meaning now; remove %s from knownFormatGaps:\n%s", gap.name, gap.src)
			}
			t.Fatalf("the reproducer fails another way; want %s matching %q:\n%s", gap.kind, gap.reason, o.describe(gap.src))
		})
	}
}

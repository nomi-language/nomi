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
		// The source's binding is a pattern binding with a map pattern,
		// which the checker rejects without `else` (a map pattern can
		// fail). Dropping the parentheses makes it a map destructure,
		// which the checker accepts and which fails at run time instead.
		// The parser chooses between the two by whether every value is a
		// bare name; the fix is for it to read `(host)` as `host` there,
		// or for the two forms to mean the same.
		name:   "gapParenthesizedNameInMapDestructure",
		kind:   "meaning",
		reason: `PatternBinding became \*ast\.MapDestructure`,
		src: `fn main() {
    config = {"host" => "localhost"}
    {"host" => (host)} = config
}
`,
	},
	{
		// A comment inside a parameter list, after its `(` or a `,`, has
		// nowhere to go in the syntax tree: the parser skips it and the
		// formatter cannot write it back. The fix is a trivia slot on
		// Param (and one for an empty list).
		name:   "gapCommentInParameterList",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `[(,]` and before `[^`]*`",
		src: `fn double( // why
    n: Int,
): Int {
    n * 2
}
`,
	},
	{
		// A comment after a grouping `(` is skipped by the parser, and the
		// group has no trivia slot to keep it in.
		name:   "gapCommentOpeningGroup",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `\\(` and before `[^`]*`",
		src: `fn f(): Int {
    y = try ( // why
        g())
    y
}
`,
	},
	{
		// A comment inside a map pattern is skipped by the parser; the
		// pattern keeps no trivia but its end trivia.
		name:   "gapCommentInMapPattern",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `\\{` and before `[^`]*`",
		src: `fn f(m: Map<String, Int>): Int {
    case m {
        { // the key
            "a" => n,
        } -> n
        _ -> 0
    }
}
`,
	},
	{
		// An empty doc comment, `///`, before an expression at file level
		// (which the checker rejects) has no declaration to attach to,
		// and the formatter drops it. With text after `///` the source
		// does not parse.
		name:   "gapDocCommentOnFileLevelExpression",
		kind:   "meaning",
		reason: "lost \"///\" after",
		src:    "///\n{}\n",
	},
	{
		// A string literal or test name holding a byte that is not UTF-8
		// lexes, and the formatter writes the byte back as U+FFFD, a
		// different string. The fix is for the lexer to reject a source
		// that is not UTF-8.
		name:   "gapInvalidUTF8InString",
		kind:   "meaning",
		reason: `: "[^"]*\\x[89a-f][0-9a-f][^"]*" became`,
		src:    "x = \"\x81\"\n",
	},
	{
		// A comment after a list spread's `..` is skipped by the parser.
		name:   "gapCommentAfterListSpread",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `\\.\\.` and before",
		src: `xs = [1, .. // rest
[2]]
`,
	},
	{
		// A compound literal that breaks because it is too wide is
		// written across lines, and the next format reads that as the
		// author's choice to stack it: a stacked literal inside a call or
		// literal opens on the call's line (`f(Point{`) instead of on a
		// line of its own.
		name:   "gapWidthBrokenLiteralHugsNextTime",
		kind:   "idempotence",
		reason: "first at line \\d+: `[^`]*[(\\[{]` became `[^`]*\\{`",
		src: `fn main() {
    m = same(Point{zed_long_long_long_long_long_long_long_long: 0, alpha_long_long_long_long_long_long_long_long_long: 3}, Point{zed: 0, alpha: 4})
}
`,
	},
	{
		// A literal nested deep enough is broken all the way down,
		// one-field literals included. The next format reads the outer
		// literal as stacked by its author, which turns the cascade off,
		// and a one-field literal written across lines does not count as
		// stacked, so it joins back onto one line.
		name:   "gapDepthCascadeUndoneNextTime",
		kind:   "idempotence",
		reason: "first at line \\d+: `[^`]*\\{` became `[^`]*\\}+,?`",
		src: `fn main() {
    deep = Struct.update(w, {label: "two", person: {address: {zip: "99999"}}})
}
`,
	},
	// The five below were found by TestFormatKeepsMeaningRotating's first
	// run, on layout variants the fixed set never made.
	{
		// An arm too wide for its line puts every arm's body on the line
		// below its `->`. The next format finds the wide arm's literal
		// already broken, so the arm fits, and the bodies join their arrows
		// again.
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
		// A parenthesized name in a namespaced distinct's destructuring is a
		// pattern binding; without the parentheses the parser reads a
		// distinct destructure. The undotted `Day((n)) = d` keeps its
		// meaning. The same family as gapParenthesizedNameInMapDestructure.
		name:   "gapParenthesizedNameInDottedDistinctDestructure",
		kind:   "meaning",
		reason: `PatternBinding became \*ast\.DistinctDestructure`,
		src: `type Day Int

type Day.Hours Int

fn hours_value(h: Day.Hours): Int {
    Day.Hours((n)) = h
    n
}
`,
	},
	{
		// An `if` stage whose condition is parenthesized, followed by a
		// subject-less `case` stage: the formatter writes the condition as
		// a stage of its own and moves the `if` into the case's subject,
		// and the result does not parse.
		name:   "gapParenthesizedIfStageConditionBeforeCaseStage",
		kind:   "meaning",
		reason: "the formatted text does not parse: .*expected '\\{' after if condition",
		src: `fn big?(n: Int): Bool {
    n > 10
}

fn mixed(n: Int): String {
    n
    |> if ((big?())) { Some("large") } else { None }
    |> case {
        Some(word) -> word
        None -> "no"
    }
}
`,
	},
	{
		// A comment after the last arm of a `case` pipe stage, before its
		// `}`, is dropped. The reason matches any comment lost before a
		// `}`: the message cannot tell a case stage apart.
		name:   "gapCommentClosingCaseStage",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `[^`]*` and before `\\}`",
		src: `fn parse_id(s: String): Result<Int, String> {
    Ok(1)
}

fn f(input: String): Int {
    input
    |> case parse_id() {
        Ok(id) -> id
        Err(_) -> 0
        // trailing
    }
}
`,
	},
	{
		// A comment after a spread's list, before the enclosing list's
		// `]`, is dropped.
		name:   "gapCommentClosingSpreadList",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `\\]` and before `\\]`",
		src: `fn main() {
    list = [1, ..[] // why
    ]
}
`,
	},
	{
		// A comment after a multi-line raw string's closing backtick moves
		// to a line of its own with a blank line after it; the next format
		// drops the blank line. Found by NOMI_GEN_SEED=5000.
		name:   "gapCommentAfterMultilineRawString",
		kind:   "idempotence",
		reason: "first at line \\d+: `` became `[^`]+`",
		src:    "fn main() {\n    pattern = `\n        a\n        ` // why\n\n    assert pattern == \"a\"\n}\n",
	},
	{
		// A literal its author stacked (a trailing comma before its `}`)
		// is broken all the way down, a list holding a one-field literal
		// included; the next format finds the one-field literal written
		// across lines, which does not count as stacked, and joins the list
		// back onto one line. gapDepthCascadeUndoneNextTime's mechanism,
		// started by the author's stacking rather than by depth. Found by
		// NOMI_GEN_SEED=20734000.
		name:   "gapStackedLiteralCascadeUndoneNextTime",
		kind:   "idempotence",
		reason: "first at line \\d+: `[^`]*\\[` became `[^`]*\\],?`",
		src: `fn main() {
    b = Bag{ items: [Point{x: 7}], label: "solo",
    }
}
`,
	},
	{
		// A comment after a `with` statement's `=`, before its value, is
		// skipped by the parser: the With node keeps no trivia there. Found
		// when edits above a spec block renamed it (spec.md:L6078~layout1)
		// and so changed the layout variant drawn for it.
		name:   "gapCommentAfterWithEquals",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `=` and before `[^`]*`",
		src: `fn publish_quietly() {
    with App.logger = // why
        Silent
    publish()
}
`,
	},
	{
		// A comment after a tuple's last element, before its `)`, is
		// dropped: the tuple keeps no trivia there. Found by the rotating
		// set (NOMI_GEN_SEED=20733001, spec.md:L2490, a tuple that is a
		// lambda parameter's default) after edits above that spec block
		// renamed it.
		name:   "gapCommentClosingTuple",
		kind:   "meaning",
		reason: "lost \"[^\"]*\" after `[^`]*` and before `\\)`",
		src: `fn main() {
    t = (1, 2 // why
    )
    _ = t
}
`,
	},
	{
		// `(f)<Dog>(x)` as a call's argument parses as the comparisons
		// `(f) < Dog > (x)`; the formatter drops the parentheses around `f`
		// as redundant, and `f<Dog>(x)` is a call with a type argument.
		// Found by NOMI_GEN_SEED=42000.
		name:   "gapParenthesizedCalleeBeforeTypeArguments",
		kind:   "meaning",
		reason: `\*ast\.Binary became \*ast\.Call`,
		src: `fn main() {
    g((f)<Dog>((Dog{weight: 31})))
}
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

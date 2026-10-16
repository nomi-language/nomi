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
		// A binding annotated with a function type whose value is a lambda
		// with a block body, too long for its line: the first format keeps
		// the annotation on one line and breaks the lambda's body, and a
		// second format breaks the annotation's parameter list instead.
		// Found by FuzzFormatKeepsMeaning; it involves no `subtests`
		// statement, the only construct whose layout the change that found
		// it touched.
		name:   "function-typed binding of a long lambda",
		kind:   "idempotence",
		reason: "first at line \\d+: `\\w+: \\(\\w+\\) -> \\w+ = \\|\\w+\\| \\{` became `\\w+: \\(`",
		src:    "{a00000000000000000000000000:(A00)->A00=|A000000000000000000000000000000000000000000000000|\"\"}",
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

package vmhost

import (
	"regexp"
	"testing"
)

// runGap is a program shape that lowers and crashes the VM when it runs: a
// known break in the rule that a lowered program runs without crashing
// (run_fuzz_test.go). Each is pinned by a minimal reproducer that
// TestKnownRunGaps runs: it must still crash, with text the gap's pattern
// matches. When a fix makes it run, or stop lowering, the test fails; delete
// the entry in the same change.
type runGap struct {
	name string
	// fix says where the fix belongs.
	fix string
	// crash matches the crash's text (runOutcome.crash: its kind, then the
	// panic or the error, then any stack). The fuzz target and the
	// deterministic tests skip a crash some gap's pattern matches.
	crash *regexp.Regexp
	// alsoSkip match the same cause in the other shapes the generator and
	// the fuzzer reach; the tests skip those crashes too.
	alsoSkip []*regexp.Regexp
	src      string
}

var knownRunGaps = []runGap{}

// knownRunGapFor is the known gap one of whose patterns matches the crash,
// or nil.
func knownRunGapFor(crash string) *runGap {
	for i := range knownRunGaps {
		gap := &knownRunGaps[i]
		for _, re := range append([]*regexp.Regexp{gap.crash}, gap.alsoSkip...) {
			if re.MatchString(crash) {
				return gap
			}
		}
	}
	return nil
}

// TestKnownRunGaps holds each known gap's reproducer to its gap: it lowers,
// and running it crashes with text the gap's pattern matches.
func TestKnownRunGaps(t *testing.T) {
	for _, gap := range knownRunGaps {
		t.Run(gap.name, func(t *testing.T) {
			o := runInput(t.TempDir(), gap.src)
			switch {
			case o.class == runSkipped:
				t.Fatalf("this reproducer no longer runs (%s); if that is the fix (%s), remove %s:\n%s",
					o.skip, gap.fix, gap.name, gap.src)
			case !o.failed():
				t.Fatalf("this reproducer runs without crashing now (%s); remove %s from knownRunGaps:\n%s",
					o.class, gap.name, gap.src)
			case !gap.crash.MatchString(o.crash):
				t.Fatalf("the reproducer crashes another way:\n%s\nwant a crash matching %s", o.crash, gap.crash)
			}
		})
	}
}

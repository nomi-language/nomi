package vmhost

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/rotation"
)

// loweringGap is a program shape the front end accepts and the IR builder
// declines: a known break in the rule that an accepted program lowers. Each
// is pinned by a minimal reproducer that TestKnownLoweringGaps checks: the
// front end must accept it and the lowering must decline it with reason.
// When a fix makes the reproducer lower, or makes the front end reject it,
// the test fails; delete the entry, and the generator's avoidance that
// names it (lowering_gen_test.go), in the same change.
type loweringGap struct {
	name string
	// fix says where the fix belongs: the checker, when the program
	// should be rejected or the checker records types the builder cannot
	// read, or the builder, when the program should run.
	fix string
	// reason is a substring of the builder's reason for the reproducer's
	// decline (NOMI_DEBUG_LOWERING), or of the panic's message and stack
	// when panics is set. The fuzz target skips a decline whose every reason matches
	// some gap's, and a panic whose message matches a panicking gap's.
	reason string
	// panics marks a gap where the compiler panics instead of declining.
	panics bool
	// alsoSkip are other reasons the same cause declines with, in other
	// shapes the fuzzer finds; the fuzz target skips them too.
	alsoSkip []string
	// noSkip marks a reason the builder derives from the node alone
	// (declineReason: "a named argument", "a ListLit", "qualified call,
	// Type.method: Iter.map"), which unrelated declines share, or a panic
	// text any lint violation of its kind shares. The fuzz target does not
	// excuse a decline or a panic by it, so a new cause behind the same node
	// still fails there.
	noSkip bool
	src    string
}

var knownLoweringGaps = []loweringGap{
	{
		name: "gapDeferInTestValueArm",
		fix: "builder: a `defer` in the block arm of an `if` or `case` used as a value in a test " +
			"body. A test body's builder admits no ordinary defer scope (deferOK), so armInto " +
			"opens none and the defer declines; the same arm in a function or lambda body runs. " +
			"The lowering generator's deferBlock writes its defer-in-arm shape only outside tests",
		reason: "a defer outside the own statements of a body, a block or a loop callback",
		src: `import std/io

test "a defer in a value arm" {
    x = if True {
        defer io.print("arm")
        1
    } else {
        2
    }
    assert x == 1
}
`,
	},
	{
		name: "gapPartialDefaultBeforeLaterPlaceholder",
		fix: "builder: a partial application whose open slot has a computed default that reads an " +
			"earlier slot of the callee left open by a placeholder written after it, as in " +
			"`span(b: _, _)` over `fn span(a: Int, b: Int = a * 2)`. The partial's parameters are " +
			"its placeholders in written order, so `b` is its first and `a` its second, and a " +
			"default supplier takes only the parameters before its own (indirectCall's convention). " +
			"The supplier for `b` would need `a`. Either a call through the partial fills an " +
			"omitted slot after the slots the callee reads first, or the supplier takes every " +
			"parameter",
		reason: "a partial's open slot whose default is not a literal, before a placeholder for an earlier slot",
		src: `import std/io

fn span(a: Int, b: Int = a * 2): (Int, Int) {
    (a, b)
}

fn main() {
    u = span(b: _, _)
    io.inspect(u(1, 2))
}
`,
	},
}

// knownGapFor is the known gap every one of reasons matches, or nil.
func knownGapFor(reasons []string) *loweringGap {
	var found *loweringGap
	for _, r := range reasons {
		var match *loweringGap
		for i := range knownLoweringGaps {
			gap := &knownLoweringGaps[i]
			if gap.panics || gap.noSkip {
				continue
			}
			for _, want := range append([]string{gap.reason}, gap.alsoSkip...) {
				if strings.Contains(r, want) {
					match = gap
				}
			}
			if match != nil {
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

// knownPanicFor is the known panicking gap whose reason the panic's message
// or stack holds, or nil.
func knownPanicFor(internal string) *loweringGap {
	for i := range knownLoweringGaps {
		if knownLoweringGaps[i].panics && !knownLoweringGaps[i].noSkip && strings.Contains(internal, knownLoweringGaps[i].reason) {
			return &knownLoweringGaps[i]
		}
	}
	return nil
}

// TestKnownLoweringGaps holds each known gap's reproducer to its gap: the
// front end accepts it and the lowering declines it for the recorded
// reason.
func TestKnownLoweringGaps(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	for _, gap := range knownLoweringGaps {
		t.Run(gap.name, func(t *testing.T) {
			o := checkLowers(t.TempDir(), gap.src)
			switch {
			case !o.accepted:
				t.Fatalf("the front end rejects this reproducer now; if that is the fix (%s), "+
					"remove the gap and the generator's avoidance of it:\n%s\n%s", gap.fix, gap.src, o.rejection)
			case gap.panics:
				if !strings.Contains(o.internal, gap.reason) {
					t.Fatalf("this reproducer no longer panics with %q; if it is fixed, remove %s:\n%s\npanic: %s\ndeclines: %v",
						gap.reason, gap.name, gap.src, o.internal, o.declines)
				}
				return
			case o.internal != "":
				t.Fatalf("the compiler panics:\n%s", o.internal)
			case len(o.unplaced) > 0:
				t.Fatalf("a decline names no cause or no position:\n%s", o.describe(gap.src))
			case len(o.declines) == 0:
				t.Fatalf("this reproducer lowers now; remove %s from knownLoweringGaps and the generator's "+
					"avoidance of it:\n%s", gap.name, gap.src)
			}
			for _, r := range o.reasons {
				if !strings.Contains(r, gap.reason) {
					t.Fatalf("the reproducer declines for another reason: %s\nwant one containing %q", r, gap.reason)
				}
			}
		})
	}
}

// TestFrontEndAcceptsSoItLowers checks every generated program, and every
// program a corpus file embeds for std/compiler to run, under plain
// `go test`: each one the front end accepts must lower, with no known gap
// excused. The generator steers clear of the known gaps, so a decline here
// is a new cause, or a gap a change re-opened. The corpus files, the tour
// blocks and the spec's programs are held to the same rule where they run
// (TestTestCommand_VMCorpus, TestTourDoctests, TestSpecPrograms); the fuzz
// target starts from all of them.
func TestFrontEndAcceptsSoItLowers(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	inputs := generatedPrograms()
	for _, s := range corpusSeeds(t) {
		if strings.Contains(s.name, "#") {
			inputs = append(inputs, s)
		}
	}
	accepted := 0
	for _, in := range inputs {
		o := checkLowers(t.TempDir(), in.src)
		if o.accepted && strings.HasPrefix(in.name, "gen/") {
			accepted++
		}
		if o.failed() {
			t.Errorf("%s: %s", in.name, o.describe(in.src))
		}
	}
	// Most generated programs check; a generator change that broke that
	// would leave this test checking nothing.
	if accepted < generatedCount*3/4 {
		t.Errorf("the front end accepts %d of %d generated programs; the generator should produce mostly well-typed programs", accepted, generatedCount)
	}
}

// TestFrontEndAcceptsSoItLowersRotating checks generatedCount generated
// programs beside the fixed ones TestFrontEndAcceptsSoItLowers checks, from
// a start seed that changes every UTC day (internal/rotation), so each day
// checks different programs. NOMI_GEN_SEED pins the start; a failure names
// the program's seed and the command that checks exactly that program.
func TestFrontEndAcceptsSoItLowersRotating(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	set := rotation.For(t, "./vmhost", generatedCount)
	accepted := 0
	for _, seed := range set.Seeds() {
		in := generatedProgram(int(seed))
		o := checkLowers(t.TempDir(), in.src)
		if o.accepted {
			accepted++
		}
		if o.failed() {
			t.Errorf("%s: %s%s", in.name, set.Failure(seed), o.describe(in.src))
		}
	}
	if set.Count >= 20 && accepted < set.Count*3/4 {
		t.Errorf("the front end accepts %d of %d generated programs; the generator should produce mostly well-typed programs", accepted, set.Count)
	}
}

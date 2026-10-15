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
	// Type.method: Iter.map"), which unrelated declines share. The fuzz
	// target does not excuse a decline by it, so a new cause behind the
	// same node still fails there.
	noSkip bool
	src    string
}

var knownLoweringGaps = []loweringGap{
	{
		name: "gapListPatternOverFunctions",
		fix: "builder: a `case` whose subject is a list of function values, as in `case xs { [] -> 0 " +
			"[_, ..rest] -> 1 + size(rest) }` with `xs: List<(Int) -> Int>`. caseRegion admits a list " +
			"subject only when its element is a retained leaf or transport kind, and a function value " +
			"is neither, though the VM holds one in a list like any reference",
		reason: "a tail `case` scrutinee outside retained literal-pattern kinds: List<(",
		src: `import std/io

fn size(xs: List<(Int) -> Int>): Int {
    case xs {
        [] -> 0
        [_, ..rest] -> 1 + size(rest)
    }
}

fn main() {
    io.inspect(size([|x: Int| x + 1]))
}
`,
	},
	{
		name: "gapBlockLocalTypeEscapesItsBlock",
		fix: "builder or checker: a block whose value has a type declared inside the block. The " +
			"checker types the binding at that block-local type; the builder projects the binding's " +
			"type outside the block, where the declaration does not resolve, so the block's value " +
			"has no IR type. Either the builder resolves it by declaration or the checker rejects " +
			"the escape",
		reason: "a branch or block value kind with no IR type: an unsupported type",
		src: `import std/io

fn main() {
    w = {
        struct Loc {
            n: Int
        }
        Loc{n: 1}
    }
    io.inspect(w)
}
`,
	},
	{
		name: "gapShortCircuitOperandWhoseArmsAllLeave",
		fix: "builder: an `if` or `case` every arm of which leaves the body by `return`, `break` " +
			"or `continue`, as the right operand of `and` or `or`, as in `x > 0 and case x { 1 -> " +
			"return True  _ -> return False }`. The operand runs only when the left one lets it, " +
			"so the `and` still has a value on the other path, and the builder does not lower a " +
			"region with no value there. An operand evaluated whatever the values before it (an " +
			"argument, a field, a list item, a pipe's source) runs. The builder could lower the " +
			"`and` as an `if` whose one arm is the region",
		reason: "an `if` or `case` whose every arm leaves the body, used for its value",
		src: `import std/io

fn short(x: Int): Bool {
    x > 0 and case x {
        1 -> return True
        _ -> return False
    }
}

fn main() {
    io.inspect(short(1))
}
`,
	},
	{
		name: "gapVectorOfIters",
		fix: "builder: a vector literal where a `Vector<Iter<T>>` is expected, as in " +
			"`v: Vector<Iter<Int>> = #[[1]]` or `#[[circle]]` for `Vector<Iter<Shape>>`. " +
			"lowerTypedOperand builds a vector literal at the expected element only when that " +
			"element holds embeds or an empty collection (irTypedWithin), so each list element " +
			"is built as a List and the vector is not viewed as one of Iters; letting it through " +
			"reaches vectorMakeOf, whose element predicate refuses an Iter with no reason. " +
			"A list in the same position (`List<Iter<Shape>>`) lowers",
		reason: "a value of kind Vector<List<Int>> where Vector<Iter<Int>> is expected",
		src: `import std/io

fn main() {
    v: Vector<Iter<Int>> = #[[1]]
    io.print(Iter.count(v))
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
		if knownLoweringGaps[i].panics && strings.Contains(internal, knownLoweringGaps[i].reason) {
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

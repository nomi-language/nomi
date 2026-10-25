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
		name: "gapTestReturnArmInAssertionSubject",
		fix: "builder: a `return` ending an arm of a region inside a test's assertion subject " +
			"(`assert case get() { ..., Err(_) -> return } == \"x\"`) declines: the subject is lowered " +
			"with its rows recorded and read back, and testExit refuses a return there; outside a " +
			"subject the same arm ends the case",
		reason: "a return in a test body the builder reads back",
		src: `fn get() -> Result<String, String> {
    Err("x")
}

test "a return arm in a subject" {
    assert case get() {
        Ok(d) -> d
        Err(_) -> return
    } == "x"
}
`,
	},
	{
		name: "gapStdInterfaceCallThroughItsModule",
		fix: "builder: a call qualified by a std interface named through its module " +
			"(`display.Display.to_string(x)`, `sql.Conn.dialect(c)`) declines: qualDottedOwner resolves " +
			"a module-qualified type owner, and a std interface is not one, so the call never reaches " +
			"the interface route the bare `Display.to_string(x)` takes",
		reason: "qualified call with a 3-segment qualifier",
		noSkip: true,
		src: `import {
    std/display
    std/io
}

fn main() {
    io.print(display.Display.to_string(3))
}
`,
	},
	{
		name: "gapPartialStdNonliteralDefault",
		fix: "builder: a partial whose open slot is a std function's parameter with a non-literal " +
			"default (`style: ListStyle = ListStyle.And`) needs that default as the slot's supplier; " +
			"partialCalleeDefault reaches only this file's functions, local function values and " +
			"sibling files' default accessors, and std has none",
		reason: "a partial's open slot whose default is not a literal, over a callee outside this file's functions",
		src: `import {
    std/io
    std/locale.Locale
    std/strings.ListStyle
}

fn main() {
    names = ["Ann", "Bo"]
    [ListStyle.And, ListStyle.Or]
    |> Iter.map(&String.join_list(names, Locale` + "`en`" + `, style: &1))
    |> Iter.each(fn s -> io.print(s))
}
`,
	},
	{
		name: "gapStdGenericInterfaceExistential",
		fix: "builder: a value held as an instantiation of a std generic interface (`Contains<Int>`) " +
			"has no kind: ifaceInstTypeOf instantiates only a program interface's template, and std's " +
			"generic interfaces have no rt dispatch table (stdIfaceSpec.matches refuses type parameters)",
		reason: "a binding annotation outside the retained value domain: an unsupported type",
		noSkip: true,
		src: `import std/io

struct Bag {
    n: Int
}

impl Contains<Int> for Bag {
    fn contains?(c: Bag, x: Int) -> Bool {
        c.n == x
    }
}

fn main() {
    b: Contains<Int> = #Bag{n: 3}
    io.print(Bool.to_string(Contains.contains?(b, 3)))
}
`,
	},
	{
		name: "gapDebugOfBoxedEmbedsEnumWithItsOwnDebug",
		fix: "builder: `Debug.inspect(s)` over an interface value that may hold an `embeds` enum " +
			"with a hand-written `impl Debug` declines in existentialDebugImpls, which cannot name " +
			"that enum's Debug body as a leaf; the box names the enum, so the renderer could select it",
		reason: "qualified call, Type.method: Debug.inspect",
		noSkip: true,
		src: `import std/io

interface Says {
    fn says(s: self) -> String
}

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

impl Says for Shape {
    fn says(_s: Shape) -> String {
        "shape"
    }
}

impl Debug for Shape {
    fn inspect(_s: Shape) -> String {
        "a shape"
    }
}

fn shape(r: Int) -> Shape {
    #Circle{r}
}

fn main() {
    s: Says = shape(4)
    io.print(Debug.inspect(s))
}
`,
	},
	{
		name: "gapSelflessImplOfStdContainerQualifiedByIt",
		fix: "builder: `Maybe.make(1)` for a program impl of a function with no `self` operand " +
			"(`impl Make for Maybe<T>`) declines in stdRecvOwnerPlan, which finds the instance from an " +
			"operand of the owner's type or from the result's head; here neither is a Maybe, and the " +
			"instance is the `self` inside the checked result `Result<Maybe<Int>, String>`",
		reason: "qualified call, Type.method: Maybe.make",
		noSkip: true,
		src: `import std/io

interface Make {
    fn make(n: Int) -> Result<self, String>
}

impl Make for Int {
    fn make(n: Int) -> Result<Int, String> {
        Ok(n)
    }
}

impl Make for Maybe<T> where T: Make {
    fn make(n: Int) -> Result<Maybe<T>, String> {
        T.make(n) |> Result.map(Some)
    }
}

fn main() {
    r: Result<Maybe<Int>, String> = Maybe.make(1)
    io.print(Debug.inspect(r))
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

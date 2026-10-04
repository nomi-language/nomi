package ir_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

const nomatchFile = "nomatch.nomi"

// caseDeform is how a plant bends the fixture. The bends are BUILD OPTIONS
// rather than mutations because `Block.Append` rejects an instruction after a
// terminator, so a plant cannot reach into a finished block.
type caseDeform struct {
	// afterTrap appends into the fallthrough block after its NoMatch.
	afterTrap func(f *ir.Func, nomatch *ir.Block)
	// trapAheadOfArm puts a NoMatch at the head of the first arm, ahead of
	// the arm's own two instructions.
	trapAheadOfArm bool
}

// buildCaseFunc builds the graph `internal/irbuild`'s `tailCase` builds for
// `fn f(n: Int): String { case n { 0 -> "z" _ -> "m" } }`: one literal test,
// two arms, a fallthrough holding the trap, and one exit.
//
// A HAND-BUILT FIXTURE AND NOT A LOWERING, so the lint plants below can
// deform ONE thing and leave everything else well-formed. The production
// producer is exercised separately, over the corpus, by `internal/irbuild`'s
// TestIRRetainedPopulationRuns.
func buildCaseFunc(t *testing.T, d caseDeform) (*ir.Func, *ir.Block) {
	t.Helper()
	var (
		fnPos   = ir.At(nomatchFile, 1, 1)
		casePos = ir.At(nomatchFile, 2, 3)
		litPos  = ir.At(nomatchFile, 3, 5)
		arm0Pos = ir.At(nomatchFile, 3, 10)
		arm1Pos = ir.At(nomatchFile, 4, 10)
	)
	ty, _ := ir.NewTable().Concrete("String", "String")
	ty.SetVal(ir.StringType)
	f := ir.NewFunc(fnPos, "f")
	subj := f.AddParam(ir.NewSymbol("n"), ir.ValInt)
	f.SetType(subj, ir.IntType)
	entry := f.NewBlock(fnPos, "entry")
	result := f.NewTemp()
	entry.Append(ir.NewSlot(fnPos, result, ty))

	exit := f.NewBlock(casePos, "exit")
	nomatch := f.NewBlock(casePos, "nomatch")
	nomatch.Append(ir.NewNoMatch(casePos))
	if d.afterTrap != nil {
		// Appended BEFORE the terminator, because `Block.Append` rejects an
		// instruction after one — which is `SetTerm`'s own discipline and is
		// why a plant is a build option rather than a mutation.
		d.afterTrap(f, nomatch)
	}
	nomatch.SetTerm(ir.NewJump(casePos, exit.ID()))

	arm0 := f.NewBlock(arm0Pos, "arm")
	arm1 := f.NewBlock(arm1Pos, "arm")
	sel := f.NewBlock(casePos, "next")

	lit := f.NewTemp()
	entry.Append(ir.NewInt(litPos, lit, 0))
	m := ir.NewMatchLitInto(litPos, f.NewTemp(), subj, lit)
	entry.Append(m)
	entry.SetTerm(ir.NewBranch(litPos, m.Dst(), arm0.ID(), sel.ID()))
	sel.SetTerm(ir.NewJump(arm1Pos, arm1.ID()))

	for _, arm := range []struct {
		b   *ir.Block
		pos ir.Pos
		s   string
	}{{arm0, arm0Pos, "z"}, {arm1, arm1Pos, "m"}} {
		if arm.b == arm0 && d.trapAheadOfArm {
			arm.b.Append(ir.NewNoMatch(arm.pos))
		}
		v := f.NewTemp()
		arm.b.Append(ir.NewString(arm.pos, v, arm.s))
		arm.b.Append(ir.NewCopy(arm.pos, result, v))
		arm.b.SetTerm(ir.NewJump(arm.pos, exit.ID()))
	}
	exit.SetTerm(ir.NewReturn(casePos, result))
	return f, nomatch
}

// TestNoMatch_TheTrapIsAnInstructionThatFaults pins what the node says, which
// is the whole of its contract: a position, a fault, no operands and no
// destination.
func TestNoMatch_TheTrapIsAnInstructionThatFaults(t *testing.T) {
	pos := ir.At(nomatchFile, 7, 3)
	n := ir.NewNoMatch(pos)

	if got := n.Pos(); got != pos {
		t.Errorf("Pos() = %v, want %v", got, pos)
	}
	if n.Dst() != ir.NoTemp {
		t.Errorf("Dst() = %v, want NoTemp: a trap computes nothing", n.Dst())
	}
	if uses := n.AppendUses(nil); len(uses) != 0 {
		t.Errorf("AppendUses = %v, want none: every arm has already tested the "+
			"scrutinee, so the trap reads nothing", uses)
	}
	if got := ir.InstrFaults(n); got != ir.FaultNoMatch {
		t.Errorf("InstrFaults = %v, want %v. The trap has to satisfy Faulting or "+
			"Block.CanFault answers false for the one block in a `case` that can only "+
			"fault", got, ir.FaultNoMatch)
	}
	if !ir.FaultNoMatch.Any() || !ir.FaultNoMatch.Has(ir.FaultNoMatch) {
		t.Error("FaultNoMatch must be a live bit of the Faults set")
	}
	if got := ir.FaultNoMatch.String(); got != "nomatch" {
		t.Errorf("FaultNoMatch.String() = %q, want %q — a bit missing from the "+
			"String table prints as the empty string, which reads like no fault",
			got, "nomatch")
	}
	// The three pre-existing bits still print, because adding a fourth to the
	// table is where a table-driven String silently loses a row.
	if got := (ir.FaultOverflow | ir.FaultDivByZero).String(); got != "overflow|div0" {
		t.Errorf("existing bits print %q, want %q", got, "overflow|div0")
	}
}

// TestNoMatch_ItNeedsAPositionLikeEveryOtherNode holds this node to the
// per-node position rule.
// The trap PRINTS its line, so a position-less one is the one case where the
// absence reaches a user's output.
func TestNoMatch_ItNeedsAPositionLikeEveryOtherNode(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("ir.NewNoMatch accepted a node with no position")
		}
	}()
	ir.NewNoMatch(ir.Pos{})
}

// TestNoMatch_ABlockHoldingTheTrapCanFault is the property `Block.CanFault`
// has to answer for the fallthrough block, with the control that a block
// WITHOUT one cannot.
//
// Without this the trap could have been a plain instruction and nothing would
// have noticed, because no fault edge is set anywhere in production — so
// `CanFault` is the only reader of `Faults` the corpus exercises. See
// fault.go.
func TestNoMatch_ABlockHoldingTheTrapCanFault(t *testing.T) {
	f, nomatch := buildCaseFunc(t, caseDeform{})
	if !nomatch.CanFault() {
		t.Error("the fallthrough block holds a trap and CanFault says it cannot fault")
	}
	if nomatch.FirstFaultAt() != 0 {
		t.Errorf("FirstFaultAt = %d, want 0: the trap is the block's only instruction",
			nomatch.FirstFaultAt())
	}
	entry := f.Blocks()[0]
	if entry.CanFault() {
		t.Error("the entry holds a Slot, an Int and a Match and CanFault says it can " +
			"fault, so the control is not a control")
	}
	if _, has := nomatch.Fault(); has {
		t.Error("nothing sets a fault edge in this fixture; the default is " +
			"that the fault leaves the function")
	}
}

// TestLintNoMatch_TheControlIsClean is what makes the plants below mean anything:
// the `case` graph the production builder produces passes every rule,
// including with an UNREACHABLE fallthrough block — which is the shape
// `caseInto` has always built and which `Arms.Finish` admits by name.
func TestLintNoMatch_TheControlIsClean(t *testing.T) {
	f, _ := buildCaseFunc(t, caseDeform{})
	if err := ir.Lint(f); err != nil {
		t.Fatalf("the well-formed case graph does not lint: %v", err)
	}
}

// TestLintNoMatch_NothingFollowsATrap plants RuleNoMatchIsLast and shows it firing.
//
// THE PLANT IS THE PRODUCER BUG THIS NODE INVITES: a producer building the
// fallthrough block appends the trap and keeps appending. The extra
// instruction is legal in every other respect — a well-positioned `ir.Const`
// writing a fresh temporary nothing reads — so no other rule has anything to
// say about it, which is what makes the new rule load-bearing rather than a
// second opinion.
func TestLintNoMatch_NothingFollowsATrap(t *testing.T) {
	f, _ := buildCaseFunc(t, caseDeform{afterTrap: func(f *ir.Func, nomatch *ir.Block) {
		nomatch.Append(ir.NewInt(ir.At(nomatchFile, 2, 3), f.NewTemp(), 99))
	}})

	err := ir.Lint(f)
	if err == nil {
		t.Fatal("an instruction after a nomatch linted clean, so RuleNoMatchIsLast " +
			"cannot fire and is not a rule")
	}
	le, isLint := err.(*ir.LintError)
	if !isLint {
		t.Fatalf("Lint returned %T, want *ir.LintError", err)
	}
	found, only := false, true
	for _, v := range le.Violations {
		if v.Rule == ir.RuleNoMatchIsLast {
			found = true
			continue
		}
		only = false
	}
	if !found {
		t.Fatalf("the violations name no %q: %v", ir.RuleNoMatchIsLast, le.Violations)
	}
	if !only {
		t.Errorf("the plant deforms one thing and should fire one rule: %v", le.Violations)
	}
	if !strings.Contains(err.Error(), "diverges") {
		t.Errorf("the message should say why nothing can follow a trap: %v", err)
	}
}

// TestLintNoMatch_ATrapAheadOfAnArmBodyIsReported is the second direction, and it
// is a different producer bug from the first: not "kept appending after the
// trap" but "put the trap in the wrong block". A reachable arm whose body
// continues past a trap is code the graph claims runs.
//
// ONE VIOLATION PER TRAP, NOT ONE PER DEAD INSTRUCTION, and the arm here
// holds TWO after it so the two are distinguishable. The violation is a fact
// about the trap's placement; the message names the FIRST instruction that
// cannot run, because that is the one a producer looks at. Reporting the rest
// would be N lines about one misplaced node, which is the opposite of this
// file's every-violation policy — that policy is about independent bugs.
func TestLintNoMatch_ATrapAheadOfAnArmBodyIsReported(t *testing.T) {
	f, _ := buildCaseFunc(t, caseDeform{trapAheadOfArm: true})

	err := ir.Lint(f)
	if err == nil {
		t.Fatal("an arm body after a trap linted clean")
	}
	if n := strings.Count(err.Error(), string(ir.RuleNoMatchIsLast)); n != 1 {
		t.Errorf("the rule reported %d times, want 1 — one violation per misplaced "+
			"trap: %v", n, err)
	}
	if !strings.Contains(err.Error(), `const string "z"`) {
		t.Errorf("the message should name the first instruction that cannot run: %v", err)
	}
	if !strings.Contains(err.Error(), "b3 instr 1") {
		t.Errorf("the message should locate the dead instruction in its block: %v", err)
	}
}

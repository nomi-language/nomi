package analysis_test

import (
	"strings"
	"testing"
)

// A map pattern's KEY is an expression, and until checkMapPatternEntries landed
// nothing type-checked it.
//
// These tests are a PAIR, and neither half is worth much alone. The first three
// demonstrate the check FIRING; the last two demonstrate it NOT firing where the
// language says it must not. A suite with only the firing half would pass just
// as well for `checkNodeExpecting(entry.Key, mt.Key)` — the stricter spelling
// that is WRONG — so the negatives are what pin the design rather than merely
// the behaviour.
//
// They run through checkSourceWithStdlib rather than checkSource, and that was
// not a preference. checkSource is deliberately prelude-free, so `Int` and
// `Maybe` are unknown types in it and EVERY case failed identically — including
// the known-present control, whose whole job is to fire. A control that reports
// no power indicts the harness before the hypothesis, and it did.

// j6KeyErrors returns the messages checkSourceWithStdlib reports for src.
func j6KeyErrors(src string) string {
	_, errs := checkSourceWithStdlib(src)
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, " | ")
}

// TestMapPatternKeyIsTypeChecked is the FIRING half: an arity error written
// inside a map pattern key must be reported.
//
// It was not, for the checker's whole life. `checkPattern`'s *ast.MapPattern
// arm walked `n.Entries` and visited only `entry.Pattern`. The builder DOES
// walk `entry.Key`, so names inside it resolved and the omission never
// presented as an unknown-identifier bug — which is why nothing noticed.
//
// The control that makes this a measurement rather than an assertion is
// TestMapLiteralKeyIsTypeChecked below: the SAME expression in a map LITERAL
// key has always been an error, so the check exists and this position was
// simply not wired to it.
func TestMapPatternKeyIsTypeChecked(t *testing.T) {
	got := j6KeyErrors(`fn f(m: Map<Int, String>): String {
  case m {
    {Int.to_string(1, 2, 3) => v} -> v
    _ -> "?"
  }
}
`)
	if !strings.Contains(got, "arguments") {
		t.Errorf("an arity error inside a map PATTERN key went unreported.\n"+
			"got: %q\n"+
			"checkPattern's *ast.MapPattern arm must reach entry.Key through "+
			"checkMapPatternEntries; visiting only entry.Pattern leaves the key "+
			"expression unchecked, which also leaves a prelude constructor there "+
			"with no instantiated Symbol.CallType.", got)
	}
}

// TestMapDestructureKeyIsTypeChecked is the same firing at the second position.
// checkMapDestructure had the identical loop, and a fix to one arm would not
// have reached the other.
func TestMapDestructureKeyIsTypeChecked(t *testing.T) {
	got := j6KeyErrors(`fn f(m: Map<Int, String>): String {
  {Int.to_string(1, 2, 3) => v} = m
  v
}
`)
	if !strings.Contains(got, "arguments") {
		t.Errorf("an arity error inside a map DESTRUCTURE key went unreported.\n"+
			"got: %q\n"+
			"checkMapDestructure must reach entry.Key through "+
			"checkMapPatternEntries.", got)
	}
}

// TestMapLiteralKeyIsTypeChecked is the KNOWN-PRESENT CONTROL, and it asserts
// something no other test here does: that the two positions AGREE.
//
// Without it, the two tests above could both be satisfied by a check invented
// for pattern keys alone, and the fact that this behaviour already existed one
// construct over — which is the entire evidence that the pattern position was
// wrong rather than undesigned — would be untested. If this test ever fails
// while the two above pass, the divergence has been re-introduced from the
// other side.
func TestMapLiteralKeyIsTypeChecked(t *testing.T) {
	got := j6KeyErrors(`fn f(): Map<Int, String> {
  {Int.to_string(1, 2, 3) => "x"}
}
`)
	if !strings.Contains(got, "arguments") {
		t.Errorf("a map LITERAL key stopped being type-checked: %q\n"+
			"this is the control for TestMapPatternKeyIsTypeChecked. Its whole "+
			"job is to show the check pre-existed at a sibling position.", got)
	}
}

// TestMapPatternKeyOfAnUnrelatedTypeStaysLegal is the NEGATIVE half, and it is
// a fact about the language rather than about this implementation.
//
// COUNTEREXAMPLE to the stricter design. `tests/08-pattern-matching/
// map_patterns_test.nomi` writes, and asserts the answer of:
//
//	fallthrough = case flags {        // flags: Map<Bool, String>
//	  {Color.Blue => v} -> v
//	  _ -> "fallthrough"
//	}
//	assert fallthrough == "fallthrough"
//
// A map pattern key is an expression EVALUATED and then looked up in a
// structurally-keyed Map. A key of an unrelated type is therefore a guaranteed
// MISS, not a type error, and the program above depends on that. So
// checkMapPatternEntries must pass NO expected type, and the natural-looking
// `checkNodeExpecting(entry.Key, mt.Key)` — which is exactly what the map
// LITERAL path does — would turn a passing corpus program into a front-end
// error.
//
// This cannot expire the way an implementation note can: it is two measured
// answers to one program.
func TestMapPatternKeyOfAnUnrelatedTypeStaysLegal(t *testing.T) {
	got := j6KeyErrors(`enum Color {
  Red
  Blue
}

fn f(m: Map<Bool, String>): String {
  case m {
    {Color.Blue => v} -> v
    _ -> "fallthrough"
  }
}
`)
	if got != "" {
		t.Errorf("a map pattern key of an unrelated type was rejected: %q\n"+
			"map_patterns_test.nomi:108-121 requires this to be LEGAL and to "+
			"fall through to `_`. checkMapPatternEntries must pass NO expected "+
			"type; imposing the map's K is the one thing this position may not do.", got)
	}
}

// TestMapDestructureKeyOfAnUnrelatedTypeStaysLegal pins the same permission at
// the destructure position, which is where imposing K would be most tempting:
// a destructure has no `_` arm to fall through to, so a mismatched key there
// traps at run time instead of missing quietly. That is still a RUN-TIME
// outcome and not a type error, and the two positions must not diverge on the
// question just because their failure modes differ.
func TestMapDestructureKeyOfAnUnrelatedTypeStaysLegal(t *testing.T) {
	got := j6KeyErrors(`enum Color {
  Red
  Blue
}

fn f(m: Map<Bool, String>): String {
  {Color.Blue => v} = m
  v
}
`)
	if got != "" {
		t.Errorf("a map destructure key of an unrelated type was rejected: %q", got)
	}
}

// TestMapPatternKeyPreludeConstructorIsAccepted pins the shape that motivated
// finding the omission: it must remain LEGAL, and it must not acquire an error
// from the new visit.
//
// The positive witness that the visit actually SOLVED the constructor's type
// argument is not here — it is internal/irbuild's
// TestMapPatternKey_PreludeConstructorLowers, which asserts the same source
// lowers. That split is deliberate rather than tidy: `Symbol.CallType` is what
// the IR builder needs, and an assertion here over `fa.References` would have been
// invariant under the bug, because the BUILDER already walks entry.Key and
// records references from it. A witness that reads the same before and after
// measures nothing.
func TestMapPatternKeyPreludeConstructorIsAccepted(t *testing.T) {
	got := j6KeyErrors(`fn f(m: Map<Maybe<Int>, String>): String {
  case m {
    {Some(2) => v} -> v
    _ -> "?"
  }
}
`)
	if got != "" {
		t.Errorf("a prelude constructor in map pattern key position was rejected: %q", got)
	}
}

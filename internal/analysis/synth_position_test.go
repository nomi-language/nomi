package analysis

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// synthPositionSource declares four types across the shapes that synthesize
// differently (struct, enum, distinct, opaque distinct) and derives several
// protocols on them, so one run allocates slots from more than one kind and
// more than one declaration.
const synthPositionSource = `
struct Point {
  x: Int
  y: Int
}

struct Key {
  id: Int
}

derive Equatable for Key
derive Hashable for Key

enum Rank {
  Low
  High
}

derive Comparable for Rank

opaque type Token Int
`

// synthSlotsOf runs the front end's synthesis sequence over a fresh parse of
// src and returns, per synthesized impl block, the label "<Iface> for <Type>"
// mapped to the block's base line.
func synthSlotsOf(t *testing.T, src string) map[string]int {
	t.Helper()
	nodes := parseNodes(t, src)
	// The whole sequence, in the order every caller runs it: `derive Iface
	// for T` statements lower first, then `@derive` decorators, then the
	// universal Debug default.
	nodes, lowerErrs := LowerDerives(nodes)
	if len(lowerErrs) > 0 {
		t.Fatalf("LowerDerives: %v", lowerErrs)
	}
	nodes, errs := SynthesizeDerives(nodes)
	if len(errs) > 0 {
		t.Fatalf("SynthesizeDerives: %v", errs)
	}
	nodes = SynthesizeUniversalDebug(nodes)
	return synthBlockSlots(nodes)
}

func synthBlockSlots(nodes []ast.Node) map[string]int {
	out := map[string]int{}
	for _, n := range nodes {
		block, ok := n.(*ast.ImplBlock)
		if !ok || block.Interface == nil || block.Receiver == nil || !IsSynthesizedLine(block.Line) {
			continue
		}
		label := fmt.Sprintf("%s for %s", TypeExprBaseName(block.Interface), TypeExprBaseName(block.Receiver))
		out[label] = block.Line
	}
	return out
}

// TestSynthPositionsAreAFunctionOfTheDeclaration is the KEY assertion behind
// the emitted-stdlib reproducibility guard, and it is deliberately not the
// same assertion.
//
// A determinism test passes if a scheme merely STABILISES. Seed the old global
// counter once per process and every derive in the build would collapse onto
// one slot: byte-identical output, and every synthesized position aliasing
// every other, which is precisely the clobbering in fa.Definitions the band
// exists to prevent. So determinism is the cheap second witness and this is
// the claim: a synthesized block's base line is a function of WHICH
// declaration and WHICH protocol, and of nothing else.
//
// The three legs are the three things the old counter was a function of and
// this one is not — repetition (execution history), interleaving with
// unrelated synthesis, and concurrency.
func TestSynthPositionsAreAFunctionOfTheDeclaration(t *testing.T) {
	want := synthSlotsOf(t, synthPositionSource)
	if len(want) < 2 {
		t.Fatalf("expected several synthesized blocks, got %d — this test is measuring nothing", len(want))
	}

	// One declaration, one protocol, one slot. Distinctness is the half a
	// determinism test cannot see.
	seen := map[int]string{}
	for label, line := range want {
		if prior, clash := seen[line]; clash {
			t.Errorf("%q and %q were allocated the same base line %d", prior, label, line)
		}
		seen[line] = label
	}

	// The band, and its ceiling. Below 2^30 would alias real source; at or
	// above 2^31 a line no longer fits the `int` `rt.NoCaseMatchError` takes
	// on a 32-bit GOARCH.
	for label, line := range want {
		if !IsSynthesizedLine(line) {
			t.Errorf("%q is at line %d, outside the synth band", label, line)
		}
		if line >= 1<<31 {
			t.Errorf("%q is at line %d, at or above 2^31", label, line)
		}
	}
	if IsSynthesizedLine(1) || IsSynthesizedLine(1<<30-1) {
		t.Error("IsSynthesizedLine classifies a real source line as synthesized")
	}

	// Leg 1 — repetition. A fresh parse of the same source, same answer.
	if got := synthSlotsOf(t, synthPositionSource); !sameSlots(got, want) {
		t.Errorf("a second run over the same source allocated different slots:\n want %s\n  got %s", showSlots(want), showSlots(got))
	}

	// Leg 2 — interleaving. Unrelated synthesis in between must not move
	// anything. A global counter fails here even though it is deterministic.
	synthSlotsOf(t, "struct Unrelated {\n  a: Int\n  b: Int\n}\n")
	synthSlotsOf(t, "struct AlsoUnrelated {\n  c: Int\n}\n\nderive Equatable for AlsoUnrelated\n")
	if got := synthSlotsOf(t, synthPositionSource); !sameSlots(got, want) {
		t.Errorf("unrelated synthesis in between moved the slots:\n want %s\n  got %s", showSlots(want), showSlots(got))
	}

	// Leg 3 — concurrency. The LSP fans BuildProject out across goroutines,
	// which is what the old counter's atomic increment made SAFE without
	// making REPRODUCIBLE.
	const goroutines = 8
	results := make([]map[string]int, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodes := parseNodes(t, synthPositionSource)
			nodes, _ = LowerDerives(nodes)
			nodes, _ = SynthesizeDerives(nodes)
			results[i] = synthBlockSlots(SynthesizeUniversalDebug(nodes))
		}()
	}
	wg.Wait()
	for i, got := range results {
		if !sameSlots(got, want) {
			t.Errorf("goroutine %d allocated different slots:\n want %s\n  got %s", i, showSlots(want), showSlots(got))
		}
	}
}

// TestSynthesizedBlocksRecordTheirOrigin holds the second half of the fix: a
// synthesized block names the DECLARATION it was synthesized for, so a
// diagnostic about it has somewhere real to point. Before this, the checker
// reported `line 1077657600, col 1: impl function 'inspect': …` — a position
// no programmer can navigate to and no editor can resolve.
func TestSynthesizedBlocksRecordTheirOrigin(t *testing.T) {
	src := "struct Point {\n  x: Int\n}\n\nenum Rank {\n  Low\n  High\n}\n\nderive Equatable for Rank\n"
	nodes := parseNodes(t, src)
	decls := map[string]Pos{}
	for _, n := range nodes {
		if name, ok := typeDeclName(n); ok {
			line, col := declPos(n)
			decls[name] = Pos{Line: line, Col: col}
		}
	}
	nodes, _ = LowerDerives(nodes)
	nodes, _ = SynthesizeDerives(nodes)
	nodes = SynthesizeUniversalDebug(nodes)

	found := 0
	for _, n := range nodes {
		block, ok := n.(*ast.ImplBlock)
		if !ok || !IsSynthesizedLine(block.Line) || block.Receiver == nil {
			continue
		}
		found++
		recv := TypeExprBaseName(block.Receiver)
		want, known := decls[recv]
		if !known {
			t.Errorf("synthesized block for unknown receiver %q", recv)
			continue
		}
		got := Pos{Line: block.SynthOriginLine, Col: block.SynthOriginCol}
		if got != want {
			t.Errorf("block for %q records origin %v, want the declaration at %v", recv, got, want)
		}
		if IsSynthesizedLine(got.Line) {
			t.Errorf("block for %q records a SYNTHESIZED origin %v — the whole point is that it is real source", recv, got)
		}
	}
	if found == 0 {
		t.Fatal("no synthesized block found — this test is measuring nothing")
	}
}

func sameSlots(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func showSlots(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("\n   %-28s %d", k, m[k])
	}
	return out
}

// TestBuildImplBlockTypes_SynthSignatureErrorsReportTheDeclaration is the
// build-phase half of the rule checker.repointSynthDiagnostics enforces for
// the check phase.
//
// IT IS A UNIT TEST ON PURPOSE, AND THAT IS THE FINDING RATHER THAN A
// SHORTCUT. No Nomi source reaches this branch today: the closed set of names
// the synthesizers put in a SIGNATURE is either registry builtins or covered
// by compilerKnownSynthType (`Ordering`, `Json`, `Json.ShapeError`,
// `Result<…>`), so every signature resolves. Measured by removing the
// repoint and re-running the source-level rows in
// analysis/synth_diag_position_test.go: they still passed, which is the
// definition of an unexercised fence. The branch exists for the case
// compilerKnownSynthType's own comment reserves — "a synthesizer emitting an
// unexpected name still fails loudly" — and a loud failure at line 1073987584
// is not loud, it is unreadable. So the fence is kept and exercised HERE,
// where the unexpected name can actually be planted.
//
// The planted line is synthSlot(synthOriginDecl, 1, synthKindAutoDebug),
// which is 1073987584 — the exact value that reached a user and started this.
func TestBuildImplBlockTypes_SynthSignatureErrorsReportTheDeclaration(t *testing.T) {
	const synthLine = 1 << 30 // recomputed below; the literal documents the value
	planted := synthSlot(synthOriginDecl, 1, synthKindAutoDebug)
	if planted != 1073987584 {
		t.Fatalf("slot arithmetic moved: synthSlot(decl, 1, autoDebug) = %d, want 1073987584", planted)
	}
	if !IsSynthesizedLine(planted) || planted < synthLine {
		t.Fatalf("planted line %d is not in the synth band", planted)
	}

	fn := &ast.FuncDef{
		Name: "inspect",
		Line: planted, Col: 1,
		Params: []ast.Param{{
			Name:           "value",
			TypeAnnotation: &ast.SimpleType{Name: "NotAThing", Line: planted, Col: 20},
			Line:           planted, Col: 14,
		}},
		ReturnTypeExpr: &ast.SimpleType{Name: "AlsoNotAThing", Line: planted, Col: 40},
	}
	block := &ast.ImplBlock{
		Interface: &ast.SimpleType{Name: "Debug", Line: planted, Col: 1},
		Receiver:  &ast.SimpleType{Name: "P", Line: planted, Col: 1},
		Items:     []ast.Node{fn},
		Line:      planted, Col: 1,
		// The declaration a reader would navigate to.
		SynthOriginLine: 7,
		SynthOriginCol:  8,
	}
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{
			{Line: planted, Col: 1}: {Name: "inspect", Kind: SymbolFunction, Pos: Pos{Line: planted, Col: 1}},
		},
	}

	errs := buildImplBlockTypes(fa, NewTypeRegistry(), block)
	if len(errs) == 0 {
		t.Fatal("no diagnostics: the planted unresolvable signature types resolved, so this row proves nothing")
	}
	want := Pos{Line: 7, Col: 8}
	for _, e := range errs {
		if got := (Pos{Line: e.Line, Col: e.Col}); got != want {
			t.Errorf("signature diagnostic reports %v, want the declaration at %v: %v", got, want, e)
		}
	}
}

// TestBuildImplBlockTypes_AHandWrittenBlockKeepsItsSignaturePosition is the
// control: a block with no recorded origin is not rewritten. Without it, a
// version that stamped every impl signature error onto some fixed position
// would pass the test above.
func TestBuildImplBlockTypes_AHandWrittenBlockKeepsItsSignaturePosition(t *testing.T) {
	fn := &ast.FuncDef{
		Name: "inspect",
		Line: 12, Col: 3,
		ReturnTypeExpr: &ast.SimpleType{Name: "NotAThing", Line: 12, Col: 30},
	}
	block := &ast.ImplBlock{
		Interface: &ast.SimpleType{Name: "Debug", Line: 11, Col: 1},
		Receiver:  &ast.SimpleType{Name: "P", Line: 11, Col: 17},
		Items:     []ast.Node{fn},
		Line:      11, Col: 1,
		// Zero on every hand-written block.
	}
	fa := &FileAnalysis{
		ModuleScope: NewScope(nil),
		References:  map[Pos]*Symbol{},
		Definitions: map[Pos]*Symbol{
			{Line: 12, Col: 3}: {Name: "inspect", Kind: SymbolFunction, Pos: Pos{Line: 12, Col: 3}},
		},
	}
	errs := buildImplBlockTypes(fa, NewTypeRegistry(), block)
	if len(errs) == 0 {
		t.Fatal("no diagnostics: the planted unresolvable return type resolved, so this row proves nothing")
	}
	for _, e := range errs {
		if e.Line != 12 || e.Col != 30 {
			t.Errorf("hand-written signature diagnostic moved: got line %d col %d, want 12/30: %v", e.Line, e.Col, e)
		}
	}
}

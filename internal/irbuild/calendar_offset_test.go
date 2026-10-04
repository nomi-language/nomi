package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestStdOffsetDateTimeOverloadSetDoesNotCollapse is the guard for the defect
// this type's shape makes easiest to reintroduce: A SPELLING IS AN OVERLOAD SET,
// NOT A DECLARATION.
//
// `impl Add<Unit, OffsetDateTime>` is written eleven times in std/calendar.nomi
// and every block spells its method `OffsetDateTime.add`. So any table that
// treats the spelling as a declaration's identity answers about the GROUP when
// asked about a MEMBER, and the failure is not a refusal: one settled rung seeds
// the settled set, every other rung reads as settled, and the pass hands out a
// name for a body it then declines to emit.
//
// # What is asserted, and why none of it is a count
//
// The POPULATION is derived from std source rather than typed, so the guard
// needs no maintenance when a rung is added or removed — and so a rung that
// disappears from std fails here rather than shrinking a number nobody rechecks.
// Then, per member:
//
//   - distinct index KEY, which is what carries the interface instantiation;
//   - distinct *stdFunc POINTER, which is the identity the fixed points use;
//   - LOWERABLE, because the collapse's symptom is a name lent to a declaration
//     that never lowers;
//   - and, across the set, no two members sharing a lowered callee — an rt
//     symbol or a Go-spelled name — because that is the wrong ANSWER the
//     fixture would print rather than the refusal a reader would notice.
func TestStdOffsetDateTimeOverloadSetDoesNotCollapse(t *testing.T) {
	declared := stdAddRungsInSource(t, "OffsetDateTime")
	if len(declared) == 0 {
		t.Fatal("std/calendar declares no `impl Add<_, OffsetDateTime>` block, so this guard is " +
			"vacuous; the walk below does not match the source it is derived from")
	}
	idx := stdlibLowering()
	set := idx.byType["OffsetDateTime.add"]
	if len(set) != len(declared) {
		t.Fatalf("std declares %d `Add<_, OffsetDateTime>` rung(s) %v but the index holds %d under "+
			"the spelling `OffsetDateTime.add`; a spelling that holds fewer entries than there are "+
			"declarations IS the collapse", len(declared), declared, len(set))
	}
	keys := map[string]bool{}
	ptrs := map[*stdFunc]bool{}
	callees := map[string]string{}
	for _, f := range set {
		if keys[f.key] {
			t.Errorf("two declarations share the index key %q, so one of them is unreachable", f.key)
		}
		keys[f.key] = true
		if ptrs[f] {
			t.Errorf("%q appears twice in the overload set as one *stdFunc", f.key)
		}
		ptrs[f] = true
		if !f.lowerable() {
			t.Errorf("%s does not lower (why=%q); every rung of this ladder is bound or has a "+
				"Nomi body, so a rung that stops lowering here is the ladder breaking rather "+
				"than a gap", f.key, f.why)
			continue
		}
		callee := f.rtCall
		if callee == "" {
			callee = f.key
		}
		if prior, clash := callees[callee]; clash {
			t.Errorf("%s and %s both emit %s, so a call to either lands in the same code — "+
				"which is the collapse producing a WRONG VALUE rather than a refusal",
				prior, f.key, callee)
		}
		callees[callee] = f.key
	}
	// And the pair that would be indistinguishable if the instantiation left the
	// key: the two rungs that reach a bound Go symbol, differing only in the unit.
	//
	// Every rung is a Nomi body that destructures its period and calls a
	// distinctly-named free function, so `rtCall` on the rung itself is empty.
	// The bound symbols are on those free functions' index rows.
	years := idx.byKey["calendar.offset_add_years"]
	months := idx.byKey["calendar.offset_add_months"]
	if years == nil || months == nil {
		t.Fatalf("the two host-bound rungs are not both in byKey (years=%v months=%v); "+
			"the assertion below cannot run", years != nil, months != nil)
	}
	if years.rtCall == "" || months.rtCall == "" {
		t.Fatalf("a host-bound rung reached no Go symbol (years=%q months=%q), so the "+
			"comparison below would compare two empty strings and pass vacuously",
			years.rtCall, months.rtCall)
	}
	if years.rtCall == months.rtCall {
		t.Errorf("both host rungs are bound to %s, so `+ Years(1)` and `+ Months(1)` are the "+
			"same call and differ by eleven months in the answer", years.rtCall)
	}
}

// stdAddRungsInSource is every `impl Add<Unit, recv> for recv` block
// std/calendar declares, as the interface instantiation each one names.
//
// Read off the AST the stdlib pass itself walks, with the SAME helpers
// (analysis.TypeExprBaseName, stdImplKey), so the population cannot be derived
// one way here and another way in collectStdCandidates.
func stdAddRungsInSource(t *testing.T, recv string) []string {
	t.Helper()
	lib := std.Load()
	nodes := lib.Nodes["calendar"]
	if len(nodes) == 0 {
		t.Fatal("std declares no module `calendar`, so nothing below can be derived from it")
	}
	var out []string
	for _, n := range nodes {
		impl, isImpl := n.(*ast.ImplBlock)
		if !isImpl {
			continue
		}
		if analysis.TypeExprBaseName(impl.Receiver) != recv {
			continue
		}
		if analysis.TypeExprBaseName(impl.Interface) != "Add" {
			continue
		}
		out = append(out, stdImplKey(impl.Interface))
	}
	return out
}

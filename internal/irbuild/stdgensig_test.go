package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestGenStructSigAndAnnotationPathsAgree is the CROSS-PATH audit for the pair
// that resolves a generic std struct, and it is written this way because the pair
// cannot audit itself.
//
// Two functions answer "what kind is `Range<Int>`":
//
//	stdGenStructTypeOf   the ANNOTATION path, in a gen, through g.genStructs and g.typeOf
//	genStructSigKind     the SIGNATURE path, no gen, through anchors.genStructs and stdTypeKind
//
// If the two disagree, `Range<Int>` is admissible in a user's annotation and
// refused in std's own constructor for it. A test comparing either against
// `sharedGenStructInstance`
// would compare it against its own callee. So this compares the TWO PATHS with
// each other, at every row of the spec table, and the table is the third thing:
// neither function is consulted to decide what to check.
//
// ASKS THE GRAPH. The population is `stdGenStructSpecs`, so a row appended for
// `Channel<T>` or anything else is covered without editing this test. A
// hand-written list of `{Set, Range}` would pass by omission.
//
// WHAT THIS TEST DOES NOT COVER: deleting the `genStructSigKind` call from
// `stdTypeKind` leaves this test GREEN, because it calls the function directly.
// Agreement and WIRING are two properties and this one is agreement;
// `TestStdRangeConstructorsLower` is the wiring guard.
func TestGenStructSigAndAnnotationPathsAgree(t *testing.T) {
	lib := std.Load()
	checked := 0
	for i := range stdGenStructSpecs {
		s := &stdGenStructSpecs[i]
		module := strings.TrimPrefix(s.origin, "std/")
		fa := lib.Files[module]
		if fa == nil {
			t.Fatalf("%s: std declares no module %q", s.nomi, module)
		}
		// DISTINCT arguments per position, not `Int` everywhere. A uniform
		// argument list makes by-position and by-anything-else agree, so a
		// positional mix-up between the two paths would be invisible — the
		// declaration-order coincidence a struct-pattern fixture has to be
		// permuted to avoid. Stated with its limit: the rows in the table take
		// ONE parameter, so the positional half is armed and unexercised
		// while no two-parameter row exists.
		neutral := []string{"Int", "String", "Bool", "Float"}
		args := make([]string, len(s.params))
		for j := range args {
			args[j] = neutral[j%len(neutral)]
		}
		src := s.nomi + "<" + strings.Join(args, ", ") + ">"
		// THE IMPORT IS DERIVED FROM THE SPEC. This loop's population is
		// `stdGenStructSpecs`, so an appended row is covered without an edit
		// here, and the instantiation has to keep up with it. `fn f(x:
		// Set<Int>)` with no import resolves only because `Set` and `Range` are
		// PRELUDE-exported; a row outside the prelude (`Channel<T>` in
		// std/channels) would fail as `unknown type "Channel"`.
		//
		// So the population and the instantiation must come from the SAME
		// place. `origin` gives the declaring module and `nomi` the item, which
		// is exactly what an import needs. Re-importing a prelude-exported name
		// is a front-end error, so the import is written only when the prelude
		// does not already bind the name; that is read from the prelude itself,
		// so no future row has a precondition to violate.
		item := s.origin + "." + s.nomi
		if lib.Primitives.Lookup(s.nomi) != nil {
			item = ""
		}
		te := parseStdTypeExprImporting(t, item, src)
		gt, isGeneric := te.(*ast.GenericType)
		if !isGeneric {
			t.Fatalf("%s parsed as %T, not a generic type", src, te)
		}

		sigKind, handled := genStructSigKind(gt, stdAnchorsOf(fa))
		if !handled {
			t.Errorf("genStructSigKind declined %s in its own declaring module. It is anchored "+
				"there — TestStdGenStructSpecsMatchStdSource asserts that — so declining means "+
				"anchors.genStructs was not filled, and every std signature naming it refuses as "+
				"`stdlib function outside the scalar subset` with the representation already present. "+
				"See stdgensig.go.", src)
			continue
		}
		if sigKind == kindInvalid {
			t.Errorf("genStructSigKind refused %s at package-neutral arguments", src)
			continue
		}

		// The annotation path, in a gen over the SAME module, so `g.genStructs`
		// and `anchors.genStructs` are resolved against one analysis.
		g := &gen{fa: fa}
		annKind, annHandled := g.stdGenStructTypeOf(gt)
		if !annHandled || annKind == kindInvalid {
			t.Errorf("stdGenStructTypeOf declined or refused %s (handled=%v) while "+
				"genStructSigKind admitted it. The two paths disagree about which instances "+
				"exist, which is the drift a shared intern table cannot detect: both would "+
				"still return a kind, and only one call site would get it", src, annHandled)
			continue
		}
		if annKind != sigKind {
			t.Errorf("%s: the annotation path gives %s and the signature path gives %s. "+
				"They must be the SAME interned def or a std signature and a user annotation "+
				"name two mutually unassignable Go types for one Nomi type",
				src, annKind.nomi(), sigKind.nomi())
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("stdGenStructSpecs is empty, so this comparison checked nothing")
	}
}

// TestGenStructSigKindRefusesWhatItCannotShare is the negative half, and both
// claimed rows are refusals the arm must OWN rather than decline — a decline
// falls through to `stdTypeKind`'s final `return kindInvalid` and the refusal
// then names nothing at all.
//
// HAND-WRITTEN ON PURPOSE, which is the one place in this file where rule (2)'s
// "enumerate a table-driven guard's inputs" does not apply. These four rows
// enumerate the ARM'S FOUR BEHAVIOURS — claim-and-refuse an unrepresentable
// argument, claim-and-refuse a wrong arity, decline a different container,
// decline a prelude instance — not a population. Growing `stdGenStructSpecs`
// adds no behaviour here, and the POPULATION is covered by the cross-path test
// above, which derives it from the table. Every annotation below names a
// prelude-exported constructor, so it needs no import; a row naming anything
// else must use parseStdTypeExprImporting, for the reason in its header.
//
// ONE ROW'S NODE COMES FROM THE PARSER ALONE, and the distinction is the point
// rather than a workaround. `parseStdTypeExpr` runs the CHECKER and fatals when
// it refuses, which is right for a witness that claims to be reachable. The
// wrong-ARITY row does not claim that — it asserts what the arm does with an
// input the arm must own defensively, and the checker refuses the spelling, so
// the analysed route cannot produce the node. It uses
// `parseStdTypeExprUnchecked`, which is the same parser and no checker. The row
// is flagged in the table rather than silently special-cased in the loop, so a
// reader can see which of the four is a contract assertion and which three are
// reachable.
func TestGenStructSigKindRefusesWhatItCannotShare(t *testing.T) {
	lib := std.Load()
	anchors := stdAnchorsOf(lib.Files["ranges"])

	for _, c := range []struct {
		src         string
		wantHandled bool
		// checkerRejects marks a row whose spelling the CHECKER refuses, so its
		// node comes from the parser alone. See the header.
		checkerRejects bool
		why            string
	}{
		{src: "Range<T>", wantHandled: true,
			why: "a bare type PARAMETER argument. stdTypeKind has no arm for one and cannot " +
				"have one, so the instance is unrepresentable — and it must be OWNED, " +
				"because declining would report the container, which lowers, instead of " +
				"the argument, which does not."},
		{src: "Range<Int, Int>", wantHandled: true, checkerRejects: true,
			why: "the wrong ARITY. The checker rejects the program first, so the " +
				"assertion is purely about the ARM's contract, and the node comes from " +
				"the parser. Owning it keeps the arm from " +
				"instantiating against a type nobody wrote, and keeps a decline from " +
				"falling through to stdTypeKind's final `return kindInvalid`, which " +
				"names nothing at all."},
		{src: "List<Int>", wantHandled: false,
			why: "not a generic std struct at all. It must DECLINE so listSigKind still answers, " +
				"and a claim here would swallow every other generic type."},
		{src: "Maybe<Int>", wantHandled: false,
			why: "a prelude instance. Same reason: the prelude arm runs before this one and " +
				"must keep its answer."},
	} {
		var te ast.TypeExpr
		if c.checkerRejects {
			te = parseStdTypeExprUnchecked(t, c.src)
		} else {
			te = parseStdTypeExpr(t, c.src)
		}
		gt, isGeneric := te.(*ast.GenericType)
		if !isGeneric {
			t.Fatalf("%s parsed as %T", c.src, te)
		}
		k, handled := genStructSigKind(gt, anchors)
		if handled != c.wantHandled {
			t.Errorf("genStructSigKind(%s) handled=%v, want %v.\n%s", c.src, handled, c.wantHandled, c.why)
			continue
		}
		if handled && k != kindInvalid {
			t.Errorf("genStructSigKind(%s) admitted %s; it must refuse.\n%s", c.src, k.nomi(), c.why)
		}
	}
}

// TestStdRangeConstructorsLower pins the capability the signature arm buys:
// `Range.from(n: Int): Range<Int>` and `Range.naturals(): Range<Int>` lower.
//
// Their signatures are admitted by the `Range` row in the spec table, and their
// bodies build `Range{start: n, end: None, inclusive: False}`. That literal
// lowers by the rule for every anchored generic std struct: a struct LITERAL of
// a std-anchored generic struct is built AT THE ANCHOR (stdgenstructlit.go), and
// the declaring module is exempt from stdGenStructTypeOf's shadowing carve-out,
// since a module is not shadowing itself. Without that, `Range<Int>` would mean
// `rt.Range` from an annotation and a freshly minted instance from a literal,
// and the constructor would be refused for disagreeing with itself.
//
// So this test is also the wiring guard for genStructSigKind, and a check that
// the anchored-literal rule is general rather than special to one module.
func TestStdRangeConstructorsLower(t *testing.T) {
	idx := stdlibLowering()
	for _, c := range []struct{ key string }{
		{"ranges.Range.from"},
		{"ranges.Range.naturals"},
	} {
		f := idx.byKey[c.key]
		if f == nil {
			t.Errorf("%s: absent from the index; std renamed or moved it", c.key)
			continue
		}
		if !f.lowerable() {
			t.Errorf("%s is refused as %q. The likely cause is the "+
				"anchored-literal path: either stdGenStructLit stopped claiming the "+
				"`Range{...}` literal, or stdGenStructDeclaringModule stopped exempting "+
				"std/ranges from the shadowing carve-out, in which case `Range<Int>` has "+
				"two representations. See stdgenstructlit.go.", c.key, f.why)
			continue
		}
	}
}

package irbuild

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/nomi-language/nomi/rt"
)

// `std/literals.Fragment<T>`, the preludeSpec that mixes a concrete payload
// with a parametric one.
//
// `Maybe` and `Result` are uniformly parametric; Fragment has to distinguish
// "this payload is the type argument" from "this payload is always a String",
// and at `T = String`, the instantiation the corpus uses, the two are
// indistinguishable by layout. So:
//
//   - the shape check compares the DECLARATION's payload token, where `String`
//     and `T` differ whatever T is (prelude.go's matches),
//   - the layout guard reflects over `rt.Fragment[int64]`, where the two
//     payloads are different Go types (TestPreludeLayoutMatchesRT),
//   - and the fixture below runs Fragment at Int, Bool and Maybe<Int> as well
//     as String, so a swapped spec changes the transcript.
//
// A comparison of two runs on its own can see NONE of this, because both would
// be wrong in the same place. Hence the absolute assertions.

// TestFragmentSlotsAreTwoFieldsAtEveryInstantiation is the slot-identity
// property, and it is the one a `Fragment<String>` fixture cannot assert.
//
// assignSlots dedupes an ordinary enum's payload slots by identical underlying
// Go type, because only one variant is live at a time. A prelude instance must
// NOT: rt declares the struct, and a Go generic struct cannot know whether the
// concrete payload and the parametric one coincide. At `T = String` they do —
// both fields are `string` — so a def that deduped them would give `Static` and
// `Dynamic` ONE field, and every `Dynamic` write would be readable as a
// `Static`. Nothing in the transcript of a program that only ever round-trips
// one variant at a time would show it.
func TestFragmentSlotsAreTwoFieldsAtEveryInstantiation(t *testing.T) {
	g := preludeGenForTest(t)
	a := g.preludeByName["Fragment"]
	if a == nil {
		t.Fatal("no Fragment anchor")
	}
	for _, arg := range []kind{kindString, kindInt, kindBool, kindUnit} {
		d := g.preludeInstance(a, []kind{arg}).def
		if len(d.slots) != 2 {
			t.Fatalf("Fragment<%s> has %d slots, want 2 — one slot means Dynamic reads Static's storage",
				arg.nomi(), len(d.slots))
		}
		// The payload each VARIANT reads, which is the thing a swap breaks.
		if got := d.variant("Static").payloads[0].k; got != kindString {
			t.Fatalf("Fragment<%s>.Static carries %s, want String at every instantiation",
				arg.nomi(), got.nomi())
		}
		if got := d.variant("Dynamic").payloads[0].k; got != arg {
			t.Fatalf("Fragment<%s>.Dynamic carries %s, want %s",
				arg.nomi(), got.nomi(), arg.nomi())
		}
		if d.variant("Static").payloads[0].slot == d.variant("Dynamic").payloads[0].slot {
			t.Fatalf("Fragment<%s>: both variants read slot %d",
				arg.nomi(), d.variant("Static").payloads[0].slot)
		}
	}
}

// TestFragmentInternKeyCarriesTheTypeArguments is the intern-key property.
//
// sharedPreludeDefs is keyed on the full rendered type. Keying on the base
// name instead would be a silent map overwrite: `Fragment<Int>` would take
// `Fragment<String>`'s slot and every later mention would resolve to the wrong
// payload type.
func TestFragmentInternKeyCarriesTheTypeArguments(t *testing.T) {
	g := preludeGenForTest(t)
	a := g.preludeByName["Fragment"]
	if a == nil {
		t.Fatal("no Fragment anchor")
	}
	fs := g.preludeInstance(a, []kind{kindString})
	fs2 := g.preludeInstance(a, []kind{kindString})
	fi := g.preludeInstance(a, []kind{kindInt})
	if fs != fs2 {
		t.Fatal("two mentions of Fragment<String> are two kinds; a value built at one would not be assignable at the other")
	}
	if fs == fi {
		t.Fatal("Fragment<String> and Fragment<Int> are one kind")
	}
	if got := fs.nomi(); got != "Fragment<String>" {
		t.Fatalf("Fragment<String> renders as %q", got)
	}
	if got := fi.nomi(); got != "Fragment<Int>" {
		t.Fatalf("Fragment<Int> renders as %q", got)
	}
	if got := fi.nomi(); got != "Fragment<Int>" {
		t.Fatalf("Fragment<Int> reports itself as %q", got)
	}
	// And a nested instance, so the key is exercised at an argument that is
	// itself interned rather than a scalar.
	mi := g.preludeInstance(g.preludeByName["Maybe"], []kind{kindInt})
	fm := g.preludeInstance(a, []kind{mi})
	if got := fm.nomi(); got != "Fragment<Maybe<Int>>" {
		t.Fatalf("Fragment<Maybe<Int>> renders as %q", got)
	}
	if fm == fi || fm == fs {
		t.Fatal("Fragment<Maybe<Int>> collapsed onto a scalar instantiation")
	}
}

// TestTryOperandSetsAgreeWithTheChecker is a tripwire on an AGREEMENT, not a
// guard on live behaviour.
//
// "Which enums may be a `try` operand" is written down twice: the checker
// admits an operand by name (analysis.TryOperandTypeNames), and the builder
// carries `tryOperand` on each spec. Nothing but this test makes them agree.
//
// The builder side cannot be derived from shape: `Fragment<T>` is
// `Static String` / `Dynamic T`, two variants with the first carrying one
// payload, so a shape rule would lower `try frag` as "unwind on Dynamic,
// unwrap Static", a wrong answer. The front end blocks that, so this guards
// the agreement rather than reachable behaviour.
func TestTryOperandSetsAgreeWithTheChecker(t *testing.T) {
	var emitter []string
	for i := range preludeSpecs {
		if preludeSpecs[i].tryOperand {
			emitter = append(emitter, preludeSpecs[i].nomi)
		}
	}
	checker := slices.Clone(analysis.TryOperandTypeNames)
	slices.Sort(emitter)
	slices.Sort(checker)
	if !slices.Equal(emitter, checker) {
		t.Fatalf("the checker admits %v as `try` operands and the builder lowers %v.\n"+
			"An operand only the CHECKER admits is silently refused; one only the BUILDER "+
			"lowers is a construct the front end never sanctioned. Fix whichever side is wrong.",
			checker, emitter)
	}
	if len(emitter) == 0 {
		t.Fatal("no spec is a `try` operand, so this test compares two empty sets")
	}
	// And a spec that is not an operand must exist, or the flag distinguishes
	// nothing.
	if len(emitter) == len(preludeSpecs) {
		t.Fatal("every spec is a `try` operand; the flag does no work here")
	}
}

// TestFragmentIdentityIsNotAName checks that a module declaring its own
// `Fragment` does not have it lowered to rt's.
//
// `Fragment` is not a reserved name the way `Maybe` and `Result` are (the
// analyzer rejects redeclaring those outright), so a user declaration of the
// same name is reachable, and the anchor's (Origin, Name) rule carries the
// weight on its own.
func TestFragmentIdentityIsNotAName(t *testing.T) {
	src := "enum Fragment<T> {\n" +
		"  Static String\n" +
		"  Dynamic T\n" +
		"}\n\n" +
		"fn main(): Unit {\n  if True { }\n}\n"
	p, err := AnalyzeSource("frag_shadow", src)
	if err != nil {
		if strings.Contains(err.Error(), "Fragment") {
			// A front-end error is a stronger version of the same guarantee.
			return
		}
		t.Fatalf("analyzing: %v", err)
	}
	g := &gen{fa: p.Modules[0].FA, types: map[string]*typeDef{}}
	g.loadPreludes()
	if a, found := g.preludeByName["Fragment"]; found {
		t.Fatalf("a locally declared Fragment was anchored as std/literals' (decl %p); "+
			"its own declaration must be refused instead", a.decl)
	}
	// And the local declaration keeps its own identity rather than being
	// silently redirected: a def whose declaration node is the file's own can
	// only be the user's. `preludeByName` staying empty above is the other
	// half.
	g2 := lowerableGen(t, src)
	d := g2.types["Fragment"]
	if d == nil {
		t.Fatal("the locally declared `Fragment` has no def at all")
	}
	if d.preludeOf != nil {
		t.Errorf("the local `Fragment` def is anchored to a prelude spec (%v); its identity "+
			"must be its own declaration, not a name match against std/literals", d.preludeOf)
	}
	if d.decl != nil {
		if _, isLocalEnum := d.decl.(*ast.EnumDef); !isLocalEnum {
			t.Errorf("the local `Fragment` def's declaration node is %T; want this file's own "+
				"*ast.EnumDef", d.decl)
		}
	}
}

// TestFragmentSharedInstanceIsNeutralAndProcessWide holds the sharing rule for
// the third spec.
//
// `rt.Fragment[string]` names rt and a Go builtin, so it renders identically in
// every module's package and is interned PROCESS-WIDE — which is what lets a
// stdlib signature's `List<Fragment<String>>` be the same kind as a user
// module's. An instance whose argument is package-relative stays per-gen.
func TestFragmentSharedInstanceIsNeutralAndProcessWide(t *testing.T) {
	spec := preludeSpecFor("std/literals", "Fragment")
	if spec == nil {
		t.Fatal("no Fragment spec")
	}
	k, shared := sharedPreludeInstance(spec, []kind{kindString})
	if !shared {
		t.Fatal("Fragment<String> is not package-neutral, so a stdlib signature cannot name it")
	}
	if got := k.def.nomi; got != "Fragment<String>" {
		t.Fatalf("the shared def's spelling is %q", got)
	}
	if !k.packageNeutral() {
		t.Fatal("the shared Fragment<String> does not report itself as neutral")
	}
	// The negative half, and it is the safety argument rather than
	// conservatism: a def whose rendering names a type declared in one
	// module's package is correct there and undefined everywhere else.
	local := &typeDef{nomi: "Point"}
	if _, sharedLocal := sharedPreludeInstance(spec, []kind{named(local)}); sharedLocal {
		t.Fatal("Fragment<Point> was interned process-wide; `rt.Fragment[NomiT_Point]` is undefined outside the package that declares Point")
	}
	// rt's own type round-trips through the same table, so an extern registry
	// row cannot claim a shape rt does not have.
	if got := preludeKindOfGoType(reflect.TypeFor[rt.Fragment[string]]()); got != k {
		t.Fatalf("rt.Fragment[string] projects to %v, not the annotation's kind", got.def)
	}
	// And at an instantiation where the concrete and parametric payloads
	// differ, so a spec that read the arguments off the wrong fields fails.
	ki, sharedInt := sharedPreludeInstance(spec, []kind{kindInt})
	if !sharedInt {
		t.Fatal("Fragment<Int> is not package-neutral")
	}
	if got := preludeKindOfGoType(reflect.TypeFor[rt.Fragment[int64]]()); got != ki {
		t.Fatalf("rt.Fragment[int64] projects to %v, want Fragment<Int>", got.def)
	}
}

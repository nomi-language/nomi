package irbuild

import (
	"reflect"
	"sync"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `std/tasks.Outcome<T>` — a GENERIC std enum one of whose variants carries
// another NOMINAL std enum, plus the two mechanism pieces that shape needed.
//
// This file owns both pieces so prelude.go takes short routes to them: the
// handler lives in its owner's file and the table-holder adds a line.
//
// Without it, `Outcome.Cancelled` refuses as `type-qualified reference` and
// `Outcome.Completed(v)` as `qualified call`: one absent representation under
// two key names, selected by nothing but whether the variant carries data.

// namedPayloadSpec is a preludeSpec variant payload whose type is CONCRETE but
// not a scalar: a monomorphic std enum whose Go type rt declares by hand.
//
// # Why a fourth payload shape was needed
//
// preludeVariantSpec could describe three: absent, the type parameter at
// `param`, and the scalar `fixed`. `matches` discharges the third through
// `scalarKind`, which is the builder's closed answer for "what type does this
// annotation name" — and `Failure` is an enum, so `scalarKind` answers
// kindInvalid and an `Outcome` row would produce no anchor at all. Fragment
// mixes parametric with SCALAR, which the three shapes cover.
//
// # WHY A NAME CHECK IS AN IDENTITY CHECK HERE, which it usually is not
//
// Comparing types by printed name is usually wrong, so a bare
// `st.Name == "Failure"` would normally be exactly the defect to avoid. It is
// sound here, and the reason is structural rather
// than a judgement about likelihood:
//
//   - `matches` is only ever reached for a declaration ALREADY established as
//     this spec's own type by the analyzer's (Origin, Name) rule plus a
//     ModuleScope lookup. So `decl` IS std/tasks' `Outcome`.
//   - `origin` below pins the payload enum to the SAME declaring module as the
//     spec that references it. `Failure` and `Outcome` are twelve lines apart
//     in std/tasks.nomi.
//   - A file-local `pub enum Failure` cannot be shadowed by an import: the
//     analyzer rejects the redeclaration. So within that declaration's own
//     file the token `Failure` cannot resolve to anything else.
//
// The co-declaration is therefore the whole licence, and it is ASSERTED rather
// than assumed — TestOutcomeFailureIsCoDeclaredWithOutcome reads std's own
// source and fails if the two ever stop sharing a module, because this name
// check is an identity check only while they do.
type namedPayloadSpec struct {
	// nomi is the name the declaration's payload type expression must spell.
	nomi string
	// origin is the declaring module, and must equal the referencing spec's
	// own origin — see the header. Held so the invariant is a field a guard
	// can read rather than a fact in prose.
	origin string
	// goType is rt's Go type, held as the TYPE rather than as its spelling for
	// stdEnumSpec.goType's reason: a rename or deletion in rt is then a Go
	// compile error here, and the pairing is checked by reflection in tests.
	goType reflect.Type
	// tagField is the exported Go field holding the variant tag.
	tagField string
	// variants are the declared variants in DECLARATION ORDER, which is what
	// fixes the tags: variant i gets tag i+1, and 0 stays reserved invalid.
	variants []namedPayloadVariant
}

// namedPayloadVariant is one variant of a named payload enum: its name, the
// SCALAR it carries, and the exported Go field in rt it is stored in.
//
// # WHY THIS IS DECLARED HERE RATHER THAN REUSING stdEnumVariantSpec
//
// This payload enum is monomorphic with scalar payloads and one slot per
// distinct field name, which looks like stdenum.go's subject. The two shapes
// differ: stdEnumVariantSpec admits a payload whose kind cannot exist until a
// shell pass has run, and this one deliberately admits ONLY a scalar, because
// a named payload's def is shared process-wide and every payload being a
// scalar is what keeps it package-neutral. A single type covering both would
// have to admit the wider shape and lose the restriction that makes this one
// safe. It also keeps this file from depending on the other table's
// representation, which can change with no edit here.
type namedPayloadVariant struct {
	nomi string
	// payload is the SCALAR this variant carries. kindInvalid marks a BARE
	// variant, which carries nothing and gets no slot.
	payload kind
	// field is the exported Go field in rt the payload is stored in, empty for
	// a bare variant.
	//
	// Several variants may name the SAME field, and that is the builder's own
	// dedup rule rather than a shortcut: one slot per distinct underlying Go
	// type, reused across variants because only one variant is live at a time.
	// `Failure`'s two variants both carry a String, so rt.Failure declares one
	// `Msg` for both.
	field string
}

// namedPayloadSpecs is every named payload any preludeSpec references.
//
// It exists so the defs can be built without the spec rows referring to their
// own def builder, which Go rejects as an initialization cycle, and so the
// guards have a POPULATION to iterate rather than one hardcoded name — a
// one-row table proves nothing about generality.
var namedPayloadSpecs = []*namedPayloadSpec{outcomeFailure}

// outcomeFailure is `std/tasks.Failure`, reachable ONLY as `Outcome.Failed`'s
// payload.
//
// # WHY IT IS NOT A stdEnumSpecs ROW, which is where its shape belongs
//
// It is monomorphic with two String payloads, so mechanically it is a textbook
// stdEnumSpecs row. It is not one because that table's bar is
// TestStdEnumSpecsAreReachable: "a row must be REACHED by something, not merely
// be representable", judged by whether any corpus file MENTIONS the type.
//
// No corpus file mentions `Failure`. It IS reached, by `Outcome.Failed(_)`,
// but not by NAME, and the guard's proxy is the name, so it would correctly
// call a `Failure` row scaffolding. So the structure is the one here: a
// payload type owned by the spec that reaches it, rather than a public
// registry row that the public registry's own bar rejects.
var outcomeFailure = &namedPayloadSpec{
	nomi:     "Failure",
	origin:   "std/tasks",
	goType:   reflect.TypeFor[rt.Failure](),
	tagField: "Tag",
	variants: []namedPayloadVariant{
		// Both variants carry a String and BOTH NAME ONE FIELD. That is the
		// builder's slot dedup, stated by the spec and honoured below: only one
		// variant is live at a time, so rt.Failure declares a single `Msg`
		// exactly as rt.CalendarError declares one for five variants.
		{nomi: "Panicked", payload: kindString, field: "Msg"},
		{nomi: "Errored", payload: kindString, field: "Msg"},
	},
}

// namedPayloadDefs is the process-wide *typeDef per named-payload spec.
//
// Process-wide, exactly like opaqueDefs and stdEnumDefs, and for the same
// pointer-identity reason: a named type's identity in the builder IS its
// *typeDef, and two gens each minting their own would make one Nomi type two
// distinct kinds. It is SAFE to share for stdEnumDefs' reason —
// every payload is a scalar, so nothing about the layout is package-relative,
// which is the precondition foreign.go's rule against shared defs protects —
// and `rtDeclared` is what stops any generated package emitting a second Go
// declaration for a type rt already hand-writes.
//
// Keyed by SPEC POINTER rather than by index, and built from
// `namedPayloadSpecs` rather than from each row's own field, because a row
// naming its own def builder is an initialization cycle Go rejects outright.
//
// Tags come from the spec's variant order and nowhere else, so there is one
// place the numbering exists. TestOutcomeLayoutMatchesRT holds the result
// against rt.Failure by reflection, so a spec claiming a field rt does not
// declare fails rather than emitting a selector that does not compile.
var namedPayloadDefs = sync.OnceValue(func() map[*namedPayloadSpec]*typeDef {
	defs := make(map[*namedPayloadSpec]*typeDef, len(namedPayloadSpecs))
	for _, s := range namedPayloadSpecs {
		d := &typeDef{nomi: s.nomi, tagName: s.tagField, isEnum: true, lowerable: true, rtDeclared: true}
		slotOf := map[string]int{}
		for i := range s.variants {
			v := &s.variants[i]
			// Tag i+1: 0 is reserved invalid.
			// kindInvalid: marker — the spec's "this variant carries nothing".
			if v.payload == kindInvalid {
				d.variants = append(d.variants, variantDef{nomi: v.nomi, tag: i + 1, kind: "bare"})
				continue
			}
			slot, made := slotOf[v.field]
			if !made {
				slot = len(d.slots)
				d.slots = append(d.slots, slotDef{k: v.payload})
				slotOf[v.field] = slot
			}
			d.slots[slot].users = append(d.slots[slot].users, v.nomi)
			d.variants = append(d.variants, variantDef{
				nomi:     v.nomi,
				tag:      i + 1,
				kind:     "positional",
				payloads: []payload{{k: v.payload, slot: slot}},
			})
		}
		defs[s] = d
	}
	return defs
})

// def is this spec's process-wide typeDef.
//
// Panics on a spec absent from namedPayloadSpecs rather than returning nil,
// because a nil here becomes a payload kind with no def and the failure would
// surface as a wrong LAYOUT somewhere else entirely. A row that a preludeSpec
// references and this table omits is a programming error at package scope, and
// the only honest report for one is at the first read.
func (s *namedPayloadSpec) def() *typeDef {
	d := namedPayloadDefs()[s]
	if d == nil {
		panic("irbuild: namedPayloadSpecs has no row for " + s.origin + "." + s.nomi)
	}
	return d
}

// namedPayloadSpecOf is the reverse of `def`: the spec a process-wide def
// belongs to, and whether d is one at all.
//
// BY POINTER IDENTITY over the same one-way table, so there is no second
// encoding of the pairing to drift — the forward map is the only place the
// association exists, and this walks it rather than adding a field to
// `typeDef` that a future row could forget to set.
//
// It exists because a position may need to recognise "this is a named payload
// enum" without knowing WHICH, and a name check would be exactly the defect
// this file's header spends three paragraphs licensing an exception to: a user
// enum called `Failure` is a different type with the same printed name. The
// caller is debuginspect.go's structural-Debug gate.
//
// Linear over a table with one row. A map keyed the other way would be a
// second table to keep in step for no measurable gain at this size, and the
// call is once per Debug rendering of a named enum.
func namedPayloadSpecOf(d *typeDef) (*namedPayloadSpec, bool) {
	if d == nil {
		return nil, false
	}
	for s, cand := range namedPayloadDefs() {
		if cand == d {
			return s, true
		}
	}
	return nil, false
}

// matchesDecl reports whether decl is the payload enum this spec describes.
//
// The same standard stdEnumSpec.matches applies, and for the same reason:
// everything here decides REPRESENTATION, and the variant list decides the
// TAGS — so a std edit that reorders `Failure`'s variants must produce NO match
// rather than lower against a numbering nobody wrote. A permuted tag is a wrong
// ANSWER and not a compile error, which is the one failure this file's identity
// machinery cannot otherwise detect.
//
// `Public` because a file-private declaration cannot be the type another module
// names; `Opaque` is rejected because an opaque enum's `case` is a use-site rule
// this does not reproduce.
func (s *namedPayloadSpec) matchesDecl(decl *ast.EnumDef) bool {
	if decl == nil || decl.Name != s.nomi || decl.Opaque || !decl.Public {
		return false
	}
	if len(decl.TypeParams) != 0 || len(decl.Variants) != len(s.variants) {
		return false
	}
	for i, want := range s.variants {
		got := decl.Variants[i]
		if got.Name != want.nomi {
			return false
		}
		// kindInvalid: marker — the spec's "this variant carries nothing".
		if want.payload == kindInvalid {
			if got.Kind != "bare" {
				return false
			}
			continue
		}
		if got.Kind != "positional" || scalarKind(got.DataTypeExpr) != want.payload {
			return false
		}
	}
	return true
}

// --- the bare-variant sentinel ---------------------------------------------

// --- naming the payload type ------------------------------------------------

// namedPayloadNamed resolves a type NAME to a named payload's process-wide def,
// for a module that imports it.
//
// # Why this exists, when the reachability argument said the type is not
// independently nameable
//
// Those are two different claims. The argument for keeping `Failure` out of
// `stdEnumSpecs` is that NO CORPUS FILE
// mentions it, so a public registry row would be scaffolding by that table's own
// bar. It is NOT that the language forbids naming it: `Failure` is an ordinary
// `pub enum` in std/tasks and `import std/tasks.{Outcome, Failure}` is a program
// the front end accepts.
//
// Left unnameable, `Outcome.Failed(Failure.Errored("x"))` would refuse
// `qualified call`, because `namedType("Failure")` misses and `variantCall`
// declines. `Outcome.Failed` could then be MATCHED but never BUILT, a
// half-present representation. The fixture checks it; no corpus file does.
//
// The identity rules are stdEnumNamed's, unweakened: the anchor requires the
// analyzer to have resolved the name in THIS module's scope to a declaration
// whose (Origin, Name) is the spec's and whose SHAPE the spec validates. A
// module declaring its own `Failure` never reaches this, because namedType
// consults `g.types` first and buildTypes put the local declaration there.
func (g *gen) namedPayloadNamed(name string) (*typeDef, bool) {
	if g.fa == nil || g.fa.ModuleScope == nil {
		return nil, false
	}
	for _, s := range namedPayloadSpecs {
		if s.nomi != name {
			continue
		}
		if !s.anchorsIn(g.fa) {
			return nil, false
		}
		return s.def(), true
	}
	return nil, false
}

// anchorsIn reports whether this module's scope resolves the spec's name to the
// declaration the spec describes.
//
// Both halves of the analyzer's nominal-identity rule, and then the SHAPE:
// (Origin, Name) decides which type it is, and the declaration's variant list
// decides the TAGS. A user enum spelling the same name has a different Origin
// and gets nothing, which is the half a name check gets wrong.
func (s *namedPayloadSpec) anchorsIn(fa *analysis.FileAnalysis) bool {
	sym := fa.ModuleScope.Lookup(s.nomi)
	if sym == nil {
		return false
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	et, isEnum := sym.Type.(*analysis.EnumType)
	if !isEnum || et.Origin != s.origin || et.Name != s.nomi {
		return false
	}
	decl, isDecl := sym.Node.(*ast.EnumDef)
	return isDecl && s.matchesDecl(decl)
}

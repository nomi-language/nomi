package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Why a declared type annotation is outside the subset — the type's OWN gap,
// not the shape of the position it appeared in.
//
// # Why the reason is the type's
//
// Every parameter and return whose type the builder can represent is admitted:
// a `List<P>` over a local struct, a tuple, a `Maybe<Int>`, an interface
// existential and a directly-spelled `(String) -> String` all lower. A
// signature refuses only when `typeOf` answers kindInvalid, for a reason that
// lives at the TYPE's declaration and not at the signature. A position-shaped
// key (`non-scalar parameter type`) would name no work anybody could do, so the
// refusal names the reason instead: `stdlib type` is one piece of work,
// `generic type` is a generic instantiation, and `unlowered type` is a cascade
// from a declaration that already reported at its own position.
//
// A NAMESPACED type name (`Probe.Reading`) is a signature-path question, and
// typeOf resolves it.
//
// The other `non-scalar <position> type` keys (field, variant payload,
// element, binding, type argument) are the same shape in other positions.
// rejectTypeAnnotation is written to be callable from any of them.
//
// # Why a second walk rather than a reason channel inside typeOf
//
// typeOf runs for every annotation in every module, including the ones that
// resolve; threading a reason through it would allocate on the hot path to
// carry a string almost nothing reads. This walk runs only after typeOf has
// already answered kindInvalid, so it costs nothing on a file that lowers.
// It reads the record typeOf reads, the type the checker recorded for each
// written type (TypeExprTypes), and asks typeOf at each leaf before naming a
// reason, so the two agree about which types are outside the subset:
// TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses drives both over the
// same annotations.

// namespacedType resolves a dotted type name to the declaration it names,
// under either of the two readings a dotted spelling has in TYPE position.
//
// FIRST, one declaration's whole NAME — `Probe.Reading`, `Day.Hours`,
// `Signal.Level`. Nomi lets a declaration take a qualified name (`pub enum
// Probe.Reading`, parser.parseQualifiedTypeDeclName), and buildTypes already
// registers it under the dotted spelling, as `signal.nomi`'s
// `pub enum Signal.Level` shows. An annotation spells that name as an
// *ast.QualifiedType, which this reads.
//
// SECOND, a MODULE QUALIFIER and a member — `sqlite.Conn`, where `sqlite` is a
// whole-file import and `Conn` is a type the imported file declares.
//
// The declaration-name reading is asked FIRST: the second arm runs only where
// the first found nothing. The two cannot both answer for one spelling anyway —
// a qualifier is bound to a `SymbolModule` and a namespaced declaration is not
// — but the order is fixed rather than left to that, because a name a file
// both declares and imports is the local declaration, as the checker resolves
// it.
//
// Neither reading can be confused with the `Enum.Variant` reading the same
// syntax has in value position, and the reason is positional rather
// than lucky: this is TYPE position, where a variant is not a type.
// `Shape.Circle` as an annotation does not type-check in the front end, so the
// only program that reaches here with a dotted head is one naming a dotted
// DECLARATION or a module member. A name nothing declares is a plain miss and
// stays refused.
func (g *gen) namespacedType(t *ast.QualifiedType) (*typeDef, bool) {
	qualifier, name, ok := qualifiedTypeParts(t)
	if !ok {
		return nil, false
	}
	d := g.writtenTypeDef(t)
	if d == nil || d.foreign == "" && d.nomi != qualifier+"."+name {
		return nil, false
	}
	return d, true
}

// rejectTypeAnnotation reports the narrowest reason te is outside the subset,
// at `at`, with `subject` prefixed to the detail so the position is findable.
//
// Reports whether it named anything. A false answer means te IS representable
// and the caller asked the wrong question; callers treat that as a decline
// rather than reporting a fallback key, because a fallback key would be exactly
// the position-shaped row this file removes.
func (g *gen) rejectTypeAnnotation(te ast.TypeExpr, subject string, at ast.Node) bool {
	construct, detail, ok := g.typeRefusal(te)
	if !ok {
		return false
	}
	if subject != "" {
		detail = subject + ": " + detail
	}
	g.reject(construct, detail, at)
	return true
}

// typeRefusal is rejectTypeAnnotation's decision, without the reporting.
//
// It descends into a composite to find the LEAF that failed —
// `List<Diagnostic>` reports `Diagnostic`, not `List<Diagnostic>`, because
// the list is fine and the element is the work — and names a leaf's reason
// by the type the checker recorded for it, as typeOf reads it
// (writtenTypeRefusal).
func (g *gen) typeRefusal(te ast.TypeExpr) (construct, detail string, ok bool) {
	switch t := te.(type) {
	case *ast.FuncType:
		// One node, two types: a nil Return is a tuple and an arrow is a
		// function. Both are structural and both fail only through a part.
		return g.partRefusal(append(append([]ast.TypeExpr{}, t.Params...), t.Return))

	case *ast.AnonStructType:
		// A RECORD lowers (anonstruct.go), so it fails only through a FIELD —
		// the same shape the FuncType arm above has, and reported the same
		// way: `{items: Map<Bad, Int>}` names the Bad, because the record is
		// fine and the field is the work.
		if len(t.Fields) == 0 {
			// A record with no fields. The one remaining shape with no
			// representation, and it keeps the key rather than falling
			// through to a reason about a field that does not exist.
			return "anonymous struct type", te.TypeString(), true
		}
		fields := make([]ast.TypeExpr, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = f.TypeAnnotation
		}
		return g.partRefusal(fields)

	case *ast.SelfType:
		// `self` outside an interface body reaches nothing; inside one the
		// interface machinery substitutes it before typeOf sees it. Named
		// rather than silently dropped so a future path that does reach here
		// shows up in the tally instead of vanishing.
		return "self type", te.TypeString(), true

	case *ast.SimpleType, *ast.GenericType, *ast.QualifiedType:
		return g.writtenTypeRefusal(te)
	}
	return "type reference", typeText(te), true
}

// partRefusal is the first refusal among parts, the parts of a composite
// type in order (a nil part is a function type's absent return).
func (g *gen) partRefusal(parts []ast.TypeExpr) (string, string, bool) {
	for _, p := range parts {
		if p == nil {
			continue
		}
		if c, d, bad := g.typeRefusal(p); bad {
			return c, d, true
		}
	}
	return "", "", false
}

// writtenTypeRefusal names why te, a type written by its name (`Point`,
// `Box<Int>`, `shapes.Shape`), is outside the subset, by the type the
// checker recorded for it (recordedTypeExpr), as typeOf reads it: nothing
// when typeOf represents it, else recordedTypeRefusal's reason. A
// transparent alias names its target, so the target's own reason is the
// one a programmer can act on, and the alias's name is carried into the
// detail: a reader looking at `p: Stream` needs to know that `Iter<Int>`
// is what `Stream` is. A bound alias names a conjunction of interfaces,
// not a type, and keeps the key its declaration uses.
func (g *gen) writtenTypeRefusal(te ast.TypeExpr) (string, string, bool) {
	written := te.TypeString()
	if qt, qualified := te.(*ast.QualifiedType); qualified && qt.Member == nil {
		return "namespaced type name", written, true
	}
	// kindInvalid: reports — a written type typeOf declines is named below.
	if g.typeOf(te) != kindInvalid {
		return "", "", false
	}
	rec, _ := g.recordedTypeExpr(te)
	rec = analysis.ResolveClosed(rec)
	var alias *ast.TypeAlias
	if sym := g.writtenTypeSymbol(te); sym != nil {
		alias, _ = sym.Node.(*ast.TypeAlias)
	}
	if alias == nil {
		return g.recordedTypeRefusal(te, rec)
	}
	target := alias.TargetTypeExpr
	if target == nil {
		return "type alias", written, true
	}
	var key, detail string
	var reported bool
	if _, here := g.recordedTypeExpr(target); here {
		key, detail, reported = g.typeRefusal(target)
	} else {
		// An alias another file declares: its target is the type this
		// use's record names.
		key, detail, reported = g.aliasTargetRefusal(target, rec)
	}
	if !reported {
		return "", "", false
	}
	return key, written + " is " + detail, true
}

// aliasTargetRefusal is the reason te, the target another file's alias
// declares, is outside the subset where a use of the alias recorded rec:
// te's parts are read with rec's, since the records of te are that file's.
func (g *gen) aliasTargetRefusal(te ast.TypeExpr, rec analysis.Type) (string, string, bool) {
	// kindInvalid: reports — a part the use's record does not represent is named below.
	if g.project(rec) != kindInvalid {
		return "", "", false
	}
	if ft, isFunc := te.(*ast.FuncType); isFunc {
		var parts []analysis.Type
		switch t := rec.(type) {
		case *analysis.FuncType:
			parts = append(append(parts, t.Params...), t.Return)
		case *analysis.TupleType:
			parts = t.Elems
		}
		written := append(append([]ast.TypeExpr{}, ft.Params...), ft.Return)
		for i, p := range written {
			if p == nil || i >= len(parts) {
				continue
			}
			if c, d, bad := g.aliasTargetRefusal(p, analysis.ResolveClosed(parts[i])); bad {
				return c, d, true
			}
		}
		return "", "", false
	}
	return g.recordedTypeRefusal(te, rec)
}

// recordedTypeRefusal is the reason te, written by its name, is outside
// the subset, where the checker recorded rec for it (nil for no type):
//
//   - a container the builder represents (a List, a Map, an Iter, a
//     prelude enum) fails only through an argument: `List<Diagnostic>`
//     reports `Diagnostic`;
//   - a name with no type is one nothing in the program declares, or one
//     only a declaration nested in a block declares, whose own refusal is
//     the reason (nestedTypeDeclKey);
//   - a type parameter needs the type-parameter dictionary;
//   - a program's interface or type reports its declaration's own recorded
//     refusal (ifaceReason, unloweredReason), and a program's generic type
//     its template's wall, then its instance's refusal, then its failing
//     argument; another file's generic type is named as written;
//   - a type no program file declares reports the key of its family:
//     `stdlib interface`, `generic type` or `stdlib type`.
func (g *gen) recordedTypeRefusal(te ast.TypeExpr, rec analysis.Type) (string, string, bool) {
	written, name, args := te.TypeString(), writtenTypeName(te, rec), writtenTypeArgs(te)
	if len(args) > 0 && g.representedContainer(te, rec) {
		return g.partRefusal(args)
	}
	switch t := rec.(type) {
	case nil:
		if key, nested := g.nestedTypeDeclKey(written); nested {
			return key, written, true
		}
		return "non-local type", written, true
	case *analysis.TypeParam_:
		return "generic type", written, true
	case *analysis.InterfaceType:
		if d, program := g.solvedIface(t.Origin, t.Name); program {
			if d.lowerable {
				return "", "", false
			}
			return ifaceReason(d)
		}
		return "stdlib interface", qualifyOrigin(t.Origin, name), true
	}
	origin := originOfType(rec)
	if tpl, _, isTemplate := g.genericTemplateOf(rec); isTemplate {
		switch {
		case g.otherFileOrigin(origin):
			return "generic type", written, true
		case tpl.why != "":
			// A program's generic type whose instantiations are WALLED,
			// reporting the wall's own reason rather than the family's
			// name, so a struct LITERAL and a SIGNATURE mentioning the
			// same instantiation report the same reason. See
			// generictype.go.
			return tpl.why, tpl.whyDetail, true
		}
		// An ADMITTED template whose instantiation still did not build:
		// the INSTANCE's own recorded reason first (genericimpl.go), then
		// the argument.
		if gt := writtenGeneric(te); gt != nil {
			if c, d, bad := g.genericInstRefusal(gt, tpl); bad {
				return c, d, true
			}
		}
		return g.partRefusal(args)
	}
	if d := g.nominalDef(rec); d != nil {
		if d.lowerable {
			return "", "", false
		}
		return unloweredReason(d)
	}
	switch {
	case analysis.IsBodyOrigin(origin):
		if key, nested := g.nestedTypeDeclKey(written); nested {
			return key, written, true
		}
		return "non-local type", written, true
	case len(args) > 0:
		return "generic type", written, true
	case len(typeParamDefsOf(rec)) > 0:
		return "generic type", qualifyOrigin(origin, name), true
	}
	return "stdlib type", qualifyOrigin(origin, name), true
}

// representedContainer reports whether te, written with type arguments, is
// a container the builder represents whatever its arguments, by the type
// the checker recorded for it, rec: a List, a Map, an Iter, or a prelude
// enum (preludeAt).
func (g *gen) representedContainer(te ast.TypeExpr, rec analysis.Type) bool {
	switch t := rec.(type) {
	case *analysis.ListType, *analysis.MapType:
		return true
	case *analysis.InterfaceType:
		return t.Origin == stdIter.Module && t.Name == stdIter.Name
	}
	gt := writtenGeneric(te)
	if gt == nil {
		return false
	}
	_, _, isPrelude := g.preludeAt(gt.Line, gt.Col)
	return isPrelude
}

// writtenTypeName is the name a refusal gives te, a type written by its
// name whose type the checker recorded as rec, without arguments: the
// bare name it is written with (`Box` of `Box<Int>`), and for a qualified
// one the name its declaration has (`Shape` of `shapes.Shape`,
// `Toml.Value` of `Toml.Value`).
func writtenTypeName(te ast.TypeExpr, rec analysis.Type) string {
	switch t := te.(type) {
	case *ast.QualifiedType:
		switch ty := rec.(type) {
		case *analysis.StructType:
			return ty.Name
		case *analysis.EnumType:
			return ty.Name
		case *analysis.DistinctType:
			return ty.Name
		case *analysis.InterfaceType:
			return ty.Name
		}
		return writtenTypeName(t.Member, rec)
	case *ast.GenericType:
		return t.Name
	case *ast.SimpleType:
		return t.Name
	}
	return typeText(te)
}

// writtenTypeArgs is the type arguments te is written with, nil for none.
func writtenTypeArgs(te ast.TypeExpr) []ast.TypeExpr {
	if gt := writtenGeneric(te); gt != nil {
		return gt.Params
	}
	return nil
}

// writtenGeneric is te's `Name<...>`, itself or a qualified type's member,
// or nil.
func writtenGeneric(te ast.TypeExpr) *ast.GenericType {
	if qt, qualified := te.(*ast.QualifiedType); qualified {
		te = qt.Member
	}
	gt, _ := te.(*ast.GenericType)
	return gt
}

// writtenTypeSymbol is the declaration the checker resolved te's name to,
// or nil.
func (g *gen) writtenTypeSymbol(te ast.TypeExpr) *analysis.Symbol {
	if qt, qualified := te.(*ast.QualifiedType); qualified {
		te = qt.Member
	}
	switch t := te.(type) {
	case *ast.SimpleType:
		return g.checkedRef(t.Line, t.Col)
	case *ast.GenericType:
		return g.checkedRef(t.Line, t.Col)
	}
	return nil
}

// typeParamDefsOf is the type parameters of the declaration the nominal
// type t names, nil for none.
func typeParamDefsOf(t analysis.Type) []*analysis.TypeParam_ {
	switch ty := t.(type) {
	case *analysis.StructType:
		return ty.TypeParamDefs
	case *analysis.EnumType:
		return ty.TypeParamDefs
	case *analysis.DistinctType:
		return ty.TypeParamDefs
	}
	return nil
}

// declRefusal is the reason a DECLARATION was not lowered, followed through
// the blocked-by chain to whichever declaration actually reports a gap.
//
// This keeps a cascade from inventing a reason of its own. The only subtle
// part is that a refusal LIST and a mirror's single `why` are the same
// question asked of two storage shapes. A MIRROR carries one reason and no
// list, and it may be a
// reason its declaring file does not have — a type cycle — so its
// own reason wins; a declaration this gen makes carries the list and no
// `why`. Reading the list is what keeps a use site of a `Config` whose
// declaration said `non-scalar field type | Config.context: Context` from
// reporting a vaguer `unlowered type`. See cascadereason_test.go.
//
// The walk is bounded by a visited set rather than a depth cap because
// settleLowerable relaxes over a graph that MAY CONTAIN CYCLES (its own
// comment says so, and `enum Chain { End; Link Chain }` builds one). A cycle
// in which no member records a reason of its own is unreachable — a cycle can
// only be retracted through a member that was refused — but it is bounded
// here rather than argued about, because the cost is one map on a path that
// runs only after a refusal has already been decided.
//
// Reports whether it named anything. False means d is unlowerable for a reason
// nothing recorded, which is the one case that still deserves a placeholder.
func declRefusal(d *typeDef) (construct, detail string, ok bool) {
	from := d
	for seen := map[*typeDef]bool{}; d != nil && !seen[d]; d = d.blocker {
		seen[d] = true
		switch {
		case d.why != "":
			construct, detail = d.why, d.whyDetail
		case len(d.refusals) > 0:
			construct, detail = d.refusals[0].construct, d.refusals[0].detail
		default:
			continue
		}
		if d != from {
			// The root alone would not say which of the file's declarations
			// the use site actually named. `(blocks X)` is the mirror of the
			// `(blocked by Y)` it replaces, pointing at the reason rather than
			// at the next link.
			detail += " (blocks " + from.nomi + ")"
		}
		return construct, detail, true
	}
	return "", "", false
}

// unloweredReason is a declared type's own recorded refusal.
//
// A construction site applies the same rule; the two are
// deliberately the same answer, so a type refused at its literal and at a
// signature reports one key rather than two.
func unloweredReason(d *typeDef) (string, string, bool) {
	if construct, detail, ok := declRefusal(d); ok {
		return construct, detail, true
	}
	detail := d.nomi
	if d.foreign != "" {
		detail = d.foreign + "." + d.nomi
	}
	return "unlowered type", detail, true
}

// mirrorWhy is declRefusal's CONSTRUCT half, for foreign.go's importWhy
// channel — which carries a reason with no detail, because the detail there is
// composed by whichever caller knows the position.
//
// It exists because that channel could carry an EMPTY reason. Two sites
// propagated `d.why` from a mirror straight into importWhy, and a mirror
// built from a declaration that records a refusal LIST has no `why` at all, so
// the empty string travelled and surfaced as a refusal with no name on it.
// Routing through declRefusal makes the channel total: every answer is a
// construct somebody could act on, and `unlowered type` is what remains when
// genuinely nothing was recorded.
func mirrorWhy(d *typeDef) string {
	if construct, _, ok := declRefusal(d); ok {
		return construct
	}
	return "unlowered type"
}

// ifaceReason is unloweredReason for an interface existential.
//
// A separate key from `unlowered type`: an interface has no layout, no
// variant tags and no type mirror, so it is a different amount of work and
// must not be counted with the types.
//
// The DETAIL is made total here, which is the same argument mirrorWhy makes for
// the construct half one function down: a channel that can carry an empty
// string will. `blockedIface` composes a detail for every mirror, and
// `declareIfaces` composes none at all, so without this `interface Holder<T>`
// would report `generic interface` with NO OPERAND. `recordedTypeRefusal` reaches
// this for the annotation `h: Holder<Int>`.
//
// A bare `d.nomi` rather than a qualified name: a mirror's own `whyDetail` is
// already file-qualified and only a LOCAL declaration reaches the fallback, so
// there is no second file for the name to be ambiguous between.
func ifaceReason(d *ifaceDef) (string, string, bool) {
	if d.why != "" {
		if d.whyDetail != "" {
			return d.why, d.whyDetail, true
		}
		return d.why, d.nomi, true
	}
	return "unlowered interface", d.nomi, true
}

// nestedTypeDeclKey is the tally key a name's OWN DECLARATION carries, when the
// only thing declaring that name in this file is a declaration nested inside a
// block. It reports whether there was one.
//
// # Why the reference must not get a key of its own
//
// `struct Point` inside a `test` body is refused at ITS position under `struct
// declaration`, and the `Point{x: 3, y: 4}` six lines down is not a second gap
// — lowering the declaration is the whole of what makes the reference work.
// Reported as `non-local type` it would point at a cause nobody lowering it
// could help, the failure foreign.go's blockedMirror and siblings.go's
// stdFileQualifier are both written against.
// 01-foundations/block_scoping/block_scoping_test.nomi has this shape: its
// block-local declarations report `struct declaration`, `enum declaration`,
// `type declaration` and `type alias`.
//
// # The answer has to be POSITIVE
//
// A miss in the module scope means two OPPOSITE things — a name nothing
// declares, which `non-local type` is exactly right for, and a name only a
// nested declaration declares — and a failed lookup cannot tell them apart. So
// the declaration is FOUND, in this file's own tree, before its key is
// borrowed.
//
// # Two spellings, one name
//
// Separate blocks are separate scopes, so `struct Point` in one `test` and
// `enum Point` in another are both legal in one file. The collapse guard is a
// DUPLICATE INSERTION WITH A DIFFERENT KEY rather than a count: the same key
// twice is one unambiguous answer and stays, two different keys is no answer
// and falls back to `non-local type` rather than picking one. Marked with ""
// and sticky, so a third insertion cannot un-mark it.
//
// A TOP-LEVEL declaration is removed outright. One has a def in g.types, so it
// cannot reach any caller of this; deleting it anyway keeps the answer from
// depending on that being true elsewhere.
//
// The switch in typeDeclName is hand-written where the traversal is not, and a
// sixth type-declaring node arriving would be missed by it. That direction is
// fail-safe: the reference keeps `non-local type`, which is the answer this
// function exists to improve rather than one it would make wrong.
func (g *gen) nestedTypeDeclKey(name string) (string, bool) {
	if g.nestedTypeKeys == nil {
		g.nestedTypeKeys = map[string]string{}
		for _, n := range g.nodes {
			walkNames(n, g.nestedTypeKeys, pickTypeDeclKey)
		}
		for _, n := range g.nodes {
			if own := typeDeclName(n); own != "" {
				delete(g.nestedTypeKeys, own)
			}
		}
	}
	key := g.nestedTypeKeys[name]
	return key, key != ""
}

// pickTypeDeclKey records a type declaration's own tally key under the name it
// declares, and NEVER stops the walk: a declaration nested in a block is
// reached only by descending past everything that contains it.
func pickTypeDeclKey(n ast.Node, out map[string]string) bool {
	name := typeDeclName(n)
	if name == "" {
		return false
	}
	key := constructName(n)
	if prior, seen := out[name]; !seen {
		out[name] = key
	} else if prior != key {
		out[name] = ""
	}
	return false
}

// typeDeclName is the name a declaration introduces for a TYPE, or "" for a
// node that introduces none.
func typeDeclName(n ast.Node) string {
	switch d := n.(type) {
	case *ast.StructDef:
		return d.Name
	case *ast.EnumDef:
		return d.Name
	case *ast.TypeDef:
		return d.Name
	case *ast.TypeAlias:
		return d.Name
	case *ast.InterfaceDef:
		return d.Name
	}
	return ""
}

// originOfType is a nominal type's declaring-file build key, or "".
//
// Read off the analyzer's own type rather than off the symbol's SourceFile,
// because Origin IS the nominal identity and a path is not.
func originOfType(t analysis.Type) string {
	switch ty := t.(type) {
	case *analysis.StructType:
		return ty.Origin
	case *analysis.EnumType:
		return ty.Origin
	case *analysis.DistinctType:
		return ty.Origin
	case *analysis.InterfaceType:
		return ty.Origin
	}
	return ""
}

// qualifyOrigin renders `std/compiler.Diagnostic` when the origin is known.
//
// The origin is in the DETAIL rather than in the key, for the reason the
// tally's own rule gives: a key must not embed an identifier that varies per
// program.
func qualifyOrigin(origin, name string) string {
	if origin == "" || strings.HasPrefix(name, origin+".") {
		return name
	}
	return origin + "." + name
}

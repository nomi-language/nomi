package irbuild

import (
	"reflect"
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
// already answered kindInvalid, so it costs nothing on a file that lowers. The
// duplication is the real risk, and it is pinned rather than trusted:
// TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses drives both over the
// same annotations and fails if they ever disagree about which are outside the
// subset.

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
// whole-file import and `Conn` is a type the imported file declares. See
// modulequaltype.go for why that spelling needs its own path beside `import
// sqlite/sqlite.{Conn}` plus a bare `Conn`.
//
// The declaration-name reading is asked FIRST: the second arm runs only where
// the first found nothing. The two cannot both answer for one spelling anyway —
// a qualifier is bound to a `SymbolModule` and a namespaced declaration is not
// — but the order is fixed rather than left to that, because it is the same
// local-wins precedence namedType states for a name a file both declares and
// imports.
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
		// A nil member, or `Probe.Reading<T>` — a generic instantiation of a
		// namespaced type. Its own gap, refused under `generic type` by
		// typeRefusal below.
		return nil, false
	}
	if d, found := g.namedType(qualifier + "." + name); found {
		return d, true
	}
	return g.moduleQualifiedType(qualifier, name)
}

// rejectTypeAnnotation reports the narrowest reason te is outside the subset,
// at `at`, with `subject` prefixed to the detail so the position is findable.
//
// Reports whether it named anything. A false answer means te IS representable
// and the caller asked the wrong question; callers treat that as a decline
// rather than emitting a fallback key, because a fallback key would be exactly
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
// It walks the same four naming forms typeOf does, in the same order, and
// descends into a composite to find the LEAF that failed — `List<Diagnostic>`
// reports `Diagnostic`, not `List<Diagnostic>`, because the list is fine and
// the element is the work.
func (g *gen) typeRefusal(te ast.TypeExpr) (construct, detail string, ok bool) {
	switch t := te.(type) {
	case *ast.FuncType:
		// One node, two types: a nil Return is a tuple and an arrow is a
		// function. Both are structural and both fail only through a part.
		for _, p := range t.Params {
			if c, d, bad := g.typeRefusal(p); bad {
				return c, d, true
			}
		}
		if t.Return != nil {
			if c, d, bad := g.typeRefusal(t.Return); bad {
				return c, d, true
			}
		}
		return "", "", false

	case *ast.GenericType:
		return g.genericRefusal(t)

	case *ast.QualifiedType:
		if t.Member == nil {
			return "namespaced type name", te.TypeString(), true
		}
		if gt, isGeneric := t.Member.(*ast.GenericType); isGeneric {
			// `shapes.Holder<Int>` — typeOf resolves this through
			// genericTemplateNamed, so this walk has to agree or
			// TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses fires.
			// The reason for a MISS is the generic family's own, reached by
			// the joined name so the refusal reads as the instantiation the
			// programmer wrote.
			// kindInvalid: reports — an instantiation typeOf declines is named below.
			if k, built := g.userGenericTypeOf(t.Module+"."+gt.Name, gt.Params); built && k != kindInvalid {
				return "", "", false
			}
			return "generic type", te.TypeString(), true
		}
		if d, found := g.namespacedType(t); found {
			if d.lowerable {
				return "", "", false
			}
			return unloweredReason(d)
		}
		// A dotted name nothing in this program declares. It is the same
		// question a bare unknown name asks, one segment longer, so it routes
		// through the same resolution rather than getting a key of its own —
		// a `json.Json.DecodeError` annotation is a STDLIB type and must not
		// read as a namespacing gap.
		return g.unresolvedRefusal(te.TypeString())

	case *ast.AnonStructType:
		// A RECORD lowers (anonstruct.go), so it fails only through a FIELD —
		// the same shape the FuncType arm above has, and reported the same
		// way: `{items: Map<Bad, Int>}` names the Bad, because the record is
		// fine and the field is the work.
		names := make([]string, len(t.Fields))
		for i, f := range t.Fields {
			names[i] = f.Name
			if c, d, bad := g.typeRefusal(f.TypeAnnotation); bad {
				return c, d, true
			}
		}
		if len(t.Fields) == 0 {
			// A record with no fields. The one remaining shape with no
			// representation, and it keeps the key rather than falling
			// through to a reason about a field that does not exist.
			return "anonymous struct type", te.TypeString(), true
		}
		return "", "", false

	case *ast.SelfType:
		// `self` outside an interface body reaches nothing; inside one the
		// interface machinery substitutes it before typeOf sees it. Named
		// rather than silently dropped so a future path that does reach here
		// shows up in the tally instead of vanishing.
		return "self type", te.TypeString(), true

	case *ast.SimpleType:
		return g.simpleRefusal(t)
	}
	return "type reference", typeText(te), true
}

// genericRefusal names why a `Name<...>` annotation is outside the subset.
func (g *gen) genericRefusal(t *ast.GenericType) (string, string, bool) {
	// The three generic spellings the builder represents. Asked in typeOf's
	// own order so a `Maybe<Bad>` reports `Bad` rather than being read as an
	// unrepresentable container.
	if _, _, isPrelude := g.preludeAt(t.Line, t.Col); isPrelude {
		return g.genericArgRefusal(t)
	}
	if t.Name == "Map" && len(t.Params) == 2 {
		return g.genericArgRefusal(t)
	}
	if (t.Name == "List" || t.Name == "Iter") && len(t.Params) == 1 {
		// `Iter<T>` is structuralTypeOf's lowered sequence, so it too fails
		// only through its element.
		return g.genericArgRefusal(t)
	}
	if tpl := g.genericTemplates[t.Name]; tpl != nil && tpl.why != "" {
		// A USER generic struct whose instantiations are WALLED, reporting the
		// wall's own reason rather than the family's name, so a struct LITERAL
		// (genericStructLit reads tpl.why) and a SIGNATURE mentioning the same
		// instantiation report the same reason. See generictype.go.
		return tpl.why, tpl.whyDetail, true
	}
	if tpl := g.genericTemplates[t.Name]; tpl != nil {
		// An ADMITTED template whose instantiation still did not build. Asked in
		// two steps, and the ORDER is the point: the INSTANCE's own recorded
		// reason first, then the argument.
		//
		// The instance arm exists because per-instance impl registration created
		// a case neither of the other two answers — every argument resolves, the
		// template is admitted, and the instance declined anyway because one of
		// its impl blocks did. Without it genericArgRefusal returns no-reason and
		// the use site reports its fallback. See genericimpl.go.
		if c, d, bad := g.genericInstRefusal(t, tpl); bad {
			return c, d, true
		}
		// The ARGUMENT is the answer: `Box<Vector<Int>>` refuses at
		// `Vector<Int>`, exactly as the prelude, Map and List arms above name
		// their argument rather than their container.
		return g.genericArgRefusal(t)
	}
	// Everything else instantiates a GENERIC DECLARATION — `Fragment<String>`,
	// `Pair<E>`, `Channel<Int>`, `Set<Int>` — that no arm above represents, so
	// it lands on the key the generic family uses rather than a new one.
	return "generic type", t.TypeString(), true
}

// declRefusal is the reason a DECLARATION was not lowered, followed through
// the blocked-by chain to whichever declaration actually reports a gap.
//
// This keeps a cascade from inventing a reason of its own. The only subtle
// part is that a refusal LIST and a mirror's single `why` are the same
// question asked of two storage shapes. A MIRROR carries one reason and no
// list, and it may be a
// reason its declaring file does not have — a type cycle — so its
// own reason wins; a declaration this package emits carries the list and no
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
// Same rule rejectUnlowered applies at a construction site; the two are
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
// A separate key from `unlowered type` for the reason foreignIfaceOwner is a
// separate accessor from foreignOwner: an interface has no layout, no variant
// tags and no mirror, so it is a different amount of work and must not be
// counted with the types.
//
// The DETAIL is made total here, which is the same argument mirrorWhy makes for
// the construct half one function down: a channel that can carry an empty
// string will. `blockedIface` composes a detail for every mirror, and
// `declareIfaces` composes none at all, so without this `interface Holder<T>`
// would report `generic interface` with NO OPERAND. `simpleRefusal` reaches
// this for the annotation `h: Holder<Int>` and projectRefusal for the
// inferred position; TestProjectReason_NamesAReasonForExactlyWhatProjectRefuses
// asserts that a reason names a construct AND an operand.
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

// genericArgRefusal finds the failing ARGUMENT of a container the builder can
// represent, so `List<Diagnostic>` is reported as `Diagnostic`.
func (g *gen) genericArgRefusal(t *ast.GenericType) (string, string, bool) {
	for _, p := range t.Params {
		if c, d, bad := g.typeRefusal(p); bad {
			return c, d, true
		}
	}
	// Every argument resolved, so the container did too and the caller should
	// not have asked. Reported as no-reason rather than guessed at.
	return "", "", false
}

// simpleRefusal names why a bare type name is outside the subset.
//
// The order mirrors typeOf's exactly, because a name that typeOf resolves at
// step 3 must not be classified here by step 5's rule.
func (g *gen) simpleRefusal(t *ast.SimpleType) (string, string, bool) {
	switch t.Name {
	case "Int", "Float", "String", "Bool", "Unit":
		return "", "", false
	}
	if d, found := g.types[t.Name]; found {
		if d.lowerable {
			return "", "", false
		}
		return unloweredReason(d)
	}
	if _, isOpaque := g.opaqueNamed(t.Name); isOpaque {
		return "", "", false
	}
	if _, isStdEnum := g.stdEnumNamed(t.Name); isStdEnum {
		return "", "", false
	}
	if _, isStdStruct := g.stdStructNamed(t.Name); isStdStruct {
		return "", "", false
	}
	if _, isStdHost := g.stdHostNamed(t.Name); isStdHost {
		return "", "", false
	}
	if _, isMarker := g.stdMarkerNamed(t.Name); isMarker {
		return "", "", false
	}
	if d, found := g.ifaces[t.Name]; found {
		if d.lowerable {
			return "", "", false
		}
		return ifaceReason(d)
	}
	if d, isForeign := g.foreignIface(t.Name); isForeign {
		if d.lowerable {
			return "", "", false
		}
		return ifaceReason(d)
	}
	// Mirrors typeOf's stdlib-interface arm, in the same position relative to
	// the sibling-file lookup. An anchored stdlib interface is representable,
	// so there is no reason to name; an UNANCHORED one falls through to
	// unresolvedRefusal below and keeps `stdlib interface`. That fall-through
	// is what makes the refusal EVALUATE its precondition instead of citing
	// it: the key is reported only after the anchor machinery has been
	// asked and answered no. See stdiface.go.
	if _, isStd := g.stdIfaceNamed(t.Name); isStd {
		return "", "", false
	}
	if d, isForeign := g.foreignType(t.Name); isForeign {
		if d.lowerable {
			return "", "", false
		}
		return unloweredReason(d)
	}
	if g.isTypeParamRef(t) {
		// A type PARAMETER the enclosing generic header introduced — the
		// receiver's `T` in `impl Ranked for Holder<T>`, whose method returns
		// it. Nothing declares it, so the resolution below would call it
		// `non-local type` ("no such type here"), which is the wrong claim
		// about the right position: it exists, it is a type variable, and it
		// needs the type-parameter dictionary.
		return "generic type", t.Name, true
	}
	return g.unresolvedRefusal(t.Name)
}

// isTypeParamRef reports whether this name is a type VARIABLE some declaration
// in the module introduced.
//
// A NAME set, gathered once by reflection over every `TypeParams []ast.TypeParam`
// field any declaration carries. Reflection for the same reason childNodes uses
// it: seven AST node types carry that field, a switch over them would
// fail SILENTLY when an eighth appears, and the failure would be a type
// variable quietly reported as "no such type here".
//
// A name set is safe HERE and would not be safe at a use site, because
// simpleRefusal has already asked every table that could hold a real type of
// this name — local, opaque, interface, foreign interface, foreign type — and
// found nothing. What is left cannot be a type that shadows the parameter.
func (g *gen) isTypeParamRef(t *ast.SimpleType) bool {
	if g.typeParamNames == nil {
		g.typeParamNames = map[string]bool{}
		collectTypeParamNames(reflect.ValueOf(g.nodes), g.typeParamNames)
	}
	return g.typeParamNames[t.Name]
}

var typeParamType = reflect.TypeOf(ast.TypeParam{})

func collectTypeParamNames(v reflect.Value, out map[string]bool) {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !v.IsNil() {
			collectTypeParamNames(v.Elem(), out)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			collectTypeParamNames(v.Index(i), out)
		}
	case reflect.Struct:
		if v.Type() == typeParamType {
			out[v.Interface().(ast.TypeParam).Name] = true
			return
		}
		if v.Type() == triviaCarrierType {
			return
		}
		tp := v.Type()
		for i := range tp.NumField() {
			if tp.Field(i).IsExported() {
				collectTypeParamNames(v.Field(i), out)
			}
		}
	}
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
// borrowed. Same rule foreign.go's foreignNoEquatableImpl states for a mirror.
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

// unresolvedRefusal classifies a type name nothing in the PROGRAM declares, by
// following it through the analyzer to the declaration that owns it.
//
// Three populations, and they are three different amounts of work — which is
// the whole reason they get three keys rather than sharing `non-local type`:
//
//   - a STDLIB type. `Diagnostic`, `Ordering`, `Json`, `AssertionFailure`. The
//     builder has no table for a stdlib type at all: irbuild.Analyze carries only
//     the user files (irbuild.go's own comment says so), and stdlib reaches the
//     builder through stdlib.go's FUNCTION index, which registers no type.
//     It gets a name that says what it needs.
//   - a stdlib INTERFACE. Same miss, different work: an existential over one
//     needs the interface's method shape and a dispatch table living in an
//     unlowered module, not a data layout.
//   - a GENERIC declaration, stdlib or otherwise. `Fragment<T>`, `Set<T>`,
//     under the key the generic family uses.
//
// Anything that resolves to nothing at all keeps `non-local type`, which is
// what that key means: no such type here.
//
// Reached from a CONSTRUCTION site as well as from an annotation, and that is
// the point rather than reuse: `Project{...}` and `p: Project` are one
// obstacle and report it under one name.
func (g *gen) unresolvedRefusal(name string) (string, string, bool) {
	sym := resolvedTypeSymbol(g.fa, name)
	if sym == nil || sym.Node == nil {
		if key, nested := g.nestedTypeDeclKey(name); nested {
			return key, name, true
		}
		return "non-local type", name, true
	}
	switch sym.Node.(type) {
	case *ast.InterfaceDef:
		return "stdlib interface", qualifyOrigin(originOfType(sym.Type), name), true
	case *ast.TypeAlias:
		// A TRANSPARENT synonym, which RESOLVES (typealias.go) — so
		// reaching here means the alias's TARGET is what this position cannot
		// represent, and the target's own reason is the one a programmer can
		// act on. Reporting `type alias` here instead would say only that a
		// synonym was involved, which is true of every use of every alias and
		// distinguishes nothing.
		//
		// THE KEY THEREFORE COUNTS DECLARATIONS AND NOT USES. `type alias` is
		// raised once, by moduleAliasDecl at the declaration, and three
		// parameters annotated with one bad alias report the target's gap three
		// times under the target's key rather than the alias's four times under
		// the alias's. That is the same split blockAliasDecl states for the
		// block-local form: the declaration is the position a programmer acts
		// on, and a use is a consequence of it.
		//
		// The alias name is carried into the detail, because a reader looking
		// at `p: Stream` needs to know that `Iter<Int>` is what `Stream` is.
		if d, ok := sym.Node.(*ast.TypeAlias); ok && d.TargetTypeExpr != nil {
			key, detail, reported := g.typeRefusal(d.TargetTypeExpr)
			if !reported {
				// THE TARGET IS FINE, SO THE ALIAS IS FINE, and this function
				// must say so rather than name a reason. `typeOf` lowers
				// `Handler` to `(String) -> String`; a reason returned here for
				// a type that HAS none is not merely noise — it fires the
				// moment any caller consults the reason before the kind. That
				// is exactly what
				// TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses exists
				// to catch, and it caught this one.
				return "", "", false
			}
			return key, name + " is " + detail, true
		}
		// A BOUND alias: it names a conjunction of interfaces rather than a
		// type, so there is no target to point at and the alias keeps the key
		// its declaration uses.
		return "type alias", name, true
	}
	if isGenericDecl(sym.Node) {
		return "generic type", qualifyOrigin(originOfType(sym.Type), name), true
	}
	return "stdlib type", qualifyOrigin(originOfType(sym.Type), name), true
}

// isGenericDecl reports whether a declaration carries type parameters, so a
// mention of `Fragment` sizes with the generic family rather than with the
// stdlib data types an ordinary mirror would cover.
func isGenericDecl(n ast.Node) bool {
	switch d := n.(type) {
	case *ast.StructDef:
		return len(d.TypeParams) > 0
	case *ast.EnumDef:
		return len(d.TypeParams) > 0
	case *ast.TypeDef:
		return false
	}
	return false
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

// --- the inferred channel ---------------------------------------------------

// preludeNamed is the prelude anchor for a name, after the table is loaded.
// A thin accessor so this file never reads g.preludeByName before
// loadPreludes has run — the ordering prelude.go's own callers observe.
func (g *gen) preludeNamed(name string) (*preludeAnchor, bool) {
	g.loadPreludes()
	a, ok := g.preludeByName[name]
	return a, ok
}

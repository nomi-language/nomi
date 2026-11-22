package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// An interface named through a WHOLE-FILE import's qualifier
// (`store.Store`, `impl other.Shape for Sq`), and the keys the impls of an
// interface are filed under.
//
// A type named through a qualifier needs nothing here: an annotation that
// spells `lib.Plain` has the type the checker recorded for it, which typeOf
// projects to the def the bare imported `Plain` reaches. An interface is not
// a type mirror, since a type mirror's layout and tags mean nothing for one.
// It resolves instead, by the interface the checker recorded, to the
// interface mirror the bare imported name reaches (solvedIface), so
// `db: sql.Conn` and `db: Conn` are one existential.

// moduleQualifiedIfaceOwner reports whether n, the owner of a reference
// `<qualifier>.<Name>.member`, names an interface a program module exports,
// through a qualifier this body does not bind as a value: by the
// declaration the checker resolved `Name` to.
func (bl *irScalarBuilder) moduleQualifiedIfaceOwner(n ast.Node) bool {
	fa, isFA := n.(*ast.FieldAccess)
	if !isFA || fa.Field == nil {
		return false
	}
	q, isIdent := ast.QualifierIdent(fa.Object)
	if !isIdent || bl.namesLocal(q) {
		return false
	}
	o := bl.g.ownerOfSymbol(bl.g.checkedRef(fa.Field.Line, fa.Field.Col))
	return o != nil && o.isIface
}

// ifaceKey is the key the impls of the interface the file whose analyzer
// origin is origin declares as name are filed under in implsByIface and
// instImpls, and the one every lookup of them reads: the interface's
// identity, (declaring file, name). It is the same in every gen, so two
// interfaces of one name (two files' `Show`, a file's own `ToJson` and
// std's) are two keys, and a gen reads another gen's impls under the key
// that gen files them under. Every key comes from here: implsKey for a def
// this gen holds, stdIfaceImplKey for a std interface the builder names,
// implIfaceKey for an impl block's header.
func ifaceKey(origin, name string) string {
	return origin + "." + name
}

// implsKey is ifaceKey for d, this gen's def of an interface: a local
// declaration, a sibling file's mirror, a std module's interface or an
// instance of any of them, which shares its template's key.
func (g *gen) implsKey(d *ifaceDef) string {
	if d == nil {
		return ""
	}
	return ifaceKey(g.ifaceOrigin(d), d.nomi)
}

// ifaceOrigin is the analyzer origin of the file that declares d.
func (g *gen) ifaceOrigin(d *ifaceDef) string {
	switch {
	case d.universal:
		return structIfaceModule
	case d.foreign != "":
		return d.foreign
	}
	if std := g.stdIfaceType(d); !std.IsZero() {
		return std.Module
	}
	if g.fa == nil {
		return analysis.OriginUnresolved
	}
	return g.fa.Origin
}

// stdIfaceImplKey is ifaceKey for std interface iface.
func stdIfaceImplKey(iface analysis.StdType) string {
	return ifaceKey(iface.Module, iface.Name)
}

// implIfaceKey is the key impl block d, whose header names name, is filed
// under (ifaceKey): its interface's def's, or, for an interface the builder
// holds no def of (std's generic `Iter` in a std module), the one the
// checker recorded for the block (rec). A derived Debug in a std module
// that imports no `Debug` is std's by construction, as resolveImplDef says.
func (g *gen) implIfaceKey(d *implDef, rec *analysis.ImplTypeArgs, name string) string {
	for _, def := range []*ifaceDef{d.iface, d.stdIface} {
		if def != nil {
			return g.implsKey(def)
		}
	}
	if rec != nil {
		return ifaceKey(rec.Origin, rec.Interface)
	}
	if d.synth && name == "Debug" {
		return g.implsKey(g.stdDebugDef())
	}
	return ifaceKey(analysis.OriginUnresolved, name)
}

// qualifiedTypeParts splits an *ast.QualifiedType into a qualifier and a plain
// member name, declining anything else.
//
// One place, because typeOf and typeRefusal must agree about which spellings
// are resolvable at all — the property
// TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses pins.
func qualifiedTypeParts(t *ast.QualifiedType) (qualifier, name string, ok bool) {
	if t == nil || t.Member == nil {
		return "", "", false
	}
	st, simple := t.Member.(*ast.SimpleType)
	if !simple {
		return "", "", false
	}
	if mod, rest, dotted := strings.Cut(t.Module, "."); dotted && !ast.IsPublic(mod) {
		// A literal's prefix splits at its last segment
		// (`json.Json.ShapeError{...}` has the qualifier `json.Json`):
		// the module is the first, and the rest is the dotted name its
		// scope binds (`Json.ShapeError`).
		return mod, rest + "." + st.Name, true
	}
	return t.Module, st.Name, true
}

package analysis

import "github.com/nomi-language/nomi/internal/ast"

// recordExprType notes the type the checker gave node (FileAnalysis.ExprTypes).
// A node checked twice keeps the later type.
func (fa *FileAnalysis) recordExprType(node ast.Node, t Type) {
	if fa == nil || node == nil || t == nil {
		return
	}
	if fa.ExprTypes == nil {
		fa.ExprTypes = map[ast.Node]Type{}
	}
	fa.ExprTypes[node] = t
}

// recordTypeExprType notes the type te resolved to, nil when it did not
// resolve (FileAnalysis.TypeExprTypes). A type expression resolved twice
// keeps the later type, and a failure keeps an earlier type.
func (fa *FileAnalysis) recordTypeExprType(te ast.TypeExpr, t Type) {
	if fa == nil {
		return
	}
	if fa.TypeExprTypes == nil {
		fa.TypeExprTypes = map[ast.TypeExpr]Type{}
	}
	if t == nil {
		if _, done := fa.TypeExprTypes[te]; done {
			return
		}
	}
	fa.TypeExprTypes[te] = t
}

// recordExpectedType notes the type node was checked against
// (FileAnalysis.ExpectedTypes).
func (fa *FileAnalysis) recordExpectedType(node ast.Node, t Type) {
	if fa == nil || node == nil || t == nil {
		return
	}
	if fa.ExpectedTypes == nil {
		fa.ExpectedTypes = map[ast.Node]Type{}
	}
	fa.ExpectedTypes[node] = t
}

// recordPatternType notes the type of the value a pattern matches
// (FileAnalysis.PatternTypes).
func (fa *FileAnalysis) recordPatternType(node ast.Node, t Type) {
	if fa == nil || node == nil || t == nil {
		return
	}
	if fa.PatternTypes == nil {
		fa.PatternTypes = map[ast.Node]Type{}
	}
	fa.PatternTypes[node] = t
}

// recordPatternIrrefutable notes whether a pattern matches every value of
// the type PatternTypes records for it (FileAnalysis.IrrefutablePatterns).
func (fa *FileAnalysis) recordPatternIrrefutable(node ast.Node, always bool) {
	if fa == nil || node == nil {
		return
	}
	if fa.IrrefutablePatterns == nil {
		fa.IrrefutablePatterns = map[ast.Node]bool{}
	}
	fa.IrrefutablePatterns[node] = always
}

// PatternIrrefutable reports whether the checker recorded that pattern
// matches every value of the type it was checked against
// (IrrefutablePatterns). recorded is false for a pattern the checker did
// not check against a known type.
func (fa *FileAnalysis) PatternIrrefutable(pattern ast.Node) (always, recorded bool) {
	if fa == nil {
		return false, false
	}
	always, recorded = fa.IrrefutablePatterns[pattern]
	return always, recorded
}

// recordBindingFallback notes the payload type a binding's `else` may fall
// back to, nil for none (FileAnalysis.BindingFallbacks).
func (fa *FileAnalysis) recordBindingFallback(n *ast.PatternBinding, payload Type) {
	if fa == nil || n == nil {
		return
	}
	if fa.BindingFallbacks == nil {
		fa.BindingFallbacks = map[*ast.PatternBinding]Type{}
	}
	fa.BindingFallbacks[n] = payload
}

// BindingFallback is the type of the payload a path of n's `else` may fall
// back to (BindingFallbacks): nil when every path must leave. recorded is
// false for a binding the checker did not record, one with no `else`.
func (fa *FileAnalysis) BindingFallback(n *ast.PatternBinding) (payload Type, recorded bool) {
	if fa == nil {
		return nil, false
	}
	payload, recorded = fa.BindingFallbacks[n]
	return payload, recorded
}

// recordPatternVariant notes the variant head, a pattern's head, names, by
// its declared name in the enum PatternTypes records for the pattern
// (FileAnalysis.PatternVariants).
func (fa *FileAnalysis) recordPatternVariant(head ast.TypeExpr, variant string) {
	if fa == nil || head == nil || variant == "" {
		return
	}
	if fa.PatternVariants == nil {
		fa.PatternVariants = map[ast.TypeExpr]string{}
	}
	fa.PatternVariants[head] = variant
}

// ResolveTypeVar follows a type variable to the type it was solved to. Any
// other type comes back unchanged, as does an unsolved variable.
func ResolveTypeVar(t Type) Type { return resolveTypeVar(t) }

// TypeOwnerName is the name owner-qualified calls on a value of type t are
// spelled with: `String` for a String, `List` for a List<Int>, `Point` for a
// Point. "" for a type no owner names (a function, a tuple, a type variable).
func TypeOwnerName(t Type) string { return concreteTypeName(resolveTypeVar(t)) }

// ImplementsInterface reports whether a value of type t satisfies the
// interface iface according to the file's impl tables and the project's,
// the tables the checker consults, by the interface's identity: an impl of
// another interface of its name does not count (suppliesReceiver). The
// universal interfaces, names the prelude reserves, answer as the checker
// does: every type is Debug, every struct is Struct. A type parameter,
// interface value or unsolved variable answers false here, where the
// checker defers it to run time: a caller asking "which interfaces does
// this value have" wants only the ones it can name.
func ImplementsInterface(fa *FileAnalysis, t Type, iface *InterfaceType) bool {
	t = resolveTypeVar(t)
	if t == nil || fa == nil || iface == nil {
		return false
	}
	if dt := subtypeOf(t); dt != nil && dt.Inner != nil && iface.Name != "Debug" {
		// A subtype has its parent's impls (subtype.go).
		return ImplementsInterface(fa, DistinctInner(dt), iface)
	}
	switch iface.Name {
	case "Debug":
		return true
	case "Struct":
		return isStructShaped(t)
	case "Distinct":
		dt, ok := t.(*DistinctType)
		return ok && distinctInnerOf(dt) != nil
	}
	if it, ok := t.(*InterfaceType); ok {
		return sameNominalIdentity(it.Origin, it.Name, iface.Origin, iface.Name)
	}
	name := concreteTypeName(t)
	if name == "" || !fa.HasImpl(name, iface.Name) {
		return false
	}
	p := fa.Program()
	if p == nil {
		return true
	}
	return p.ImplIndex.suppliesReceiver(t, name, iface.Origin, iface.Name, p.unindexed())
}

// ImplOfInterface reports whether impl, an impl block's `fn` or `host fn`,
// is of the interface it, by identity: its block's header resolves to that
// declaration. An impl whose interface the program did not resolve, and an
// interface of no known origin, answer true.
func (p *Program) ImplOfInterface(impl ast.Node, it *InterfaceType) bool {
	if p == nil || p.ImplIndex == nil || it == nil || it.Origin == OriginUnresolved {
		return true
	}
	idx := p.ImplIndex
	origin := OriginUnresolved
	switch d := impl.(type) {
	case *ast.FuncDef:
		origin = idx.ImplIfaceOrigin[d]
	case *ast.ExternFunc:
		origin = externImplIfaceOrigin(p.File(idx.ImplExternFiles[d]), d, it.Name)
	}
	return sameNominalIdentity(origin, it.Name, it.Origin, it.Name)
}

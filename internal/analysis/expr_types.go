package analysis

import "github.com/nomi-language/nomi/internal/ast"

// recordExprType notes the type the checker gave node (FileAnalysis.ExprTypes).
// A node checked twice keeps the later type.
func (fa *FileAnalysis) recordExprType(node ast.Node, t Type) {
	if fa == nil || node == nil || t == nil || IsSynthesizedLine(node.LineNum()) {
		return
	}
	if fa.ExprTypes == nil {
		fa.ExprTypes = map[ast.Node]Type{}
	}
	fa.ExprTypes[node] = t
}

// recordExpectedType notes the type node was checked against
// (FileAnalysis.ExpectedTypes).
func (fa *FileAnalysis) recordExpectedType(node ast.Node, t Type) {
	if fa == nil || node == nil || t == nil || IsSynthesizedLine(node.LineNum()) {
		return
	}
	if fa.ExpectedTypes == nil {
		fa.ExpectedTypes = map[ast.Node]Type{}
	}
	fa.ExpectedTypes[node] = t
}

// ResolveTypeVar follows a type variable to the type it was solved to. Any
// other type comes back unchanged, as does an unsolved variable.
func ResolveTypeVar(t Type) Type { return resolveTypeVar(t) }

// TypeOwnerName is the name owner-qualified calls on a value of type t are
// spelled with: `String` for a String, `List` for a List<Int>, `Point` for a
// Point. "" for a type no owner names (a function, a tuple, a type variable).
func TypeOwnerName(t Type) string { return concreteTypeName(resolveTypeVar(t)) }

// ImplementsInterface reports whether a value of type t satisfies the
// interface named iface according to the file's impl tables and the
// project's, the tables the checker consults. The universal interfaces
// answer as the checker does: every type is Debug, every struct is Struct.
// A type parameter, interface value or unsolved variable answers false here,
// where the checker defers it to run time: a caller asking "which interfaces
// does this value have" wants only the ones it can name.
func ImplementsInterface(fa *FileAnalysis, t Type, iface string) bool {
	t = resolveTypeVar(t)
	if t == nil || fa == nil {
		return false
	}
	switch iface {
	case "Debug":
		return true
	case "Struct":
		return isStructShaped(t)
	}
	if it, ok := t.(*InterfaceType); ok {
		return it.Name == iface
	}
	name := concreteTypeName(t)
	if name == "" {
		return false
	}
	if fa.Impls[name][iface] {
		return true
	}
	return fa.ProjectImpls != nil && fa.ProjectImpls.Impls[name][iface]
}

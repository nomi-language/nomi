package analysis

import "github.com/nomi-language/nomi/internal/ast"

// What the language server asks of a default: the type a field or
// parameter would take from it with its annotation left out, and how the
// file writes a type. Both answer from a finished analysis and leave it as
// it was.

// DefaultOwner is the type whose items a top-level node declares, the
// owner a default in it is checked under (a bare same-type call resolves
// to that type's function), or "" for a node outside one.
func DefaultOwner(node ast.Node) string {
	switch n := node.(type) {
	case *ast.ImplBlock:
		return TypeExprBaseName(n.Receiver)
	case *ast.StructDef:
		return n.Name
	case *ast.EnumDef:
		return n.Name
	case *ast.TypeDef:
		return n.Name
	case *ast.ExternType:
		return n.Name
	}
	return ""
}

// DefaultTypeAlone is the type value, a field's or parameter's default,
// has when it is checked with no expected type: the type the field or
// parameter would take with its annotation left out (default_type.go).
// nodes are the file's top-level nodes and owner the default's
// DefaultOwner. ok is false when that check reports an error or leaves a
// part of the type unknown (`None`, `[]`, a `.Variant`), so no field or
// parameter could take its type from the default.
//
// The check runs on a copy of fa's body tables, so what it records is
// dropped and fa stays as readers of it see it.
func (fa *FileAnalysis) DefaultTypeAlone(nodes []ast.Node, owner string, value ast.Node) (t Type, ok bool) {
	if fa == nil || fa.ModuleScope == nil || value == nil {
		return nil, false
	}
	cp := *fa
	cp.FileBodies = fa.FileBodies.clone()
	c := newChecker(&cp, nodes)
	c.selfTypeName = owner
	c.annotationTrial = true
	site := &exitlessSite{what: "a default", why: "a default has no function to return from"}
	got := c.checkDefaultAlone(site, value)
	if len(c.errors) != 0 || !defaultDetermines(got) {
		return nil, false
	}
	return resolveTypeVar(got), true
}

// SameDefaultType reports whether written, a field's or parameter's
// annotated type, is exactly alone, the type its default has on its own
// (DefaultTypeAlone): one type, with no `embeds` coercion between them, and
// spelled alike.
func SameDefaultType(written, alone Type) bool {
	written, alone = resolveTypeVar(written), resolveTypeVar(alone)
	if written == nil || alone == nil || isNilType(written) || isNilType(alone) {
		return false
	}
	return TypesIdentical(written, alone) && written.String() == alone.String()
}

// SpellType is t as the file nodes belong to writes it, by the names its
// scope gives types: the text an annotation of t takes. ok is false for a
// type the file cannot name, or one with a part not known.
func (fa *FileAnalysis) SpellType(nodes []ast.Node, t Type) (string, bool) {
	if fa == nil || fa.ModuleScope == nil || !defaultDetermines(t) {
		return "", false
	}
	return newChecker(fa, nodes).spellType(t, 0)
}

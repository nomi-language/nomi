package analysis

import "github.com/nomi-language/nomi/internal/ast"

// recordUpdateFieldDecls points each label of the struct update n,
// `{..base, field: value}` over a value of the declared struct st, at the
// field's declaration, as a named literal's labels point (the builder's
// registerStructLitFieldRefs): hover, references and rename reach the
// update's labels from the field. A punned label's field goes in
// PunnedFieldLabels, beside the variable it reads. The label keeps the type
// the literal's check recorded there, which is the field's type at the
// head's type arguments.
func (c *checker) recordUpdateFieldDecls(n *ast.StructLit, st *StructType) {
	if c.fa == nil {
		return
	}
	structSym := c.structDeclSymbol(st)
	if structSym == nil {
		return
	}
	for structSym.Resolved != nil {
		structSym = structSym.Resolved
	}
	for _, f := range n.Fields {
		if f.Line == 0 || IsSynthesizedLine(f.Line) {
			continue
		}
		fieldSym, ok := structSym.Members[f.Name]
		if !ok || fieldSym.Kind != SymbolField {
			continue
		}
		pos := Pos{Line: f.Line, Col: f.Col}
		labels := c.fa.References
		if IsPunnedField(f) {
			labels = c.fa.PunnedFieldLabels
		}
		proxy := *fieldSym
		if recorded := labels[pos]; recorded != nil && recorded.Type != nil {
			proxy.Type = recorded.Type
		}
		if IsPunnedField(f) {
			c.fa.setPunnedFieldLabel(pos, &proxy)
			continue
		}
		c.fa.References[pos] = &proxy
	}
}

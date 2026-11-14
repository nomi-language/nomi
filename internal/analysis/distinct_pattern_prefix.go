package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A pattern over a distinct type may name the type before its brackets,
// braces or payload: `Items[a, b]`, `kinds.Kvs{"a" => v}`, `kinds.Pt((x, y))`.
// The name resolves as an annotation's does, through the module qualifier,
// the imports and the registry, and matches when it is the scrutinee's type
// by identity (declaring file, name): `other.Items` is not a `kinds.Items`.

// patternPrefixType is the type te, the name a pattern writes before its
// brackets, braces or payload, resolves to. A bare name the registry does
// not hold (a type declared in a block or an attached test) resolves
// through the symbol the builder recorded at it.
func (c *checker) patternPrefixType(te ast.TypeExpr) (Type, error) {
	resolved, err := ResolveTypeExpr(te, c.reg, c.fnTypeParams, c.fa)
	if err == nil {
		return resolved, nil
	}
	var pos Pos
	switch t := te.(type) {
	case *ast.SimpleType:
		pos = Pos{Line: t.Line, Col: t.Col}
	case *ast.GenericType:
		pos = Pos{Line: t.Line, Col: t.Col}
	default:
		return nil, err
	}
	if c.fa == nil {
		return nil, err
	}
	sym := c.fa.References[pos]
	for sym != nil && sym.Resolved != nil {
		sym = sym.Resolved
	}
	if sym == nil || sym.Type == nil {
		return nil, err
	}
	switch sym.Kind {
	case SymbolType, SymbolStruct, SymbolEnum, SymbolTypeAlias:
		return sym.Type, nil
	}
	return nil, err
}

// patternNamesDistinct reports whether te, a pattern's type prefix, names
// dt, the scrutinee's distinct type. A prefix that names no type, or
// another type, is reported at line:col.
func (c *checker) patternNamesDistinct(te ast.TypeExpr, dt *DistinctType, line, col int) bool {
	if dvt, ok := te.(*ast.DotVariantType); ok {
		// `.Items(x)` names no module: it names the scrutinee's type or
		// nothing.
		if dvt.Name == dt.Name {
			return true
		}
		c.addError(line, col, fmt.Sprintf("pattern names %s, but the value is %s", dvt.Name, withArticle(dt.Name)))
		return false
	}
	named, err := c.patternPrefixType(te)
	if err != nil {
		if terr, ok := err.(TypeError); ok {
			c.report(terr)
		} else {
			c.addError(line, col, fmt.Sprintf("unknown type %q", te.TypeString()))
		}
		return false
	}
	if c.typeNamesDistinct(named, dt) {
		return true
	}
	var spelled any = te.TypeString()
	if other, ok := resolveTypeVar(named).(*DistinctType); ok && other.Name == dt.Name {
		// Two declarations of one name: rendered together, each is
		// qualified by its module.
		spelled = other
	}
	both := c.typef("%s\x00%s", spelled, dt)
	pattern, value, _ := strings.Cut(both, "\x00")
	c.addError(line, col, fmt.Sprintf("pattern names %s, but the value is %s", pattern, withArticle(value)))
	return false
}

// typeNamesDistinct reports whether named, a pattern prefix's type, is dt.
func (c *checker) typeNamesDistinct(named Type, dt *DistinctType) bool {
	other, ok := resolveTypeVar(named).(*DistinctType)
	return ok && sameNominalIdentity(other.Origin, other.Name, dt.Origin, dt.Name)
}

// patternMayNameDistinct is patternNamesDistinct without reporting, for a
// pass that runs after the checker reported any prefix that does not
// resolve: only a prefix that resolves to another type is not dt.
func (c *checker) patternMayNameDistinct(te ast.TypeExpr, dt *DistinctType) bool {
	if dvt, ok := te.(*ast.DotVariantType); ok {
		return dvt.Name == dt.Name
	}
	named, err := c.patternPrefixType(te)
	return err != nil || c.typeNamesDistinct(named, dt)
}

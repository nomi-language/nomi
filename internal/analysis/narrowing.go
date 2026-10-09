package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Type narrowing (spec §8 *Type Narrowing*): inside a `case` arm, or the
// first branch of an `if Pattern = value`, whose pattern matches an `embeds`
// variant, a plain name matched there holds the embedded type. In
//
//	case value {
//	    .Circle{radius} -> area(value)
//	    ...
//	}
//
// `value` is a Circle in the first arm. The checker records that type for
// each read of the name (FileAnalysis.ExprTypes), and the IR builder reads
// the variant's payload there.

// narrowingFor is the name scrutinee reads and the type it is narrowed to
// in an arm whose pattern is pattern, or nil when the arm narrows nothing:
// the scrutinee is not a parameter or local binding read by name, its type
// is not an enum, or the pattern does not match exactly one `embeds`
// variant of it whose type is a struct or a distinct type. An `as` around
// the pattern does not change the variant it matches.
func (c *checker) narrowingFor(scrutinee ast.Node, scrutineeTy Type, pattern ast.Node) (*Symbol, Type) {
	id, ok := ungroupExpr(scrutinee).(*ast.Ident)
	if !ok || pattern == nil {
		return nil, nil
	}
	sym := c.fa.References[Pos{Line: id.Line, Col: id.Col}]
	if sym == nil || sym.Resolved != nil || (sym.Kind != SymbolParam && sym.Kind != SymbolBinding) {
		return nil, nil
	}
	et, ok := resolveTypeVar(scrutineeTy).(*EnumType)
	if !ok {
		return nil, nil
	}
	for _, v := range et.Variants {
		if v.Kind != VariantEmbedded || !narrowsTo(v.Embedded) {
			continue
		}
		if _, matches := c.variantPatternPayload(pattern, v.Name); !matches {
			continue
		}
		ty := v.Embedded
		if len(et.TypeParamDefs) > 0 && len(et.TypeArgs) == len(et.TypeParamDefs) {
			subs := make(map[*TypeParam_]Type, len(et.TypeParamDefs))
			for i, def := range et.TypeParamDefs {
				subs[def] = et.TypeArgs[i]
			}
			ty = Substitute(ty, subs)
		}
		return sym, ty
	}
	return nil, nil
}

// narrowsTo reports whether an `embeds` variant whose type is embedded
// narrows a name matched against it. Only an embedded struct or distinct type
// does (spec §8 *Type Narrowing*). Bool's `embeds True` and `embeds False`
// name host singletons: narrowing there would make `x` in a `True -> assert
// x` arm a `True`, which is not a Bool, so it would not fit `assert` or any
// other place a Bool is expected.
func narrowsTo(embedded Type) bool {
	switch embedded.(type) {
	case *StructType, *DistinctType:
		return true
	}
	return false
}

// withNarrowing runs check with sym narrowed to ty, when sym is not nil.
func (c *checker) withNarrowing(sym *Symbol, ty Type, check func()) {
	if sym == nil {
		check()
		return
	}
	prev, had := c.narrowed[sym]
	if c.narrowed == nil {
		c.narrowed = map[*Symbol]Type{}
	}
	c.narrowed[sym] = ty
	defer func() {
		if had {
			c.narrowed[sym] = prev
		} else {
			delete(c.narrowed, sym)
		}
	}()
	check()
}

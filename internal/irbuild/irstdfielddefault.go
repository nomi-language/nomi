package irbuild

// A stdlib struct's omitted field takes std's declared default. std states each default in a
// `stdFieldDefault` anchored against its declaration by shape, and `lit` is
// that same value in the form the IR builder constructs: a String constant,
// a payload-free variant of the field's enum (`None`), or an empty List or Map
// coerced to the field's kind.
// Every form is a constant, so where it lands among the written fields'
// effects is unobservable.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

type stdDefaultForm uint8

const (
	stdDefaultNone stdDefaultForm = iota
	stdDefaultString
	stdDefaultVariant
	stdDefaultEmptyList
	stdDefaultEmptyMap
)

// stdDefaultLit is one std field default as a constant.
type stdDefaultLit struct {
	form stdDefaultForm
	// text is the String's value or the variant's name.
	text string
}

// stdFieldDefault builds f's std default at the literal at, of f's kind.
func (bl *irScalarBuilder) stdFieldDefault(at ast.Node, f *fieldDef) (ir.Temp, bool) {
	pos := bl.g.irNodePos(at)
	lit := f.stdDeflt.lit
	switch lit.form {
	case stdDefaultString:
		if f.k != kindString {
			return ir.NoTemp, false
		}
		c := ir.NewString(pos, bl.f.NewTemp(), lit.text)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindString})
		return c.Dst(), true
	case stdDefaultVariant:
		if f.k.tag != tagNamed || !irRetainedEnumKind(f.k.def) {
			return ir.NoTemp, false
		}
		v := f.k.def.variant(lit.text)
		if v == nil || len(v.payloads) != 0 {
			return ir.NoTemp, false
		}
		dst, k, _, ok := bl.variantValue(at, f.k.def, v, nil, true)
		return dst, ok && k == f.k
	case stdDefaultEmptyList:
		c := ir.NewEmptyList(pos, bl.f.NewTemp(), nil)
		bl.b.Append(c)
		// Typed as `lower` types an empty list literal it answers, so the
		// constant the coercion copies has a stored type.
		bl.side(c.Dst(), irScalarSide{k: kindEmptyList})
		v, k, ok := bl.coerceEmpty(at, c.Dst(), kindEmptyList, f.k)
		return v, ok && k == f.k
	case stdDefaultEmptyMap:
		n := ir.NewMakeMap(pos, bl.f.NewTemp(), nil)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: kindEmptyMap, pureMake: true})
		v, k, ok := bl.coerceEmpty(at, n.Dst(), kindEmptyMap, f.k)
		return v, ok && k == f.k
	}
	return ir.NoTemp, false
}

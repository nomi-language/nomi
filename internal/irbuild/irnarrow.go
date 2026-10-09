package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// narrowedRead is a read of t, whose binding holds v of kind k, where the
// checker narrowed t to an embedded type of k's enum (spec §8 *Type
// Narrowing*): in an arm that matched t against `.Circle{..}`, t is that
// variant's payload, the Circle. The checker narrows only where the match
// succeeded, so the projection cannot fail. A read the checker did not
// narrow is v itself.
func (bl *irScalarBuilder) narrowedRead(t *ast.Ident, v ir.Temp, k kind, mobile bool) (ir.Temp, kind, bool, bool) {
	if bl.g.fa == nil || k.tag != tagNamed || k.def == nil {
		return v, k, mobile, true
	}
	var name string
	switch ty := bl.g.fa.ExprTypes[t].(type) {
	case *analysis.StructType:
		name = ty.Name
	case *analysis.DistinctType:
		name = ty.Name
	default:
		return v, k, mobile, true
	}
	variant := k.def.variant(name)
	if variant == nil || variant.kind != "embedded" {
		return v, k, mobile, true
	}
	if !irRetainedEmbed(k.def, variant) || irEmbedsEnum(variant) {
		return ir.NoTemp, kindInvalid, false, false
	}
	payload := bl.variantPayload(t, v, k.def, variant)
	return payload, variant.payloads[0].k, false, true
}

package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// variantNameHost is the VM intrinsic behind std/phrase/format's generic
// `variant_name<T>(value: T) -> String`: the name of the variant an enum
// value is, read off the value itself, so that no `Debug` impl the enum
// writes can change what a phrase's `:string` selects by.
const variantNameHost = "phrase/format.variant_name"

// variantNameCall lowers `std::phrase::format.variant_name(x)` to the intrinsic.
// handled is false for any other call.
func (bl *irScalarBuilder) variantNameCall(t *ast.Call, owner *ast.Ident, method string) (ir.Temp, kind, bool, bool, bool) {
	if method != "variant_name" || len(t.Args) != 1 {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	module, isStd := stdFileQualifier(bl.g.fa, owner)
	if !isStd {
		module, isStd = bl.stdOwnFileQualifier(owner.Name)
	}
	if !isStd || module != "phrase/format" && module != "std/phrase/format" {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	v, _, _, ok := bl.lower(argExpr(t.Args[0]))
	if !ok {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(variantNameHost, variantNameHost), v)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindString})
	return c.Dst(), kindString, false, true, true
}

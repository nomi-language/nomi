package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// preludePredicates names the variant each prelude predicate tests
// (`Option.is_some` tests Some). std's bodies are each a two-arm `case`
// answering whether the value is that variant, which is exactly one
// variant test. stdQualRoutes routes each to preludePredicateCall.
var preludePredicates = map[stdQualKey]string{
	{stdOptionType, "is_some"}: "Some",
	{stdOptionType, "is_none"}: "None",
	{stdResultType, "is_ok"}:   "Ok",
	{stdResultType, "is_err"}:  "Err",
}

// preludePredicateCall lowers `Result.is_err(r)` and its three siblings,
// the predicate key names, as a variant test on the operand. The operand
// is lowered through irQualLowerArgs, so an assertion subject records it
// as any qualified call's operands are recorded.
func (bl *irScalarBuilder) preludePredicateCall(t *ast.Call, key stdQualKey) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	spec := preludeSpecFor(key.Owner.Module, key.Owner.Name)
	args := bl.irQualLowerArgs(t)
	if !args.ok || len(args.kinds) != 1 {
		return no()
	}
	k := args.kinds[0]
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil || k.def.preludeOf.spec != spec || !irRetainedEnumKind(k.def) {
		return no()
	}
	v := k.def.variant(preludePredicates[key])
	if v == nil {
		return no()
	}
	match := bl.variantTest(t, args.temps[0], k.def, v)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	return match.Dst(), kindBool, false, true
}

package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irPreludePredicate names the variant a prelude predicate tests
// (`Maybe.some?` tests Some), or "" for any other owner and method. std's
// bodies are each a two-arm `case` answering whether the value is that
// variant, which is exactly one variant test.
func irPreludePredicate(owner, method string) string {
	switch owner + "." + method {
	case "Maybe.some?":
		return "Some"
	case "Maybe.none?":
		return "None"
	case "Result.ok?":
		return "Ok"
	case "Result.err?":
		return "Err"
	}
	return ""
}

// preludePredicateCall lowers `Result.err?(r)` and its three siblings as a
// variant test on the operand. The operand is lowered through
// irQualLowerArgs, so an assertion subject records it as any qualified call's
// operands are recorded.
func (bl *irScalarBuilder) preludePredicateCall(t *ast.Call, owner, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	spec := preludeSpecFor("std/maybe", "Maybe")
	if owner == "Result" {
		spec = preludeSpecFor("std/results", "Result")
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok || len(args.kinds) != 1 {
		return no()
	}
	k := args.kinds[0]
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil || k.def.preludeOf.spec != spec || !irRetainedEnumKind(k.def) {
		return no()
	}
	v := k.def.variant(irPreludePredicate(owner, method))
	if v == nil {
		return no()
	}
	match := bl.variantTest(t, args.temps[0], k.def, v)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	return match.Dst(), kindBool, false, true
}

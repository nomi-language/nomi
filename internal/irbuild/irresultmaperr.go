package irbuild

import "github.com/nomi-language/nomi/internal/ast"

func (bl *irScalarBuilder) resultMapErrPlan(t *ast.Call, args irQualArgs) *irQualPlan {
	if !args.ok || len(args.kinds) != 2 {
		return nil
	}
	anchor := bl.g.preludeByName["Result"]
	recv, cb := args.kinds[0], args.kinds[1]
	if anchor == nil || recv.tag != tagNamed || recv.def.preludeOf == nil || recv.def.preludeOf.spec != anchor.spec || !irRetainedEnumKind(recv.def) {
		return nil
	}
	d := recv.def
	if len(d.preludeArgs) != 2 || cb.tag != tagFunc || len(funcParams(cb)) != 1 || funcParams(cb)[0] != d.preludeArgs[1] || !irRetainedPreludePayload(funcResult(cb)) {
		return nil
	}
	result := bl.g.preludeInstanceOf(anchor.spec, []kind{d.preludeArgs[0], funcResult(cb)})
	if result.tag != tagNamed || !irRetainedEnumKind(result.def) {
		return nil
	}
	return &irQualPlan{token: "Result.map_err", name: "Result.map_err", result: result, host: true}
}

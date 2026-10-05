package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// preludeWithDefaultPlan plans `Maybe.with_default(m, d)` and
// `Result.with_default(r, d)`: a host crossing to the row's rt function over a
// receiver that instantiates the prelude enum, whose default already has the
// payload kind. A default that would need a coercion declines.
func (bl *irScalarBuilder) preludeWithDefaultPlan(t *ast.Call, args irQualArgs, owner string) *irQualPlan {
	key := owner + ".with_default"
	fn, known := preludeFns[key]
	if !known || !args.ok || len(args.kinds) != fn.args || bl.rowsUnrecorded(t) {
		return nil
	}
	bl.g.loadPreludes()
	if _, anchored := bl.g.preludeByName[owner]; !anchored {
		return nil
	}
	d := args.kinds[0].def
	if args.kinds[0].tag != tagNamed || d == nil || d.preludeOf == nil || d.preludeOf.spec.nomi != owner ||
		len(d.preludeArgs) != len(d.preludeOf.spec.params) {
		return nil
	}
	payload := d.preludeArgs[0]
	if args.kinds[1] != payload || !irRetainedValueKind(payload) {
		return nil
	}
	return &irQualPlan{token: key, name: key, result: payload, host: true}
}

// preludeToResultPlan plans `Maybe.to_result(m, e)`: a host crossing to
// `rt.MaybeToResult` whose result is `Result<T, E>`, with T the receiving
// Maybe's argument and E the error operand's own kind. An error operand whose
// kind preludeArgUsable rejects, such as an untyped literal, declines.
func (bl *irScalarBuilder) preludeToResultPlan(t *ast.Call, args irQualArgs) *irQualPlan {
	const key = "Maybe.to_result"
	fn := preludeFns[key]
	if !args.ok || len(args.kinds) != fn.args || bl.rowsUnrecorded(t) {
		return nil
	}
	bl.g.loadPreludes()
	if _, anchored := bl.g.preludeByName["Result"]; !anchored {
		return nil
	}
	d := args.kinds[0].def
	if args.kinds[0].tag != tagNamed || d == nil || d.preludeOf == nil || d.preludeOf.spec.nomi != "Maybe" ||
		len(d.preludeArgs) != len(d.preludeOf.spec.params) || !irRetainedEnumKind(d) {
		return nil
	}
	errK := args.kinds[1]
	if !preludeArgUsable(errK) || !irRetainedPreludePayload(errK) {
		return nil
	}
	res := bl.g.preludeInstanceOf(preludeSpecNamed("Result"), []kind{d.preludeArgs[0], errK})
	if res.tag != tagNamed || !irRetainedEnumKind(res.def) {
		return nil
	}
	return &irQualPlan{token: key, name: key, result: res, host: true}
}

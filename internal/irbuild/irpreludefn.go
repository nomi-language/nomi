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
	if args.kinds[1] != payload && payload.tag == tagSeq {
		// `Result.with_default(Err("no"), [False])` where the checker typed
		// the payload `Iter<Bool>` from the position the call sits in: the
		// list default is viewed as the sequence, as any source entering a
		// declared `Iter<T>` is. The operand slots are the caller's, so the
		// viewed temporary is what qualEmit passes.
		if v, k, ok := bl.coerceEmpty(t.Args[1], args.temps[1], args.kinds[1], payload); ok && k == payload {
			args.temps[1], args.kinds[1] = v, k
		}
	}
	if args.kinds[1] != payload || !irCallableValueKind(payload) {
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

package irbuild

import "github.com/nomi-language/nomi/internal/ast"

func (bl *irScalarBuilder) listCallPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	if method == "contains" {
		return bl.seqContainsPlan(t, args, "List")
	}
	fn, known := listFuncs[method]
	if !known || !args.ok || len(args.kinds) != fn.args {
		return nil
	}
	lk := kindInvalid
	// kindInvalid: sentinel — recordedTypeArg's nothing-recorded answer; the operands' kinds are read instead.
	if elem, _ := bl.g.recordedTypeArg(t, method, "T"); elem != kindInvalid {
		// The list the checker solved T at, which a later operand may
		// have widened past an earlier one's (`List.concat(circles,
		// shapes)` is a List<Shape>).
		lk = bl.g.listKind(elem)
	}
	for _, k := range args.kinds {
		// kindInvalid: sentinel — no typed list representation found yet, not an operand's kind.
		if lk == kindInvalid && k.tag == tagList {
			lk = k
		}
	}
	// The list operands: the index of remove_at is not one.
	lists := args.kinds
	if method == "remove_at" {
		lists = lists[:1]
	}
	// first, tail, concat and remove_at are cell operations that never read an
	// element, so a list of retained structs or enums crosses as a scalar
	// list does.
	// A list of any other element with a stored type crosses too: none of
	// them reads an element.
	// kindInvalid: sentinel — still no typed list representation.
	if lk == kindInvalid || !(irRetainedListKind(lk) || irListTransportKind(lk) || bl.g.irValType(lk) != nil) {
		bl.g.dc.note("a List function without a retained typed list operand")
		return nil
	}
	for i, k := range args.kinds {
		if method == "remove_at" && i == 1 {
			if k != kindInt {
				panic("irbuild: listCallPlan: List.remove_at declares its index Int, so the checker rejects any other argument")
			}
			continue
		}
		v, got, ok := bl.convertTo(t.Args[i], args.temps[i], k, lk)
		if !ok {
			return nil
		}
		args.temps[i], args.kinds[i] = v, got
	}
	if method != "concat" {
		if _, anchored := preludeSpecAnchor(preludeSpecNamed("Maybe")); !anchored {
			return nil
		}
	}
	// The result is the call's record. A call the builder writes for no
	// source node carries the type the checker gives it (a std interface's
	// thunk for a held type, stdProgThunk, carries InterfaceResult's); one
	// for a value held by its kind alone carries none, its result is the
	// row's, and noteUnrecordedKind counts it.
	result, ok := bl.g.recordedKind(t)
	if !ok {
		bl.noteUnrecordedKind("List result", t)
		result, ok = bl.g.listResultKind(fn.result, lk.comp.parts[0], lk, t)
	}
	if !ok || !(irRetainedValueKind(result) || bl.g.irValType(result) != nil) {
		bl.g.dc.note("a List function result outside the retained domain: " + result.nomi())
		return nil
	}
	name := "List." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}

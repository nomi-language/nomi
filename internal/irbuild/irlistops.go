package irbuild

import "github.com/nomi-language/nomi/internal/ast"

func (bl *irScalarBuilder) listCallPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	fn, known := listFuncs[method]
	if !known || !args.ok || len(args.kinds) != fn.args {
		return nil
	}
	lk := kindInvalid
	for _, k := range args.kinds {
		if k.tag == tagList {
			lk = k
			break
		}
	}
	// head, tail and concat are cell operations that never read an element,
	// so a list of retained structs or enums crosses as a scalar list does.
	// kindInvalid: sentinel — no argument supplied a typed list representation.
	if lk == kindInvalid && irAllEmptyLists(args.kinds) {
		// `List.head([])`: nothing constrains the element, so no value of it
		// is built or observed, as checkedPreludeArgsUnitHoles argues for an
		// unconstrained prelude argument. A List of Unit is not a retained
		// kind, so the list is typed as one of Int, the representation of an
		// element the program never holds.
		lk = bl.g.listKind(kindInt)
	}
	// A list of any other element with a stored type crosses too: none of
	// the three reads an element.
	// kindInvalid: sentinel — still no typed list representation.
	if lk == kindInvalid || !(irRetainedListKind(lk) || irListTransportKind(lk) || bl.g.irValType(lk) != nil) {
		irDeclineNote("a List function without a retained typed list operand")
		return nil
	}
	for i, k := range args.kinds {
		v, got, ok := bl.coerceEmpty(t.Args[i], args.temps[i], k, lk)
		if !ok {
			return nil
		}
		args.temps[i], args.kinds[i] = v, got
	}
	if method != "concat" {
		if _, anchored := bl.g.preludeByName["Maybe"]; !anchored {
			return nil
		}
	}
	result, ok := bl.g.listResultKind(fn.result, lk.comp.parts[0], lk, t)
	if !ok || !(irRetainedValueKind(result) || bl.g.irValType(result) != nil) {
		irDeclineNote("a List function result outside the retained domain: " + result.nomi())
		return nil
	}
	name := "List." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}

// irAllEmptyLists reports whether every operand is an empty list literal.
func irAllEmptyLists(kinds []kind) bool {
	for _, k := range kinds {
		if k != kindEmptyList {
			return false
		}
	}
	return len(kinds) > 0
}

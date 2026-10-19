package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// seqContainsPlan is `List.contains?(xs, x)` and `Vector.contains?(v, x)`,
// the `Contains<T>` impls std declares as host functions: one crossing by
// name (`List.contains?`, `Vector.contains?`) that the VM answers with rt's
// scan, comparing each element with x as `==` does. owner is "List" or
// "Vector".
//
// The element must be one `==` compares (valueEquates), as a Map key must:
// the scan is the same comparison a Set or Map lookup makes once.
func (bl *irScalarBuilder) seqContainsPlan(t *ast.Call, args irQualArgs, owner string) *irQualPlan {
	if !args.ok || len(args.kinds) != 2 {
		return nil
	}
	seq, elem := args.kinds[0], args.kinds[1]
	g := bl.g
	empty, of := kindEmptyList, g.listKind
	if owner == "Vector" {
		empty, of = kindEmptyVector, g.vectorKindOf
	}
	if seq == empty {
		// `List.contains?([], x)`: the element operand types the sequence.
		seq = of(elem)
	}
	var have kind
	var ok bool
	if owner == "Vector" {
		have, ok = vectorElem(seq)
	} else if seq.tag == tagList {
		have, ok = seq.comp.parts[0], true
	}
	// kindInvalid: sentinel — the sequence kind did not form.
	if !ok || have == kindInvalid {
		return nil
	}
	if !(irRetainedValueKind(seq) || g.irValType(seq) != nil) {
		irDeclineNote("a " + owner + ".contains? sequence outside the retained domain: " + seq.nomi())
		return nil
	}
	if !g.valueEquates(have) {
		g.reject(owner+".contains? over an element `==` does not compare", have.nomi(), t)
		return nil
	}
	for i, want := range []kind{seq, have} {
		v, got, ok := bl.coerceEmpty(t.Args[i], args.temps[i], args.kinds[i], want)
		if !ok {
			return nil
		}
		args.temps[i], args.kinds[i] = v, got
	}
	name := owner + ".contains?"
	return &irQualPlan{token: name, name: name, result: kindBool, host: true}
}

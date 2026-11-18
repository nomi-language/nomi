package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// seqContainsPlan is `List.contains(xs, x)` and `Vector.contains(v, x)`,
// the `Contains<T>` impls std declares as host functions: one crossing by
// name (`List.contains`, `Vector.contains`) that the VM answers with rt's
// scan, comparing each element with x as `==` does. owner is "List" or
// "Vector".
//
// The element must be one `==` compares (valueEquates), as a Map key must:
// the scan is the same comparison a Set or Map lookup makes once.
func (bl *irScalarBuilder) seqContainsPlan(t *ast.Call, args irQualArgs, owner string) *irQualPlan {
	if !args.ok || len(args.kinds) != 2 {
		return nil
	}
	seq := args.kinds[0]
	g := bl.g
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
	if g.irValType(seq) == nil {
		// A lowered sequence, or one built of a lowered element, is held,
		// and every held kind has a stored type.
		panic("irbuild: a " + owner + ".contains sequence with no stored type: " + seq.nomi())
	}
	if !g.valueEquates(have) {
		g.reject(owner+".contains over an element `==` does not compare", have.nomi(), t)
		return nil
	}
	// The sequence is seq, args.kinds[0]; the element enters its element.
	v, got, ok := bl.convertTo(t.Args[1], args.temps[1], args.kinds[1], have)
	if !ok {
		return nil
	}
	args.temps[1], args.kinds[1] = v, got
	name := owner + ".contains"
	return &irQualPlan{token: name, name: name, result: kindBool, host: true}
}

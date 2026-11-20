package irbuild

import (
	"maps"
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// `Hashable.hash` over a tuple or a record. A tuple is Hashable structurally,
// as it is Equatable structurally (spec §Hashable on tuples): its hash is the
// derive mix over its slots, `Hashable.hash(t0) * 31 + Hashable.hash(t1) ...`, with
// wrapping arithmetic, which is what `derive Hashable` answers for a
// positional payload of the same slots without the variant index. Each
// slot's hash is its own type's `Hashable.hash`, so a slot of a declared type
// with a hand-written impl hashes through it.
// A record is the same mix over its fields in name order, which is the order
// its type lists them in, so `{a: 1, b: 2}` and `{b: 2, a: 1}` hash alike.
//
// It is lowered as that expression: each slot is projected and bound to a
// name no program can spell, and the mix is lowered as the Nomi a derive
// would write. The synthesized nodes sit at column 0 of the call's line,
// where the checker records nothing, so no route reads the call's own
// checker facts for them.

// tupleHashCall lowers `Hashable.hash(x)` when x is a tuple or a record. handled is false
// when the operand is not one, and the caller lowers the call as usual.
func (bl *irScalarBuilder) tupleHashCall(t *ast.Call) (v ir.Temp, k kind, mobile, ok, handled bool) {
	if len(t.Args) != 1 || namedArgNode(t.Args) != nil {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	ft := bl.g.checkedCallSignature(t)
	var st analysis.Type
	if ft == nil {
		// A call this file synthesized (a slot of an enclosing tuple's mix,
		// or a box's thunk) has no checker facts: its operand is a name
		// bound here, whose kind says whether it is a tuple or a record,
		// and its record names the slot's type where the enclosing mix
		// had one (stdIfaceCallRecord).
		id, isIdent := t.Args[0].(*ast.Ident)
		if !isIdent {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		k, bound := bl.boundK[id.Name]
		if !bound || !irStructuralBoxes(k) {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		st = bl.g.calleeOf(t).For
	} else if len(ft.Params) != 1 {
		return ir.NoTemp, kindInvalid, false, false, false
	} else if tag := bl.g.project(ft.Params[0]).tag; tag != tagTuple && tag != tagAnonStruct {
		return ir.NoTemp, kindInvalid, false, false, false
	} else {
		st = bl.g.checkerTypeArg(ft.Params[0])
	}
	subj, sk, _, sok := bl.lower(t.Args[0])
	if !sok {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	// The operand has the tuple or record type the parameter projects to.
	if sk.tag == tagAnonStruct {
		irLoweredRecordRetained(sk, "Hashable.hash")
	} else {
		irLoweredTupleRetained(sk, "Hashable.hash")
	}
	v, k, mobile, ok = bl.tupleHashOf(t, subj, sk, st)
	return v, k, mobile, ok, true
}

// partCheckerType is the checker type of part i of a value of kind sk,
// whose checker type is st: a tuple's i-th element, an anonymous record's
// field named as sk's i-th, and a std generic container's i-th type
// argument, in the order stdRecvParts lists sk's (a List's element, a
// Map's key and value, a Maybe's payload). Nil when st is nil or not that
// shape.
func partCheckerType(st analysis.Type, sk kind, i int) analysis.Type {
	containerArg := func(args []analysis.Type) analysis.Type {
		if _, parts, ok := stdRecvParts(sk); ok && len(parts) == len(args) && i < len(args) {
			return args[i]
		}
		return nil
	}
	switch t := analysis.ResolveTypeVar(st).(type) {
	case *analysis.EnumType:
		return containerArg(t.TypeArgs)
	case *analysis.StructType:
		return containerArg(t.TypeArgs)
	case *analysis.DistinctType:
		// A generic host type: a Vector's element.
		return containerArg(t.TypeArgs)
	case *analysis.ListType:
		return containerArg([]analysis.Type{t.Elem})
	case *analysis.MapType:
		return containerArg([]analysis.Type{t.Key, t.Val})
	case *analysis.TupleType:
		if sk.tag == tagTuple && i < len(t.Elems) && len(t.Elems) == len(sk.comp.parts) {
			return t.Elems[i]
		}
	case *analysis.AnonStructType:
		if sk.tag == tagAnonStruct && i < len(sk.comp.names) {
			for _, f := range t.Fields {
				if f.Name == sk.comp.names[i] {
					return f.Type
				}
			}
		}
	}
	return nil
}

// preludePayloadType is the checker type of a payload of kind pk of a
// value of sk, a prelude enum instance whose checker type is st: the type
// argument the payload is (`Some`'s T, `Err`'s E), by partCheckerType. Nil
// when st is nil or the payload is no type argument of sk.
func preludePayloadType(st analysis.Type, sk, pk kind) analysis.Type {
	_, args, ok := stdRecvParts(sk)
	if !ok {
		return nil
	}
	for i, a := range args {
		if a == pk {
			return partCheckerType(st, sk, i)
		}
	}
	return nil
}

// embeddedVariantType is the declared type of the `embeds` variant named
// name of st, a non-generic enum's checker type: the struct or distinct
// itself, where variantPayloadType answers a wrapping distinct's inner
// type. Nil when st is nil, generic or has no such variant.
func embeddedVariantType(st analysis.Type, name string) analysis.Type {
	et, ok := analysis.ResolveTypeVar(st).(*analysis.EnumType)
	if !ok || len(et.TypeParams) > 0 {
		return nil
	}
	for _, v := range et.Variants {
		if v.Name == name && v.Kind == analysis.VariantEmbedded {
			return v.Embedded
		}
	}
	return nil
}

// variantPayloadType is the checker type of the payload of the variant
// named name of st, a non-generic enum's checker type (Json's `Obj` is
// `Map<String, Json>`). Nil when st is nil, generic or has no such
// variant with a payload.
func variantPayloadType(st analysis.Type, name string) analysis.Type {
	et, ok := analysis.ResolveTypeVar(st).(*analysis.EnumType)
	if !ok || len(et.TypeParams) > 0 {
		return nil
	}
	for _, v := range et.Variants {
		if v.Name == name {
			return v.DataType
		}
	}
	return nil
}

// tupleHashOf is the derive mix over subj, a lowered tuple or record of kind
// sk and checker type st (nil where the builder holds it by kind alone),
// for the call t. Each slot's call carries the record of `Hashable.hash`
// at the slot's type, and each operator of the mix Int's.
func (bl *irScalarBuilder) tupleHashOf(t *ast.Call, subj ir.Temp, sk kind, st analysis.Type) (v ir.Temp, k kind, mobile, ok bool) {
	if bl.qualRecord == t {
		// In an assertion subject the tuple is the call's one row.
		bl.qualRecordArgs = irQualArgs{temps: []ir.Temp{subj}, kinds: []kind{sk}, mobile: []bool{true}, ok: true}
		bl.qualRecordSeen = true
	}
	// The synthesized slots have no source to name in a `values:` row.
	recording, qualRecord := bl.recording, bl.qualRecord
	bl.recording, bl.qualRecord = 0, nil
	defer func() { bl.recording, bl.qualRecord = recording, qualRecord }()
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	line := t.Line
	intOp := func(iface analysis.StdType, method string) analysis.Callee {
		return bl.g.instanceFA().InterfaceCallee(iface, method, analysis.TypeInt)
	}
	var mix ast.Node
	for i, part := range sk.comp.parts {
		name := "%hash" + strconv.Itoa(t.Line) + "." + strconv.Itoa(t.Col) + "." + strconv.Itoa(i)
		var slot ir.Temp
		if sk.tag == tagAnonStruct {
			slot, _, _, _ = bl.recordRead(t, sk.comp.names[i], subj, sk, true)
		} else {
			slot = bl.tupleProjection(t, subj, i, part)
		}
		bl.patternBinding(t, name, slot, part)
		hash := &ast.Call{
			Func: &ast.FieldAccess{
				Object: &ast.TypeIdent{Name: "Hashable", Line: line},
				Field:  &ast.Ident{Name: "hash", Line: line},
				Line:   line,
			},
			Args: []ast.Node{bl.g.madeIdent(name, line, 0)},
			Line: line,
		}
		bl.g.carryCall(hash, bl.g.stdIfaceCallRecord(stdHashable, "hash", 1, partCheckerType(st, sk, i)))
		if mix == nil {
			mix = hash
			continue
		}
		scaled := &ast.Binary{Left: mix, Op: "*", Right: &ast.IntLit{Value: 31, Line: line}, Wrapping: true, Line: line}
		sum := &ast.Binary{Left: scaled, Op: "+", Right: hash, Wrapping: true, Line: line}
		// Int's Multiply and Add, as the checker records them for `a * b`
		// and `a + b` over Ints, which the machine computes (machineImpls).
		defer bl.g.carryOperatorUse(scaled, stdMultiply, intOp(stdMultiply, "multiply"), analysis.TypeInt)()
		defer bl.g.carryOperatorUse(sum, stdAdd, intOp(stdAdd, "add"), analysis.TypeInt)()
		mix = sum
	}
	v, k, mobile, ok = bl.lower(mix)
	if ok && k != kindInt {
		return ir.NoTemp, kindInvalid, false, false
	}
	return v, k, mobile, ok
}

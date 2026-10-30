package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func (bl *irScalarBuilder) scalarOperatorCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	op, known := scalarArithOp(owner, method)
	if !known || !args.ok || len(args.kinds) != 2 || args.kinds[0] != args.kinds[1] {
		return no()
	}
	// Local or imported user interfaces retain their own dispatch path.
	if d, found := bl.g.ifaceNamed(owner); found && !d.stdShared {
		return no()
	}
	k := args.kinds[0]
	if k == kindString && op == "+" {
		n := ir.NewConcat(bl.g.irNodePos(t), bl.f.NewTemp(), args.temps...)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: k})
		return n.Dst(), k, false, true
	}
	shape, ok := bl.g.irArithKind(irArithOps[op], k, false)
	if !ok {
		return no()
	}
	n := ir.NewArith(bl.g.irNodePos(t), bl.f.NewTemp(), irArithOps[op], shape, args.temps[0], args.temps[1])
	bl.b.Append(n)
	return n.Dst(), k, false, true
}

// scalarEqualCall lowers `Int.equal(a, b)` and its Float/String/Bool/Equatable
// spellings over two scalars of one kind to an `ir.Compare`, which the Go
// reader spells through `scalarEqualCode` (`==`, or `rt.EqFloat`). Without it
// the call would reach the stdlib index and become a call to the std
// `Equatable.equal` body.
//
// `ir.Compare` does not admit a Unit shape, so a Unit pair declines the body.
func (bl *irScalarBuilder) scalarEqualCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, kind, bool, bool, bool) {
	if method != "equal" || len(t.Args) != 2 {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	switch owner {
	case "Equatable", "Int", "Float", "String", "Bool":
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	// From here this row owns the call: operands it cannot compare decline
	// the body rather than falling through to a later arm.
	if !args.ok || args.kinds[0] != args.kinds[1] {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	var shape ir.ValShape
	switch args.kinds[0] {
	case kindInt:
		shape = ir.ValInt
	case kindFloat:
		shape = ir.ValFloat
	case kindString:
		shape = ir.ValString
	case kindBool:
		shape = ir.ValBool
	case kindUnit:
		// Unit has one value, so two Units are equal once both operands
		// have run, as std's `impl Equatable for Unit` and `==` answer.
		c := ir.NewBool(bl.g.irNodePos(t), bl.f.NewTemp(), true)
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), kindBool, false, true, true
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	c := ir.NewCompare(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OpEq, shape, args.temps[0], args.temps[1])
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindBool})
	return c.Dst(), kindBool, false, true, true
}

// irHashValueHost is the VM intrinsic that hashes an interface value as a
// Map key or Set element of interface type hashes it (vm/keys.go): by the
// held type's hand-written Hashable when it has one, by its name when only
// its Equatable is hand-written, structurally otherwise. Two values `==`
// calls equal hash alike.
const irHashValueHost = "vm.hash_value"

// existentialHashCall lowers `Hashable.hash(x)` over an interface value to
// the irHashValueHost call. owned is false for any other call.
func (bl *irScalarBuilder) existentialHashCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, bool) {
	if owner != "Hashable" || method != "hash" || len(t.Args) != 1 || !args.ok || len(args.kinds) != 1 || !irExistentialKind(args.kinds[0]) {
		return ir.NoTemp, false
	}
	if d, found := bl.g.ifaceNamed(owner); found && !d.stdShared {
		// A program's own Hashable.
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(t)
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(irHashValueHost, irHashValueHost), args.temps[0])
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindInt})
	return c.Dst(), true
}

// structuralEqualCall lowers `Equatable.equal(a, b)` over two tuples or two
// records of one kind as `a == b` lowers them: a tuple or a record is
// Equatable structurally and has no impl to select, so the call and the
// operator are one comparison (valueEquality). owned is false for any other
// call, which the caller lowers as before.
func (bl *irScalarBuilder) structuralEqualCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, bool, bool) {
	if owner != "Equatable" || method != "equal" || len(t.Args) != 2 || !args.ok || len(args.kinds) != 2 {
		return ir.NoTemp, false, false
	}
	if d, found := bl.g.ifaceNamed(owner); found && !d.stdShared {
		// A program's own Equatable.
		return ir.NoTemp, false, false
	}
	k := args.kinds[0]
	if irExistentialKind(k) && irExistentialKind(args.kinds[1]) {
		// Two interface values: `Equatable.equal(a, b)` is `a == b`, which
		// compares them by their run-time types (ir.ValIface).
		c := ir.NewCompare(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OpEq, ir.ValIface, args.temps[0], args.temps[1])
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), true, true
	}
	if k != args.kinds[1] || !irRetainedTupleKind(k) && !irRetainedRecordKind(k) {
		return ir.NoTemp, false, false
	}
	at := &ast.Binary{Op: "==", Left: t.Args[0], Right: t.Args[1], Line: t.Line, Col: t.Col}
	v, _, ok := bl.valueEquality(at, ir.OpEq, args.temps[0], args.temps[1], k)
	return v, ok, true
}

package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func (bl *irScalarBuilder) scalarOperatorCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if method == "bitwise_not" && (owner == "Int" || owner == "BitwiseNot") {
		// `Int.bitwise_not(n)`, `~~~n` written as a call.
		if !args.ok || len(args.kinds) != 1 || args.kinds[0] != kindInt {
			return no()
		}
		if d, found := bl.g.ifaceNamed(owner); found && !d.stdShared {
			return no()
		}
		shape, _ := bl.g.irArithKind(ir.OpNot, kindInt, false)
		n := ir.NewUnaryArith(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OpNot, shape, args.temps[0])
		bl.b.Append(n)
		return n.Dst(), kindInt, false, true
	}
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
	if held := args.kinds[0].iface; held.stdOrigin == "std/hashable" && held.nomi == "Hashable" {
		// A value held as std Hashable itself dispatches to its type's impl
		// through the table (stdprogiface.go), so the call answers what
		// `Hashable.hash` of the concrete value answers. vm.hash_value is
		// the Map key hash, which only has to agree with `==`.
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
	if k != args.kinds[1] {
		return ir.NoTemp, false, false
	}
	at := &ast.Binary{Op: "==", Left: t.Args[0], Right: t.Args[1], Line: t.Line, Col: t.Col}
	if bl.g.meetsByEquality("Equatable", k) {
		// A declared type with no Equatable of its own meets the bound by
		// its equality, so the call is `a == b` over it.
		v, _, _, ok := bl.compareOperands(at, ir.OpEq, args.temps[0], k, args.temps[1], k, args.mobile[1])
		return v, ok, true
	}
	if !irRetainedTupleKind(k) && !irRetainedRecordKind(k) {
		return ir.NoTemp, false, false
	}
	v, _, ok := bl.valueEquality(at, ir.OpEq, args.temps[0], args.temps[1], k)
	return v, ok, true
}

// meetsByEquality reports whether k, a declared type, meets a `where T:
// iface` bound (std's Equatable or Hashable) by its equality: no file of the
// program and nothing in std declares an impl of iface for it (spec §5
// *Equality and Ordering Semantics*). Its `Equatable.equal` is then `==`, and
// its `Hashable.hash` the hash a Map key of it takes.
func (g *gen) meetsByEquality(iface string, k kind) bool {
	if k.tag != tagNamed || k.def == nil || isDecimalKind(k) || !irKeyImplRecv(k) || !g.noImplOf(iface, k) {
		// A prelude or std container instance (`Maybe<Int>`, `Set<Int>`)
		// takes std's generic impl, which byIface does not key by the
		// instance, and an rt leaf or scalar has its own.
		return false
	}
	if reg := g.programReg(); reg != nil && k.def.decl != nil && reg.byDecl[k.def.decl] != nil {
		// A user declaration, here or in a std instance's body: another
		// file of its module may write or derive the impl.
		return !reg.declaresImpl(iface, k.def.decl)
	}
	return true
}

// structuralHashCall lowers `Hashable.hash(x)` over a declared type that
// meets Hashable by its equality to the irHashValueHost call: the hash a Map
// key of the type takes, so it agrees with `==`. owned is false for any
// other call.
func (bl *irScalarBuilder) structuralHashCall(t *ast.Call, args irQualArgs, owner, method string) (ir.Temp, bool) {
	if owner != "Hashable" || method != "hash" || len(t.Args) != 1 || !args.ok || len(args.kinds) != 1 {
		return ir.NoTemp, false
	}
	if d, found := bl.g.ifaceNamed(owner); found && !d.stdShared {
		// A program's own Hashable.
		return ir.NoTemp, false
	}
	if !bl.g.meetsByEquality("Hashable", args.kinds[0]) {
		return ir.NoTemp, false
	}
	pos := bl.g.irNodePos(t)
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(irHashValueHost, irHashValueHost), args.temps[0])
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindInt})
	return c.Dst(), true
}

package analysis

import "github.com/nomi-language/nomi/internal/ast"

// OperatorForm is what a binary operator over a value that is not a
// primitive means (OperatorUse).
type OperatorForm uint8

const (
	// OperatorByImpl calls an impl of the operator's interface
	// (OperatorUse.Impl): hand-written, derived or a std `host fn`.
	OperatorByImpl OperatorForm = iota + 1
	// OperatorStructural compares by the operands' shape: a tuple, an
	// anonymous struct, a collection, or a declared type with no impl of
	// Equatable, which meets `==` by its equality (spec §5 *Equality
	// bounds*).
	OperatorStructural
	// OperatorDispatched compares two interface values by their run-time
	// types, through the impl of the value each holds.
	OperatorDispatched
	// OperatorPerInstance is an operator over a type parameter of the
	// enclosing declaration: OperatorUse.Bound names the interface its
	// bound gives it, and each instance resolves the impl.
	OperatorPerInstance
)

// OperatorUse is what one operator means (FileAnalysis.Operators):
// the checker's decision, which the IR builder lowers.
type OperatorUse struct {
	Form OperatorForm
	// Impl is, for OperatorByImpl, the impl: the operator's OperatorImpls
	// record, or for an operator in a derive-synthesized body, which
	// OperatorImpls leaves out, the same record made for it here.
	Impl *OperatorImpl
	// Bound is, for OperatorPerInstance, the interface the operator goes
	// through: Equatable, Comparable, Add and the others.
	Bound string
}

// operatorIface is the interface and function op goes through.
func operatorIface(op string) (string, string, bool) {
	switch op {
	case "==", "!=":
		return "Equatable", "equal", true
	case "<", "<=", ">", ">=":
		return "Comparable", "compare", true
	}
	if oi, ok := operatorInterfaceForOp(op); ok {
		return oi.Interface, oi.Method, true
	}
	return "", "", false
}

// recordOperatorUses records what each operator the file's bodies hold
// means (a binary operator, a prefix `~~~`), once every body is checked and its variables closed. An
// operator over a primitive has no record: the machine's operation is the
// only one. A declared type whose impls the index holds but cannot narrow
// to one (two files each write one) is left unrecorded, which the
// verifier reports.
func (c *checker) recordOperatorUses() {
	if c.fa == nil || len(c.operatorUses) == 0 {
		return
	}
	for _, n := range c.operatorUses {
		if use := operatorUseOf(c.fa, n); use != nil {
			if c.fa.Operators == nil {
				c.fa.Operators = map[ast.Node]*OperatorUse{}
			}
			c.fa.Operators[n] = use
		}
	}
	c.operatorUses = nil
}

// operatorSubject is the interface and function the operator n goes
// through, and the type that implements it: a binary operator's left
// operand's, `~~~`'s operand's (a subtype's value is its base's, as
// checkBitwiseNot types it). ok is false for any other node, and for an
// operand with no type.
func operatorSubject(fa *FileAnalysis, n ast.Node) (iface, method string, t Type, ok bool) {
	var operand ast.Node
	switch n := n.(type) {
	case *ast.Binary:
		iface, method, ok = operatorIface(n.Op)
		operand = n.Left
	case *ast.Unary:
		iface, method, ok = "BitwiseNot", "bitwise_not", n.Op == "~~~"
		operand = n.Right
	}
	if !ok {
		return "", "", nil, false
	}
	t, ok = fa.ExprTypes[operand]
	if !ok || t == nil {
		return "", "", nil, false
	}
	if _, unary := n.(*ast.Unary); unary && refinementOf(t) != nil {
		t = RefinementBase(t)
	}
	return iface, method, t, true
}

// operatorUseOf is what the operator n means, nil for an operator over a
// primitive.
func operatorUseOf(fa *FileAnalysis, n ast.Node) *OperatorUse {
	iface, method, lt, ok := operatorSubject(fa, n)
	if !ok {
		return nil
	}
	if b, binary := n.(*ast.Binary); binary {
		if impl := fa.OperatorImpls[b]; impl != nil {
			return &OperatorUse{Form: OperatorByImpl, Impl: impl}
		}
	}
	return fa.interfaceUseFor(iface, method, resolveTypeVar(ResolveClosed(lt)))
}

// interfaceUseFor is what the operator through iface's function method
// means when the operand implementing it is of type t: the answer its
// shape decides (shapeUse), else the impl of a declared type, from the
// block a call of the function reaches (implMethodFor, implBlockFor),
// else, for a declared type with no block of iface, noImplUse's answer.
// A declared type with several blocks none of which answers has none.
// operatorUseOf asks it at the type an operator is checked with and
// OperatorUseAt at an instance's. A call of the same function asks
// shapeUse, implBlockFor and noImplUse too (callForm), so `a == b` and
// `Equatable.equal(a, b)` are answered by one rule.
func (fa *FileAnalysis) interfaceUseFor(iface, method string, t Type) *OperatorUse {
	if use, decided := shapeUse(iface, t); decided {
		return use
	}
	if fa == nil || concreteTypeName(t) == "" {
		return nil
	}
	if m := fa.implMethodFor(t, iface, method); m != nil {
		return &OperatorUse{Form: OperatorByImpl, Impl: &OperatorImpl{Interface: iface, Receiver: t, Method: m}}
	}
	if len(fa.implBlocksOf(t, iface, "")) == 0 {
		return noImplUse(iface, t)
	}
	return nil
}

// shapeUse is how a use of iface's function on a value of type t is
// answered when t's shape alone decides it: nil for a primitive, which
// the machine operates on itself; per instance for a type parameter; by
// dispatch for an interface value; structurally for a tuple, an
// anonymous struct, a collection and a function value, except
// BitwiseNot, which none has. decided is false for a declared type, whose
// impls decide.
func shapeUse(iface string, t Type) (use *OperatorUse, decided bool) {
	switch t.(type) {
	case nil, *PrimitiveType, *TypeVar:
		return nil, true
	case *TypeParam_:
		return &OperatorUse{Form: OperatorPerInstance, Bound: iface}, true
	case *InterfaceType, *ConjunctionType:
		return &OperatorUse{Form: OperatorDispatched}, true
	case *TupleType, *AnonStructType, *ListType, *MapType, *FuncType:
		if iface == "BitwiseNot" {
			return nil, true
		}
		return &OperatorUse{Form: OperatorStructural}, true
	}
	return nil, false
}

// noImplUse is how a use of iface's function on t, a declared type with
// no impl of iface, is answered: structurally for Equatable and
// Hashable, which it meets by its structural equality (spec §5 *Equality
// bounds*, *Hashing and keys*), for Debug, whose rendering of a type with
// no Debug block of its own (one declared inside a body) is structural,
// and for Struct, the marker every struct meets. Nil for any other
// interface: nothing answers.
func noImplUse(iface string, t Type) *OperatorUse {
	switch iface {
	case "Equatable", "Hashable", "Debug":
		return &OperatorUse{Form: OperatorStructural}
	case "Struct":
		if _, isStruct := t.(*StructType); isStruct {
			return &OperatorUse{Form: OperatorStructural}
		}
	}
	return nil
}

// OperatorUseAt is what the operator n means at an instance of the
// generic body that holds it, whose types by answers for the body's type
// parameters (ReplaceTypeParams' form). body is the file whose check
// recorded n (FileAnalysis.Operators); fa is the file whose program's
// impls the instance's types have, as CalleeAt asks. The body is checked
// once, so an operator over a type parameter records only
// OperatorPerInstance and the bound. At an instance where the operand's
// type is known the answer is the record an operator written at that type
// has (interfaceUseFor): an impl, a structural comparison, or nil for a
// primitive, which the machine operates on itself. resolved is false, and
// the record n's own, for any other operator and where a type parameter
// has no answer.
func (fa *FileAnalysis) OperatorUseAt(body *FileAnalysis, n ast.Node, by func(*TypeParam_) (Type, bool)) (use *OperatorUse, resolved bool) {
	if body == nil {
		return nil, false
	}
	use = body.Operators[n]
	if fa == nil || use == nil || use.Form != OperatorPerInstance {
		return use, false
	}
	_, method, lt, ok := operatorSubject(body, n)
	if !ok {
		return use, false
	}
	t, all := ReplaceTypeParams(resolveTypeVar(ResolveClosed(lt)), by)
	if !all {
		return use, false
	}
	t = resolveTypeVar(t)
	if _, still := t.(*TypeParam_); still {
		return use, false
	}
	return fa.interfaceUseFor(use.Bound, method, t), true
}

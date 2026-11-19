package analysis

import "github.com/nomi-language/nomi/internal/ast"

// OperatorForm is how a use of an interface's function is answered: an
// operator through the interface (OperatorUse) or a call of the function
// by name (Callee.Form), both from interfaceUse.
type OperatorForm uint8

const (
	// OperatorByImpl calls an impl of the operator's interface
	// (OperatorUse.Impl), the operand's type's block of it: hand-written,
	// derived, or std's `fn` or `host fn`, a primitive's, a List's or a
	// Map's included.
	OperatorByImpl OperatorForm = iota + 1
	// OperatorStructural compares by the operands' shape, where their
	// type has no block of the interface: a tuple, an anonymous struct, a
	// function value, or a declared type with no impl of Equatable, which
	// meets `==` by its equality (spec §5 *Equality bounds*).
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
	// Impl is, for OperatorByImpl, the impl: the function of the block
	// interfaceUse reaches, with the operand's type and the interface's
	// type arguments at the use.
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
// means (a binary operator, a prefix `~~~`), once every body is checked
// and its variables closed (operatorUseOf). An operator nothing answers
// has no record: a declared type whose impls the index holds but cannot
// narrow to one (two files each write one), which the verifier reports.
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
// through, and the type that implements it: a comparison's compared
// operand's (comparedOperand), any other binary operator's left
// operand's, `~~~`'s operand's (a subtype's value is its base's, as
// checkBitwiseNot types it). ok is false for any other node, and for an
// operand with no type.
func operatorSubject(fa *FileAnalysis, n ast.Node) (iface, method string, t Type, ok bool) {
	switch n := n.(type) {
	case *ast.Binary:
		iface, method, ok = operatorIface(n.Op)
		t = fa.ExprTypes[n.Left]
		if iface == "Equatable" || iface == "Comparable" {
			_, t = comparedOperand(n, t, fa.ExprTypes[n.Right])
		}
	case *ast.Unary:
		iface, method, ok = "BitwiseNot", "bitwise_not", n.Op == "~~~"
		t = fa.ExprTypes[n.Right]
	}
	if !ok || t == nil {
		return "", "", nil, false
	}
	if _, unary := n.(*ast.Unary); unary && refinementOf(t) != nil {
		t = RefinementBase(t)
	}
	return iface, method, t, true
}

// comparedOperand is the operand of the comparison n, whose operands have
// types left and right, whose type answers it: the left one, except where
// the right one is the wider. The right one is wider where it is an
// interface value and the left one is not: the concrete value is held as
// the interface value it is compared with, and `==` and `!=` compare the
// two by their run-time types (spec §12 *Interface values*), on either
// side; an ordering is the interface value's, which has none. It is wider
// too where the left one enters it by `embeds` alone (differsByEmbeds): in
// `circle == shape` the Circle is the Shape it widens into, as the
// operands are checked (checkOperandAgainst), and the two compare as
// Shapes.
func comparedOperand(n *ast.Binary, left, right Type) (ast.Node, Type) {
	if isInterfaceValue(right) && !isInterfaceValue(left) {
		return n.Right, right
	}
	if left != nil && right != nil && !TypesIdentical(resolveTypeVar(left), resolveTypeVar(right)) && differsByEmbeds(right, left) {
		return n.Right, right
	}
	return n.Left, left
}

// isInterfaceValue reports whether t is the type of an interface value:
// an interface or a conjunction of interfaces. The `self` of an interface
// default is none: it is the implementor (defaultSelfParam).
func isInterfaceValue(t Type) bool {
	switch t := resolveTypeVar(ResolveClosed(t)).(type) {
	case *InterfaceType:
		return defaultSelfParam(t) == nil
	case *ConjunctionType:
		return true
	}
	return false
}

// defaultSelfParam is the type parameter t stands for when t is the type
// of a `self` value of an interface default (an InterfaceType marked
// DefaultSelf): the interface's SelfParam, which each instance of the
// default binds to its implementor, as ReplaceTypeParams reads it. Nil for
// any other type.
func defaultSelfParam(t Type) *TypeParam_ {
	if it, ok := t.(*InterfaceType); ok && it.DefaultSelf && it.SelfParam != nil {
		return it.SelfParam
	}
	return nil
}

// operatorUseOf is what the operator n means where it is written: the
// answer interfaceUse gives at its operand's type, with the impl the
// checker selected there as the block: a binary operator's OperatorImpls
// record, at the interface's type arguments it chose, else the block
// implMethodFor finds, which over the enclosing declaration's type
// parameters is the type's one block.
func operatorUseOf(fa *FileAnalysis, n ast.Node) *OperatorUse {
	iface, method, lt, ok := operatorSubject(fa, n)
	if !ok {
		return nil
	}
	t := resolveTypeVar(ResolveClosed(lt))
	args := closedTypeArgs(operatorTypeArgs(fa, n), nil)
	m := fa.implMethodFor(t, iface, method)
	if b, binary := n.(*ast.Binary); binary {
		if impl := fa.OperatorImpls[b]; impl != nil {
			m, args = impl.Method, impl.Args
		}
	}
	use, _ := fa.interfaceUse(iface, method, "", t, args, fa.implBlockHolding(m))
	return use
}

// interfaceUse is how a use of the function method of the interface
// iface (declared by the file of origin) on a value of type recv is
// answered, and the impl block it reaches. It is the one rule a written
// operator (operatorUseOf), an operator at an instance (OperatorUseAt)
// and a call of the interface's function (interfaceImplAt, at the types
// the call is checked with and at an instance's) are answered by, so
// `a == b` and `Equatable.equal(a, b)` agree wherever they are asked. In
// order:
//
//   - A declared type or collection whose type arguments hold a type
//     parameter (`List<T>`, `Box<T>` in a generic body): per instance,
//     as a type parameter is. Each instance answers at its types, where
//     the type's blocks are narrowed as at any other use.
//   - The impl block: chosen, the one the checker selected at the use,
//     else the one block of the interface for recv, at args where recv
//     implements the interface at several type arguments (implBlockFor).
//     The answer is OperatorByImpl whatever recv's shape: a primitive, a
//     List or a Map has std's block, a derived type its derived one.
//   - recv's shape (shapeUse): per instance for a type parameter, by
//     dispatch for an interface value, structurally for a tuple, an
//     anonymous struct, a function value and a collection with no block.
//   - For a declared type with no block of the interface at all, what it
//     meets the interface by all the same (noImplUse).
//
// Nil where none answers: a type with several blocks none of which is
// selected, an unsolved variable, a primitive with no block.
func (fa *FileAnalysis) interfaceUse(iface, method, origin string, recv Type, args []Type, chosen *ast.ImplBlock) (*OperatorUse, *ast.ImplBlock) {
	// A variable the checker closed (`Maybe<?1>` of `Debug.inspect(None)`)
	// is its closed type, at any depth.
	recv = Elaborate(recv)
	t := resolveTypeVar(ResolveClosed(recv))
	if heldPerInstance(t) {
		return &OperatorUse{Form: OperatorPerInstance, Bound: iface}, nil
	}
	b := chosen
	if b == nil && fa != nil {
		b = fa.implBlockFor(recv, iface, origin, args)
	}
	if b != nil {
		return &OperatorUse{Form: OperatorByImpl, Impl: &OperatorImpl{Interface: iface, Receiver: t, Args: args, Method: implBlockMethod(b, method)}}, b
	}
	if use, decided := shapeUse(iface, t); decided {
		return use, nil
	}
	if nominal, _ := nominalReceiver(recv); fa != nil && nominal != nil && len(fa.implBlocksOf(nominal, iface, origin)) == 0 {
		return noImplUse(iface, nominal), nil
	}
	return nil, nil
}

// heldPerInstance reports whether t, a use's receiver type, is a declared
// type or a collection whose type arguments hold a type parameter, whose
// impl each instance of the generic body answers (interfaceUse). A type
// parameter itself, an interface value, a tuple, an anonymous struct and
// a function value are answered by their shape (shapeUse).
func heldPerInstance(t Type) bool {
	switch t.(type) {
	case nil, *TypeParam_, *TypeVar, *PrimitiveType, *InterfaceType, *ConjunctionType, *TupleType, *AnonStructType, *FuncType:
		return false
	}
	return ContainsTypeParam(t)
}

// shapeUse is how a use of iface's function on a value of type t, which
// has no impl block of iface, is answered when t's shape alone decides
// it: nil for a primitive and an unsolved variable; per instance for a
// type parameter, the `self` of an interface default among them; by
// dispatch for an interface value; structurally for a tuple, an anonymous
// struct, a collection and a function value, except BitwiseNot, which
// none has. decided is false for a declared type, whose impls decide
// (interfaceUse).
func shapeUse(iface string, t Type) (use *OperatorUse, decided bool) {
	if p := defaultSelfParam(t); p != nil {
		t = p
	}
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
// has (interfaceUse), at the interface's type arguments there
// (operatorTypeArgs): `a + b` over `L: Add<R, O>` at Day, Days and Day
// reaches Day's `impl Add<Days, Day>`. resolved is false, and the record
// n's own, for any other operator and where a type parameter has no
// answer.
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
	t = resolveTypeVar(Elaborate(t))
	if _, still := t.(*TypeParam_); still {
		return use, false
	}
	at, _ := fa.interfaceUse(use.Bound, method, "", t, closedTypeArgs(operatorTypeArgs(body, n), by), nil)
	return at, true
}

// operatorTypeArgs is the type arguments of the interface the operator
// n goes through, as its operands and result are typed: `R, O` of
// `Add<R, O>` for `a + b` with b of type R and the sum of type O, `O` of
// `BitwiseNot<O>` for `~~~a`. Nil for `==` and the orderings, whose
// interfaces take none.
func operatorTypeArgs(fa *FileAnalysis, n ast.Node) []Type {
	typeOf := func(e ast.Node) Type { return resolveTypeVar(ResolveClosed(fa.ExprTypes[e])) }
	switch n := n.(type) {
	case *ast.Binary:
		if _, ok := operatorInterfaceForOp(n.Op); ok {
			return []Type{typeOf(n.Right), typeOf(n)}
		}
	case *ast.Unary:
		if n.Op == "~~~" {
			return []Type{typeOf(n)}
		}
	}
	return nil
}

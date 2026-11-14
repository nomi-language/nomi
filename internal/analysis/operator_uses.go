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

// OperatorUse is what one binary operator means (FileAnalysis.Operators):
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

// recordOperatorUses records what each binary operator the file's bodies
// hold means, once every body is checked and its variables closed. An
// operator over a primitive has no record: the machine's operation is the
// only one. A declared type whose impls the index holds but cannot narrow
// to one (two files each write one) is left unrecorded, which the
// verifier reports.
func (c *checker) recordOperatorUses() {
	if c.fa == nil || len(c.operatorUses) == 0 {
		return
	}
	p := c.fa.Program()
	for _, n := range c.operatorUses {
		if use := operatorUseOf(p, c.fa, n); use != nil {
			if c.fa.Operators == nil {
				c.fa.Operators = map[*ast.Binary]*OperatorUse{}
			}
			c.fa.Operators[n] = use
		}
	}
	c.operatorUses = nil
}

// operatorUseOf is what the operator n means, nil for an operator over a
// primitive.
func operatorUseOf(p *Program, fa *FileAnalysis, n *ast.Binary) *OperatorUse {
	iface, method, ok := operatorIface(n.Op)
	if !ok {
		return nil
	}
	lt, ok := fa.ExprTypes[n.Left]
	if !ok || lt == nil {
		return nil
	}
	if impl := fa.OperatorImpls[n]; impl != nil {
		return &OperatorUse{Form: OperatorByImpl, Impl: impl}
	}
	t := resolveTypeVar(ResolveClosed(lt))
	switch t.(type) {
	case nil, *PrimitiveType, *TypeVar:
		return nil
	case *TypeParam_:
		return &OperatorUse{Form: OperatorPerInstance, Bound: iface}
	case *InterfaceType, *ConjunctionType:
		return &OperatorUse{Form: OperatorDispatched}
	case *TupleType, *AnonStructType, *ListType, *MapType, *FuncType:
		return &OperatorUse{Form: OperatorStructural}
	}
	name := concreteTypeName(t)
	if name == "" || p == nil {
		return nil
	}
	if m := implMethodOf(p, iface, method, t, name); m != nil {
		return &OperatorUse{Form: OperatorByImpl, Impl: &OperatorImpl{Interface: iface, Receiver: t, Method: m}}
	}
	if iface == "Equatable" && !p.writesImpl(iface, method, t, name) {
		return &OperatorUse{Form: OperatorStructural}
	}
	return nil
}

// writesImpl reports whether the program's impl index holds a function
// method of an impl of iface for concrete, a type named receiver.
func (p *Program) writesImpl(iface, method string, concrete Type, receiver string) bool {
	idx := p.ImplIndex
	if idx == nil {
		return true
	}
	var candidates []*ast.FuncDef
	for _, fn := range idx.IfaceMethodImpls[iface][method] {
		if fn != nil && idx.ReceiverOf(fn) == receiver {
			candidates = append(candidates, fn)
		}
	}
	if len(ownReceiverImpls(idx, candidates, concrete, p.entryOrigin())) > 0 {
		return true
	}
	for _, e := range idx.IfaceMethodImplExterns[iface][method] {
		if e != nil && idx.ImplBlockReceiverExtern[e] == receiver {
			return true
		}
	}
	return false
}

package analysis

import "github.com/nomi-language/nomi/internal/ast"

// Coercion is a value of a concrete type entering an interface value
// (FileAnalysis.Coercions): an argument, a field, an element, a result or
// an annotated binding whose position expects an interface and whose value
// has a type of its own. The IR builder makes the interface value from
// this record (spec §12 *Interface values*).
type Coercion struct {
	// Iface is the interface the value enters, with its type arguments
	// (`Gets<String>`).
	Iface *InterfaceType
	// Value is the value's own type: a concrete type, or a type parameter
	// of the enclosing declaration whose bound the interface is, concrete
	// in each instance.
	Value Type
	// Impl is the impl block by which Value implements Iface where the
	// interface or the value's type has type arguments, so that the type's
	// name alone does not select it (`impl Show for List<Int>` beside `impl
	// Show for List<String>`, `impl Gets<String> for Cup`): the one the
	// checker selects among the type's impls of the interface at Iface's
	// type arguments (implCandidates). Nil otherwise: a type whose name
	// selects its impl and whose value names it at run time, a universal
	// interface (Debug, Struct), a tuple or record whose parts implement
	// it, and a type parameter's bound.
	Impl *ast.ImplBlock
}

// recordCoercions records, once every body of the file is checked and its
// open variables closed, each expression checked against an interface
// type whose own type is concrete, or a type parameter bounded by the
// interface, which each instance makes concrete (Coercions). Two such
// expressions are no coercion and are left out: the receiver of a call of
// an interface function, which the call passes to the impl it reaches
// (coercionReceivers), and a source at an `Iter<T>` position, which the
// callee receives as a view over the source rather than as an interface
// value (isStdIterType). A source has a record of its view instead
// (IterViews), the receiver of `Iter.each_while` and `Iter.known_count`
// among them, and so have a `subtests` statement's rows.
func (c *checker) recordCoercions(nodes []ast.Node) {
	if c.fa == nil {
		return
	}
	c.recordSubtestsViews(nodes)
	if len(c.fa.ExpectedTypes) == 0 {
		return
	}
	receivers := coercionReceivers(c.fa, nodes)
	for n, exp := range c.fa.ExpectedTypes {
		it, ok := resolveTypeVar(exp).(*InterfaceType)
		if !ok {
			continue
		}
		if isStdIterType(it) {
			c.recordIterView(n, it, c.fa.ExprTypes[n])
			continue
		}
		if receivers[n] {
			continue
		}
		t := c.fa.ExprTypes[n]
		switch resolveTypeVar(t).(type) {
		case *InterfaceType, *TypeVar, nil:
			// An interface value is one already, and an unsolved
			// variable's value is never made.
			continue
		}
		if c.fa.Coercions == nil {
			c.fa.Coercions = map[ast.Node]*Coercion{}
		}
		c.fa.Coercions[n] = &Coercion{Iface: it, Value: t, Impl: c.coercionImpl(it, t)}
	}
}

// coercionImpl is the impl block by which a value of type t implements
// it: among t's impls of the interface, the one at it's type arguments,
// or the only one.
func (c *checker) coercionImpl(it *InterfaceType, t Type) *ast.ImplBlock {
	cands := c.implCandidates(resolveTypeVar(t), it.Name, it.Origin)
	for _, cand := range cands {
		if len(it.TypeArgs) > 0 && typesAllEqual(cand.args, it.TypeArgs) {
			return cand.info.Block
		}
	}
	if len(cands) == 1 {
		return cands[0].info.Block
	}
	return nil
}

// coercionReceivers is the expressions of a file, whose top-level
// declarations are nodes, that a call of an interface function passes as
// its receiver: the argument at the function's first `self` parameter
// (interfaceSelfIndex), a pipe's subject when the stage leaves that
// parameter to it, and a membership test's collection (`c` in `x in c`,
// the call `Contains.contains(c, x)`). The checker checks such an argument
// against the interface, and the call reaches the impl of the argument's
// own type: the value never becomes an interface value.
func coercionReceivers(fa *FileAnalysis, nodes []ast.Node) map[ast.Node]bool {
	out := map[ast.Node]bool{}
	selfAt := func(call *ast.Call) int {
		pos, named := calleeKey(call.Func)
		if !named {
			return -1
		}
		sym := fa.References[pos]
		if sym == nil {
			return -1
		}
		base := sym
		for base.Resolved != nil && base.Resolved != base {
			base = base.Resolved
		}
		if base.Kind != SymbolInterfaceMethod {
			return -1
		}
		return interfaceSelfIndex(fa, sym)
	}
	// stages are the pipe stages seen, whose subject fills the first slot.
	stages := map[*ast.Call]ast.Node{}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Call:
				i := selfAt(n)
				if subject, stage := stages[n]; stage {
					if i == 0 {
						out[subject] = true
						break
					}
					i--
				}
				if i >= 0 && i < len(n.Args) {
					out[argValue(n.Args[i])] = true
				}
			case *ast.Binary:
				if n.Op != "|>" {
					break
				}
				if call, ok := ungroupExpr(n.Right).(*ast.Call); ok {
					stages[call] = n.Left
				}
			}
			return true
		})
	}
	for _, call := range fa.Memberships {
		if len(call.Args) > 0 {
			out[argValue(call.Args[0])] = true
		}
	}
	return out
}

// interfaceSelfIndex is the index of the argument a call of the interface
// function sym passes its self in: the function's first parameter of the
// interface's self type. It is -1 when the function takes no self, and 0
// when the interface cannot be found.
func interfaceSelfIndex(fa *FileAnalysis, sym *Symbol) int {
	base := sym
	for base.Resolved != nil && base.Resolved != base {
		base = base.Resolved
	}
	if base.OwningInterface == "" || fa.ModuleScope == nil {
		return 0
	}
	isym := fa.ModuleScope.Lookup(base.OwningInterface)
	for isym != nil && isym.Resolved != nil && isym.Resolved != isym {
		isym = isym.Resolved
	}
	if isym == nil {
		return 0
	}
	it, ok := isym.Type.(*InterfaceType)
	if !ok || it.SelfParam == nil {
		return 0
	}
	for _, m := range it.Methods {
		if m.Name != base.Name {
			continue
		}
		for i, p := range m.Params {
			if p == Type(it.SelfParam) {
				return i
			}
		}
		return -1
	}
	return 0
}

package analysis

import "github.com/nomi-language/nomi/internal/ast"

// OperatorImpl is the impl a binary operator uses at one site
// (FileAnalysis.OperatorImpls).
type OperatorImpl struct {
	// Interface is the interface the operator goes through: Add, Subtract,
	// Multiply, Divide, Equatable or Comparable.
	Interface string
	// Receiver is the left operand's type, and Args the interface's type
	// arguments at it (`Days, Date` of `impl Add<Days, Date> for Date`); nil
	// for Equatable and Comparable, which take none.
	Receiver Type
	Args     []Type
	// Method is the impl's function: `fn add` of the chosen block, a `host
	// fn` for a std scalar, or a derive's synthesized function
	// (IsSynthesizedLine).
	Method ast.ImplMethodDecl
}

// Derived reports an impl a `derive` declaration synthesized.
func (o *OperatorImpl) Derived() bool {
	if o == nil {
		return false
	}
	fd, ok := o.Method.(*ast.FuncDef)
	return ok && IsSynthesizedLine(fd.Line)
}

// recordOperatorImpl notes the impl the operator n uses.
func (fa *FileAnalysis) recordOperatorImpl(n *ast.Binary, impl *OperatorImpl) {
	if fa == nil || n == nil || impl == nil || impl.Method == nil || IsSynthesizedLine(n.Line) {
		return
	}
	if fa.OperatorImpls == nil {
		fa.OperatorImpls = map[*ast.Binary]*OperatorImpl{}
	}
	fa.OperatorImpls[n] = impl
}

// recordArithmeticImpl notes the impl of an arithmetic operator that
// selectOperatorImpl chose.
func (c *checker) recordArithmeticImpl(n *ast.Binary, opIface operatorInterface, left Type, chosen *implCandidate) {
	if chosen == nil || chosen.info == nil {
		return
	}
	method := implBlockMethod(chosen.info.Block, opIface.Method)
	if method == nil {
		return
	}
	c.fa.recordOperatorImpl(n, &OperatorImpl{Interface: opIface.Interface, Receiver: left, Args: chosen.args, Method: method})
}

// recordComparisonImpl notes the Equatable or Comparable impl, hand-written
// or derived, of the type an `==` or `<` compares, when it has one.
func (c *checker) recordComparisonImpl(n *ast.Binary, operand Type, iface, method string) {
	concrete := resolveTypeVar(operand)
	if m := c.fa.implMethodFor(concrete, iface, method); m != nil {
		c.fa.recordOperatorImpl(n, &OperatorImpl{Interface: iface, Receiver: concrete, Method: m})
	}
}

// implMethodFor is the function method of the impl of the interface iface
// that t has: of the block a call of the interface's function on a value
// of t reaches (implBlockFor), a hand-written `fn`, a derived one or a
// `host fn`; a derived Equatable, Hashable, Debug or Display writes none
// (derivesByShape). A
// type over the enclosing declaration's type parameters
// (`Box<T>` in a generic body) has its one block, which is its block at
// every instance; a call there names the interface's function and each
// instance resolves it (CalleeAt), but an operator and a rendering are
// recorded once. Nil when t has no single block of iface, or the block
// does not write method. A type declared inside a body has no block,
// whatever another module declares under its name (implBlocksOf). iface is
// an operator's interface or Display, names the prelude reserves, which no
// program declares twice, so the blocks of the name are the interface's.
func (fa *FileAnalysis) implMethodFor(t Type, iface, method string) ast.ImplMethodDecl {
	return implBlockMethod(fa.implBlockOf(t, iface), method)
}

// implBlockOf is t's impl block of iface that implMethodFor reads: the one
// implBlockFor selects, else t's one block of it. Nil for a nil fa.
func (fa *FileAnalysis) implBlockOf(t Type, iface string) *ast.ImplBlock {
	if fa == nil {
		return nil
	}
	b := fa.implBlockFor(t, iface, "", nil)
	if b == nil {
		if blocks := fa.implBlocksOf(t, iface, ""); len(blocks) == 1 {
			b = blocks[0].Block
		}
	}
	return b
}

// implMethodPos is the position of an impl function's name.
func implMethodPos(m ast.ImplMethodDecl) (int, int) {
	switch n := m.(type) {
	case *ast.FuncDef:
		return n.Line, n.Col
	case *ast.ExternFunc:
		return n.Line, n.Col
	}
	return 0, 0
}

// ImplMethodFile is the home module key of an impl's function (`std/int`,
// `money`, "" for the entry); ok is false when the program does not know
// it.
func (p *Program) ImplMethodFile(m ast.ImplMethodDecl) (string, bool) {
	if p == nil || p.ImplIndex == nil {
		return "", false
	}
	switch n := m.(type) {
	case *ast.FuncDef:
		key, ok := p.ImplIndex.ImplFiles[n]
		return key, ok
	case *ast.ExternFunc:
		key, ok := p.ImplIndex.ImplExternFiles[n]
		return key, ok
	}
	return "", false
}

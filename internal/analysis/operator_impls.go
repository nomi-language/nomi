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
	name := concreteTypeName(concrete)
	if name == "" {
		return
	}
	if m := ImplMethodOf(c.fa.Program().ImplIndex, iface, method, name); m != nil {
		c.fa.recordOperatorImpl(n, &OperatorImpl{Interface: iface, Receiver: concrete, Method: m})
	}
}

// ImplMethodOf is the function method of the one impl of the interface
// iface for the type named receiver: a hand-written one, a derived one or a
// `host fn`. It is nil when there is none or when two files each have one.
func ImplMethodOf(idx *ProjectImplIndex, iface, method, receiver string) ast.ImplMethodDecl {
	if idx == nil {
		return nil
	}
	var fd *ast.FuncDef
	for _, fn := range idx.IfaceMethodImpls[iface][method] {
		if fn == nil || idx.ReceiverOf(fn) != receiver {
			continue
		}
		if fd != nil {
			// The same impl recorded twice, by pointer or by a copy of
			// its file, is one impl.
			if fd == fn || fd.Line == fn.Line && fd.Col == fn.Col && idx.ImplFiles[fd] == idx.ImplFiles[fn] {
				continue
			}
			return nil
		}
		fd = fn
	}
	var ext *ast.ExternFunc
	for _, e := range idx.IfaceMethodImplExterns[iface][method] {
		if e == nil || idx.ImplBlockReceiverExtern[e] != receiver {
			continue
		}
		if ext != nil && ext != e {
			return nil
		}
		ext = e
	}
	switch {
	case fd != nil && ext != nil:
		return nil
	case fd != nil:
		return fd
	case ext != nil:
		return ext
	}
	return nil
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

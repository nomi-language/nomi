package analysis

import "github.com/nomi-language/nomi/internal/ast"

// DefaultedCall is what fills the parameters a call leaves out
// (FileAnalysis.CallDefaults), or a function value's call fills
// (FileAnalysis.DefaultedRefs): the declaration whose defaults they take,
// and which slots.
type DefaultedCall struct {
	// Decl is the declaration whose parameter list holds the defaults: a
	// `fn` (*ast.FuncDef), a `host fn` (*ast.ExternFunc) or an interface
	// function (*ast.InterfaceMethod). A call of an interface function the
	// checker resolved to an impl (DispatchImpl) takes the impl
	// function's, which an inherited default shares with the interface.
	Decl ast.Node
	// Params is Decl's parameter list; a slot's default is
	// Params[slot].Default, an expression of the declaring file.
	Params []ast.Param
	// File is the path of the file that declares Decl, "" for the stdlib
	// and a file with no path.
	File string
	// Slots are the parameters filled from their defaults, ascending.
	Slots []int
	// Ref, for a call through a local name that copies a declared
	// function (`g = parse` then `g("1")`, calleeCopiesADeclaration), is
	// the expression the copy was made from (`parse`): the call goes
	// through the function that name is, which fills the slots. Nil for a
	// call of the declaration by name.
	Ref ast.Node
}

// defaultsDecl is the declaration whose defaults a call through funcNode
// takes, with its symbol, and for a local copy of a declared function the
// expression the copy was made from (DefaultedCall.Ref). ok is false for a
// callee with no declaration behind it (a parameter, a field, a lambda or a
// partial bound to a name, whose parameters the value fills).
func (c *checker) defaultsDecl(funcNode ast.Node) (sym *Symbol, params []ast.Param, ref ast.Node, ok bool) {
	for depth := 0; depth < maxPartialChain; depth++ {
		s, found := c.callFuncSym(funcNode)
		if !found || s == nil || s.Kind == SymbolParam {
			return nil, nil, nil, false
		}
		switch d := s.Node.(type) {
		case *ast.FuncDef:
			return s, d.Params, ref, true
		case *ast.ExternFunc:
			return s, d.Params, ref, true
		case *ast.InterfaceMethod:
			return s, d.Params, ref, true
		case *ast.Binding:
			if _, isIdent := funcNode.(*ast.Ident); !isIdent || s.Kind != SymbolBinding || d.Value == nil {
				return nil, nil, nil, false
			}
			value := ungroupExpr(d.Value)
			if _, named := c.namedFuncParams(value); !named {
				// A copy of a copy follows the chain; anything else (a
				// lambda, a partial) carries its own defaults.
				if _, chained := value.(*ast.Ident); !chained {
					return nil, nil, nil, false
				}
			} else {
				ref = value
			}
			funcNode = value
		default:
			return nil, nil, nil, false
		}
	}
	return nil, nil, nil, false
}

// recordCallDefaults notes the parameters of call that its arguments leave
// to their defaults: args are the written arguments, slots their routing
// (computePositionalSlots or computePipeArgSlots), pipedSlot the slot a
// pipe's subject fills or -1, and params the callee's parameter types. A
// callee with no declaration behind it records nothing (defaultsDecl).
func (c *checker) recordCallDefaults(call *ast.Call, args []ast.Node, slots []int, pipedSlot int, params []Type) {
	if c.fa == nil || call == nil {
		return
	}
	sym, decl, ref, ok := c.defaultsDecl(call.Func)
	if !ok || len(decl) != len(params) {
		return
	}
	names := make([]string, len(decl))
	for i, p := range decl {
		names[i] = p.Name
	}
	filled, _ := filledSlots(args, slots, pipedSlot, params, names)
	var omitted []int
	for i, f := range filled {
		if !f && decl[i].Default != nil {
			omitted = append(omitted, i)
		}
	}
	if len(omitted) == 0 {
		return
	}
	if c.fa.CallDefaults == nil {
		c.fa.CallDefaults = map[*ast.Call]*DefaultedCall{}
	}
	c.fa.CallDefaults[call] = &DefaultedCall{Decl: sym.Node, Params: decl, File: sym.SourceFile, Slots: omitted, Ref: ref}
}

// recordDefaultedRef notes that node, a declared function named as a value
// where a function of its first n parameters is expected
// (defaultFilledFuncValue), is that shorter function, whose call fills the
// rest from their defaults.
func (c *checker) recordDefaultedRef(node ast.Node, n int) {
	if na, ok := node.(*ast.NamedArg); ok {
		node = na.Value
	}
	node = ungroupExpr(node)
	sym, ok := c.callFuncSym(node)
	if !ok || sym == nil {
		return
	}
	var decl []ast.Param
	switch d := sym.Node.(type) {
	case *ast.FuncDef:
		decl = d.Params
	case *ast.ExternFunc:
		decl = d.Params
	case *ast.InterfaceMethod:
		decl = d.Params
	default:
		return
	}
	var slots []int
	for i := n; i < len(decl); i++ {
		slots = append(slots, i)
	}
	if len(slots) == 0 {
		return
	}
	if c.fa.DefaultedRefs == nil {
		c.fa.DefaultedRefs = map[ast.Node]*DefaultedCall{}
	}
	c.fa.DefaultedRefs[node] = &DefaultedCall{Decl: sym.Node, Params: decl, File: sym.SourceFile, Slots: slots}
}

// settleCallDefaults points each recorded call of an interface function
// that the checker resolved to an impl (DispatchImpl) at the impl
// function's parameters, once every body is checked and the dispatch
// records are final: the call fills its omitted parameters from the
// function it reaches. An impl function without a default at a recorded
// slot keeps the interface's.
func (c *checker) settleCallDefaults() {
	for call, rec := range c.fa.CallDefaults {
		if _, iface := rec.Decl.(*ast.InterfaceMethod); !iface || rec.Ref != nil {
			continue
		}
		pos, named := calleeKey(call.Func)
		if !named {
			continue
		}
		ref := c.fa.References[pos]
		if ref == nil {
			continue
		}
		fd, ok := ref.DispatchImpl.(*ast.FuncDef)
		if !ok || len(fd.Params) != len(rec.Params) {
			continue
		}
		own := true
		for _, s := range rec.Slots {
			own = own && fd.Params[s].Default != nil
		}
		if !own {
			continue
		}
		rec.Decl, rec.Params = fd, fd.Params
		if p := c.fa.Program(); p != nil {
			if declaring := p.ScopedFunctionFiles[fd]; declaring != nil {
				rec.File = declaring.FilePath
			}
		}
	}
}

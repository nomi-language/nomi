package analysis

import "github.com/nomi-language/nomi/internal/ast"

// CalleeInterfaceFunc is the std interface's function the call c reaches,
// named as StdFunc names an interface's own function (the interface's
// module, the interface as Owner, the function's name): for a call a
// block answers (CalleeImpl), the function of the interface that block
// implements, whether the block writes, derives or inherits it; for a
// call of an interface's function no block answers (CalleeInterface: by
// shape, by dispatch, per instance), the function itself. The zero
// StdFunc for a function no interface declares and for an interface a
// program's file declares.
func (fa *FileAnalysis) CalleeInterfaceFunc(c Callee) StdFunc {
	var iface StdType
	switch c.Kind {
	case CalleeImpl:
		if b, ok := c.Impl.(*ast.ImplBlock); ok && b != nil {
			iface = fa.implBlockInterface(b, c.Decl)
		}
	case CalleeInterface:
		if c.Owner.Kind == QualInterface {
			iface = c.Owner.Std
		}
	}
	if iface.IsZero() {
		return StdFunc{}
	}
	var name string
	switch d := c.Decl.(type) {
	case *ast.FuncDef:
		name = d.Name
	case *ast.ExternFunc:
		name = d.Name
	case *ast.InterfaceMethod:
		name = d.Name
	}
	if name == "" {
		return StdFunc{}
	}
	return iface.Func(name)
}

// implBlockInterface is the std interface the impl block b implements, by
// the declaration its header resolved to (ImplTypeArgs.Origin), or the
// zero StdType for a block of a program's interface and one no table
// holds. decl, the function of b a call reaches, finds b among its
// receiver's blocks; an inherited default, which b does not declare, is
// found among every block.
func (fa *FileAnalysis) implBlockInterface(b *ast.ImplBlock, decl ast.Node) StdType {
	recv := ""
	switch d := decl.(type) {
	case *ast.FuncDef:
		recv = fa.ImplBlockReceiver[d]
		if p := fa.Program(); recv == "" && p != nil && p.ImplIndex != nil {
			recv = p.ImplIndex.ImplBlockReceiver[d]
		}
	case *ast.ExternFunc:
		recv = fa.ImplBlockReceiverExtern[d]
		if p := fa.Program(); recv == "" && p != nil && p.ImplIndex != nil {
			recv = p.ImplIndex.ImplBlockReceiverExtern[d]
		}
	}
	find := func(byIface map[string][]*ImplTypeArgs) (StdType, bool) {
		for _, blocks := range byIface {
			for _, info := range blocks {
				if info.Block == b {
					return info.Std(), true
				}
			}
		}
		return StdType{}, false
	}
	for _, table := range fa.implBlockTables() {
		if recv != "" {
			if s, found := find(table[recv]); found {
				return s
			}
			continue
		}
		for _, byIface := range table {
			if s, found := find(byIface); found {
				return s
			}
		}
	}
	return StdType{}
}

package analysis

import "github.com/nomi-language/nomi/internal/ast"

// ImplImport is an import of a whole project file that no name in the
// importing file uses. Importing a file also puts its impl blocks into the
// program: an impl block declared in a file nothing imports does not exist.
// So such an import is used when the program uses one of those impl blocks,
// and CheckImplImports decides that once every file is type-checked.
type ImplImport struct {
	UnusedImport
	// Unused is set by CheckImplImports when the program uses none of the
	// file's impl blocks. Until then the import counts as used.
	Unused bool
}

// ProgramFile is one analyzed, type-checked file of a program with its tree.
type ProgramFile struct {
	FA    *FileAnalysis
	Nodes []ast.Node
}

// CheckImplImports settles every file's ImplImports against the whole program
// and returns the unused-import errors, by file. Call it after CheckTypes has
// run on every file in program: the uses it reads are the checker's.
//
// An import of file F is used when the program uses an impl block written in
// F for a type F does not declare. The program uses an impl block when
//
//   - a call in any file but F resolves to one of its functions
//     (`Thing.go(x)`, `Alpha.go(x)`, a dispatch the checker resolved to it),
//     or
//   - any file demands the block's (type, interface) pair: a bound, an
//     interface-typed parameter, an operator, a derive or an interpolation
//     that needs `impl Iface for Type`. This is the ImplManifest the
//     missing-impl check reads, matched by type and interface name.
//
// Impl blocks for a type F declares do not count. A value of that type can
// only be made by naming something in F, so some import names F and F is in
// the program without this one. That also leaves out the Debug impl
// synthesized for every declared type.
//
// Every import of F counts as used once one of F's impls is: which import
// brought F in is not a question the program can answer.
//
// The ImplManifest demands are not attributed to a file (the entry's manifest
// holds every file's), so a demand made inside F for F's own impl counts. A
// call inside F to F's own impl, such as recursion, does not.
func CheckImplImports(program []ProgramFile) map[*FileAnalysis][]TypeError {
	pending := false
	for _, f := range program {
		if f.FA != nil && len(f.FA.ImplImports) > 0 {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	byScope := make(map[*Scope]ProgramFile, len(program))
	for _, f := range program {
		if f.FA != nil && f.FA.ModuleScope != nil {
			byScope[f.FA.ModuleScope] = f
		}
	}
	uses := collectImplUses(program)
	var out map[*FileAnalysis][]TypeError
	for _, f := range program {
		if f.FA == nil {
			continue
		}
		for _, ii := range f.FA.ImplImports {
			target, ok := byScope[ii.module]
			ii.Unused = !ok || !uses.usesImplIn(target)
			if !ii.Unused {
				continue
			}
			if out == nil {
				out = make(map[*FileAnalysis][]TypeError)
			}
			out[f.FA] = append(out[f.FA], unusedImportError(ii.UnusedImport))
		}
	}
	return out
}

type implPair struct {
	typeName, iface string
}

// implUses is what the program's type-checked files use of impl blocks: the
// impl functions calls resolved to, with the files the calls are in, and the
// (type, interface) pairs demanded.
type implUses struct {
	decls map[ast.Node]map[*FileAnalysis]bool
	pairs map[implPair]bool
}

func (u implUses) addDecl(n ast.Node, from *FileAnalysis) {
	if u.decls[n] == nil {
		u.decls[n] = make(map[*FileAnalysis]bool)
	}
	u.decls[n][from] = true
}

func collectImplUses(program []ProgramFile) implUses {
	u := implUses{decls: make(map[ast.Node]map[*FileAnalysis]bool), pairs: make(map[implPair]bool)}
	for _, f := range program {
		fa := f.FA
		if fa == nil {
			continue
		}
		for _, ref := range fa.References {
			if ref == nil {
				continue
			}
			if ref.DispatchImpl != nil {
				u.addDecl(ref.DispatchImpl, fa)
			}
			for r := ref; r != nil; r = r.Resolved {
				if r.Kind != SymbolFunction {
					continue
				}
				switch r.Node.(type) {
				case *ast.FuncDef, *ast.ExternFunc:
					u.addDecl(r.Node, fa)
				}
			}
		}
		for iface, byType := range fa.ImplManifest {
			for typeName := range byType {
				u.pairs[implPair{typeName: typeName, iface: iface}] = true
			}
		}
	}
	return u
}

// usesImplIn reports whether the program uses an impl block that the file
// target declares for a type it does not declare. A call from inside the file
// itself, such as an impl function calling itself, does not count.
func (u implUses) usesImplIn(target ProgramFile) bool {
	nodes := target.Nodes
	declared := make(map[string]bool)
	collectDeclaredTypeNames(nodes, declared)
	for _, n := range nodes {
		block, ok := n.(*ast.ImplBlock)
		if !ok || block.Receiver == nil {
			continue
		}
		recv := TypeExprBaseName(block.Receiver)
		if recv == "" || declared[recv] {
			continue
		}
		for _, item := range block.Items {
			for from := range u.decls[item] {
				if from != target.FA {
					return true
				}
			}
		}
		if block.Interface != nil && u.pairs[implPair{typeName: recv, iface: TypeExprBaseName(block.Interface)}] {
			return true
		}
	}
	return false
}

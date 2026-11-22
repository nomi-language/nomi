package analysis

import "github.com/nomi-language/nomi/internal/ast"

// ImplImport is an import of a whole project file that no name in the
// importing file uses. Importing a file also puts its impl blocks into the
// program: an impl block declared in a file nothing imports does not exist.
// So such an import is used when the program uses one of those impl blocks,
// and CheckImplImports decides that once every file is type-checked. The
// decision is the program's (Program.ImplImportUnused); until it is made the
// import counts as used.
type ImplImport struct {
	UnusedImport
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
// The ImplManifest demands are not attributed to a file (they are the union
// of every file of the program's own), so a demand made inside F for F's own
// impl counts. A call inside F to F's own impl, such as recursion, does not.
//
// It records what it decides on the program (Program.ImplImportUnused) and
// writes no file's analysis. A check reads each file as ImplImportFacts
// keyed by analysis and node; a reduction over stored facts
// (reduceImplImports) reads them keyed by module key and DeclRef. Both
// decide by unusedImports.
func CheckImplImports(program []ProgramFile) map[*FileAnalysis][]TypeError {
	if !implImportsPending(program) {
		return nil
	}
	return newKeptImplUses(program).settle(program)
}

// implImportsPending reports whether a file of program has a whole-file
// import no name uses.
func implImportsPending(program []ProgramFile) bool {
	for _, f := range program {
		if f.FA != nil && len(f.FA.ImplImports) > 0 {
			return true
		}
	}
	return false
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
	// base, when the program's demanders include every file of a stdlib
	// base, holds those files' pairs, which pairs then leaves out.
	base *StdBase
}

// demanded reports whether some file demands pair.
func (u implUses) demanded(pair implPair) bool {
	return u.pairs[pair] || u.base != nil && u.base.pairs[pair]
}

func (u implUses) addDecl(n ast.Node, from *FileAnalysis) {
	if u.decls[n] == nil {
		u.decls[n] = make(map[*FileAnalysis]bool)
	}
	u.decls[n][from] = true
}

// implImportsOf is the whole-file imports of fa no name uses, in order,
// each with the file it imports as target answers it (the zero F for none
// of the program's) and its diagnostic.
func implImportsOf[F comparable](fa *FileAnalysis, target func(*Scope) F) []ImplImportFact[F] {
	out := make([]ImplImportFact[F], 0, len(fa.ImplImports))
	for _, ii := range fa.ImplImports {
		out = append(out, ImplImportFact[F]{Target: target(ii.module), Err: unusedImportError(ii.UnusedImport)})
	}
	return out
}

// implUsesOf passes each function fa's references resolve or dispatch to,
// in no order and perhaps more than once, to use.
func implUsesOf(fa *FileAnalysis, use func(ast.Node)) {
	for _, ref := range fa.References {
		if ref == nil {
			continue
		}
		if ref.DispatchImpl != nil {
			use(ref.DispatchImpl)
		}
		for r := ref; r != nil; r = r.Resolved {
			if r.Kind != SymbolFunction {
				continue
			}
			switch r.Node.(type) {
			case *ast.FuncDef, *ast.ExternFunc:
				use(r.Node)
			}
		}
	}
}

// foreignImplsOf is the impl blocks of the file analyzed as fa with nodes
// for a type it does not declare, their functions keyed by key.
func foreignImplsOf[K comparable](fa *FileAnalysis, nodes []ast.Node, key func(ast.Node) K) []ForeignImplFact[K] {
	declared := make(map[string]bool)
	collectDeclaredTypeNames(nodes, declared)
	var out []ForeignImplFact[K]
	for _, n := range nodes {
		block, ok := n.(*ast.ImplBlock)
		if !ok || block.Receiver == nil {
			continue
		}
		recv := TypeExprBaseName(block.Receiver)
		if recv == "" || declared[recv] {
			continue
		}
		fi := ForeignImplFact[K]{Recv: recv, HasInterface: block.Interface != nil}
		if block.Interface != nil {
			fi.Iface = implInterfaceName(fa, block.Interface)
		}
		for _, item := range block.Items {
			fi.Items = append(fi.Items, key(item))
		}
		out = append(out, fi)
	}
	return out
}

// unusedImports decides each of imports, one file's whole-file imports no
// name uses. One is unused when it imports none of the program's files
// (foreignOf answers false for its target), or when the program uses none
// of the impl blocks its file writes for a type it does not declare
// (foreignOf's answer). The program uses such a block when a file other
// than the block's own calls one of its functions (usedBy holds the files
// whose references reach each function), or demands its (type, interface)
// pair.
func unusedImports[F, K comparable](imports []ImplImportFact[F], foreignOf func(F) ([]ForeignImplFact[K], bool), usedBy map[K]map[F]bool, demanded func(implPair) bool) []bool {
	usesImplIn := func(target F, foreign []ForeignImplFact[K]) bool {
		for _, block := range foreign {
			for _, item := range block.Items {
				for from := range usedBy[item] {
					if from != target {
						return true
					}
				}
			}
			if block.HasInterface && demanded(implPair{typeName: block.Recv, iface: block.Iface}) {
				return true
			}
		}
		return false
	}
	out := make([]bool, len(imports))
	for i, ii := range imports {
		foreign, ok := foreignOf(ii.Target)
		out[i] = !ok || !usesImplIn(ii.Target, foreign)
	}
	return out
}

package analysis

import (
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
)

// Program is what one build knows about the whole program rather than about
// one file: the impl index, the impl-method tables of the build's own files,
// the `once` table, the entry boots and application types, and the stdlib's
// module scopes. Every file a build analyzes holds a pointer to the same
// Program (FileAnalysis.Program).
//
// A build hands its files their Program only once the Program is complete,
// every identity pass and impl type-argument template included, so before any
// CheckTypes runs. Nothing writes a Program's fields or maps after that. The
// passes inside a build that need part of it earlier (the builder, BuildTypes,
// CheckDeriveBounds, the boot signature check) are given what they read as
// arguments.
//
// The one part that changes after that is Onces' inference state, and only
// in the phase the program's first CheckTypes runs before it checks
// anything: every unannotated `once` gets its type there, in a fixed order,
// and the table is read-only from then on. See OnceTable.
type Program struct {
	// ImplIndex is the impl index of every file the program reaches, the
	// stdlib's included. Nil for a file analyzed on its own with no stdlib
	// index attached.
	ImplIndex *ProjectImplIndex

	implTables

	// Onces is the program's module-level `once` bindings.
	Onces *OnceTable

	// ScopedFunctionFiles maps every function of the build's own files to
	// the analysis of the file that declares it, and BootFunctions holds
	// every entry boot (a top-level `fn boot` in a file that defines
	// `fn main`), so the application-field root check can follow calls
	// across files (app_fields.go). AppTypes are the structs those boots
	// return: `T.field` is an application-field read exactly when T is one
	// of them.
	ScopedFunctionFiles map[*ast.FuncDef]*FileAnalysis
	BootFunctions       map[*ast.FuncDef]bool
	AppTypes            []*StructType

	// ContextType is the canonical std/context.Context identity.
	ContextType Type
	// StdlibModuleScopes maps each stdlib module's short name ("iter",
	// "maps", ...) to its defining module Scope. Stdlib symbols carry no
	// SourceFile, so this is how a pass finds the scope that defines a
	// stdlib symbol (the iter-sensitivity pass, derive synthesis, the IR
	// builder).
	StdlibModuleScopes map[string]*Scope

	// files is every file the program reaches, the stdlib's analyses
	// included, in module-key order: the files whose own facts the
	// program-wide reductions fold. filesByKey holds them by module key.
	files      []*FileAnalysis
	filesByKey map[string]*FileAnalysis

	// unindexedImpls is the union of the Impls of the program's files that
	// ImplIndex does not fold: a project build's modules a function body's
	// import loaded after the index was built, and a file analyzed on its own
	// with the user modules its imports loaded. A file holds only its own
	// impls, so this is how such a file sees another's.
	unindexedImpls map[string]map[string]bool

	// results holds what the checks that run once every file is checked
	// decide about the program, and only that: no file's analysis records
	// it. Those checks write it; nothing else does.
	results *programResults
}

// programResults are the program-wide decisions made after every file is
// checked.
type programResults struct {
	// implImportsUnused holds each whole-file import CheckImplImports
	// settled, and whether the program uses none of its file's impl blocks.
	implImportsUnused map[*ImplImport]bool
}

// newProgramResults answers an empty set of results.
func newProgramResults() *programResults {
	return &programResults{implImportsUnused: map[*ImplImport]bool{}}
}

// settleImplImport records CheckImplImports' decision on ii.
func (p *Program) settleImplImport(ii *ImplImport, unused bool) {
	if p.results == nil {
		return
	}
	p.results.implImportsUnused[ii] = unused
}

// ImplImportUnused reports whether CheckImplImports found that the program
// uses none of the impl blocks of the file ii imports. An import it has not
// settled counts as used.
func (p *Program) ImplImportUnused(ii *ImplImport) bool {
	return p.results != nil && p.results.implImportsUnused[ii]
}

// unionImpls answers the union of files' own Impls, or nil when they have
// none.
func unionImpls(files []*FileAnalysis) map[string]map[string]bool {
	var out map[string]map[string]bool
	for _, fa := range files {
		for typeName, ifaces := range fa.Impls {
			if out == nil {
				out = make(map[string]map[string]bool)
			}
			if out[typeName] == nil {
				out[typeName] = make(map[string]bool, len(ifaces))
			}
			for iface := range ifaces {
				out[typeName][iface] = true
			}
		}
	}
	return out
}

// HasImpl reports whether an impl fa can see supplies (typeName, iface), by
// bare name: one fa declares, one the program's impl index holds, or one of
// a file of the program the index does not fold.
func (fa *FileAnalysis) HasImpl(typeName, iface string) bool {
	if fa == nil {
		return false
	}
	if fa.Impls[typeName][iface] {
		return true
	}
	p := fa.Program()
	return (p.ImplIndex != nil && p.ImplIndex.Impls[typeName][iface]) || p.unindexedImpls[typeName][iface]
}

// ImplementedInterfaces answers, sorted, every interface HasImpl sees an impl
// of for typeName.
func (fa *FileAnalysis) ImplementedInterfaces(typeName string) []string {
	if fa == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ifaces := range fa.implsOf(typeName) {
		for iface := range ifaces {
			if !seen[iface] {
				seen[iface] = true
				out = append(out, iface)
			}
		}
	}
	sort.Strings(out)
	return out
}

// implsOf answers the impl tables HasImpl reads, each narrowed to typeName.
func (fa *FileAnalysis) implsOf(typeName string) []map[string]bool {
	p := fa.Program()
	out := []map[string]bool{fa.Impls[typeName]}
	if p.ImplIndex != nil {
		out = append(out, p.ImplIndex.Impls[typeName])
	}
	return append(out, p.unindexedImpls[typeName])
}

// setFiles records byKey as the program's files.
func (p *Program) setFiles(byKey map[string]*FileAnalysis) {
	p.filesByKey = byKey
	p.files = filesInKeyOrder(byKey)
}

// File answers the analysis of the program's file with module key key ("std/iter",
// "shapes"), or nil.
func (p *Program) File(key string) *FileAnalysis {
	return p.filesByKey[key]
}

// Files answers every file the program reaches, the stdlib's included, in
// module-key order.
func (p *Program) Files() []*FileAnalysis {
	return p.files
}

// implTables are the impl-method tables of a build's own files.
//
// IfaceMethodImpls indexes every `impl Iface for T { fn method(...) }` method:
// interface name → method name → the impl FuncDefs. Unlike
// ProjectImplIndex.IfaceMethodImpls it holds no shared stdlib file's impls,
// except in the stdlib's own build. IfaceMethodImplFiles maps each of those
// FuncDefs to the module key of its file ("foo", "sub/log"; "" for the
// entry), which the LSP's `textDocument/implementation` turns back into a
// path.
//
// ImplBlockReceiver and ImplBlockInterfaceKey give each impl-block item
// FuncDef the receiver base name and interface key from its block header. A
// file analyzed on its own holds its own maps here
// (FileAnalysis.ImplBlockReceiver).
//
// The Extern maps are the `host fn` parallels, for go-to-definition and hover
// on an extern interface impl.
type implTables struct {
	IfaceMethodImpls            map[string]map[string][]*ast.FuncDef
	IfaceMethodImplFiles        map[*ast.FuncDef]string
	ImplBlockReceiver           map[*ast.FuncDef]string
	ImplBlockInterfaceKey       map[*ast.FuncDef]string
	IfaceMethodImplExterns      map[string]map[string][]*ast.ExternFunc
	IfaceMethodImplExternFiles  map[*ast.ExternFunc]string
	ImplBlockInterfaceKeyExtern map[*ast.ExternFunc]string
}

// fileImplTables are the tables of one file's own impl blocks, as the
// builder recorded them.
func fileImplTables(fa *FileAnalysis) implTables {
	return implTables{
		ImplBlockReceiver:           fa.ImplBlockReceiver,
		ImplBlockInterfaceKey:       fa.ImplBlockInterfaceKey,
		ImplBlockInterfaceKeyExtern: fa.ImplBlockInterfaceKeyExtern,
	}
}

// noProgram is the Program of an analysis that was given none: every field
// is empty. It is never written.
var noProgram = &Program{}

// Program answers the program fa belongs to, or an empty one when fa was
// given none.
func (fa *FileAnalysis) Program() *Program {
	if fa == nil || fa.program == nil {
		return noProgram
	}
	return fa.program
}

// standaloneProgram is the Program of a file analyzed on its own: its own
// impl-block tables and `once` bindings, the stdlib it was built against, and
// impls (nil, or an index over the stdlib). Its files are fa and the modules
// its build loaded (loaded, by module key).
func standaloneProgram(fa *FileAnalysis, nodes []ast.Node, impls *ProjectImplIndex, stdlibScopes map[string]*Scope, contextType Type, loaded map[string]*FileAnalysis) *Program {
	onces := NewOnceTable()
	onces.Index(fa, nodes)
	files := map[string]*FileAnalysis{"": fa}
	users := []*FileAnalysis{fa}
	for key, file := range loaded {
		if file != fa {
			files[key] = file
			if !IsStdlibKey(key) {
				users = append(users, file)
			}
		}
	}
	p := &Program{
		ImplIndex:          impls,
		implTables:         fileImplTables(fa),
		Onces:              onces,
		ContextType:        contextType,
		StdlibModuleScopes: stdlibScopes,
		// The user modules' impls. The stdlib's are not among them: an
		// index over the stdlib, when one is attached, holds those.
		unindexedImpls: unionImpls(users),
		results:        newProgramResults(),
	}
	p.setFiles(files)
	return p
}

// filesInKeyOrder answers the distinct analyses of byKey in module-key order.
func filesInKeyOrder(byKey map[string]*FileAnalysis) []*FileAnalysis {
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*FileAnalysis, 0, len(keys))
	seen := make(map[*FileAnalysis]bool, len(keys))
	for _, key := range keys {
		if file := byKey[key]; file != nil && !seen[file] {
			seen[file] = true
			out = append(out, file)
		}
	}
	return out
}

// implTablesOf answers the impl-method tables fa's impls are listed in: own
// for a file of the build being assembled (owned), fa's Program's for a file
// another build analyzed, and fa's own impl blocks' for a file with no
// Program.
func implTablesOf(fa *FileAnalysis, own *implTables, owned map[*FileAnalysis]bool) *implTables {
	if owned[fa] && own != nil {
		return own
	}
	if fa.program != nil {
		return &fa.program.implTables
	}
	tables := fileImplTables(fa)
	return &tables
}

package analysis

import "github.com/nomi-language/nomi/internal/ast"

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
// impls (nil, or an index over the stdlib).
func standaloneProgram(fa *FileAnalysis, nodes []ast.Node, impls *ProjectImplIndex, stdlibScopes map[string]*Scope, contextType Type) *Program {
	onces := NewOnceTable()
	onces.Index(fa, nodes)
	return &Program{
		ImplIndex:          impls,
		implTables:         fileImplTables(fa),
		Onces:              onces,
		ContextType:        contextType,
		StdlibModuleScopes: stdlibScopes,
	}
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

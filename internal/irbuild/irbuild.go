// Package irbuild builds the IR the VM runs: it takes a checked Nomi program
// (Analyze, AnalyzeSource, AnalyzeVirtual — the same front end `nomi run`
// runs) and lowers every body it can into `ir.Func`s (GenerateIR). `nomi run`,
// `nomi test`, the REPL and the tour execute the result on internal/vm
// through vmhost.
//
// # The builder's Go-shaped representation
//
// Each module is walked by a `gen` that assigns every expression a `kind`
// (the builder's type, which names a Go type) and an `expr` whose `code`
// field is a Go spelling; IR nodes are built beside those spellings. Nothing
// prints, compiles or returns that text. Removing it means replacing `kind`
// and `expr` with the checker's solved types and IR temporaries throughout
// the builder, which is its own piece of work.
//
// # The stdlib
//
// The standard library is lowered once per process (stdlibLowering), and a
// program's IR links every cached stdlib module (Result.IRModules).
//
// # Correctness
//
// The IR is checked by what the VM does with it, against the golden files in
// testdata/expectations (see internal/expectation), and every
// retained graph must pass ir.LintModule.
package irbuild

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
)

// Module is one Nomi module's checked AST, the unit of codegen.
type Module struct {
	// Path is the .nomi file the module was checked from, absolute. Used to
	// blame Nomi source in diagnostics.
	Path string
	// Name is the module-relative Nomi name ("main", "tools/seed").
	Name  string
	Nodes []ast.Node
	// FA is the analysis that produced Nodes, and it is here for exactly one
	// reason: the checked AST carries the annotations the programmer WROTE,
	// while the checker also SOLVED types that appear nowhere in the tree.
	// The one this builder needs is an unannotated lambda parameter's type,
	// which checkLambdaExpecting solves from the expected FuncType and
	// attachParamType records on the SymbolParam in FA.Definitions — the same
	// fact LSP hover renders. See inferred.go for the projection, and for why
	// this is a lookup rather than a second inference pass.
	//
	// Nil when the front end ran without an analysis library (bare-nodes
	// mode), in which case every inferred lookup misses and the builder
	// refuses exactly what it refused before this field existed.
	FA *analysis.FileAnalysis
	// DeclaresTests marks the module whose `test` declarations RUN. It is the
	// entry and only the entry: `nomi test <file>` collects that file's cases
	// and no others, so a test declaration anywhere else is refused rather
	// than lowered into a table nothing reads.
	DeclaresTests bool
}

// Program is a checked Nomi program. Modules[0] is the entry.
type Program struct {
	Modules []Module
	// HasTests records that the entry declares tests, which decides both what
	// the program IS — a test suite rather than a program — and what its
	// reference behaviour is: `nomi test`, not `nomi run`. The two questions
	// must have one answer.
	HasTests bool
	// Root is the project root the front end resolved this program's imports
	// against. Carried because std/compiler's hosts have to reproduce that
	// resolution at RUN time for the source `compiler.check` is handed, and
	// the machine has no entry file to rediscover it from. For an in-memory
	// program it is the root the host was configured with, or empty.
	Root string
	// VirtualFiles are the in-memory sibling sources an in-memory program was
	// analyzed with (frontend.Config.VirtualFiles), or nil. std/compiler's hosts
	// resolve a checked or run source's imports against them.
	VirtualFiles map[string]string
}

// Entry returns the entry module.
func (p *Program) Entry() *Module { return &p.Modules[0] }

// Analyze runs the front end (internal/frontend) on
// entryPath and returns the checked AST without evaluating it.
//
// The Program holds EVERY user file in the entry's import graph, one Module
// each, and that is what makes a call to a free function in a sibling file
// lowerable at all — see siblings.go. Stdlib files are not among them: they
// reach the builder through stdlib.go.
//
// An imported file is NOT inert, which is why every one of them is carried
// rather than only the ones the entry calls into: its `impl` blocks register
// for dispatch and its `once` bindings initialize.
func Analyze(entryPath string) (*Program, error) {
	return AnalyzeFile(entryPath, frontend.Config{})
}

// AnalyzeFile is Analyze with the host's front-end configuration: cfg.Provided
// names the `host fn` keys the host's tables answer.
//
// Source-bound declarations (`go alias.Symbol`, inline `go { }`) are always
// treated as provided here: the VM reaches them through adapters the FFI
// wrapper generates, never by looking a key up in a registry, so a missing
// registration is not something this front end can see or needs to.
func AnalyzeFile(entryPath string, cfg frontend.Config) (*Program, error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, err
	}
	// Which mode to run is decided before running one, because the two modes
	// disagree about attached `//!` tests: the test path keeps and type-checks
	// them, `nomi run` strips them, so the analyzed tree cannot be asked which
	// mode produced it. Parse errors fall through to the chosen mode so the
	// reported text is its own.
	hasTests, _ := frontend.FileDeclaresTests(abs)
	cfg.SourceBoundProvided = true
	fe := frontend.New(cfg)
	proj, err := fe.CheckFile(abs, frontend.Mode{Tests: hasTests})
	if err != nil {
		return nil, err
	}
	return fileProgram(abs, proj, hasTests), nil
}

// AnalyzeFileSource is AnalyzeFile for the file at entryPath whose text is
// src rather than what is on disk: the language server's open buffer.
func AnalyzeFileSource(entryPath, src string, cfg frontend.Config) (*Program, error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, err
	}
	hasTests, _ := frontend.SourceDeclaresTests(src)
	cfg.SourceBoundProvided = true
	proj, err := frontend.New(cfg).CheckFileSource(abs, src, frontend.Mode{Tests: hasTests})
	if err != nil {
		return nil, err
	}
	return fileProgram(abs, proj, hasTests), nil
}

// fileProgram is the Program of a checked file entry at abs.
func fileProgram(abs string, proj *frontend.Project, hasTests bool) *Program {
	name := strings.TrimSuffix(filepath.Base(abs), ".nomi")
	mods := make([]Module, 0, len(proj.Files)+1)
	mods = append(mods, Module{Path: abs, Name: name, Nodes: proj.Nodes, FA: proj.FA})
	for _, f := range proj.Files {
		// A sibling that imports the ENTRY back appears in the graph under
		// its own key; the entry is Modules[0], so the duplicate is dropped.
		if f.Path != "" && f.Path == abs {
			continue
		}
		path := f.Path
		if path == "" {
			path = f.Key + ".nomi"
		}
		// A sibling's tail calls are marked here, because the entry pipeline
		// marks only the entry's (frontend.Checker.Analyze). Without it a
		// sibling's tail-recursive function formed no tail plan. The pass is
		// idempotent. See siblingtailmark_test.go.
		analysis.MarkTailCalls(f.Nodes)
		mods = append(mods, Module{Path: path, Name: f.Key, Nodes: f.Nodes, FA: f.FA})
	}
	return &Program{Modules: mods, HasTests: hasTests, Root: proj.Root}
}

// AnalyzeSource is Analyze for an in-memory module, with the same front-end
// mode choice.
func AnalyzeSource(name, src string) (*Program, error) {
	hasTests, _ := frontend.SourceDeclaresTests(src)
	proj, err := frontend.New(frontend.Config{}).CheckSource(name, src, frontend.Mode{Tests: hasTests})
	if err != nil {
		return nil, err
	}
	return &Program{
		Modules:  []Module{{Path: name + ".nomi", Name: name, Nodes: proj.Nodes, FA: proj.FA}},
		HasTests: hasTests,
	}, nil
}

// AnalyzeVirtual is Analyze for an in-memory entry whose sibling files, if any,
// are virtual (cfg.VirtualFiles, cfg.VirtualManifest). It is the front end the
// tour playground and the REPL run, neither of which has a filesystem to read
// a sibling from. Every sibling becomes a Module, as Analyze makes one per
// file.
func AnalyzeVirtual(name, src string, cfg frontend.Config) (*Program, error) {
	hasTests, _ := frontend.SourceDeclaresTests(src)
	fe := frontend.New(cfg)
	proj, err := fe.CheckSource(name, src, frontend.Mode{Tests: hasTests})
	if err != nil {
		return nil, err
	}
	entry := name + ".nomi"
	mods := []Module{{Path: entry, Name: name, Nodes: proj.Nodes, FA: proj.FA}}
	for _, f := range proj.Files {
		if f.Key == name {
			continue
		}
		path := f.Path
		if path == "" {
			path = f.Key + ".nomi"
		}
		// Analyze's reason: a sibling's nodes did not come through the entry
		// pipeline that marks tail calls.
		analysis.MarkTailCalls(f.Nodes)
		mods = append(mods, Module{Path: path, Name: f.Key, Nodes: f.Nodes, FA: f.FA})
	}
	return &Program{Modules: mods, HasTests: hasTests, Root: proj.Root, VirtualFiles: fe.VirtualFiles()}, nil
}

// Result is a program lowered to IR.
type Result struct {
	// HasMain reports whether the program defines `fn main`. A program
	// without one runs and exits 0.
	HasMain bool
	// IR is the `ir.Module` each compilation unit that retained a function
	// produced, in unit order. It is the SECOND CONSUMER's input, and it is
	// on the Result rather than behind a second entry point because the two
	// consumers must read what ONE lowering built: a `BuildIR` that re-ran
	// the front end and the producer would be a second lowering, and "the
	// two backends read the same graph" would then be a claim about two
	// graphs that happened to agree.
	//
	// A unit that retained nothing contributes no entry, because
	// `gen.irModule` mints the container lazily — `irtable.go`'s reason, "a
	// container nobody asked for is a map nobody reads".
	// Use IRModules for execution with the compilation's stdlib dependencies.
	IR []*ir.Module
	// irLibraries shares immutable graphs with the process-cached stdlib index.
	irLibraries []*ir.Module
	// irHostKeys are the extern keys the program's Go-bound `host fn` bodies
	// cross under: bindings only a host that linked the project's Go
	// packages can supply. See irhostfn.go.
	irHostKeys map[string]bool
}

// IRModules returns the graphs needed to link this compilation, including the
// cached stdlib modules lowered beside its user units. IR itself retains the
// user-unit view for entry selection and user-body coverage measurements.
func (r *Result) IRModules() []*ir.Module {
	mods := make([]*ir.Module, 0, len(r.IR)+len(r.irLibraries))
	mods = append(mods, r.IR...)
	return append(mods, r.irLibraries...)
}

// HostKeys are the extern keys the program's Go-bound `host fn` bodies cross
// under, sorted: what a runner's host tables must answer. ir.Image.HostKeys
// holds the same list.
func (r *Result) HostKeys() []string {
	keys := make([]string, 0, len(r.irHostKeys))
	for k := range r.irHostKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GenerateIR lowers p to IR for the VM: `nomi run`, `nomi test`, the REPL and
// the tour all run what it retains on internal/vm.
//
// It needs no filesystem, so it runs in a browser. A construct the builder
// does not lower does not fail the call: the VM runs only what was retained
// and checks that itself (vm.Machine.Unretained). The refusal the lowering
// recorded is returned beside the Result so a caller can still say it
// happened.
func GenerateIR(p *Program) (res *Result, refusal error, err error) {
	if p == nil || len(p.Modules) == 0 {
		return nil, nil, fmt.Errorf("irbuild: no modules to generate")
	}
	var unsupported []Unsupported
	// masked rides beside unsupported for the whole program, because the
	// blocker set the report reads is the whole program's and so is its error
	// bar: a refusal in a SIBLING file is what masks the entry's type-shaped
	// blockers. See suppression.go.
	var masked []Suppression
	hasMain := false
	// The lowered standard library, built once for the process.
	std := stdlibLowering()
	// Every type declaration in the program, keyed by the node that declared
	// it and named program-uniquely, so one module can name both its own
	// `Point` and a sibling file's. See foreign.go.
	reg := buildTypeRegistry(p)
	// The program's own files as each other's call sites see them, indexed
	// before any of them is lowered: whether a cross-file reference closes an
	// import cycle is a whole-program property. See siblings.go.
	files := buildFileIndex(p, reg)
	// Three declaration waves before any body is lowered, and the order is forced
	// rather than chosen. A signature in file B may name a type declared in
	// file A while A's names one of B's, and Nomi has no forward-declaration
	// rule in either direction — so no per-file order satisfies both, and the
	// program has to finish every file's TYPES before it types any file's
	// signatures. See foreign.go.
	gens := make([]*gen, len(p.Modules))
	for i := range p.Modules {
		p.Modules[i].DeclaresTests = i == 0 && p.HasTests
		gens[i] = newGen(&p.Modules[i], reg.pkgOf[i], std, files, i, reg)
	}
	reg.gens = gens
	// One table of generic stdlib instances for the whole program, so a
	// sibling file's instances are the entry's too. See stdinst.go.
	insts := newStdInstances(std)
	for _, g := range gens {
		g.stdInsts = insts
	}
	for i := range gens {
		reg.ensureTypes(i)
	}
	files.resolveSignatures(gens)
	for _, g := range gens {
		g.declareFuncs()
	}
	// Every impl block in the program, by the declaration of the type it is
	// for, in the same window and for the same reason as the index below: a
	// type-qualified call in one file may name an impl function declared in
	// another. See siblingimpl.go.
	files.resolveImplMembers(gens)
	// Every file's impls are registered now and no body has been lowered yet,
	// which is the one window in which "does this PROGRAM implement I for T?"
	// is both answerable and needed: a boxing site in one file dispatches
	// through a table another file bound into. See existential.go.
	impls := buildImplIndex(gens)
	for _, g := range gens {
		g.impls = impls
	}
	for i := range p.Modules {
		errs, hidden := gens[i].emitModule(&p.Modules[i])
		unsupported = append(unsupported, errs...)
		masked = append(masked, hidden...)
		if i == 0 {
			hasMain = definesMain(&p.Modules[i])
		}
	}
	if len(unsupported) > 0 {
		refusal = newUnsupportedError(unsupported, masked)
	}
	// The VM links by IR symbol, so it links every cached stdlib module, in
	// package order.
	stdPkgs := make([]string, 0, len(std.irModules))
	for pkg := range std.irModules {
		stdPkgs = append(stdPkgs, pkg)
	}
	sort.Strings(stdPkgs)
	irLibraries := make([]*ir.Module, 0, len(stdPkgs))
	for _, pkg := range stdPkgs {
		irLibraries = append(irLibraries, std.irModules[pkg])
	}
	// The IR each unit retained, in unit order. The entry unit's module
	// records the program boot and its `tests` group boots, retained or not.
	// A unit that retained nothing has no module, and nothing to start. The
	// VM-only test bodies are built last.
	for _, g := range gens {
		g.irRetryWalkOnlyTestBodies()
	}
	// A generic function instance one file asked another for after that
	// file's walk ended. See siblinggeneric.go.
	irFlushLateInstances(gens)
	var irMods []*ir.Module
	hostKeys := map[string]bool{}
	for _, g := range gens {
		for _, key := range g.irHostKeys {
			hostKeys[key] = true
		}
		if g.irMod != nil {
			irMods = append(irMods, g.irMod)
		}
	}
	// The program's stdlib instances link beside the cached modules and are
	// never written into the cache.
	instMods := insts.modules()
	for _, m := range instMods {
		irLintFinished(m)
	}
	irLibraries = append(irLibraries, instMods...)
	// Before the impl entries, which name only bodies that survive it.
	irWithdrawUnforceableReads(irMods, irLibraries)
	for i, g := range gens {
		if g.irMod != nil {
			if fd := g.bootDecl(); i == 0 && fd != nil {
				g.irMod.SetBoot(g.irCalleeSym(fd, fd.Name))
			}
			for _, sym := range g.irTestBoots {
				g.irMod.AddTestBoot(sym)
			}
			g.irRecordImpls()
		}
	}
	for _, m := range irMods {
		irLintFinished(m)
	}
	return &Result{HasMain: hasMain,
		IR: irMods, irLibraries: irLibraries, irHostKeys: hostKeys}, refusal, nil
}

// definesMain reports whether the module declares a zero-parameter `fn main`.
func definesMain(m *Module) bool {
	for _, n := range m.Nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == "main" && !fd.ImplFunction && len(fd.Params) == 0 {
			return true
		}
	}
	return false
}

// unitPackage is the Go package name for the i'th Nomi module. Positional
// rather than derived from the module name: Nomi module paths contain
// separators and can collide after sanitizing, and the package name is not a
// user-visible surface. It still qualifies the builder's Go-spelled names.
func unitPackage(i int) string { return fmt.Sprintf("nomimod%d", i) }

// rtModulePath is the import path of the runtime library. The builder compares a Go type's reflect PkgPath against
// it to recognise an rt type, and a host function's package against it to
// recognise an rt crossing.
const (
	rtModulePath = "github.com/nomi-language/nomi/rt"
)

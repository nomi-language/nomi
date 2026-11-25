package irbuild

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
)

// Module is one module's checked AST, the unit of lowering.
type Module struct {
	// Path is the .nomi file the module was checked from, absolute. Used to
	// blame Nomi source in diagnostics.
	Path string
	// Name is the package-relative Nomi name ("main", "tools/seed").
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
	// twins maps a declaration of the shared stdlib analysis to this
	// module's own, when this module is a std module checked from edited
	// source (programstd.go's implFuncTwins). Nil otherwise.
	twins map[ast.Node]ast.Node
	// bodies marks, in a lowering of one session file, an earlier file
	// its check read with its bodies: its gen lowers the instances of its
	// generic declarations the file asks for and the earlier lowerings did
	// not make (session.go).
	bodies bool
	// LinesAbove is how many lines of the file its author did not write
	// above what they did: a REPL session writes imports and declarations
	// above each input. Every position the module lowers is a line of what
	// the author wrote (ir.Pos.Below), so a `dbg`, a `todo`, a failed
	// assertion and a trap name the line the author typed. 0 for a file
	// read as written.
	LinesAbove int
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
	// ErrorDecls is each declaration whose check reported an error, in a
	// program the front end refused (frontend.Project.ErrorDecls): a
	// function, an impl function, a test or a group. The builder leaves
	// each unlowered, attempting nothing in it, and lowers the rest, so an
	// editor can show the effects of what checked. Nil for a program that
	// checked.
	ErrorDecls map[ast.Node]bool
	// Stop, when set, reports that the lowering's caller no longer wants
	// it. GenerateIR asks before each phase, and in each per-file phase
	// before each file, and answers ErrStopped once it reports true.
	Stop func() bool
	// Declines observe the lowering's declines. GenerateIR hands them to
	// this lowering's census alone, so a host lowering two programs at once
	// sees each program's declines in its own hooks.
	Declines DeclineHooks
	// halted records that Stop reported true: a phase that stopped part
	// way leaves its work unfinished, and every later question answers
	// true.
	halted bool
	// stdCases, when set, lowers a shared std module's `//!` cases against
	// the program, whose modules are the listed std modules the cases import
	// (stdtests.go's generateStdlibTestIRWithProgramStd). GenerateIR calls
	// it once every module's bodies are lowered, and links the gen it
	// answers first.
	stdCases func(b *programBuild) *gen
	// noLinkStd lowers every listed std module the program reaches with
	// it rather than linking a kept lowering (programstdlink.go): set on
	// the program that makes the kept lowering.
	noLinkStd bool
	// linkAllStd links every module of the program, the entry included,
	// to the kept lowering of the listed std modules: each is a listed
	// module as the shared analysis holds it, and only what stdCases
	// lowers is the program's own (a reference editor's case,
	// GenerateStdlibReferenceIR).
	linkAllStd bool
	// objects, when set, holds the lowering of every module of the
	// program but the first: their gens are linked to it as a listed std
	// module's are to the kept lowering, and only the first module's bodies
	// are lowered (perfile.go).
	objects *linkedStd
	// perFile, set with objects, is what the lowering of one file is given
	// of the program it belongs to (perfile.go).
	perFile *perFileState
	// interfaces answers the program's files as interfaces, against which
	// each can be checked on its own, for the per-file object harness
	// (perfileharness.go); nil for a program the front end did not check
	// from files.
	interfaces func() *analysis.ProgramInterfaces
}

// programBuild is what a GenerateIR call shares among its gens, for a gen
// made outside the call (Program.stdCases).
type programBuild struct {
	reg   *typeRegistry
	files *fileIndex
	impls *implIndex
	insts *stdInstances
	// units is the number of the program's files, so a gen made outside
	// the call can take a unit index none of them has.
	units int
}

// ErrStopped is GenerateIR's error when Program.Stop stopped it.
var ErrStopped = errors.New("irbuild: lowering stopped")

// stopped reports whether p's caller stopped the lowering.
func (p *Program) stopped() bool {
	if !p.halted && p.Stop != nil && p.Stop() {
		p.halted = true
	}
	return p.halted
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
// A program the front end refuses is answered as a nil Program and the
// error, except to a caller that sets cfg.ErrorDecls: when every error is
// in a function, an impl function, a test or a group
// (frontend.Project.ErrorDecls), that caller gets the Program beside the
// error, with ErrorDecls set, the program an editor lowers to show the
// effects of the declarations that checked (refusedProgram).
func AnalyzeFile(entryPath string, cfg frontend.Config) (*Program, error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	// Which mode to run is decided before running one, because the two modes
	// disagree about attached `//!` tests: the test path keeps and type-checks
	// them, `nomi run` strips them, so the analyzed tree cannot be asked which
	// mode produced it. Parse errors fall through to the chosen mode so the
	// reported text is its own.
	hasTests, _ := frontend.FileDeclaresTests(abs)
	return analyzeFileSource(abs, string(data), hasTests, cfg)
}

// AnalyzeFileSource is AnalyzeFile for the file at entryPath whose text is
// src rather than what is on disk: the language server's open buffer.
func AnalyzeFileSource(entryPath, src string, cfg frontend.Config) (*Program, error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, err
	}
	hasTests, _ := frontend.SourceDeclaresTests(src)
	return analyzeFileSource(abs, src, hasTests, cfg)
}

// analyzeFileSource is AnalyzeFileSource for the file at abs, checked with
// its tests when hasTests is set.
func analyzeFileSource(abs, src string, hasTests bool, cfg frontend.Config) (*Program, error) {
	fe := frontend.New(cfg)
	proj, err := fe.AnalyzeFileSource(abs, src, frontend.Mode{Tests: hasTests})
	if refusedProgram(proj, err) {
		return nil, err
	}
	p := fileProgram(abs, proj, hasTests)
	p.interfaces = fe.Interfaces
	p.ErrorDecls = proj.ErrorDecls
	return p, err
}

// refusedProgram reports whether the front end's answer, proj beside err,
// is a refusal that answers no Program: an error, unless the caller asked
// for the declarations that hold it (frontend.Config.ErrorDecls) and every
// error is in one (frontend.Project.ErrorDecls).
func refusedProgram(proj *frontend.Project, err error) bool {
	return err != nil && (proj == nil || proj.ErrorDecls == nil)
}

// fileProgram is the Program of a checked file entry at abs.
func fileProgram(abs string, proj *frontend.Project, hasTests bool) *Program {
	name := strings.TrimSuffix(filepath.Base(abs), ".nomi")
	mods := make([]Module, 0, len(proj.Files)+1)
	mods = append(mods, Module{Path: abs, Name: name, Nodes: proj.Nodes, FA: proj.FA})
	for _, f := range proj.Files {
		path := f.Path
		if path == "" {
			path = f.Key + ".nomi"
		}
		// A sibling's tail calls are marked here, because the entry pipeline
		// marks only the entry's (frontend.Checker.Analyze). An unmarked tail
		// call lowers as an ordinary call. The pass is idempotent. See
		// siblingtailmark_test.go.
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
// file. A program the front end refuses is answered as AnalyzeFile answers
// it: a nil Program, or for a caller that sets cfg.ErrorDecls, the Program
// beside the error when every error is in a declaration.
func AnalyzeVirtual(name, src string, cfg frontend.Config) (*Program, error) {
	hasTests, _ := frontend.SourceDeclaresTests(src)
	fe := frontend.New(cfg)
	proj, err := fe.AnalyzeSource(name, src, frontend.Mode{Tests: hasTests})
	if refusedProgram(proj, err) {
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
	return &Program{Modules: mods, HasTests: hasTests, Root: proj.Root, VirtualFiles: fe.VirtualFiles(), interfaces: fe.Interfaces, ErrorDecls: proj.ErrorDecls}, err
}

// Result is a program lowered to IR.
type Result struct {
	// HasMain reports whether the program defines `fn main`. A program
	// without one runs and exits 0.
	HasMain bool
	// IR is the `ir.Module` each compilation unit that retained a function
	// produced, in unit order.
	//
	// A unit that retained nothing contributes no entry, because
	// `gen.irModule` makes the container on first use.
	// Use IRModules for execution with the compilation's stdlib dependencies.
	IR []*ir.Module
	// abstract is the lowering's abstract bodies, made on first use
	// (Abstract, abstract.go).
	abstract *abstractLowering
	// irLibraries shares immutable graphs with the process-cached stdlib index.
	irLibraries []*ir.Module
	// sharedStd is how many of irLibraries, the first, are the shared
	// stdlib's modules (GenerateIR's).
	sharedStd int
	// irHostKeys are the extern keys the program's `host fn` bodies
	// cross under: bindings only a host that linked the project's Go
	// packages can supply. See irhostfn.go.
	irHostKeys map[string]bool
	// Literals are the typed literals checked at compile time
	// that the user units lowered, in unit order (irliteralcell.go).
	Literals []LiteralSite
	// Subtypes are the literals the user units' files check as
	// subtypes, in unit order (subtype.go).
	Subtypes []SubtypeSite
	// SubtypeRules are the subtypes the user units' files declare,
	// whose rules must be pure (subtype.go).
	SubtypeRules []SubtypeRuleSite
	// Exports are the program's `export fn` declarations, in unit order
	// and then declaration order (exports.go).
	Exports []Export
	// HostFns are the `host fn`s at the top of the program's own files, in
	// unit order and then declaration order, with the conversions a host
	// that answers them in JSON needs (exports.go).
	HostFns []HostFn
	// instMods are the program's std instances, the last of irLibraries.
	instMods []*ir.Module
	// linkedMods are the kept lowering's modules of the listed std modules
	// the program links (programstdlink.go), which irLibraries holds: the
	// listed modules a program that lowers them itself holds in IR.
	linkedMods []*ir.Module
	// synthRefused is each synthesized impl block whose speculative
	// lowering failed, for a kept lowering (Program.noLinkStd) only.
	synthRefused map[*ast.ImplBlock]bool
	// instNeeds is, for a kept lowering only, the listed std modules each
	// of instMods' type arguments are declared in (stdInstances.needs).
	instNeeds [][]string
	// kept is what Keep writes of the lowering (perfile.go).
	kept *keptState
	// perFileTables are, for a lowering of one file, the registry's
	// program-wide sets once it is done, the interfaces it rendered through
	// every implementer's Debug and those the file implements
	// (KeptDebugRendering), and file what it lowered.
	perFileTables struct {
		erased, boxed, synthRefused     []string
		debugRendered, debugImplemented []string
		// subtypes and subtypeRules are the file's own.
		subtypes     []SubtypeSite
		subtypeRules []SubtypeRuleSite
	}
	file *relowered
	// declines and refusals are each attempt's decline and each refused
	// construct, by the file it is in (perfile.go).
	declines, refusals map[string][]string
}

// IRModules returns the graphs needed to link this compilation, including the
// cached stdlib modules lowered beside its user units. IR itself retains the
// user-unit view for entry selection and user-body coverage measurements.
func (r *Result) IRModules() []*ir.Module {
	mods := make([]*ir.Module, 0, len(r.IR)+len(r.irLibraries))
	mods = append(mods, r.IR...)
	return append(mods, r.irLibraries...)
}

// HostKeys are the extern keys the program's `host fn` bodies cross
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
	// This lowering's decline census, which watches every attempt's
	// decline, by the file it lowered: a kept lowering records them and a
	// lowering of one file is compared with them (perfile.go).
	declines := newDeclineCensus(p.Declines)
	declines.watch = true
	// masked rides beside unsupported for the whole program, because the
	// blocker set the report reads is the whole program's and so is its error
	// bar: a refusal in a SIBLING file is what masks the entry's type-shaped
	// blockers. See suppression.go.
	var masked []Suppression
	hasMain := false
	own := len(p.Modules)
	p = withProgramStd(p)
	// The listed std modules p reaches, lowered once for the process and
	// linked by name rather than lowered here (programstdlink.go).
	var linked *linkedStd
	linkFrom := own
	if p.linkAllStd {
		linkFrom = 0
	}
	if names := linkedStdNames(p, linkFrom); len(names) > 0 {
		if ls := linkedStdFor(names); ls != nil && ls.err == nil {
			linked = ls
		}
	}
	// The lowered standard library, built once for the process. Taken after
	// the listed lowering is, whose decoding decodes the shared modules it
	// names, so the program links them.
	std := stdlibLowering()
	// Every type declaration in the program, keyed by the node that declared
	// it and named program-uniquely, so one module can name both its own
	// `Point` and a sibling file's. See foreign.go.
	if p.stopped() {
		return nil, nil, ErrStopped
	}
	reg := buildTypeRegistry(p)
	reg.linkNames = programLinkNames(p)
	defer holdDeclKeys(reg.linkNames)()
	if p.perFile != nil {
		reg.perFile = p.perFile
		reg.erasedInsts, reg.boxedEnums = setOf(p.perFile.erased), setOf(p.perFile.boxed)
	}
	if linked != nil {
		reg.imports, reg.linked = linked.defined, linked
	}
	if p.objects != nil {
		reg.imports, reg.linked = mergeLinked(linked, p.objects)
	}
	// The program's own files as each other's call sites see them, indexed
	// before any of them is lowered: whether a cross-file reference closes an
	// import cycle is a whole-program property. See siblings.go.
	files := buildFileIndex(p, reg)
	if p.stopped() {
		return nil, nil, ErrStopped
	}
	// Three declaration waves before any body is lowered, and the order is forced
	// rather than chosen. A signature in file B may name a type declared in
	// file A while A's names one of B's, and Nomi has no forward-declaration
	// rule in either direction — so no per-file order satisfies both, and the
	// program has to finish every file's TYPES before it types any file's
	// signatures. See foreign.go.
	gens := make([]*gen, len(p.Modules))
	for i := range p.Modules {
		p.Modules[i].DeclaresTests = i == 0 && p.HasTests && p.entryFirst()
		gens[i] = newGen(&p.Modules[i], reg.pkgOf[i], std, files, i, reg)
		gens[i].dc = declines
		gens[i].errorDecls = p.ErrorDecls
		gens[i].linked = linked != nil && i >= linkFrom
		gens[i].fromObjects = p.objects != nil && i > 0 && i < own
		if gens[i].fromObjects && p.Modules[i].bodies {
			gens[i].fromObjects, gens[i].linked = false, true
		}
	}
	reg.gens = gens
	// One table of generic stdlib instances for the whole program, so a
	// sibling file's instances are the entry's too. See stdinst.go.
	insts := newStdInstances(std, declines)
	insts.program, insts.stdOwn = gens, p.noLinkStd
	for _, g := range gens {
		g.stdInsts = insts
	}
	for i := range gens {
		if p.stopped() {
			return nil, nil, ErrStopped
		}
		reg.ensureTypes(i)
	}
	files.resolveSignatures(gens)
	for _, g := range gens {
		if p.stopped() {
			return nil, nil, ErrStopped
		}
		g.declareFuncs()
	}
	// Every impl block in the program, by the declaration of the type it is
	// for, in the same window and for the same reason as the index below: a
	// type-qualified call in one file may name an impl function declared in
	// another. See siblingimpl.go.
	files.resolveImplMembers(gens)
	// A subtype's generated `try_from` calls its parent's, which another
	// file's owner block may hold, so owner functions are indexed before
	// the synthesized impls are lowered too (subtype.go).
	files.resolveOwnerMembers(gens)
	// The synthesized impls are lowered once the index exists, since a
	// derived body may call another file's impl, and indexed again after,
	// so a call never plans one the speculation refused. See settleImpls.
	for _, g := range gens {
		if p.stopped() {
			return nil, nil, ErrStopped
		}
		g.settleImpls()
	}
	files.resolveImplMembers(gens)
	// Owner functions are registered with their blocks and lowered with
	// them; a call from another file finds them here (siblingOwnerPlanFor).
	files.resolveOwnerMembers(gens)
	// Every file's impls are registered now and no body has been lowered yet,
	// which is the one window in which "does this PROGRAM implement I for T?"
	// is both answerable and needed: a boxing site in one file dispatches
	// through a table another file bound into. See existential.go.
	impls := buildImplIndex(gens)
	for _, g := range gens {
		g.impls = impls
	}
	for i := range p.Modules {
		if p.stopped() {
			return nil, nil, ErrStopped
		}
		errs, hidden := gens[i].emitModule(&p.Modules[i])
		unsupported = append(unsupported, errs...)
		masked = append(masked, hidden...)
		if i == 0 && p.entryFirst() {
			hasMain = definesMain(&p.Modules[i])
		}
	}
	if linked != nil {
		unsupported = append(unsupported, linked.unsupported...)
		masked = append(masked, linked.masked...)
	}
	if len(unsupported) > 0 {
		refusal = newUnsupportedError(unsupported, masked)
	}
	// A shared std module's cases, lowered once the listed modules they
	// call are. Their gen is linked first, as an entry is; it records no
	// boot and no impls, which belong to the module's cached lowering.
	var cases *gen
	if p.stdCases != nil {
		cases = p.stdCases(&programBuild{reg: reg, files: files, impls: impls, insts: insts, units: len(gens)})
	}
	// The libraries the program's withdrawn reads are checked against: every
	// cached stdlib module, in package order, only consulted. What the
	// program links is its closure of them, chosen once its own modules are
	// done (linkedShared).
	irLibraries := std.sortedModules()
	if linked != nil {
		irLibraries = append(irLibraries, linked.mods...)
	}
	// The IR each unit retained, in unit order. The entry unit's module
	// records the program boot and its `group` boots, retained or not.
	// A unit that retained nothing has no module, and nothing to start. The
	// test bodies queued during the walk are built last
	// (irRetryWalkOnlyTestBodies).
	for _, g := range gens {
		g.irRetryWalkOnlyTestBodies()
	}
	if p.stopped() {
		return nil, nil, ErrStopped
	}
	// The dispatch entries of std interface defaults the program's blocks
	// inherit, built before the std instances they reach are collected.
	for _, g := range gens {
		g.irBuildStdDefaultThunks()
	}
	// A literal of a generic subtype is checked through its `try_from`
	// at the literal's type arguments, an instance its check may ask
	// another file for, so the sites are built before those are flushed.
	var subtypes []SubtypeSite
	var subtypeRules []SubtypeRuleSite
	for _, g := range gens {
		g.subtypes, g.subtypeRuleSites = g.subtypeSites(), g.subtypeRules()
		subtypes = append(subtypes, g.subtypes...)
		subtypeRules = append(subtypeRules, g.subtypeRuleSites...)
	}
	// A generic function instance one file asked another for after that
	// file's walk ended. See siblinggeneric.go.
	irFlushLateInstances(gens)
	exports := collectExports(p, gens)
	hostFns := collectHostFns(p, gens)
	var irMods []*ir.Module
	var literals []LiteralSite
	hostKeys := map[string]bool{}
	units := gens
	if cases != nil {
		units = append([]*gen{cases}, gens...)
	}
	for _, g := range units {
		literals = append(literals, g.literalSites...)
		for _, key := range g.irHostKeys {
			hostKeys[key] = true
		}
		if g.irMod != nil {
			irMods = append(irMods, g.irMod)
		}
	}
	if linked != nil {
		literals = append(literals, linked.literals...)
	}
	// The program's stdlib instances link beside the cached modules and are
	// never written into the cache.
	instMods := insts.modules()
	for _, m := range instMods {
		irLintFinished(m)
	}
	irLibraries = append(irLibraries, instMods...)
	// Before the impl entries, which name only bodies that survive it.
	if p.objects != nil {
		irWithdrawUnforceableReads(declines, irMods, append(slices.Clip(irLibraries), p.objects.mods...))
	} else {
		irWithdrawUnforceableReads(declines, irMods, irLibraries)
	}
	for i, g := range gens {
		if g.irMod != nil {
			if fd := g.bootDecl(); i == 0 && fd != nil && p.entryFirst() {
				g.irMod.SetBoot(g.irCalleeSym(fd, fd.Name))
			}
			for _, sym := range g.irTestBoots {
				g.irMod.AddTestBoot(sym)
			}
			g.irRecordImpls()
		}
	}
	for _, m := range irMods {
		if p.stopped() {
			return nil, nil, ErrStopped
		}
		irLintFinished(m)
	}
	if reg.linked != nil {
		dropLinkedDuplicates(append(append([]*ir.Module(nil), irMods...), instMods...), reg.linked)
	}
	// The VM links by IR symbol: the program links the cached stdlib
	// modules of its closure, in package order, then the listed modules and
	// its instances.
	var linkedMods []*ir.Module
	if linked != nil {
		linkedMods = linked.mods
	}
	shared := std.linkedShared(std.irModules, programStdImports(p), irMods, linkedMods, instMods)
	irLibraries = append(append(slices.Clip(shared), linkedMods...), instMods...)

	res = &Result{HasMain: hasMain,
		IR: irMods, irLibraries: irLibraries, sharedStd: len(shared), irHostKeys: hostKeys, Literals: literals, Subtypes: subtypes, SubtypeRules: subtypeRules,
		Exports: exports, HostFns: hostFns, instMods: instMods}
	if linked != nil {
		res.linkedMods = linked.units
	}
	if p.noLinkStd {
		res.synthRefused = synthRefused(gens)
		res.instNeeds = insts.needs()
	}
	res.kept = keptStateOf(p, own, gens, reg, linked, cases != nil)
	res.declines, res.refusals = declines.byFile(), refusalsByFile(unsupported)
	if p.perFile != nil {
		res.perFileTables.erased, res.perFileTables.boxed = sortedSet(reg.erasedInsts), sortedSet(reg.boxedEnums)
		for key := range reg.debugRendered {
			res.perFileTables.debugRendered = append(res.perFileTables.debugRendered, key)
		}
		sort.Strings(res.perFileTables.debugRendered)
		if len(gens) > 0 && gens[0] != nil {
			res.perFileTables.debugImplemented = sortedSet(gens[0].debugImplemented())
			res.perFileTables.subtypes, res.perFileTables.subtypeRules = gens[0].subtypes, gens[0].subtypeRuleSites
		}
		for decl := range synthRefused(gens[:1]) {
			if name, ok := reg.linkNames[decl]; ok {
				res.perFileTables.synthRefused = append(res.perFileTables.synthRefused, name.String())
			}
		}
	}
	// Each generic function's abstract body is lowered on first use, once
	// the lowering is done, into modules no program links (abstract.go). A
	// lowering of one file, and one relinked through objects below, name
	// other files' symbols as decoded objects, and build none.
	if p.perFile == nil && !perFileHarness() && !(linksThroughObjects() && !p.noLinkStd) {
		res.abstract = newAbstractLowering(gens, declines, insts)
	}
	if perFileHarness() {
		if err := res.relowerEachFile(p); err != nil {
			return nil, nil, err
		}
	}
	// A kept lowering is linked through objects with each program that
	// links it, never on its own: its modules are kept as they lowered.
	// One file lowered on its own (LowerFile) already names the other
	// files' symbols as its linker decoded them from their objects;
	// relinking its lowering alone would give each of those names a
	// symbol of its own, which no module defines.
	if linksThroughObjects() && !p.noLinkStd && p.perFile == nil {
		if err := relinkThroughObjects(res); err != nil {
			return nil, nil, err
		}
	}
	return res, refusal, nil
}

// definesMain reports whether the module declares a zero-parameter `fn main`.
func definesMain(m *Module) bool {
	for _, n := range m.Nodes {
		if fd, ok := n.(*ast.FuncDef); ok && fd.Name == "main" && len(fd.Params) == 0 {
			return true
		}
	}
	return false
}

// unitPackage is the unit package name of the i'th module: a namespace
// key that names no compiled artifact (package doc, "Unit packages").
// Positional rather than derived from the module name, since two module
// names can collide and the key is not a user-visible surface.
func unitPackage(i int) string { return fmt.Sprintf("nomimod%d", i) }

// rtModulePath is the import path of the runtime library. The builder compares a Go type's reflect PkgPath against
// it to recognise an rt type, and a host function's package against it to
// recognise an rt crossing.
const (
	rtModulePath = "github.com/nomi-language/nomi/rt"
)

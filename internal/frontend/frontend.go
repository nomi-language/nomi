// Package frontend is Nomi's front end: parse, inject the synthetic host
// declarations an FFI project mirrors from its Go bindings, lower derives,
// synthesize derived and universal Debug impls, analyze the whole import
// graph, and check that every `host` declaration has something to answer it.
// It executes nothing.
//
// internal/irbuild's Analyze, vmhost and `nomi check` call it directly.
package frontend

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/syntheticextern"
	"github.com/nomi-language/nomi/std"
)

// Config is what a Checker resolves a program against.
type Config struct {
	// VirtualFiles are in-memory sibling sources by bare module name, and
	// an in-memory program's message catalogs by their path with `.toml`
	// (read through a staged VirtualManifest's `[i18n]`), consulted
	// before disk for a one-segment import (the tour playground and the REPL).
	VirtualFiles map[string]string
	// VirtualManifest is a pre-parsed nomi.toml for a program with no project
	// on disk, so manifest-driven checks still fire.
	VirtualManifest *analysis.Manifest
	// ProjectRoot is the directory an in-memory entry resolves project imports
	// against. A file entry discovers its own root and replaces it.
	ProjectRoot string
	// SyntheticExterns are hidden `host` declarations mirrored from an FFI
	// project's source-level Go bindings.
	SyntheticExterns []syntheticextern.Decl
	// Provided reports whether the host answers the user `host fn` or
	// `host type` registered under key. A declaration nothing provides is a
	// check error. Nil provides nothing.
	Provided func(key string) bool
	// SourceBoundProvided treats every `go`-bound or inline-`go` declaration
	// as provided: the consumer compiles the binding itself (the FFI wrapper
	// generates an adapter for it) rather than looking it up by key.
	SourceBoundProvided bool
	// HostTypesAreHandles treats every plain `host type` (no `go` binding)
	// as provided: its values are opaque handles the host's functions create
	// and read, so there is nothing for the host to register under the
	// type's own key. vmhost's tables answer functions only.
	HostTypesAreHandles bool
	// UnmetHint is the last line of the unmet-declaration error, telling the
	// reader how this consumer registers a host function.
	UnmetHint string
	// AllowUnusedImports drops the entry's unused-import errors. A REPL
	// session is the one user: every input's program carries the imports of
	// the inputs before it, which that input need not use.
	AllowUnusedImports bool
	// Macros runs the macros the program's declarations are
	// written with (analysis.MacroHost); nil runs none, and each such
	// declaration is an error. vmhost sets it.
	Macros analysis.MacroHost
	// MacroDrivers adds to the entry, a macro's file, the hidden
	// functions the compiler calls to run each macro it declares
	// (analysis.MacroDrivers). vmhost's macro runner sets it when
	// it builds a macro module.
	MacroDrivers bool
	// AllowUnusedBindings drops unused-binding errors, in the entry and its
	// siblings. The language server is the one user: it lowers the program
	// being edited to evaluate its typed literals, and a binding typed but
	// not yet read must not stop that.
	AllowUnusedBindings bool
}

// allowed reports whether the configuration drops e.
func (c *Checker) allowed(e analysis.TypeError) bool {
	return (c.cfg.AllowUnusedImports && e.Code == analysis.UnusedImportCode) ||
		(c.cfg.AllowUnusedBindings && e.Code == analysis.UnusedBindingCode)
}

// kept is a new slice of errs without the ones the configuration drops.
func (c *Checker) kept(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if !c.allowed(e) {
			out = append(out, e)
		}
	}
	return out
}

// Checker runs the front end. It keeps the state one check leaves behind
// (the project root, the entry's sibling files) for the caller to read, so a
// Checker checks one program at a time.
type Checker struct {
	cfg Config
	lib *std.StdLib

	root        string
	entryPath   string
	entryModRel string
	// placeEntry runs nomi.toml's entry_points placement check on the
	// entry; false for a test file.
	placeEntry   bool
	projectFiles []ProjectFile
	// diagPath is the file the entry's diagnostics name: its absolute
	// path, or an in-memory entry's name.
	diagPath string
	// entrySrc is the text of the file at diagPath, for showing a
	// diagnostic with its source.
	entrySrc string
	// siblingSyntax holds the syntax errors of every project file the
	// loader read, by path.
	siblingSyntax map[string][]parser.ParseError
	// nodesByPath holds every project file's nodes by its path, for moving
	// a related location in another file's macro expansion
	// (relatedInBand).
	nodesByPath map[string][]ast.Node
	// rootManifest is the root's nomi.toml once read (manifestRead).
	rootManifest *analysis.Manifest
	manifestRead bool
}

// New returns a Checker over the embedded standard library.
func New(cfg Config) *Checker {
	return &Checker{cfg: cfg, lib: std.Shared(), root: cfg.ProjectRoot}
}

// SetEntry records the entry file's absolute path and root, as a file load
// does.
func (c *Checker) SetEntry(absPath, root string) {
	c.entryPath = absPath
	c.root = root
	c.manifestRead = false
}

// VirtualFiles are the configured in-memory siblings.
func (c *Checker) VirtualFiles() map[string]string { return c.cfg.VirtualFiles }

// manifest is the program's nomi.toml: the staged one, else the one at the
// root, nil for none.
func (c *Checker) manifest() *analysis.Manifest {
	if c.cfg.VirtualManifest != nil {
		return c.cfg.VirtualManifest
	}
	if c.root == "" {
		return nil
	}
	if !c.manifestRead {
		c.manifestRead = true
		c.rootManifest, _ = analysis.LoadManifest(c.root)
	}
	return c.rootManifest
}

// site is the expansion site of the project file with module path
// modulePath: its catalogs are read from disk under the root when the root
// has a manifest, and from the virtual files for an in-memory program with
// a staged one, whose module root is the virtual files' `.`.
func (c *Checker) site(modulePath string) *analysis.ExpansionSite {
	m := c.manifest()
	s := &analysis.ExpansionSite{ModulePath: modulePath, Manifest: m, Macros: c.cfg.Macros}
	switch {
	case m == nil:
	case c.cfg.VirtualManifest == nil:
		s.Root = c.root
	default:
		s.Root = "."
		s.Read = c.virtualInput
	}
	return s
}

// virtualInput reads an input of an in-memory program from its virtual
// files, by its path from the virtual module root.
func (c *Checker) virtualInput(path string) (string, bool) {
	if filepath.IsAbs(path) || !strings.HasSuffix(path, ".toml") {
		return "", false
	}
	text, ok := c.cfg.VirtualFiles[filepath.ToSlash(filepath.Clean(path))]
	return text, ok
}

// Loader is the file loader analysis runs with: virtual files first for a
// one-segment import, then the embedded stdlib and disk, with the synthetic
// host declarations injected into every loaded file and its keyword
// declarations expanded.
func (c *Checker) Loader() analysis.FileLoader {
	inner := c.innerLoader()
	return func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		nodes, err := inner(projectRoot, modulePath)
		if err != nil || len(modulePath) > 0 && modulePath[0] == "std" {
			return nodes, err
		}
		site := c.site(strings.Join(modulePath, "/"))
		if filepath.Clean(projectRoot) != filepath.Clean(c.root) {
			// A file of another module: its own root and manifest.
			m, _ := analysis.LoadManifest(projectRoot)
			site = &analysis.ExpansionSite{ModulePath: strings.Join(modulePath, "/"), Manifest: m, Macros: c.cfg.Macros}
			if m != nil {
				site.Root = projectRoot
			}
		}
		return analysis.ExpandDeclarations(nodes, site), nil
	}
}

func (c *Checker) innerLoader() analysis.FileLoader {
	base := std.MakeReportingLoader(c.recordSyntax)
	synth := c.cfg.SyntheticExterns
	if len(c.cfg.VirtualFiles) == 0 && len(synth) == 0 {
		return base
	}
	return func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		var nodes []ast.Node
		virtual := false
		if len(modulePath) == 1 {
			if src, ok := c.cfg.VirtualFiles[modulePath[0]]; ok {
				var errs []parser.ParseError
				nodes, errs = parser.ParseWithRecovery(lexer.Lex(src))
				c.recordSyntax(modulePath[0]+".nomi", errs)
				virtual = true
			}
		}
		// An empty virtual file parses to no nodes and is still that file.
		if !virtual {
			var err error
			nodes, err = base(projectRoot, modulePath)
			if err != nil {
				return nil, err
			}
		}
		return syntheticextern.Inject(nodes, synth, false)
	}
}

// filesParsed counts the project files every Checker in the process has
// parsed: each entry Prepare reads and each project file its loader reads.
var filesParsed atomic.Int64

// FilesParsed is how many project files the front end has parsed in this
// process, for tests that count what a path costs.
func FilesParsed() int64 { return filesParsed.Load() }

// recordSyntax keeps a loaded project file's syntax errors, replacing any an
// earlier load of the same file recorded.
func (c *Checker) recordSyntax(path string, errs []parser.ParseError) {
	filesParsed.Add(1)
	if len(errs) == 0 {
		delete(c.siblingSyntax, path)
		return
	}
	if c.siblingSyntax == nil {
		c.siblingSyntax = map[string][]parser.ParseError{}
	}
	c.siblingSyntax[path] = errs
}

// Mode is how a source is prepared.
type Mode struct {
	// Tests keeps attached `//!` tests; otherwise they are stripped, as
	// `nomi run` strips them.
	Tests bool
}

// Prepare parses src and runs the passes before analysis: synthetic host
// injection, derive lowering, attached-test stripping, derive and universal
// Debug synthesis.
func (c *Checker) Prepare(src string, mode Mode) ([]ast.Node, error) {
	c.entrySrc = src
	filesParsed.Add(1)
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		return nil, located(c.diagPath, src, err)
	}
	nodes, err = syntheticextern.Inject(nodes, c.cfg.SyntheticExterns, true)
	if err != nil {
		return nil, err
	}
	nodes = analysis.ExpandDeclarations(nodes, c.site(c.entryModRel))
	if c.cfg.MacroDrivers {
		nodes = append(nodes, analysis.MacroDrivers(nodes)...)
	}
	nodes, err = lowerDerives(c.diagPath, src, nodes)
	if err != nil {
		return nil, err
	}
	if !mode.Tests {
		StripAttachedTests(nodes)
	}
	nodes, _ = analysis.SynthesizeDerives(nodes)
	return analysis.SynthesizeUniversalDebug(nodes), nil
}

// lowerDerives lowers the derives of the file at path.
func lowerDerives(path, src string, nodes []ast.Node) ([]ast.Node, error) {
	nodes, errs := analysis.LowerDerives(nodes)
	if len(errs) == 0 {
		return nodes, nil
	}
	return nil, typeDiagnostics(path, src, errs)
}

// Analyze runs the analysis pipeline over a prepared entry: the whole import
// graph (BuildProject), CheckTypes on the entry and on every sibling,
// coherence, iteration sensitivity, the concurrency and boot-scope rules,
// and tail-call marking. It answers the entry's analysis, every module's
// nodes (the entry under "", siblings under their import key) and one error
// listing every diagnostic.
func (c *Checker) Analyze(nodes []ast.Node) (*analysis.FileAnalysis, map[string][]ast.Node, error) {
	if c.lib == nil {
		c.projectFiles = nil
		return nil, map[string][]ast.Node{"": nodes}, nil
	}
	c.siblingSyntax = nil
	fa, siblingFAs, siblingNodes := analysis.BuildProjectFromEntryWithManifest(
		c.entryPath,
		nodes,
		c.lib.Primitives,
		c.lib.Modules,
		c.lib.Files,
		c.root,
		c.Loader(),
		c.cfg.VirtualManifest,
		c.entryModRel,
		c.placeEntry,
	)
	c.projectFiles = collectProjectFiles(siblingFAs, siblingNodes)
	c.nodesByPath = map[string][]ast.Node{fa.FilePath: nodes}
	for key, sibFA := range siblingFAs {
		if sibFA.FilePath != "" {
			c.nodesByPath[sibFA.FilePath] = siblingNodes[key]
		}
	}
	errs := c.buildErrors(fa)
	// The program's own errors (duplicate and orphan impls) are reported with
	// the entry's, after its build-phase errors, each in the file it names
	// (entryDiagnostics).
	errs = append(errs, c.kept(fa.Program().Errors)...)
	errs = append(errs, c.kept(analysis.CheckTypes(fa, nodes))...)
	// A sibling's CheckTypes records the conformance its bodies demand in its
	// own ImplManifest, which the missing-impl check folds, and reports the
	// sibling's own diagnostics at the sibling's position, after its
	// build-phase errors (sibFA.TypeErrors), as the entry's are ordered.
	// siblingFAs holds one analysis per file, so a file imported by several
	// others is reported once. Keyed order so the text is stable.
	sibKeys := make([]string, 0, len(siblingFAs))
	for key := range siblingFAs {
		sibKeys = append(sibKeys, key)
	}
	sort.Strings(sibKeys)
	sibErrs := make(map[string][]analysis.TypeError, len(sibKeys))
	program := []analysis.ProgramFile{{FA: fa, Nodes: nodes}}
	for _, key := range sibKeys {
		sibFA := siblingFAs[key]
		// A stdlib module the build resolved to the shared analysis was
		// checked once, by std.Load: its diagnostics are in lib.Errors, and
		// its ImplManifest holds what its bodies demand. Checking it again
		// would write to an analysis every program in the process reads.
		if name, shared := c.sharedStdlib(key, sibFA); shared {
			program = append(program, analysis.ProgramFile{FA: sibFA, Nodes: c.lib.Nodes[name]})
			continue
		}
		own := c.buildErrors(sibFA)
		sibErrs[key] = append(own, c.kept(analysis.CheckTypes(sibFA, siblingNodes[key]))...)
		// Its break and continue placement, which only its own call sites
		// decide (analysis.AnalyzeIterSensitivity).
		sibErrs[key] = append(sibErrs[key], c.kept(analysis.AnalyzeIterSensitivity(sibFA, siblingNodes[key]))...)
		program = append(program, analysis.ProgramFile{FA: sibFA, Nodes: siblingNodes[key]})
	}
	// The application-field roots of every file, now that every file is
	// checked: a root's walk reads the checks of the files it calls into.
	errs = append(errs, c.kept(analysis.CheckAppRoots(fa, nodes))...)
	for _, key := range sibKeys {
		sibFA := siblingFAs[key]
		if _, shared := c.sharedStdlib(key, sibFA); shared {
			continue
		}
		sibErrs[key] = append(sibErrs[key], c.kept(analysis.CheckAppRoots(sibFA, siblingNodes[key]))...)
	}
	// Whole-file imports nothing names, now that every file's uses of impl
	// blocks are known.
	implImportErrs := analysis.CheckImplImports(program)
	if !c.cfg.AllowUnusedImports {
		errs = append(errs, implImportErrs[fa]...)
	}
	// Each macro's whole-module check, over every declaration of its
	// keyword the program's files expanded, and the agreement of every
	// noun with every message.
	extFiles := map[string][]ast.Node{c.entryModRel: nodes}
	for _, key := range sibKeys {
		if !analysis.IsStdlibKey(key) {
			extFiles[key] = siblingNodes[key]
		}
	}
	extChecks := analysis.CheckProgramDeclarations(extFiles)
	errs = append(errs, extChecks[c.entryModRel]...)
	if c.entryModRel != "" {
		errs = append(errs, extChecks[""]...)
	}
	for _, key := range sibKeys {
		if key != c.entryModRel {
			sibErrs[key] = append(sibErrs[key], extChecks[key]...)
		}
	}
	var sibDiags Diagnostics
	sibDiagsOf := map[string]Diagnostics{}
	for _, key := range sibKeys {
		sibFA := siblingFAs[key]
		if _, shared := c.sharedStdlib(key, sibFA); shared {
			continue
		}
		sibPath := sibFA.FilePath
		if sibPath == "" {
			sibPath = key + ".nomi"
		}
		own := analysis.RelocateExpansionErrors(siblingNodes[key], sibErrs[key])
		if !c.cfg.AllowUnusedImports {
			own = append(own, implImportErrs[sibFA]...)
		}
		// The file is read only to show a diagnostic with its source.
		if len(own) == 0 {
			continue
		}
		sibDiagsOf[key] = c.fileDiagnostics(sibPath, "", own)
		sibDiags = append(sibDiags, sibDiagsOf[key]...)
	}
	// After every file is checked, so the missing-impl check sees every
	// recording site. Its errors are the program's; they are reported with
	// the entry's.
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	if c.cfg.VirtualManifest == nil {
		// The module's catalogs that nothing reads.
		files := map[string][]ast.Node{c.entryModRel: nodes}
		for key, n := range siblingNodes {
			files[key] = n
		}
		errs = append(errs, analysis.CheckCatalogTree(c.root, c.manifest(), files)...)
	}
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	errs = append(errs, analysis.CheckConcurrentScope(fa, nodes)...)
	errs = append(errs, analysis.CheckBootScope(fa, nodes)...)
	errs = append(errs, analysis.CheckTaskLifetime(fa, nodes)...)
	analysis.MarkTailCalls(nodes)
	modules := make(map[string][]ast.Node, len(siblingNodes)+1)
	modules[""] = nodes
	for key, sibNodes := range siblingNodes {
		modules[key] = sibNodes
	}
	// A syntax error in the embedded stdlib is a defect in this nomi, and
	// every program is checked against that stdlib, so it is reported ahead
	// of anything the program's own files say.
	if ds := stdSyntaxDiagnostics(c.lib); ds != nil {
		return fa, modules, ds
	}
	// A syntax error in an imported file is reported alone, as one in the
	// entry is: what the checker says about a file recovery read only part
	// of follows from the syntax error.
	if len(c.siblingSyntax) > 0 {
		paths := make([]string, 0, len(c.siblingSyntax))
		for path := range c.siblingSyntax {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		var syntax Diagnostics
		for _, path := range paths {
			syntax = append(syntax, parseDiagnostics(path, c.sourceOf(path), c.siblingSyntax[path])...)
		}
		return fa, modules, syntax
	}
	// A macro that could not expand a declaration is reported alone:
	// what the rest of the program says follows from what it did not
	// declare (analysis.MacroFailed).
	if analysis.MacroFailed(modules) {
		entryChecks := extChecks[c.entryModRel]
		if c.entryModRel != "" {
			entryChecks = append(entryChecks, extChecks[""]...)
		}
		alone := c.entryDiagnostics(fa, analysis.MacroErrorsAlone(nodes, entryChecks))
		for _, key := range sibKeys {
			sibFA := siblingFAs[key]
			if _, shared := c.sharedStdlib(key, sibFA); shared || key == c.entryModRel {
				continue
			}
			sibPath := sibFA.FilePath
			if sibPath == "" {
				sibPath = key + ".nomi"
			}
			// A file of a macro module that did not build keeps its own
			// errors: they are why its declarations declare nothing.
			if analysis.InUnbuiltMacroModule(modules, sibFA.FilePath) {
				alone = append(alone, sibDiagsOf[key]...)
				continue
			}
			own := analysis.MacroErrorsAlone(siblingNodes[key], extChecks[key])
			if len(own) == 0 {
				continue
			}
			alone = append(alone, c.fileDiagnostics(sibPath, "", own)...)
		}
		return fa, modules, alone
	}
	if len(errs) == 0 && len(sibDiags) == 0 {
		return fa, modules, nil
	}
	return fa, modules, append(c.entryDiagnostics(fa, analysis.RelocateExpansionErrors(nodes, errs)), sibDiags...)
}

// entryDiagnostics renders the errors reported with the entry fa, each in
// the file its position is in: the entry's own, or another file's when the
// error says so (TypeError.File), as a derive's missing impl in an imported
// file does. The order is kept.
func (c *Checker) entryDiagnostics(fa *analysis.FileAnalysis, errs []analysis.TypeError) Diagnostics {
	return c.fileDiagnostics(c.diagPath, fa.FilePath, errs)
}

// fileDiagnostics renders the errors reported with the file at path (also
// known as alias), each in the file its position is in: path's own, or
// another file's when the error names it (TypeError.File), as a catalog's
// errors do. A non-Nomi file is shown with its own text. The order is kept.
func (c *Checker) fileDiagnostics(path, alias string, errs []analysis.TypeError) Diagnostics {
	fileOf := func(e analysis.TypeError) string {
		if e.File == "" || alias != "" && e.File == alias || samePath(e.File, path) {
			return ""
		}
		return e.File
	}
	errs = c.relatedInBand(errs)
	var out Diagnostics
	for i := 0; i < len(errs); {
		file := fileOf(errs[i])
		j := i + 1
		for j < len(errs) && fileOf(errs[j]) == file {
			j++
		}
		if file == "" {
			out = append(out, typeDiagnostics(path, c.sourceOf(path), errs[i:j])...)
		} else {
			out = append(out, typeDiagnostics(file, c.sourceOf(file), errs[i:j])...)
		}
		i = j
	}
	return out
}

// relatedInBand moves each related location of errs that is in another
// file's macro expansion to what it was made from
// (analysis.ExpansionRelated), and drops one in code the compiler
// generated that no expansion holds: a line in the synthesized band is
// never shown.
func (c *Checker) relatedInBand(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for i, e := range errs {
		band := false
		for _, r := range e.Related {
			band = band || analysis.IsSynthesizedLine(r.Line)
		}
		if !band {
			continue
		}
		if out == nil {
			out = slices.Clone(errs)
		}
		var related []analysis.RelatedInfo
		for _, r := range e.Related {
			if r.File != "" {
				if moved, ok := analysis.ExpansionRelated(c.nodesByPath[r.File], r); ok {
					r = moved
				}
			}
			if !analysis.IsSynthesizedLine(r.Line) {
				related = append(related, r)
			}
		}
		out[i].Related = related
	}
	if out == nil {
		return errs
	}
	return out
}

// samePath reports whether a and b name one file.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

// sharedStdlib reports whether fa, the build's analysis of the module key,
// is the shared stdlib's analysis of that module, and answers the module's
// name in lib.Files.
func (c *Checker) sharedStdlib(key string, fa *analysis.FileAnalysis) (string, bool) {
	if !analysis.IsStdlibKey(key) {
		return "", false
	}
	name := strings.TrimPrefix(key, "std/")
	return name, c.lib.Files[name] == fa
}

// sourceOf is the text of the file at path, a diagnostic's file: the
// entry's source, a configured in-memory sibling ("name.nomi"), a stdlib
// module, or the file on disk. "" when none is available.
func (c *Checker) sourceOf(path string) string {
	if path == c.diagPath && c.entrySrc != "" {
		return c.entrySrc
	}
	if src, ok := c.cfg.VirtualFiles[strings.TrimSuffix(path, ".nomi")]; ok && !filepath.IsAbs(path) {
		return src
	}
	return fileSource(path)
}

// buildErrors answers a file's build-phase errors, without the ones the
// configuration drops (kept).
func (c *Checker) buildErrors(fa *analysis.FileAnalysis) []analysis.TypeError {
	return c.kept(fa.TypeErrors)
}

// AnalyzeStdlibModule analyzes a prepared stdlib module's source as the
// module moduleKey ("std/<name>"), with the single-file stdlib-aware analysis
// the LSP uses.
func (c *Checker) AnalyzeStdlibModule(moduleKey string, nodes []ast.Node) (*analysis.FileAnalysis, error) {
	if c.lib == nil {
		return nil, nil
	}
	if ds := stdSyntaxDiagnostics(c.lib); ds != nil {
		return nil, ds
	}
	fa := analysis.BuildFileWithStdlibAtPath(nodes, nil, c.lib.Modules, c.root, c.Loader(), moduleKey+".nomi")
	// The impl index holds THIS analysis for the module, not the shared
	// stdlib's analysis of the same file, so an impl the module declares is
	// typed by the signature in the source being checked. With the shared
	// copy in its place, an edited `impl Literal for Regex` whose
	// `from_fragments` returned `Result<Regex, Int>` still typed Regex`(` as
	// the embedded `Result<Regex, String>`.
	files := make(map[string]*analysis.FileAnalysis, len(c.lib.Files))
	for name, sfa := range c.lib.Files {
		files[name] = sfa
	}
	module := strings.TrimPrefix(moduleKey, "std/")
	files[module] = fa
	analysis.AttachEditedStdlibImpls(fa, nodes, module, files)
	var errs []analysis.TypeError
	errs = append(errs, fa.TypeErrors...)
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	// Again, now that BuildTypes has typed this file's impls: the index's
	// function types and receiver identities are read off typed symbols.
	analysis.AttachEditedStdlibImpls(fa, nodes, module, files)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckAppRoots(fa, nodes)...)
	errs = append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
	errs = append(errs, analysis.CheckConcurrentScope(fa, nodes)...)
	errs = append(errs, analysis.CheckBootScope(fa, nodes)...)
	errs = append(errs, analysis.CheckTaskLifetime(fa, nodes)...)
	if len(errs) > 0 {
		return nil, typeDiagnostics(c.diagPath, c.entrySrc, errs)
	}
	return fa, nil
}

// Project is a checked entry file plus every other file its import graph
// reaches.
type Project struct {
	// Path is the entry's path as requested, or the module name of an
	// in-memory entry.
	Path string
	// Nodes and FA are the entry's checked tree and analysis. The analysis
	// carries what the tree cannot: the types the checker solved.
	Nodes []ast.Node
	FA    *analysis.FileAnalysis
	// Root is the directory module resolution ran against.
	Root string
	// Files is every non-entry file, stdlib excluded, in key order.
	Files []ProjectFile
}

// ProjectFile is one file in a checked entry's import graph other than the
// entry.
type ProjectFile struct {
	// Key is the analyzer's "/"-joined import key, with no extension.
	Key string
	// Path is the file's absolute path, or "" for a virtual file.
	Path  string
	Nodes []ast.Node
	FA    *analysis.FileAnalysis
}

// PrepareFile points the Checker at a file entry, rooted at
// analysis.ProjectRoot, the rule the LSP uses too. It answers the
// entry's module-relative name, the form nomi.toml's entry_points uses.
// An extensionless `#!` script is rooted at its own directory
// (analysis.ProjectRoot).
func (c *Checker) PrepareFile(path, absPath string) string {
	root := analysis.ProjectRoot(absPath, "")
	c.SetEntry(absPath, root)
	moduleName := strings.TrimSuffix(filepath.Base(path), ".nomi")
	if rel, err := filepath.Rel(root, absPath); err == nil {
		moduleName = strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
	}
	return moduleName
}

// CheckFile checks the program at path. mode.Tests keeps and type-checks its
// attached tests, as `nomi test` does.
func (c *Checker) CheckFile(path string, mode Mode) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return c.CheckFileSource(path, string(data), mode)
}

// CheckFileSource is CheckFile for the file at path whose text is src rather
// than what is on disk: an editor's unsaved buffer. Its root, module name and
// siblings are the file's own.
func (c *Checker) CheckFileSource(path, src string, mode Mode) (*Project, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	moduleName := c.PrepareFile(path, absPath)
	if analysis.IsForeignStdlibDir(c.root) {
		return nil, ForeignStdlibError(absPath, c.root)
	}
	// A test file is not an entry point, so entry_points placement does not
	// apply to it. It keeps its key all the same: a file in its import
	// cycle that imports it back reaches this analysis of it, not a second
	// copy.
	return c.check(path, moduleName, !mode.Tests, src, mode)
}

// AnalyzeFileSource is CheckFileSource for a reader of the program's
// analyses, as AnalyzeSource is CheckSource's.
func (c *Checker) AnalyzeFileSource(path, src string, mode Mode) (*Project, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	moduleName := c.PrepareFile(path, absPath)
	if analysis.IsForeignStdlibDir(c.root) {
		return nil, ForeignStdlibError(absPath, c.root)
	}
	return c.analyze(path, moduleName, !mode.Tests, src, mode)
}

// CheckSource checks an in-memory entry named moduleName. Its siblings come
// from Config.VirtualFiles.
func (c *Checker) CheckSource(moduleName, src string, mode Mode) (*Project, error) {
	return c.check(moduleName, moduleName, true, src, mode)
}

// AnalyzeSource is CheckSource for a reader of the program's analyses, such
// as the tour's hover: the Project holds every file's analysis whenever the
// entry parsed, the error beside it carries the diagnostics, and it is nil
// only when the entry did not parse.
func (c *Checker) AnalyzeSource(moduleName, src string, mode Mode) (*Project, error) {
	return c.analyze(moduleName, moduleName, true, src, mode)
}

func (c *Checker) check(path, entryModRel string, placeEntry bool, src string, mode Mode) (*Project, error) {
	p, err := c.analyze(path, entryModRel, placeEntry, src, mode)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// analyze is check, answering the project beside the error once the
// entry has been analyzed.
func (c *Checker) analyze(path, entryModRel string, placeEntry bool, src string, mode Mode) (*Project, error) {
	c.entryModRel = entryModRel
	c.placeEntry = placeEntry
	c.diagPath = c.entryPath
	if c.diagPath == "" {
		c.diagPath = path
		if !strings.HasSuffix(c.diagPath, ".nomi") {
			c.diagPath += ".nomi"
		}
	}
	nodes, err := c.Prepare(src, mode)
	if err != nil {
		return nil, err
	}
	fa, modules, err := c.Analyze(nodes)
	if fa == nil {
		return nil, err
	}
	p := &Project{Path: path, Nodes: nodes, FA: fa, Root: c.root, Files: c.projectFiles}
	if err != nil {
		return p, err
	}
	if err := c.ValidateHosts(modules); err != nil {
		return p, err
	}
	return p, nil
}

// CheckStdlibSource checks src as the stdlib module moduleKey ("regex" or
// "std/regex"), as `nomi test` checks a stdlib file: nothing is evaluated,
// and mode.Tests keeps its `//!` prompts.
func (c *Checker) CheckStdlibSource(moduleKey, src string, mode Mode) ([]ast.Node, *analysis.FileAnalysis, error) {
	moduleKey = "std/" + strings.TrimSuffix(strings.TrimPrefix(moduleKey, "std/"), ".nomi")
	if c.diagPath == "" {
		c.diagPath = moduleKey + ".nomi"
	}
	if c.lib == nil {
		return nil, nil, fmt.Errorf("checking %s needs the standard library", moduleKey)
	}
	nodes, err := c.prepareStdlib(src, mode)
	if err != nil {
		return nil, nil, err
	}
	fa, err := c.AnalyzeStdlibModule(moduleKey, nodes)
	if err != nil {
		return nil, nil, err
	}
	return nodes, fa, nil
}

// StdlibModuleErrors answers the diagnostics the process's one analysis of
// the stdlib (std.Shared) recorded for module ("regex" or "std/regex"), or nil
// when it recorded none. That analysis keeps every `//!` prompt and checks it
// in its module's scope, as CheckStdlibSource does, so a run of the embedded
// module's cases built from it reports what a fresh check of the same source
// would. Without this, `nomi test std/<module>.nomi` lowered prompts the
// checker had rejected, and the builder resolved a name such as a bare
// `False` that the module never imported.
//
// A syntax error anywhere in the embedded stdlib is answered first and alone,
// for every module: the shared analysis read that file only as far as
// recovery could, and what it says about any module follows from that.
func StdlibModuleErrors(module string) error {
	name := strings.TrimSuffix(strings.TrimPrefix(module, "std/"), ".nomi")
	if ds := stdSyntaxDiagnostics(std.Shared()); ds != nil {
		return ds
	}
	errs := std.Shared().Errors[name]
	if len(errs) == 0 {
		return nil
	}
	path := "std/" + name + ".nomi"
	return typeDiagnostics(path, fileSource(path), errs)
}

func (c *Checker) prepareStdlib(src string, mode Mode) ([]ast.Node, error) {
	c.entrySrc = src
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		return nil, located(c.diagPath, src, err)
	}
	nodes, err = lowerDerives(c.diagPath, src, nodes)
	if err != nil {
		return nil, err
	}
	if !mode.Tests {
		StripAttachedTests(nodes)
	}
	nodes, _ = analysis.SynthesizeDerives(nodes)
	return analysis.SynthesizeUniversalDebug(nodes), nil
}

// StdlibFile reports whether absPath is a file of the stdlib source tree on
// disk, and answers its module key ("std/<name>") and the tree's root. An
// installed binary has no source tree, so the answer there is always no.
func StdlibFile(absPath string) (moduleKey, stdRoot string, ok bool) {
	stdRoot, err := analysis.StdlibPath()
	if err != nil || !sameDirOrChild(absPath, stdRoot) {
		return "", "", false
	}
	rel, err := filepath.Rel(stdRoot, absPath)
	if err != nil {
		return "", "", false
	}
	return "std/" + analysis.StdlibLogicalModuleName(rel), stdRoot, true
}

// ForeignStdlibError refuses a file of another checkout's standard library,
// stdRoot being that library's directory. Such a file is not this binary's
// stdlib module, and read as a user project it fails for reasons that say
// nothing about it (the manifest's reserved name, `Ordering` against
// `Ordering`). Testing it against this binary's other modules and compiler
// would answer for a mixture of two checkouts, so the file is refused and the
// reader is pointed at the nomi that belongs to it.
func ForeignStdlibError(absPath, stdRoot string) error {
	own, err := analysis.StdlibPath()
	if err != nil {
		own = "unknown"
	}
	return fmt.Errorf("%s is in the standard library of another Nomi checkout (%s); this nomi's standard library is %s, so run that checkout's nomi",
		absPath, filepath.Dir(stdRoot), own)
}

func sameDirOrChild(path, dir string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = real
	}
	if real, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = real
	}
	if absPath == absDir {
		return true
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// collectProjectFiles projects the analyzer's two per-sibling maps onto one
// ordered slice, dropping stdlib keys and any file missing one half.
func collectProjectFiles(fas map[string]*analysis.FileAnalysis, nodes map[string][]ast.Node) []ProjectFile {
	out := make([]ProjectFile, 0, len(fas))
	for key, fa := range fas {
		if key == "" || fa == nil || analysis.IsStdlibKey(key) {
			continue
		}
		fileNodes, ok := nodes[key]
		if !ok || len(fileNodes) == 0 {
			continue
		}
		out = append(out, ProjectFile{Key: key, Path: fa.FilePath, Nodes: fileNodes, FA: fa})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

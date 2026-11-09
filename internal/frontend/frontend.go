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
	// an in-memory program's phrase catalogs by their path with `.toml`
	// (read through a staged VirtualManifest's `[i18n]`), consulted
	// before disk for a one-segment import (the tour playground and the REPL).
	VirtualFiles map[string]string
	// VirtualManifest is a pre-parsed nomi.toml for a program with no project
	// on disk, so manifest-driven checks still fire.
	VirtualManifest *analysis.Manifest
	// VirtualData are an in-memory program's other files, by their
	// slash-separated paths from its root, which its `embed` declarations
	// read instead of the disk. Nil reads the disk under the root.
	VirtualData map[string]string
	// EmbedRoot is where the `embed` declarations of an in-memory program
	// with no root read their files: the REPL's working directory.
	EmbedRoot string
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
	// entryMode is the mode Prepare prepared the entry in, and prepared
	// whether Prepare made the nodes Analyze is given.
	entryMode Mode
	prepared  bool
	// rootManifest is the root's nomi.toml once read (manifestRead).
	rootManifest *analysis.Manifest
	manifestRead bool
	// last is the program Analyze last built, for Interfaces.
	last *analyzedProgram
}

// analyzedProgram is a program Analyze built: the entry's analysis and
// nodes and the files it reached.
type analyzedProgram struct {
	fa           *analysis.FileAnalysis
	nodes        []ast.Node
	siblingFAs   map[string]*analysis.FileAnalysis
	siblingNodes map[string][]ast.Node
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
	s := &analysis.ExpansionSite{ModulePath: modulePath, Manifest: m}
	switch {
	case m == nil:
	case c.cfg.VirtualManifest == nil:
		s.Root = c.root
	default:
		s.Root = "."
		s.Read = c.virtualInput
	}
	s.Embed = c.embedFS()
	return s
}

// embedFS is what the program's `embed` declarations read: an in-memory
// program's data files, else the disk under the root, nil when there is
// neither.
func (c *Checker) embedFS() analysis.EmbedFS {
	switch {
	case c.cfg.VirtualData != nil:
		return analysis.MapEmbedFS(c.cfg.VirtualData)
	case c.root != "":
		return analysis.DiskEmbedFS{Root: c.root}
	case c.cfg.EmbedRoot != "":
		return analysis.DiskEmbedFS{Root: c.cfg.EmbedRoot}
	}
	return nil
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

// Loader is the file loader analysis runs with: virtual files first for an
// import, by its path, then the embedded stdlib and disk, with the synthetic
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
			site = &analysis.ExpansionSite{ModulePath: strings.Join(modulePath, "/"), Manifest: m, Embed: analysis.DiskEmbedFS{Root: projectRoot}}
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
		if len(modulePath) > 0 {
			key := strings.Join(modulePath, "/")
			if src, ok := c.cfg.VirtualFiles[key]; ok {
				var errs []parser.ParseError
				nodes, errs = parser.ParseWithRecovery(lexer.Lex(src))
				c.recordSyntax(key+".nomi", errs)
				virtual = true
			}
		}
		// An empty virtual file parses to no nodes and is still that file.
		if !virtual {
			// A directory the virtual files are under is a directory
			// import's, whatever else the path could name: with no root,
			// the base loader reads any path as the standard library's.
			if len(modulePath) > 0 && modulePath[0] != "std" {
				if dir := c.virtualDirectory(modulePath); dir != nil {
					return nil, dir
				}
			}
			var err error
			nodes, err = base(projectRoot, modulePath)
			if err != nil {
				if dir := c.virtualDirectory(modulePath); dir != nil {
					return nil, dir
				}
				return nil, err
			}
		}
		return syntheticextern.Inject(nodes, synth, false)
	}
}

// virtualDirectory is the directory modulePath names among the virtual
// files' module paths, or nil.
func (c *Checker) virtualDirectory(modulePath []string) *analysis.DirectoryError {
	if len(c.cfg.VirtualFiles) == 0 {
		return nil
	}
	keys := make([]string, 0, len(c.cfg.VirtualFiles))
	for key := range c.cfg.VirtualFiles {
		if !strings.HasSuffix(key, ".toml") {
			keys = append(keys, key)
		}
	}
	return analysis.VirtualDirectory(modulePath, keys, nil)
}

// filesParsed counts the project files every Checker in the process has
// parsed: each entry Prepare reads, each project file its loader reads and
// each stdlib module source CheckStdlibSource checks.
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
	c.entrySrc, c.entryMode, c.prepared = src, mode, true
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
	nodes, err = lowerDerives(c.diagPath, src, nodes, c.entryModRel, c.projectNodes)
	if err != nil {
		return nil, err
	}
	if !mode.Tests {
		StripAttachedTests(nodes)
	}
	nodes, _ = analysis.SynthesizeDerives(nodes)
	return analysis.SynthesizeUniversalDebug(nodes), nil
}

// lowerDerives lowers the derives of the file at path, whose module key
// is key: a partner test's for its partner's types first, with the
// partner's nodes from fileNodes (analysis.LowerPartnerDerives).
func lowerDerives(path, src string, nodes []ast.Node, key string, fileNodes func(key string) ([]ast.Node, bool)) ([]ast.Node, error) {
	nodes, errs := analysis.LowerPartnerDerives(nodes, key, fileNodes)
	nodes, lowerErrs := analysis.LowerDerives(nodes)
	errs = append(errs, lowerErrs...)
	if len(errs) == 0 {
		return nodes, nil
	}
	return nil, typeDiagnostics(path, src, errs)
}

// projectNodes answers the nodes of the project file with module key as
// the program's build loads them, and false for none.
func (c *Checker) projectNodes(key string) ([]ast.Node, bool) {
	if c.lib == nil || key == "" {
		return nil, false
	}
	nodes, err := c.Loader()(c.root, strings.Split(key, "/"))
	return nodes, err == nil
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
	// The stdlib modules the build reached, which it built if they were not
	// yet (std.StdLib.Latest).
	c.lib = c.lib.Latest()
	c.projectFiles = collectProjectFiles(siblingFAs, siblingNodes)
	c.last = &analyzedProgram{fa: fa, nodes: nodes, siblingFAs: siblingFAs, siblingNodes: siblingNodes}
	// Each file's own diagnostics, for the check against interfaces
	// (NOMI_CHECK_FROM_INTERFACES) to be compared with.
	local := map[string]*analysis.FileCheck{c.entryModRel: {Build: slices.Clone(fa.TypeErrors)}}
	errs := c.buildErrors(fa)
	// The program's own errors (duplicate and orphan impls) are reported with
	// the entry's, after its build-phase errors, each in the file it names
	// (entryDiagnostics).
	errs = append(errs, c.kept(fa.Program().Errors)...)
	local[c.entryModRel].Check = analysis.CheckTypes(fa, nodes)
	errs = append(errs, c.kept(local[c.entryModRel].Check)...)
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
		l := &analysis.FileCheck{Build: slices.Clone(sibFA.TypeErrors), Check: analysis.CheckTypes(sibFA, siblingNodes[key])}
		// Its break and continue placement, which only its own call sites
		// decide (analysis.AnalyzeIterSensitivity).
		l.Check = append(l.Check, analysis.AnalyzeIterSensitivity(sibFA, siblingNodes[key])...)
		local[key] = l
		sibErrs[key] = append(own, c.kept(l.Check)...)
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
	// The agreement of every noun with every phrase.
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
	// Every file's calls to boot-only functions, which the check follows
	// across the whole program (analysis.CheckBootScope).
	for _, key := range sibKeys {
		sibFA := siblingFAs[key]
		if _, shared := c.sharedStdlib(key, sibFA); shared {
			continue
		}
		sibErrs[key] = append(sibErrs[key], c.kept(analysis.CheckBootScope(sibFA, siblingNodes[key]))...)
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
		own := sibErrs[key]
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
	iter := analysis.AnalyzeIterSensitivity(fa, nodes)
	concurrent := analysis.CheckConcurrentScope(fa, nodes)
	tasks := analysis.CheckTaskLifetime(fa, nodes)
	local[c.entryModRel].EntryChecks = append(append(slices.Clone(iter), concurrent...), tasks...)
	errs = append(errs, iter...)
	errs = append(errs, concurrent...)
	errs = append(errs, analysis.CheckBootScope(fa, nodes)...)
	errs = append(errs, tasks...)
	analysis.MarkTailCalls(nodes)
	if checkFromInterfaces && c.prepared {
		if err := c.compareFromInterfaces(fa, nodes, siblingFAs, siblingNodes, local); err != nil {
			return fa, map[string][]ast.Node{"": nodes}, err
		}
	}
	modules := make(map[string][]ast.Node, len(siblingNodes)+1)
	modules[""] = nodes
	for key, sibNodes := range siblingNodes {
		modules[key] = sibNodes
	}
	// A syntax or check error in the embedded stdlib is a defect in this
	// nomi, and every program is checked against that stdlib and runs its
	// bodies, so it is reported ahead of anything the program's own files
	// say.
	if ds := stdDefectDiagnostics(c.lib); ds != nil {
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
	// A type declared under a name the language reserves is refused, and
	// every use of the name still means the built-in, so what the checker
	// says about those uses follows from the refusal: it is reported alone.
	if refused := analysis.ReservedNameRefusals(errs); len(refused) > 0 || anyReservedNameRefusal(sibErrs) {
		ds := c.entryDiagnostics(fa, refused)
		for _, key := range sibKeys {
			if own := analysis.ReservedNameRefusals(sibErrs[key]); len(own) > 0 {
				sibPath := siblingFAs[key].FilePath
				if sibPath == "" {
					sibPath = key + ".nomi"
				}
				ds = append(ds, c.fileDiagnostics(sibPath, "", own)...)
			}
		}
		return fa, modules, ds
	}
	if len(errs) == 0 && len(sibDiags) == 0 {
		return fa, modules, nil
	}
	return fa, modules, append(c.entryDiagnostics(fa, errs), sibDiags...)
}

// anyReservedNameRefusal reports whether any file's errors refuse a
// declaration for its reserved name.
func anyReservedNameRefusal(errsOf map[string][]analysis.TypeError) bool {
	for _, errs := range errsOf {
		if len(analysis.ReservedNameRefusals(errs)) > 0 {
			return true
		}
	}
	return false
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

// relatedInBand drops each related location of errs in code the compiler
// generated: a line in the synthesized band is never shown.
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
	c.lib = c.lib.Latest()
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
	if ds := stdDefectDiagnostics(c.lib); ds != nil {
		return nil, ds
	}
	// A module's own source is checked against every other module: its
	// impl index holds the stdlib's impls.
	c.lib = c.lib.Complete()
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

// CheckStdlibTest checks the `test` declaration that src, the source of
// the stdlib module moduleKey with one test appended, holds from line on,
// in the module's scope, as CheckStdlibSource checks it, but against the
// shared stdlib's analysis of the module rather than src's: the module's own
// declarations and bodies are the embedded ones, already checked, so only
// the test is (analysis.FileAnalysis.CheckAddedTest). It answers the test,
// the copy of the module's analysis holding it, and the restore the copy
// asks for once the caller is done with it.
//
// ok is false when the appended text is not one test declaration alone (an
// edit that closes the test and declares more), or the stdlib was analyzed
// from source; the caller checks src whole with CheckStdlibSource.
func (c *Checker) CheckStdlibTest(moduleKey, src string, line int) (test *ast.TestDecl, fa *analysis.FileAnalysis, restore func(), ok bool, err error) {
	module := strings.TrimSuffix(strings.TrimPrefix(moduleKey, "std/"), ".nomi")
	moduleKey = "std/" + module
	if c.diagPath == "" {
		c.diagPath = moduleKey + ".nomi"
	}
	if c.lib == nil || !c.lib.FromInterfaces() || stdDefectDiagnostics(c.lib) != nil || line < 1 {
		return nil, nil, nil, false, nil
	}
	if c.lib.Ensure(module).Files[module] == nil {
		return nil, nil, nil, false, nil
	}
	// The test alone, at its lines: the module's lines above it are blank.
	lines := strings.SplitAfterN(src, "\n", line)
	if len(lines) < line {
		return nil, nil, nil, false, nil
	}
	text := strings.Repeat("\n", line-1) + lines[line-1]
	nodes, perr := parser.Parse(lexer.Lex(text))
	if perr != nil {
		return nil, nil, nil, true, located(c.diagPath, src, perr)
	}
	if nodes, err = lowerDerives(c.diagPath, src, nodes, "", nil); err != nil {
		return nil, nil, nil, true, err
	}
	nodes, _ = analysis.SynthesizeDerives(nodes)
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	if len(nodes) != 1 {
		return nil, nil, nil, false, nil
	}
	test, isTest := nodes[0].(*ast.TestDecl)
	if !isTest {
		return nil, nil, nil, false, nil
	}
	// The module and every module the test imports, which the test's check
	// in the module's scope resolves. A stdlib built a module at a time
	// (std.Shared) may have built one of them after the module, so the
	// module's impl index lacks its impls: the test is then checked with the
	// whole module, as an edit that is not one test alone is.
	imports := analysis.StdModuleImports(module, nodes)
	c.lib = c.lib.Ensure(append([]string{module}, imports...)...)
	shared := c.lib.Files[module]
	for _, name := range imports {
		if !shared.ProgramHolds(c.lib.Files[name]) {
			return nil, nil, nil, false, nil
		}
	}
	if err := c.lib.Bodies(module); err != nil {
		return nil, nil, nil, false, nil
	}
	fa, errs, restore, kept := shared.CheckAddedTest(test)
	if !kept {
		return nil, nil, nil, false, nil
	}
	if len(errs) > 0 {
		restore()
		return nil, nil, nil, true, typeDiagnostics(c.diagPath, src, errs)
	}
	return test, fa, restore, true, nil
}

// StdlibErrors answers the diagnostics the process's one analysis of the
// stdlib (std.Shared) recorded, as a program's check reports them: every
// syntax error first and alone, else every module's check errors, each with
// the hint that the file is the stdlib built into this nomi. nil when the
// stdlib analyzed cleanly. That analysis keeps every `//!` prompt and checks
// it in its module's scope, as CheckStdlibSource does, so a run of an
// embedded module's cases built from it reports what `nomi check` on the
// module reports. Without this, `nomi test std/<module>.nomi` lowered
// prompts the checker had rejected, and the builder resolved a name such as
// a bare `False` that the module never imported; and an error in a module
// the cases reach (a body of std/duration.nomi that a std/timer.nomi case
// calls) made the case BLOCKED, since the body did not lower, rather than
// failing the file with the error.
func StdlibErrors() error { return stdlibErrors(std.Shared()) }

// stdlibErrors is StdlibErrors for lib.
func stdlibErrors(lib *std.StdLib) error {
	if ds := stdDefectDiagnostics(lib); ds != nil {
		return ds
	}
	return nil
}

func (c *Checker) prepareStdlib(src string, mode Mode) ([]ast.Node, error) {
	c.entrySrc, c.prepared = src, false
	filesParsed.Add(1)
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		return nil, located(c.diagPath, src, err)
	}
	nodes, err = lowerDerives(c.diagPath, src, nodes, "", nil)
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

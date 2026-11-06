package std

// The process's stdlib, built from its modules' interfaces, a module at a
// time.
//
// A program's check reads of the stdlib only what its modules declare
// (docs/implementation-notes.md, "File interfaces"), so Shared builds the
// stdlib from each module's interface, whose bodies are stubs, and checks
// no body. What the bodies tell a program's check (the stdlib's
// diagnostics, the conformance demands its bodies and attached tests make,
// and which of its functions are boot-only) are the stdlib's facts
// (analysis.StdFacts), worked out from an analysis from source (Load) and
// kept beside the interfaces. A module's bodies and attached tests are
// grafted back from its source and checked when something first reads them
// (Bodies): the module's lowering when no std bundle has it, an instance of
// one of its generic functions, a program that lowers a listed module, its
// cases, an editor's hover of one of its declarations.
//
// Shared holds only the modules something has reached: at first the
// prelude and what it imports, then each module a build against it reaches
// with every module that one imports (analysis.StdSource, Ensure), so a
// program pays for the stdlib it uses. Each answer is a snapshot: a larger
// stdlib is a new StdLib sharing the smaller one's analyses, so a caller
// holding one keeps a consistent view while another goroutine extends it.
// All builds every module, for what reads the whole stdlib (the language
// server, the reference).
//
// The interfaces and facts are one artifact (stdArtifact), which a binary
// built with the nomi_stdbundle tag embeds (stdbundlegen writes it with the
// std bundle) and any other binary keeps in the artifact cache under its
// build ID. A process that finds none parses the stdlib and extracts the
// interfaces, analyzes it from source for the facts, builds it from the
// interfaces as a later process does, and writes the artifact for the next.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// stdBaseModules are the modules the stdlib is first built with, with
// every module they import: the prelude, which every program file imports,
// and std/assertions, whose AssertionFailure is the error every test, `?`
// in a test and `assert` carries (analysis.TypeAssertionFailure), which a
// file declaring tests does not import, and which the first build that
// holds it installs for the process. std/io adds one module to them, and
// nearly every program imports it, which would otherwise cost a build of
// its own (each costs about 3 ms over the modules built before it).
var stdBaseModules = []string{"prelude", "assertions", "io"}

// stdCore is a stdlib built from an artifact a module at a time: the
// artifact, the modules built so far (cur, the latest snapshot) and what
// checking a module's bodies later needs. mu serializes building modules
// and checking bodies.
type stdCore struct {
	mu  sync.Mutex
	art *stdArtifact
	// names is every module the artifact holds, sorted.
	names []string
	// facts are the artifact's facts, decoded once the prelude is built
	// (its path names every module's), nil until then. computing is set
	// while buildFromArtifact works them out, so no build installs them.
	facts     *analysis.StdFacts
	computing bool
	deferred  *deferredBodies
	cur       atomic.Pointer[StdLib]
}

// deferredBodies is what a stdlib built from interfaces keeps to check a
// module's bodies later: the interface nodes the build was given for each
// module, which the bodies are grafted into, and the modules whose bodies
// are checked. Guarded by the core's mu.
type deferredBodies struct {
	// declaredIn maps each declaration node of the modules in indexed to
	// its module (AttachedTests).
	declaredIn map[ast.Node]string
	indexed    map[string]bool

	iface   map[string][]ast.Node
	checked map[string]bool
	// tests holds each checked module's test declarations, which lib.Nodes
	// does not hold.
	tests map[string][]*ast.TestDecl
}

func newStdCore(art *stdArtifact) *stdCore {
	c := &stdCore{
		art: art,
		deferred: &deferredBodies{
			declaredIn: map[ast.Node]string{},
			indexed:    map[string]bool{},
			iface:      map[string][]ast.Node{},
			checked:    map[string]bool{},
			tests:      map[string][]*ast.TestDecl{},
		},
	}
	for name := range art.ifaces {
		c.names = append(c.names, name)
	}
	sort.Strings(c.names)
	return c
}

// FromInterfaces reports whether lib was built from its modules' interfaces
// (Shared), so a module's bodies are checked only when Bodies asks.
func (lib *StdLib) FromInterfaces() bool { return lib != nil && lib.deferred != nil }

// Ensure answers a stdlib holding lib's modules and the named ones, with
// every module they import: lib itself when it holds them, or when it was
// analyzed whole (Load). A name that is no module is left out.
func (lib *StdLib) Ensure(names ...string) *StdLib {
	c := lib.core
	if c == nil {
		return lib
	}
	missing := false
	for _, name := range names {
		if lib.Files[name] == nil {
			missing = true
			break
		}
	}
	if !missing {
		return lib
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next, err := c.ensureLocked(names)
	if err != nil {
		panic("std: the stdlib does not build from its own interfaces: " + err.Error())
	}
	return next
}

// Latest answers the largest snapshot of lib's stdlib, which holds lib's
// modules and any a build has added since (analysis.StdSource): what a
// caller that held lib reads after a build against it. lib itself when it
// was analyzed whole (Load).
func (lib *StdLib) Latest() *StdLib {
	if lib == nil || lib.core == nil {
		return lib
	}
	// A snapshot only grows, so one as large as lib holds lib's modules:
	// lib itself is kept, with whatever a caller set on its copy.
	if cur := lib.core.cur.Load(); len(cur.Files) != len(lib.Files) {
		return cur
	}
	return lib
}

// Complete answers lib with every module built (Ensure of every module).
func (lib *StdLib) Complete() *StdLib {
	if lib.core == nil {
		return lib
	}
	return lib.Ensure(lib.core.names...)
}

// ModuleNames answers every module of the stdlib, built or not, by its name
// under std/, sorted.
func (lib *StdLib) ModuleNames() []string {
	if lib.core != nil {
		return slices.Clone(lib.core.names)
	}
	names := make([]string, 0, len(lib.Files))
	for name := range lib.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All is the process's stdlib with every module built: what reads the
// whole stdlib rather than a program's part of it (the language server,
// the stdlib reference, a test that walks every module).
func All() *StdLib { return Shared().Complete() }

// Extend is analysis.StdSource's: have's modules and the named ones built,
// when have is a part of this core's stdlib. Any other stdlib (Load's)
// holds every module already.
func (c *stdCore) Extend(have map[string]*analysis.FileAnalysis, names []string) map[string]*analysis.FileAnalysis {
	cur := c.cur.Load()
	for name, fa := range have {
		if cur == nil || cur.Files[name] != fa {
			return have
		}
		break
	}
	return cur.Ensure(names...).Latest().Files
}

// Built is analysis.StdSource's.
func (c *stdCore) Built(name string) *analysis.FileAnalysis {
	if cur := c.cur.Load(); cur != nil {
		return cur.Files[name]
	}
	return nil
}

// ModuleNames is analysis.StdSource's.
func (c *stdCore) ModuleNames() []string { return slices.Clone(c.names) }

// Base is analysis.StdSource's: the base of the snapshot whose analyses
// have are, which a program's build takes (analysis.StdBase), worked out
// once per snapshot when a build first asks.
func (c *stdCore) Base(have map[string]*analysis.FileAnalysis) *analysis.StdBase {
	lib := c.cur.Load()
	if lib == nil || len(lib.Files) != len(have) {
		return nil
	}
	for name, fa := range have {
		if lib.Files[name] != fa {
			return nil
		}
	}
	return lib.stdBase()
}

// StdBase is lib's base, formed on the first call for a stdlib built a
// module at a time (which forms it when a build first asks): what Base
// holds once formed.
func (lib *StdLib) StdBase() *analysis.StdBase { return lib.stdBase() }

// stdBase is lib's base (Base), built on the first call.
func (lib *StdLib) stdBase() *analysis.StdBase {
	if lib.core == nil || lib.core.computing || lib.baseOnce == nil {
		return lib.Base
	}
	lib.baseOnce.Do(func() {
		lib.Base = analysis.BuildStdBase(lib.Files)
	})
	return lib.Base
}

// closure answers the modules among names and what they import, at any
// depth, that have lacks, sorted. A name that is no module is left out.
func (c *stdCore) closure(names []string, have map[string]*analysis.FileAnalysis) []string {
	seen := map[string]bool{}
	queue := slices.Clone(names)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] || have[name] != nil {
			continue
		}
		if _, ok := c.art.ifaces[name]; !ok {
			continue
		}
		seen[name] = true
		queue = append(queue, c.art.imports[name]...)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ensureLocked builds the named modules and what they import, under c.mu,
// and answers the latest stdlib.
func (c *stdCore) ensureLocked(names []string) (*StdLib, error) {
	cur := c.cur.Load()
	var have map[string]*analysis.FileAnalysis
	if cur != nil {
		have = cur.Files
	}
	missing := c.closure(names, have)
	if len(missing) == 0 {
		return cur, nil
	}
	var decodeErr error
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		name := embeddedStdlibName(modulePath)
		data, ok := c.art.ifaces[name]
		if !ok {
			return nil, missingStdModule(name, modulePath)
		}
		fi, err := analysis.DecodeInterface(data)
		if err != nil {
			decodeErr = fmt.Errorf("std/%s: %w", name, err)
			return nil, err
		}
		c.deferred.iface[name] = fi.Nodes
		return fi.Nodes, nil
	}
	entry := make([]ast.Node, 0, len(missing))
	for i, name := range missing {
		entry = append(entry, stdlibDependencyImport(name, i+1))
	}
	var cache map[string]*analysis.FileAnalysis
	var extended map[string][]ast.Node
	if cur == nil {
		cache, extended = analysis.BuildStdDeclarations(entry, loader)
	} else {
		cache, extended = analysis.BuildStdDeclarationsOver(entry, loader, have)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	next := &StdLib{
		Modules:      make(map[string]*analysis.Scope, len(have)+len(cache)),
		Files:        make(map[string]*analysis.FileAnalysis, len(have)+len(cache)),
		Nodes:        make(map[string][]ast.Node, len(have)+len(cache)),
		SyntaxErrors: c.art.syntax,
		deferred:     c.deferred,
		core:         c,
		baseOnce:     new(sync.Once),
	}
	if cur != nil {
		copyModules(next, cur)
		next.Primitives, next.Errors = cur.Primitives, cur.Errors
	}
	var added []string
	for key, fa := range cache {
		name, ok := strings.CutPrefix(key, "std/")
		if !ok {
			continue
		}
		added = append(added, name)
		next.Files[name] = fa
		next.Modules[name] = fa.ModuleScope
		next.Nodes[name] = extended[key]
	}
	sort.Strings(added)
	if cur == nil {
		if preludeFA, ok := next.Files["prelude"]; ok {
			next.Primitives = preludeFA.ModuleScope
		} else {
			next.Primitives = analysis.NewScope(nil)
		}
		// The prelude's scope is the parent of every program file's. No
		// build parents a scope under another std module's scope, which
		// holds its body scopes once Bodies has walked them.
		next.Primitives.MarkShared()
	}
	if !c.computing {
		if err := c.installFacts(next, added); err != nil {
			return nil, err
		}
	}
	c.cur.Store(next)
	return next, nil
}

// copyModules copies from's modules into to's maps.
func copyModules(to, from *StdLib) {
	for name, fa := range from.Files {
		to.Files[name] = fa
		to.Modules[name] = from.Modules[name]
		to.Nodes[name] = from.Nodes[name]
	}
}

// installFacts installs the facts of the modules added into lib, decoding
// them on the first call.
func (c *stdCore) installFacts(lib *StdLib, added []string) error {
	if c.facts == nil {
		prelude := lib.Files["prelude"]
		if prelude == nil {
			return fmt.Errorf("the stdlib has no prelude")
		}
		// A position names a module's file by the path the build gives it
		// (analysis.ResolveModulePath), the prelude's beside every other
		// module's; the facts name it by the module.
		dir := filepath.Dir(prelude.FilePath)
		var badName string
		facts, err := analysis.DecodeStdFacts(c.art.facts, func(name string) string {
			if module, ok := strings.CutPrefix(name, "@"); ok {
				if _, ok := c.art.ifaces[module]; ok {
					return filepath.Join(dir, filepath.FromSlash(module)) + ".nomi"
				}
			}
			badName = name
			return name
		})
		if err == nil && badName != "" {
			err = fmt.Errorf("the stdlib's facts name %s, which is not a std module", badName)
		}
		if err != nil {
			return err
		}
		c.facts = facts
		lib.Errors = facts.Errors
	}
	for _, name := range added {
		if fa := lib.Files[name]; fa.FilePath != filepath.Join(filepath.Dir(lib.Files["prelude"].FilePath), filepath.FromSlash(name))+".nomi" {
			return fmt.Errorf("std/%s is at %s, not beside the prelude", name, fa.FilePath)
		}
	}
	return c.facts.InstallModules(lib.Files, added)
}

// Bodies checks module's bodies, when lib was built from interfaces and
// they are not checked yet: they are grafted from the module's source into
// the nodes lib.Nodes holds, attached tests included, and walked and
// checked in place with the module's test declarations, so lib.Files'
// analysis of the module then reads them as Load's does
// (TestShared_ModulesAreLoadsModules). A module lib has not built is built
// first, with every module its bodies import. Anything that reads a std
// module's bodies (its lowering, an instance of one of its generic
// functions, its cases) asks for them first. A program's check reads none:
// what it needs of std's bodies is in the facts, and a stub body keeps the
// `break` and `continue` its function's own scope holds. An error says the
// embedded source is not the one the interfaces were extracted from.
func (lib *StdLib) Bodies(module string) error {
	d := lib.deferred
	if d == nil {
		return nil
	}
	c := lib.core
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bodiesLocked(module)
}

func (c *stdCore) bodiesLocked(module string) error {
	d := c.deferred
	if d.checked[module] {
		return nil
	}
	cur, err := c.ensureLocked([]string{module})
	if err != nil {
		return err
	}
	iface, ok := d.iface[module]
	fa := cur.Files[module]
	if !ok || fa == nil {
		return fmt.Errorf("std: no module %s", module)
	}
	data, ok := ReadFile(module)
	if !ok {
		return fmt.Errorf("std: no source for %s", module)
	}
	source, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	tests, err := analysis.GraftBodies(iface, source)
	if err != nil {
		return fmt.Errorf("std/%s: %w", module, err)
	}
	// As Load checks a module: its declarations, then its test
	// declarations.
	nodes := cur.Nodes[module]
	checked := make([]ast.Node, 0, len(nodes)+len(tests))
	checked = append(checked, nodes...)
	for _, td := range tests {
		checked = append(checked, td)
	}
	// The module's demands and diagnostics are the facts' (Install), which
	// the check makes again.
	manifest := fa.ImplManifest
	if _, ok := fa.CheckDeferredBodies(checked); !ok {
		return fmt.Errorf("std/%s was not built from its interface", module)
	}
	fa.ImplManifest = manifest
	analysis.MarkTailCalls(checked)
	d.tests[module] = tests
	d.checked[module] = true
	return nil
}

// AllBodies builds every module and checks every module's bodies (Bodies),
// in module order. Bodies writes into the shared modules' nodes, analyses
// and scopes, which every check of a program reads, so a process that
// checks programs on several goroutines while it lowers one (the language
// server) calls this before it checks any: no later Bodies then writes, and
// no build extends the stdlib.
func (lib *StdLib) AllBodies() error {
	c := lib.core
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.ensureLocked(c.names); err != nil {
		return err
	}
	for _, module := range c.names {
		if err := c.bodiesLocked(module); err != nil {
			return err
		}
	}
	return nil
}

// AttachedTests answers the attached tests of n, a declaration: those n
// holds. A stdlib built from its interfaces holds a module's attached tests
// once Bodies has checked them, so for a declaration of such a stdlib whose
// module's bodies are unchecked this checks them first. An editor's hover
// shows a std declaration's attached tests as its examples.
func (lib *StdLib) AttachedTests(n ast.Node) []ast.AttachedTest {
	if c := lib.core; c != nil && lib.deferred != nil {
		c.mu.Lock()
		d := c.deferred
		cur := c.cur.Load()
		for module, nodes := range cur.Nodes {
			if d.indexed[module] {
				continue
			}
			d.indexed[module] = true
			for _, top := range nodes {
				ast.Inspect(top, func(m ast.Node) bool {
					d.declaredIn[m] = module
					switch m.(type) {
					case *ast.FuncDef, *ast.OnceBinding:
						return false
					}
					return true
				})
			}
		}
		module, ok := d.declaredIn[n]
		var err error
		if ok {
			err = c.bodiesLocked(module)
		}
		c.mu.Unlock()
		if err != nil {
			return nil
		}
	}
	return ast.AttachedTestsOf(n)
}

// TestDecls answers module's test declarations, which lib.Nodes holds when
// lib was analyzed from source (Load) and Bodies checks otherwise.
func (lib *StdLib) TestDecls(module string) ([]ast.Node, error) {
	var out []ast.Node
	if c := lib.core; c != nil && lib.deferred != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := c.bodiesLocked(module); err != nil {
			return nil, err
		}
		for _, td := range c.deferred.tests[module] {
			out = append(out, td)
		}
		return out, nil
	}
	for _, n := range lib.Nodes[module] {
		if td, ok := n.(*ast.TestDecl); ok {
			out = append(out, td)
		}
	}
	return out, nil
}

// sharedCore is the process's stdlib (Shared).
var sharedCore = sync.OnceValue(loadShared)

// Shared is the process's one analysis of the stdlib, built from its
// modules' interfaces: the modules built so far, which a build against it
// adds to (Ensure, analysis.StdSource), so each call may answer a larger
// stdlib than the last. The front end (internal/frontend) resolves a
// program against it and internal/irbuild lowers the stdlib from it, so
// `nomi run` analyzes the stdlib once rather than once per consumer.
//
// Callers only read it. A project build writes nothing into a shared
// FileAnalysis it does not own (analysis.buildProjectWithCache gives its
// Program only to the files it built). A module's bodies are checked only
// when something asks for them (Bodies).
func Shared() *StdLib { return sharedCore().cur.Load() }

// loadShared is Shared's core: the stdlib built from its interfaces, with
// its facts, from the artifact when there is one, its prelude and what that
// imports built; otherwise the artifact is made here, with every module
// built, and kept for the next process.
func loadShared() *stdCore {
	loadMu.Lock()
	defer loadMu.Unlock()
	loadCount.Add(1)
	art, from := readStdArtifact()
	var c *stdCore
	var err error
	if art != nil {
		c, err = lazyFromArtifact(art)
		if err != nil {
			// An artifact this binary did not make (a stale or corrupt
			// cache, or a broken build that embedded it): make it again.
			stdArtifactDebug("%s not used: %v", from, err)
			art = nil
		}
	}
	if art == nil {
		art = extractStdArtifact()
		if _, err = coreFromArtifact(art, load()); err == nil {
			writeStdArtifact(art)
			// This process builds the stdlib from the artifact as every
			// later one does, a module at a time.
			c, err = lazyFromArtifact(art)
		}
	}
	if err != nil {
		panic("std: the stdlib does not build from its own interfaces: " + err.Error())
	}
	analysis.SetStdSource(c)
	return c
}

// lazyFromArtifact is the stdlib art holds with its base modules built. A
// stdlib with errors (Errors) is built whole, so every module's diagnostics
// name the paths a build gives them, and so is every stdlib under
// NOMI_STD_WHOLE=1.
func lazyFromArtifact(art *stdArtifact) (*stdCore, error) {
	c := newStdCore(art)
	c.mu.Lock()
	defer c.mu.Unlock()
	first := stdBaseModules
	if os.Getenv("NOMI_STD_WHOLE") == "1" {
		// Every module in one build, as before the stdlib was built a
		// module at a time: what a lazily built stdlib's answers are
		// compared with (TestStdLazyRunsAsWhole).
		first = c.names
	}
	lib, err := c.ensureLocked(first)
	if err != nil {
		return nil, err
	}
	if len(lib.Errors) > 0 {
		if _, err := c.ensureLocked(c.names); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// buildFromArtifact builds the stdlib from art's interfaces, every module of
// it. With full, the stdlib analyzed from source (Load), art has no facts
// yet: they are worked out from full, written into art and read back, as a
// process that finds art reads them.
func buildFromArtifact(art *stdArtifact, full *StdLib) (*StdLib, error) {
	c, err := coreFromArtifact(art, full)
	if err != nil {
		return nil, err
	}
	return c.cur.Load(), nil
}

// coreFromArtifact is buildFromArtifact's core.
func coreFromArtifact(art *stdArtifact, full *StdLib) (*stdCore, error) {
	c := newStdCore(art)
	c.mu.Lock()
	defer c.mu.Unlock()
	if full == nil {
		if _, err := c.ensureLocked(c.names); err != nil {
			return nil, err
		}
		return c, nil
	}
	c.computing = true
	lib, err := c.ensureLocked(c.names)
	c.computing = false
	if err != nil {
		return nil, err
	}
	// The facts of an analysis from source, laid over the modules built
	// from their interfaces.
	facts, err := analysis.ComputeStdFacts(full.Files, lib.Files, full.Errors)
	if err != nil {
		return nil, err
	}
	// A position names a module's file by the path the analysis gave it,
	// which depends on where the process found the stdlib, so the facts
	// name it by the module.
	byPath := map[string]string{}
	for name, fa := range full.Files {
		byPath[fa.FilePath] = name
	}
	var bad []string
	art.facts = facts.Encode(func(path string) string {
		if path == "" {
			return ""
		}
		name, ok := byPath[path]
		if !ok {
			bad = append(bad, path)
			return path
		}
		return "@" + name
	})
	if len(bad) > 0 {
		return nil, fmt.Errorf("the stdlib's facts name files outside it: %s", strings.Join(bad, ", "))
	}
	if err := c.installFacts(lib, c.names); err != nil {
		return nil, err
	}
	return c, nil
}

// stdEntryNodes is the entry Load builds the stdlib from: an import of
// every module, which binds no name.
func stdEntryNodes() []ast.Node {
	var entryNodes []ast.Node
	line := 1
	for _, name := range embeddedStdlibNames() {
		entryNodes = append(entryNodes, stdlibDependencyImport(name, line))
		line++
	}
	return append(entryNodes, stdlibDependencyImport("prelude", line))
}

// stdArtifactDebug reports why an artifact was not used, under
// NOMI_DEBUG_STD_BUNDLE=1, as internal/irbuild reports its bundle.
func stdArtifactDebug(format string, args ...any) {
	if os.Getenv("NOMI_DEBUG_STD_BUNDLE") == "1" {
		fmt.Fprintf(os.Stderr, "nomi: std interfaces: "+format+"\n", args...)
	}
}

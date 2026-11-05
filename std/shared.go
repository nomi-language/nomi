package std

// The process's stdlib, built from its modules' interfaces.
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
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// deferredBodies is what Shared keeps to check a module's bodies later:
// the interface nodes the build was given for each module, which the
// bodies are grafted into, and the modules whose bodies are checked.
type deferredBodies struct {
	// declaredIn maps each declaration node to its module (AttachedTests).
	declaredIn     map[ast.Node]string
	declaredInOnce sync.Once

	mu      sync.Mutex
	iface   map[string][]ast.Node
	checked map[string]bool
	// tests holds each checked module's test declarations, which lib.Nodes
	// does not hold.
	tests map[string][]*ast.TestDecl
}

// FromInterfaces reports whether lib was built from its modules' interfaces
// (Shared), so a module's bodies are checked only when Bodies asks.
func (lib *StdLib) FromInterfaces() bool { return lib != nil && lib.deferred != nil }

// Bodies checks module's bodies, when lib was built from interfaces and
// they are not checked yet: they are grafted from the module's source into
// the nodes lib.Nodes holds, attached tests included, and walked and
// checked in place with the module's test declarations, so lib.Files'
// analysis of the module then reads them as Load's does
// (TestShared_ModulesAreLoadsModules). Anything that reads a std module's
// bodies (its lowering, an instance of one of its generic functions, its
// cases) asks for them first. A program's check reads none: what it needs
// of std's bodies is in the facts, and a stub body keeps the `break` and
// `continue` its function's own scope holds. An error says the embedded
// source is not the one the interfaces were extracted from.
func (lib *StdLib) Bodies(module string) error {
	d := lib.deferred
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.checked[module] {
		return nil
	}
	iface, ok := d.iface[module]
	fa := lib.Files[module]
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
	nodes := lib.Nodes[module]
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

// AllBodies checks every module's bodies (Bodies), in module order. Bodies
// writes into the shared modules' nodes, analyses and scopes, which every
// check of a program reads, so a process that checks programs on several
// goroutines while it lowers one (the language server) calls this before
// it checks any: no later Bodies then writes.
func (lib *StdLib) AllBodies() error {
	if lib.deferred == nil {
		return nil
	}
	modules := make([]string, 0, len(lib.Files))
	for module := range lib.Files {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	for _, module := range modules {
		if err := lib.Bodies(module); err != nil {
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
	if d := lib.deferred; d != nil {
		d.declaredInOnce.Do(func() {
			d.declaredIn = map[ast.Node]string{}
			for module, nodes := range lib.Nodes {
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
		})
		if module, ok := d.declaredIn[n]; ok {
			if err := lib.Bodies(module); err != nil {
				return nil
			}
		}
	}
	return ast.AttachedTestsOf(n)
}

// TestDecls answers module's test declarations, which lib.Nodes holds when
// lib was analyzed from source (Load) and Bodies checks otherwise.
func (lib *StdLib) TestDecls(module string) ([]ast.Node, error) {
	var out []ast.Node
	if d := lib.deferred; d != nil {
		if err := lib.Bodies(module); err != nil {
			return nil, err
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, td := range d.tests[module] {
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

// loadShared is Shared: the stdlib built from its interfaces, with its
// facts, from the artifact when there is one and otherwise made here and
// kept for the next process.
func loadShared() *StdLib {
	loadMu.Lock()
	defer loadMu.Unlock()
	loadCount.Add(1)
	art, from := readStdArtifact()
	var full *StdLib
	if art == nil {
		art, full = extractStdArtifact(), load()
	}
	lib, err := buildFromArtifact(art, full)
	if err != nil && from != "" {
		// An artifact this binary did not make (a stale or corrupt cache,
		// or a broken build that embedded it): make it again.
		stdArtifactDebug("%s not used: %v", from, err)
		art, full = extractStdArtifact(), load()
		lib, err = buildFromArtifact(art, full)
		from = ""
	}
	if err != nil {
		panic("std: the stdlib does not build from its own interfaces: " + err.Error())
	}
	if from == "" && art.facts != nil {
		writeStdArtifact(art)
	}
	return lib
}

// buildFromArtifact builds the stdlib from art's interfaces. With full, the
// stdlib analyzed from source (Load), art has no facts yet: they are worked
// out from full, written into art and read back, as a process that finds art
// reads them.
func buildFromArtifact(art *stdArtifact, full *StdLib) (*StdLib, error) {
	lib := &StdLib{
		Modules:      make(map[string]*analysis.Scope),
		Files:        make(map[string]*analysis.FileAnalysis),
		Nodes:        make(map[string][]ast.Node),
		Errors:       make(map[string][]analysis.TypeError),
		SyntaxErrors: make(map[string][]parser.ParseError),
		deferred: &deferredBodies{
			iface:   map[string][]ast.Node{},
			checked: map[string]bool{},
			tests:   map[string][]*ast.TestDecl{},
		},
	}
	for name, errs := range art.syntax {
		lib.SyntaxErrors[name] = errs
	}
	var decodeErr error
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		name := embeddedStdlibName(modulePath)
		data, ok := art.ifaces[name]
		if !ok {
			return nil, fmt.Errorf("module not found: %s", filepath.Join(modulePath...))
		}
		fi, err := analysis.DecodeInterface(data)
		if err != nil {
			decodeErr = fmt.Errorf("std/%s: %w", name, err)
			return nil, err
		}
		lib.deferred.iface[name] = fi.Nodes
		return fi.Nodes, nil
	}
	cache, extended := analysis.BuildStdDeclarations(stdEntryNodes(), loader)
	if decodeErr != nil {
		return nil, decodeErr
	}
	for key, fa := range cache {
		name, ok := strings.CutPrefix(key, "std/")
		if !ok {
			continue
		}
		lib.Files[name] = fa
		lib.Modules[name] = fa.ModuleScope
		lib.Nodes[name] = extended[key]
	}
	if preludeFA, ok := lib.Files["prelude"]; ok {
		lib.Primitives = preludeFA.ModuleScope
	} else {
		lib.Primitives = analysis.NewScope(nil)
	}

	if full != nil {
		// The facts of an analysis from source, laid over the modules
		// built from their interfaces.
		facts, err := analysis.ComputeStdFacts(full.Files, lib.Files, full.Errors)
		if err != nil {
			return nil, err
		}
		// A position names a module's file by the path the analysis gave
		// it, which depends on where the process found the stdlib, so the
		// facts name it by the module.
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
	}
	var badName string
	facts, err := analysis.DecodeStdFacts(art.facts, func(name string) string {
		if module, ok := strings.CutPrefix(name, "@"); ok {
			if fa := lib.Files[module]; fa != nil {
				return fa.FilePath
			}
		}
		badName = name
		return name
	})
	if err == nil && badName != "" {
		err = fmt.Errorf("the stdlib's facts name %s, which is not a std module", badName)
	}
	if err != nil {
		return nil, err
	}
	if err := facts.Install(lib.Files); err != nil {
		return nil, err
	}
	lib.Errors = facts.Errors

	// The prelude's scope is the parent of every program file's. No build
	// parents a scope under another std module's scope, which holds its
	// body scopes once Bodies has walked them.
	lib.Primitives.MarkShared()
	lib.Base = analysis.BuildStdBase(lib.Files)
	return lib, nil
}

// stdEntryNodes is the entry Load and Shared build the stdlib from: an
// import of every module, which binds no name.
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

package std

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestLoadPrimitives(t *testing.T) {
	lib := Load()
	if lib.Primitives == nil {
		t.Fatal("expected primitives to be loaded")
	}
	sym := lib.Primitives.Lookup("Int")
	if sym == nil {
		t.Fatal("expected Int in primitives scope")
	}
	// Maybe and Some come into the prelude scope via std.maybe imports
	sym = lib.Primitives.Lookup("Maybe")
	if sym == nil {
		t.Fatal("expected Maybe in primitives scope")
	}
	sym = lib.Primitives.Lookup("Some")
	if sym == nil {
		t.Fatal("expected Some in primitives scope")
	}
}

func TestLoadModules(t *testing.T) {
	lib := Load()

	modules := []string{"io", "maybe", "results", "iter", "maps", "lists", "strings", "ranges", "structs", "calendar", "regex"}
	for _, name := range modules {
		mod, ok := lib.Modules[name]
		if !ok {
			t.Errorf("expected module %s to be loaded", name)
			continue
		}
		if mod == nil {
			t.Errorf("module %s scope is nil", name)
		}
	}

	if ioSym := lib.Modules["io"].Lookup("inspect"); ioSym == nil {
		t.Error("expected io.inspect in io module")
	}
	if tailSym := lib.Files["lists"].TypeMethods["List"]["tail"]; tailSym == nil {
		t.Error("expected List.tail in lists type methods")
	}
	if dateSym := lib.Modules["calendar"].Lookup("Date"); dateSym == nil {
		t.Error("expected calendar.Date in calendar module")
	} else if !dateSym.Public {
		t.Error("expected calendar.Date to be public")
	}
	if regexSym := lib.Modules["regex"].Lookup("Regex"); regexSym == nil {
		t.Error("expected co-located std/regex.Regex to resolve")
	}

	supervisorsMod, ok := lib.Modules["supervisors"]
	if !ok {
		t.Fatal("expected supervisors module to be loaded")
	}
	if restartSym := supervisorsMod.Lookup("Restart"); restartSym == nil {
		t.Error("expected supervisors.Restart in supervisors module")
	} else if restartSym.Kind != analysis.SymbolEnum {
		t.Errorf("expected supervisors.Restart to be enum, got kind %v", restartSym.Kind)
	}
}

func TestStdlibLoadOrderCoversEmbeddedModules(t *testing.T) {
	want := map[string]bool{}
	if err := fs.WalkDir(stdlibFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isEmbeddedStdlibSource(path) {
			return err
		}
		name := embeddedStdlibLogicalName(path)
		if name != "prelude" {
			want[name] = false
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, name := range embeddedStdlibNames() {
		if _, ok := want[name]; !ok {
			t.Errorf("embeddedStdlibNames returned non-embedded module %q", name)
			continue
		}
		want[name] = true
	}
	for name, saw := range want {
		if !saw {
			t.Errorf("embeddedStdlibNames missing embedded module %q", name)
		}
	}
	for _, name := range analysis.StdlibLoadOrder {
		if _, ok := want[name]; !ok {
			t.Errorf("StdlibLoadOrder names non-embedded module %q", name)
		}
	}
}

func TestNestedStdlibSourceVisibility(t *testing.T) {
	const fixtureModule = "_fixtures/nested/deeper/module"
	fixtureData, err := stdlibFS.ReadFile(fixtureModule + ".nomi")
	if err != nil || len(fixtureData) == 0 {
		t.Fatalf("expected recursively embedded fixture source: %v", err)
	}
	if _, ok := ReadFile(fixtureModule); ok {
		t.Fatal("test fixture must be excluded from production stdlib source lookup")
	}
	for _, name := range embeddedStdlibNames() {
		if strings.HasPrefix(name, "_fixtures/") {
			t.Fatalf("test fixture leaked into production stdlib names: %q", name)
		}
	}

	if _, err := MakeLoader()("", []string{"std", "_fixtures", "nested", "deeper", "module"}); err == nil {
		t.Fatal("MakeLoader loaded excluded test fixture as a standard module")
	}

	lib := &StdLib{}
	uri := lib.FileURI(fixtureModule)
	wantPath := filepath.Join(lib.diskDir, "_fixtures", "nested", "deeper", "module.nomi")
	if uri != "file://"+wantPath {
		t.Fatalf("FileURI(%s) = %q, want file URI for %q", fixtureModule, uri, wantPath)
	}
	materialized, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("FileURI did not materialize recursive parent directories: %v", err)
	}
	if string(materialized) != string(fixtureData) {
		t.Fatal("materialized recursive fixture differs from embedded source")
	}
}

// TestFileURI_UsesTheWorktreeSource pins that jump-to-definition on an adapter
// module lands in the checkout rather than in the ~/.cache materialization.
//
// `regex` is the subject because it is one of the four modules with a Go
// support directory beside it. Its facade used to be nested at
// `std/regex/regex.nomi`, and FileURI had a second lookup shape for that; now
// every stdlib module is a flat `std/<name>.nomi` and there is one shape.
func TestFileURI_UsesTheWorktreeSource(t *testing.T) {
	lib := &StdLib{}
	uri := lib.FileURI("regex")
	stdRoot, err := analysis.StdlibPath()
	if err != nil {
		t.Fatalf("StdlibPath: %v", err)
	}
	want := "file://" + filepath.Join(stdRoot, "regex.nomi")
	if uri != want {
		t.Fatalf("FileURI(regex) = %q, want %q", uri, want)
	}
}

func TestStdlibFuncTypesResolved(t *testing.T) {
	lib := Load()

	// Check io module — io.print should have FuncType
	ioScope := lib.Modules["io"]
	if ioScope == nil {
		t.Fatal("io module not found")
	}
	printSym := ioScope.Lookup("print")
	if printSym == nil {
		t.Fatal("io.print symbol not found")
	}
	if printSym.Type == nil {
		t.Error("io.print.Type is nil — BuildTypes not called on stdlib")
	}

	// Check std/strings — `String.length` is a type-owned function.
	lengthSym := lib.Files["strings"].TypeMethods["String"]["length"]
	if lengthSym == nil {
		t.Fatal("String.length symbol not found")
	}
	if lengthSym.Type == nil {
		t.Error("String.length.Type is nil")
	}
}

// TestLoad_CrossModuleReturnTypesResolved pins the std.Load refactor's
// key behavior: cross-module function return types must resolve to their
// declared types (e.g. Maybe<T>, Result<T, E>) rather than degrading to
// Unit when the producer module hasn't loaded a referenced type yet.
//
// Pre-refactor, std.Load was a linear loadModule loop that built each
// module in StdlibLoadOrder. Cyclic imports (A imports B AND B imports
// A) broke because the second-loaded module's import of the first
// resolved at parse time when the first wasn't fully built. The
// refactor delegates to BuildProjectWithCache, whose two-pass Sweep
// A/B/C machinery registers all symbol stubs before any annotation
// resolution, eliminating the file-order dependency.
//
// This test asserts the acyclic-but-cross-module case still works
// post-refactor (regression guard) and that the cycle-supporting
// path is in use. Cycle-specific tests live in
// analysis/project_build_test.go::TestBuildProject_ResolvesTypeLevelCycles
// and friends; std.Load inherits their coverage transitively.
func TestLoad_CrossModuleReturnTypesResolved(t *testing.T) {
	lib := Load()
	cases := []struct {
		module, fn  string
		wantNonUnit bool
	}{
		// Result.to_maybe(r: Result<T, E>): Maybe<T> — result uses
		// Maybe in its return type. If Maybe weren't resolved when
		// result was processed, return type would be Unit.
		{"results", "to_maybe", true},
		// Result.from_maybe(m: Maybe<T>, e: E): Result<T, E> — same
		// direction, different shape.
		{"results", "from_maybe", true},
		// Iter.to_map(src: Iter<(K, V)>): Map<K, V> — a free function
		// (constructor/materializer) with a cross-module Map return.
		{"iter", "to_map", true},
		// Map.values(m: self): List<V> — type-promoted method, cross-module
		// dependency on List.
		{"maps", "values", true},
	}
	// resolveFn finds `fn`'s resolved type whether it's a module-scope export
	// (unmigrated module) or a type-promoted impl-block method (migrated module,
	// reachable as `Type.fn`, recorded in the FA's TypeMethods, not module
	// scope). This lookup handles both without per-case flags.
	resolveFn := func(moduleName, fn string) (*analysis.FuncType, bool) {
		if mod := lib.Modules[moduleName]; mod != nil {
			if sym := mod.Lookup(fn); sym != nil {
				ft, ok := sym.Type.(*analysis.FuncType)
				return ft, ok
			}
		}
		if fa := lib.Files[moduleName]; fa != nil {
			if fa.ModuleScope != nil {
				if sym := fa.ModuleScope.LookupLocal(fn); sym != nil {
					ft, ok := sym.Type.(*analysis.FuncType)
					return ft, ok
				}
			}
			for _, byName := range fa.TypeMethods {
				if sym := byName[fn]; sym != nil {
					ft, ok := sym.Type.(*analysis.FuncType)
					return ft, ok
				}
			}
		}
		return nil, false
	}
	for _, c := range cases {
		ft, ok := resolveFn(c.module, c.fn)
		if ft == nil {
			t.Errorf("%s.%s not found (module export or type-promoted method)", c.module, c.fn)
			continue
		}
		if !ok {
			t.Errorf("%s.%s not a FuncType", c.module, c.fn)
			continue
		}
		if c.wantNonUnit && ft.Return == analysis.TypeUnit {
			t.Errorf("%s.%s return resolved to Unit (regression — cross-module type didn't propagate)", c.module, c.fn)
		}
	}
}

func TestLoadDocComments(t *testing.T) {
	lib := Load()
	sym := lib.Modules["io"].Lookup("inspect")
	if sym == nil {
		t.Fatal("expected io.inspect in io module")
	}
	if sym.Doc == "" {
		t.Error("expected inspect to have a doc comment")
	}
}

// TestStdlib_InstantTimerDuration pins the exported surface of the
// `instant` (Instant + arithmetic) and `timer` (sleep + future
// scheduling primitives) modules. These symbols back the per-life
// Context deadline plumbing (see context.nomi) and the general-
// purpose absolute-time / duration arithmetic the runtime builds on
// top of `Instant.now`. The test fails fast when a rename or removal
// would break either consumer.
func TestStdlib_InstantTimerDuration(t *testing.T) {
	lib := Load()
	instantMod, ok := lib.Modules["instant"]
	if !ok {
		t.Fatalf("instant module missing")
	}
	if sym := instantMod.Lookup("Instant"); sym == nil {
		t.Errorf("instant.Instant type not exported")
	}
	instantMethods := lib.Files["instant"].TypeMethods["Instant"]
	for _, name := range []string{"now", "from_seconds", "to_seconds", "add", "between", "before?"} {
		if sym := instantMethods[name]; sym == nil {
			t.Errorf("Instant.%s not exported", name)
		}
	}
	timerMod, ok := lib.Modules["timer"]
	if !ok {
		t.Fatalf("timer module missing")
	}
	if sym := timerMod.Lookup("sleep"); sym == nil {
		t.Errorf("timer.sleep not exported")
	}
	durMod, ok := lib.Modules["duration"]
	if !ok {
		t.Fatalf("duration module missing")
	}
	if sym := durMod.Lookup("Duration"); sym == nil {
		t.Errorf("duration.Duration type not exported")
	}
	durationMethods := lib.Files["duration"].TypeMethods["Duration"]
	for _, name := range []string{
		"nanoseconds", "microseconds", "milliseconds", "seconds", "minutes", "hours",
		"as_nanos", "as_micros", "as_millis", "as_seconds", "as_minutes", "as_hours",
		"add", "subtract", "multiply",
	} {
		if sym := durationMethods[name]; sym == nil {
			t.Errorf("Duration.%s not exported", name)
		}
	}
}

// TestStdlib_ContextTypeExists pins that the per-life Context opaque
// type is exported from the `context` stdlib module. The type itself
// is `pub host type Context` — user code can name it (in
// signatures, in `with context = ...` statements) but cannot construct it;
// only the language runtime and the `context.with_*` derivers
// produce values.
func TestStdlib_ContextTypeExists(t *testing.T) {
	lib := Load()
	contextMod, ok := lib.Modules["context"]
	if !ok {
		t.Fatalf("context module missing")
	}
	if sym := contextMod.Lookup("Context"); sym == nil {
		t.Errorf("context.Context type not exported")
	}
}

// TestStdlib_ContextAPIExports pins the `Context` type's full API: the `root`
// constructor, read functions (deadline, deadline_remaining), and the `with_*`
// derivers. They live as type-owned exports reachable as `Context.<name>(...)`.
func TestStdlib_ContextAPIExports(t *testing.T) {
	lib := Load()
	contextFile, ok := lib.Files["context"]
	if !ok {
		t.Fatalf("context module missing")
	}
	methods := contextFile.TypeMethods["Context"]
	for _, name := range []string{"root", "deadline", "deadline_remaining", "with_deadline", "with_timeout", "with_value", "value"} {
		if sym := methods[name]; sym == nil {
			t.Errorf("Context.%s not exported", name)
		}
	}
}

// The prelude exposes the universally-bare names. Every entry here is
// a name that user code references without an `import` statement.
// Adding or removing prelude entries is a deliberate breaking change;
// this test pins the contract.
//
// Keep in sync with std/prelude.nomi.
func TestPreludeExposesUniversalNames(t *testing.T) {
	lib := Load()
	want := []string{
		// primitives
		"Int", "Float", "Decimal", "String", "Unit", "Infallible", "List", "Vector", "Map", "Set",
		// bool
		"Bool",
		"True", "False",
		// The std/literals cluster (Literal, Fragment, and the Fragment
		// Static/Dynamic variants) is NOT prelude-exported — typed-literal
		// handler modules import it explicitly via
		// std/literals.{Fragment, Literal} (+ Fragment.{Static, Dynamic}).
		// maybe / result
		"Maybe", "Some", "None",
		"Result", "Ok", "Err",
		// core protocols
		"Add", "Subtract", "Multiply", "Divide",
		"Comparable", "Debug", "Display", "Equatable", "Hashable",
		"Discrete", "Iter", "Steppable", "Struct",
		// ordering type, but not Ordering's variants
		"Ordering",
		// range struct (needed for range-literal type resolution)
		"Range",
		// type witnesses
		"Type",
		// application lifecycle
		"Startup", "Context",
	}
	for _, name := range want {
		if sym := lib.Primitives.Lookup(name); sym == nil {
			t.Errorf("expected %q in prelude scope, missing", name)
		}
	}

	for _, name := range []string{"assertions", "codepoints", "context", "startup", "decimal", "float", "int", "iter", "maps", "maybe", "ranges", "results", "sets", "strings", "vectors"} {
		if sym := lib.Primitives.Lookup(name); sym != nil {
			t.Errorf("expected file API object %q to require an explicit import", name)
		}
	}
}

// TestStdlib_AnalyzesWithoutErrors fails on any diagnostic the stdlib's own
// analysis produces. Load used to discard them, and that is how std/json's
// `Json.String`, `Json.Int` and `Json.Float` variants silently took the
// module-scope slots of its `strings.String`, `int.Int` and `float.Float`
// imports: the redeclaration errors went nowhere and json's impls for those
// receivers took their identity from the variants.
func TestStdlib_AnalyzesWithoutErrors(t *testing.T) {
	lib := Load()
	if len(lib.Files) == 0 {
		t.Fatal("no stdlib files analyzed; an empty error set would be vacuous")
	}
	names := make([]string, 0, len(lib.Errors))
	for name := range lib.Errors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, e := range lib.Errors[name] {
			t.Errorf("std/%s:%d:%d: %s", name, e.Line, e.Col, e.Message)
		}
	}
}

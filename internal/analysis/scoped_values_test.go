package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// startupFn is a helper test sources call to build a boot's Startup.
const startupFn = `
 fn st(): Startup { Startup{} }
 `

// An application's fields are read by naming its type and replaced with
// `with` lines, whatever its Context field is called.
func TestAppFieldsReadAndReplaced(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Services { execution: Context
 port: Int }
 fn boot(): Services { Services{execution: Context.root(), port: 3} }
 fn greet(): Int { Services.port }
 fn main() {
  with Services.port = 4
  with Services.execution = Context.root()
  _ = greet()
 }
 `))
}

// An application struct needs no Context field.
func TestAppFieldsAppWithoutAContext(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct App { port: Int }
 fn boot(): App { App{port: 3} }
 fn main() {
  with App.port = App.port + 1
  _ = App.port
 }
 `))
}

// An anonymous record may be booted, and code that reads no field runs under
// it; it has no name a read could use.
func TestAppFieldsAnonymousBootReadsNothing(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 fn boot(): {execution: Context, port: Int} { {execution: Context.root(), port: 3} }
 fn main() { _ = 1 }
 `))
}

// A program or test with no boot publishes no field; one reading none runs.
func TestAppFieldsWithoutBoot(t *testing.T) {
	prefix := `
 struct App { context: Context
 n: Int }
 fn boot(): App { App{context: Context.root(), n: 1} }
 fn main() { _ = 1 }
 `
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` test "default" { assert True }
 `))
	expectErrorT(t, buildProjectForDefaultConfig(prefix+` test "default" { assert App.n == 1 }`),
		"this test has no boot, but code it runs reads `App.n` (line 6)")
	expectErrorT(t, buildProjectForDefaultConfig(prefix+startupFn+` tests "g" {
  setup 1
  test "default" { assert App.n == 1 }
 }`),
		"this test has no boot, but code it runs reads `App.n` (line 10)")

	// A program in an entry file without a boot does not run another
	// entry's boot.
	root := appScopeProject(t, map[string]string{
		"server.nomi": `pub struct App { context: Context
n: Int }
pub fn boot(): App { App{context: Context.root(), n: 1} }
fn main() { _ = App.n }`,
		"main.nomi": `import server.App
fn main() { _ = App.n }`,
	})
	_, errs := appScopeAnalyze(t, root, "main.nomi", nil)
	expectErrorT(t, errs, "this program has no boot, but code it runs reads `App.n` (main.nomi:2)")
}

// A struct no entry boot returns is not an application type, whatever its
// fields: `T.field` on it is owner access, which a struct's field is not, and
// the error says why.
func TestAppFieldsUnbootedStructIsNotAnApplicationType(t *testing.T) {
	errs := buildProjectForDefaultConfig(`
 struct Services { context: Context
 port: Int }
 fn main() { _ = Services.port }
 `)
	if len(errs) == 0 {
		t.Fatal("`Services.port` was admitted though no boot returns Services")
	}
	for _, e := range errs {
		if strings.Contains(e.Message, "no boot") {
			t.Fatalf("`Services.port` was read as an application field: %v", e)
		}
	}
	expectErrorT(t, errs, "`Services.port` reads an application field only when an entry boot returns `Services`, and no `fn boot` in this project does")
	// The same struct returned by the entry boot is an application type.
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Services { context: Context
 port: Int }
 fn boot(): Services { Services{context: Context.root(), port: 1} }
 fn main() { _ = Services.port }
 `))
}

func TestAppFieldsBootValidation(t *testing.T) {
	for schema, want := range map[string]string{
		`{a: Context, b: Context}`: "at most one Context-typed field",
		`Int`:                      "boot must return a struct",
		`(Context, Int)`:           "boot must return a struct",
	} {
		expectErrorT(t, buildProjectForDefaultConfig(`
 fn boot(startup: Startup): `+schema+` { 3 }
 fn main() {}
 `), want)
	}
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App { a: Context
 b: Context }
 fn boot(): App { App{a: Context.root(), b: Context.root()} }
 fn main() {}
 `), "an application struct may have at most one Context-typed field")
	// The (Startup, Context) signature is gone.
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App { context: Context }
 fn boot(_startup: Startup, context: Context): App { App{context} }
 fn main() {}
 `), bootSignatureMsg)
	expectErrorT(t, buildProjectForDefaultConfig(` struct Ctx { n: Int }
 fn boot(_startup: Ctx): {execution: Context} { {execution: Context.root()} }
 fn main() {}
 `), bootSignatureMsg)
}

// A top-level boot belongs in an entry file, one that defines `fn main`.
func TestAppFieldsBootOutsideAnEntryFile(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App { context: Context }
 fn boot(): App { App{context: Context.root()} }
 `), "`boot` belongs in an entry file, one that defines `fn main`")
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct App { context: Context }
 fn boot(): App { App{context: Context.root()} }
 fn main() {}
 `))
}

// Fields are published only after boot returns, so code a boot runs may read
// none, directly, through a call, or through a callback it hands a call.
func TestAppFieldsPublication(t *testing.T) {
	prefix := `
 struct Services { execution: Context
 port: Int }
 fn read(): Int { Services.port }
 fn call(f: () -> Int): Int { f() }
 `
	for _, c := range []struct{ source, read string }{
		{`fn boot(): Services { Services{execution: Context.root(), port: Services.port} } fn main() {}`, "`Services.port` (line 6)"},
		{`fn boot(): Services { Services{execution: Context.root(), port: read()} } fn main() {}`, "`Services.port` (line 4)"},
		{`fn boot(): Services { Services{execution: Services.execution, port: 1} } fn main() {}`, "`Services.execution` (line 6)"},
		{`fn boot(): Services { Services{execution: Context.root(), port: call(|| Services.port)} } fn main() {}`, "`Services.port` (line 6)"},
	} {
		expectErrorT(t, buildProjectForDefaultConfig(prefix+c.source),
			"boot runs before any application field is published, but code it runs reads "+c.read)
	}
	// A closure boot stores is not run by boot.
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Services { context: Context
 read: () -> Int
 port: Int }
 fn boot(): Services { Services{context:Context.root(),read:|| Services.port,port:3} }
 fn main() { f = Services.read
 _ = f() }
 `))
	boot := `fn boot(): Services { Services{execution:Context.root(),port:1} } `
	expectErrorT(t, buildProjectForDefaultConfig(prefix+boot+`fn main() { _ = boot }`), "cannot be called or captured")
	expectErrorT(t, buildProjectForDefaultConfig(prefix+boot+`fn main() {
 with Services.port = "bad"
 _ = 1
 }`), "`Services.port` replacement expects Int, got String")
}

// An entry boot is called only on a group's `boot` line.
func TestAppFieldsEntryBootCalledOnlyOnABootLine(t *testing.T) {
	prefix := `
 struct App { context: Context
 n: Int }
 fn boot(): App { App{context: Context.root(), n: 1} }
 fn main() {}
 `
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` tests "g" {
  boot boot()
  test "reads" { assert App.n == 1 }
 }`))
	expectErrorT(t, buildProjectForDefaultConfig(prefix+` test "calls" { assert boot().n == 1 }`),
		"entry-point boot cannot be called or captured as an ordinary function; a `tests` group calls it on its `boot` line")
	expectErrorT(t, buildProjectForDefaultConfig(prefix+` fn again(): App { boot() }`),
		"entry-point boot cannot be called or captured as an ordinary function")
}

// A group's `boot` line runs before any field is published, so its argument
// may read none.
func TestAppFieldsBootLineArgumentReadsNoField(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App { context: Context
 n: Int }
 fn boot(_startup: Startup): App { App{context: Context.root(), n: 1} }
 fn main() {}
 fn st(): Startup { Startup{args: ["${App.n}"]} }
 tests "g" {
  boot boot(st())
  test "reads" { assert App.n == 1 }
 }`), "boot runs before any application field is published, but code it runs reads `App.n` (line 6)")
}

// A group's `boot` line names an entry boot and nothing else.
func TestAppFieldsGroupBootMustNameAnEntryBoot(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct Life { life: Context
 n: Int }
 fn make(_startup: Startup): Life { Life{life: Context.root(), n: 1} }
 fn main() {}
 `+startupFn+`
 tests "custom" {
  boot make(st())
  test "read" { assert True }
 }
 `), "`boot` in a `tests` group calls an entry's boot, the `fn boot` of a file that defines `fn main`, such as `boot server.boot(startup)`; `make` is not one")
	// A `fn boot` in a file without `fn main` is not an entry boot either.
	root := appScopeProject(t, map[string]string{
		"lib.nomi": `pub struct Life { life: Context
n: Int }
pub fn boot(): Life { Life{life: Context.root(), n: 1} }`,
		"lib_test.nomi": `import lib
tests "custom" {
 boot lib.boot()
 test "read" { assert True }
}`,
	})
	_, errs := appScopeAnalyze(t, root, "lib_test.nomi", nil)
	expectErrorT(t, errs, "`lib.boot` is not one")
}

// Each test group boots its own entry's application, of any type.
func TestAppFieldsEachGroupBootsItsOwnApplication(t *testing.T) {
	files := map[string]string{
		"services.nomi": `pub struct Services { execution: Context
port: Int }
pub fn boot(): Services { Services{execution: Context.root(), port: 3} }
pub fn read(): Int { Services.port }
fn main() { _ = read() }`,
		"other.nomi": `pub struct Other { life: Context
text: String }
pub fn boot(): Other { Other{life: Context.root(), text: "yes"} }
fn main() { _ = Other.text }`,
	}
	head := `import {
 services
 services.Services
 other
 other.Other
}
`
	files["app_test.nomi"] = head + `tests "services" {
 boot services.boot()
 test "reads" {
  assert Services.port == 3
  assert services.read() == 3
 }
}
tests "other" {
 boot other.boot()
 test "reads" { assert Other.text == "yes" }
}
`
	root := appScopeProject(t, files)
	_, errs := appScopeAnalyze(t, root, "app_test.nomi", nil)
	expectNoErrorsT(t, errs)

	files["app_test.nomi"] = head + `tests "other" {
 boot other.boot()
 test "reads" { assert services.read() == 3 }
}
`
	root = appScopeProject(t, files)
	_, errs = appScopeAnalyze(t, root, "app_test.nomi", nil)
	expectErrorT(t, errs, "this test boots `Other`, but code it runs reads `Services.port` (services.nomi:4)")

	files["app_test.nomi"] = head + `tests "other" {
 boot other.boot()
 setup services.read()
 test "reads" { assert True }
}
`
	root = appScopeProject(t, files)
	_, errs = appScopeAnalyze(t, root, "app_test.nomi", nil)
	expectErrorT(t, errs, "this group's setup boots `Other`, but code it runs reads `Services.port` (services.nomi:4)")
}

// A helper's read names one type, so the boot of every test that runs it must
// return that type; reads of two types under one boot are an error there.
func TestAppFieldsHelpersNameTheirType(t *testing.T) {
	files := map[string]string{
		"a.nomi": `pub struct A { context: Context
n: Int }
pub fn boot(): A { A{context: Context.root(), n: 1} }
fn main() {}`,
		"b.nomi": `pub struct B { context: Context
n: Int }
pub fn boot(): B { B{context: Context.root(), n: 2} }
fn main() {}`,
		"helpers.nomi": `import {
 a.A
 b.B
}
pub fn read(): Int { A.n }
pub fn both(): Int { A.n + B.n }`,
	}
	for _, c := range []struct{ entry, call, want string }{
		{"a", "read", ""},
		{"b", "read", "this test boots `B`, but code it runs reads `A.n` (helpers.nomi:5)"},
		{"a", "both", "code this test runs reads `A.n` (helpers.nomi:6) and `B.n` (helpers.nomi:6); one boot publishes one application type"},
	} {
		files["app_test.nomi"] = "import {\n " + c.entry + "\n helpers\n}\n" +
			"tests \"g\" {\n boot " + c.entry + ".boot()\n test \"reads\" { assert helpers." + c.call + "() > 0 }\n}\n"
		root := appScopeProject(t, files)
		_, errs := appScopeAnalyze(t, root, "app_test.nomi", nil)
		if c.want == "" {
			expectNoErrorsT(t, errs)
		} else {
			expectErrorT(t, errs, c.want)
		}
	}
}

// A file that reads an imported application type's field is checked as
// part of the project its entry loads, where the entry's boot makes the type
// an application type.
func TestAppFieldsImportedType(t *testing.T) {
	files := map[string]string{
		"settings.nomi": ` pub struct Settings { context:Context
port:Int }`,
		"reader.nomi": `import settings.Settings
pub fn read(): Int { Settings.port }`,
		"main.nomi": `import {settings.Settings reader }
 fn boot(): Settings { Settings{context:Context.root(),port:3} }
 fn main() { _ = reader.read() }`,
	}
	root := appScopeProject(t, files)
	_, errs := appScopeAnalyze(t, root, "main.nomi", nil)
	expectNoErrorsT(t, errs)
	expectNoErrorsT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"))
}

// A file the entry loads knows the type, also when it or a file it loads
// carries `//!` tests that read nothing.
func TestAppFieldsImportedTypeBesideAttachedTests(t *testing.T) {
	main := `import {settings.Settings reader }
 fn boot(): Settings { Settings{context:Context.root(),port:3} }
 fn main() { _ = reader.read() }`
	settings := ` pub struct Settings { context:Context
port:Int }`
	helper := `//! assert double(2) == 4
//
pub fn double(n: Int): Int { n * 2 }
`
	for name, reader := range map[string]string{
		"a loaded file has a //! test": `import {
 helper
 settings.Settings
}
pub fn read(): Int { helper.double(Settings.port) }
`,
		"the file has a //! test": `import settings.Settings
pub fn read(): Int { double(Settings.port) }

//! assert double(2) == 4
//
pub fn double(n: Int): Int { n * 2 }
`,
	} {
		t.Run(name, func(t *testing.T) {
			root := appScopeProject(t, map[string]string{
				"main.nomi": main, "settings.nomi": settings, "helper.nomi": helper, "reader.nomi": reader,
			})
			expectNoErrorsT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"))
		})
	}
}

// A file that reads no field itself loads one that does.
func TestAppFieldsImportedTypeThroughAnImport(t *testing.T) {
	root := appScopeProject(t, map[string]string{
		"settings.nomi": ` pub struct Settings { context:Context
port:Int }`,
		"reader.nomi": `import settings.Settings
pub fn read(): Int { Settings.port }`,
		"twice.nomi": `import reader
pub fn twice(): Int { reader.read() * 2 }`,
		"main.nomi": `import {settings.Settings twice }
 fn boot(): Settings { Settings{context:Context.root(),port:3} }
 fn main() { _ = twice.twice() }`,
	})
	expectNoErrorsT(t, appScopeCheckSibling(t, root, "main.nomi", "twice"))
	expectNoErrorsT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"))
}

// A `//!` test has no boot, so one that reaches a read of an application
// field is rejected at the test.
func TestAppFieldsAttachedTestCannotReachAField(t *testing.T) {
	root := appScopeProject(t, map[string]string{
		"settings.nomi": ` pub struct Settings { context:Context
port:Int }`,
		"reader.nomi": `import settings.Settings

//! assert read() == 3
//
pub fn read(): Int { Settings.port }
`,
		"main.nomi": `import {settings.Settings reader }
 fn boot(): Settings { Settings{context:Context.root(),port:3} }
 fn main() { _ = reader.read() }`,
	})
	expectErrorT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"),
		"this test has no boot, but code it runs reads `Settings.port` (reader.nomi:5)")
}

// appScopeCheckSibling builds the project from entry and checks the file the
// entry loads under key, as the LSP checks a file main.nomi imports.
func appScopeCheckSibling(t *testing.T, root, entry, key string) []analysis.TypeError {
	t.Helper()
	entryPath := filepath.Join(root, entry)
	src, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
	lib := std.Load()
	_, siblings, siblingNodes := analysis.BuildProjectFromEntry(entryPath, nodes, lib.Primitives, lib.Modules, lib.Files, root, appScopeLoader(nil))
	fa := siblings[key]
	if fa == nil {
		t.Fatalf("%s.nomi was not loaded: %v", key, siblings)
	}
	return append(append([]analysis.TypeError{}, fa.TypeErrors...), analysis.CheckTypes(fa, siblingNodes[key])...)
}

// Without a boot anywhere in the project, Settings is no application type,
// so `Settings.port` is not an application-field read, and the error says so.
func TestAppFieldsNoProgramBootHasNoFields(t *testing.T) {
	root := appScopeProject(t, map[string]string{
		"settings.nomi": ` pub struct Settings { context:Context
port:Int }`,
		"reader.nomi": `import settings.Settings
pub fn read(): Int { Settings.port }`,
		"main.nomi": `import reader
 fn main() { _ = reader.read() }`,
	})
	expectErrorT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"), "`Settings.port` reads an application field only when an entry boot returns `Settings`, and no `fn boot` in this project does")
}

// Code that reads no field may run under a boot, and a group's tests read
// the fields its boot publishes.
func TestAppFieldsUnreadByMainWithCustomTests(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Life { life: Context
 n: Int }
 fn read(): Int { 1 }
 fn boot(): Life { Life{life:Context.root(),n:1} }
 fn main() { _ = read() }
 tests "custom" {
  boot boot()
  test "read" { assert Life.n == read() }
 }
 `))
}

// A test binds its group's setup value with an irrefutable pattern only.
func TestAppFieldsTestPatternIsIrrefutable(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 tests "g" {
  setup Some(1)
  test "x", Some(n) { assert n == 1 }
 }
 `), "a test's pattern must match every setup value")
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 tests "g" {
  setup (Some(1), "a")
  test "x", (n, s) {
   assert n == Some(1)
   assert s == "a"
  }
 }
 `))
}

func TestAppFieldsInterfaceReplacement(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 interface Logger { fn log(logger: self, msg: String): Unit }
 struct Console {}
 struct Audit {}
 impl Logger for Console { fn log(_logger: Console, msg: String): Unit { _ = msg } }
 impl Logger for Audit { fn log(_logger: Audit, msg: String): Unit { _ = msg } }
 struct App { life: Context
 logger: Logger }
 fn boot(): App { App{life:Context.root(),logger:Console{}} }
 fn greet() { Logger.log(App.logger,"hello") }
 fn main() {
  with App.logger = Audit{}
  greet()
 }
 `))
}

// A `.Variant` override takes its enum from the field's type.
func TestAppFieldsReplacementSeesTheFieldType(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 enum Level { Info
 Debug }
 struct App { context: Context
 level: Level }
 fn boot(): App { App{context: Context.root(), level: .Info} }
 fn main() {
  with App.level = .Debug
  _ = App.level
 }
 `))
}

func TestAppFieldsWithTargetsAnApplicationField(t *testing.T) {
	prefix := `
 struct App { context: Context
 n: Int }
 struct Point { x: Int }
 fn boot(): App { App{context: Context.root(), n: 1} }
 `
	expectErrorT(t, buildProjectForDefaultConfig(prefix+`fn main() {
 with Point.x = 1
 _ = 1
 }`),
		"`with` replaces an application field, such as `MyApp.logger`; `Point.x` is not one")
	// Two lines may replace one field; the later one wins.
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+`fn main() {
 with App.n = 1
 with App.n = App.n + 1
 _ = App.n
 }`))
}

// `App.port` always names the field of an application type, so the type may
// not declare an inherent member of a field's name.
func TestAppFieldsTypeMayNotShadowAField(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App { context: Context
 port: Int }
 impl App { fn port(): Int { 1 } }
 fn boot(): App { App{context: Context.root(), port: 1} }
 fn main() { _ = App.port }
 `), "`App` is an application type, so `App.port` reads its field `port`; rename this member")
	// A type no boot returns is not an application type, and its
	// owner-qualified members are unchanged, Context field or not.
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Point { context: Context
 x: Int }
 impl Point { fn x(): Int { 1 } }
 fn main() { _ = Point.x() }
 `))
}

func TestAppFieldsMetadata(t *testing.T) {
	root := appScopeProject(t, map[string]string{"main.nomi": `
 struct App { execution: Context
 n: Int }
 fn boot(): App { App{execution:Context.root(),n:3} }
 fn main() {
 with App.n = 4
 _ = App.execution
 }
 `})
	fa, errs := appScopeAnalyze(t, root, "main.nomi", nil)
	expectNoErrorsT(t, errs)
	if len(fa.AppTypes) != 1 || fa.AppTypes[0].Name != "App" {
		t.Fatalf("application types: %v", fa.AppTypes)
	}
	if len(fa.AppReadsByPos) != 2 {
		t.Fatalf("app-field metadata: %v", fa.AppReadsByPos)
	}
	for _, read := range fa.AppReadsByPos {
		if read.App == nil || read.App.Name != "App" {
			t.Fatalf("application type: %v", read)
		}
		if read.ContextField != "execution" {
			t.Fatalf("context metadata: %v", read)
		}
		if read.Field.Name == "n" && read.Field.Type != analysis.TypeInt {
			t.Fatalf("declared field type: %v", read)
		}
	}
}

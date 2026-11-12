package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// startupFn is a helper test sources call to build a boot's Startup.
const startupFn = `
 fn st() -> Startup
     Startup{}
 end
 `

// An application's fields are read by naming its type and replaced with
// `with` lines, whatever its Context field is called.
func TestAppFieldsReadAndReplaced(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Services
     execution: Context
     port: Int
 end
 fn boot() -> Result<Services, String>
     Ok(Services{execution: Context.root(), port: 3})
 end
 fn greet() -> Int
     Services.port
 end
 fn main()
  with Services.port = 4
  with Services.execution = Context.root()
  _ = greet()
 end
 `))
}

// An application struct needs no Context field.
func TestAppFieldsAppWithoutAContext(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct App
     port: Int
 end
 fn boot() -> Result<App, String>
     Ok(App{port: 3})
 end
 fn main()
  with App.port = App.port + 1
  _ = App.port
 end
 `))
}

// An anonymous record may be booted, and code that reads no field runs under
// it; it has no name a read could use.
func TestAppFieldsAnonymousBootReadsNothing(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 fn boot() -> Result<{execution: Context, port: Int}, String>
     Ok({execution: Context.root(), port: 3})
 end
 fn main()
     _ = 1
 end
 `))
}

// A program or test with no boot publishes no field; one reading none runs.
func TestAppFieldsWithoutBoot(t *testing.T) {
	prefix := `
 struct App
     context: Context
     n: Int
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root(), n: 1})
 end
 fn main()
     _ = 1
 end
 `
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` test "default"
     assert True
 end
 `))
	expectErrorT(t, buildProjectForDefaultConfig(prefix+` test "default"
     assert App.n == 1
 end`),
		"this test has no boot, but code it runs reads `App.n` (line 13)")
	expectErrorT(t, buildProjectForDefaultConfig(prefix+startupFn+` group "g"
  setup 1
  test "default"
      assert App.n == 1
  end
 end`),
		"this test has no boot, but code it runs reads `App.n` (line 19)")

	// A program in an entry file without a boot does not run another
	// entry's boot.
	root := appScopeProject(t, map[string]string{
		"server.nomi": `pub struct App
    context: Context
    n: Int
end
pub fn boot() -> Result<App, String>
    Ok(App{context: Context.root(), n: 1})
end
fn main()
    _ = App.n
end`,
		"main.nomi": `import server.App
fn main()
    _ = App.n
end`,
	})
	_, errs := appScopeAnalyze(t, root, "main.nomi", nil)
	expectErrorT(t, errs, "this program has no boot, but code it runs reads `App.n` (main.nomi:3)")
}

// A struct no entry boot returns is not an application type, whatever its
// fields: `T.field` on it is owner access, which a struct's field is not, and
// the error says why.
func TestAppFieldsUnbootedStructIsNotAnApplicationType(t *testing.T) {
	errs := buildProjectForDefaultConfig(`
 struct Services
     context: Context
     port: Int
 end
 fn main()
     _ = Services.port
 end
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
 struct Services
     context: Context
     port: Int
 end
 fn boot() -> Result<Services, String>
     Ok(Services{context: Context.root(), port: 1})
 end
 fn main()
     _ = Services.port
 end
 `))
}

func TestAppFieldsBootValidation(t *testing.T) {
	for schema, want := range map[string]string{
		`{a: Context, b: Context}`: "at most one Context-typed field",
		`Int`:                      "boot must return `Result<App, E>`",
		`(Context, Int)`:           "boot must return `Result<App, E>`",
	} {
		expectErrorT(t, buildProjectForDefaultConfig(`
 fn boot(startup: Startup) -> `+schema+`
     3
 end
 fn main()
 end
 `), want)
	}
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App
     a: Context
 b: Context
 end
 fn boot() -> Result<App, String>
     Ok(App{a: Context.root(), b: Context.root()})
 end
 fn main()
 end
 `), "an application struct may have at most one Context-typed field")
	// The (Startup, Context) signature is gone.
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App
     context: Context
 end
 fn boot(_startup: Startup, context: Context) -> Result<App, String>
     Ok(App{context:})
 end
 fn main()
 end
 `), bootSignatureMsg)
	expectErrorT(t, buildProjectForDefaultConfig(` struct Ctx
     n: Int
 end
 fn boot(_startup: Ctx) -> Result<{execution: Context}, String>
     Ok({execution: Context.root()})
 end
 fn main()
 end
 `), bootSignatureMsg)
}

// A top-level boot belongs in an entry file, one that defines `fn main`.
func TestAppFieldsBootOutsideAnEntryFile(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App
     context: Context
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root()})
 end
 `), "`boot` belongs in an entry file, one that defines `fn main`")
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct App
     context: Context
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root()})
 end
 fn main()
 end
 `))
}

// Fields are published only after boot returns, so code a boot runs may read
// none, directly, through a call, or through a callback it hands a call.
func TestAppFieldsPublication(t *testing.T) {
	prefix := `
 struct Services
     execution: Context
     port: Int
 end
 fn read() -> Int
     Services.port
 end
 fn call(f: () -> Int) -> Int
     f()
 end
 `
	for _, c := range []struct{ source, read string }{
		{`fn boot() -> Result<Services, String>
    Ok(Services{execution: Context.root(), port: Services.port})
end
fn main()
end`, "`Services.port` (line 13)"},
		{`fn boot() -> Result<Services, String>
    Ok(Services{execution: Context.root(), port: read()})
end
fn main()
end`, "`Services.port` (line 7)"},
		{`fn boot() -> Result<Services, String>
    Ok(Services{execution: Services.execution, port: 1})
end
fn main()
end`, "`Services.execution` (line 13)"},
		{`fn boot() -> Result<Services, String>
    Ok(Services{execution: Context.root(), port: call(fn -> Services.port)})
end
fn main()
end`, "`Services.port` (line 13)"},
	} {
		expectErrorT(t, buildProjectForDefaultConfig(prefix+c.source),
			"boot runs before any application field is published, but code it runs reads "+c.read)
	}
	// A closure boot stores is not run by boot.
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Services
     context: Context
 read: () -> Int
     port: Int
 end
 fn boot() -> Result<Services, String>
     Ok(Services{context:Context.root(),read:fn -> Services.port,port:3})
 end
 fn main()
     f = Services.read
 _ = f()
 end
 `))
	boot := `fn boot() -> Result<Services, String>
    Ok(Services{execution:Context.root(),port:1})
end `
	expectErrorT(t, buildProjectForDefaultConfig(prefix+boot+`fn main()
    _ = boot
end`), "cannot be called or captured")
	expectErrorT(t, buildProjectForDefaultConfig(prefix+boot+`fn main()
 with Services.port = "bad"
 _ = 1
 end`), "`Services.port` replacement expects Int, got String")
}

// An entry boot is called on a group's `boot` line, or as an ordinary
// function in a test, and nowhere else.
func TestAppFieldsEntryBootCalledOnlyOnABootLineOrInATest(t *testing.T) {
	prefix := `
 struct App
     context: Context
     n: Int
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root(), n: 1})
 end
 fn main()
 end
 `
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` group "g"
  boot boot()
  test "reads"
      assert App.n == 1
  end
 end`))
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` test "calls"
  app = boot()?
  assert app.n == 1
  refute boot() is Err(_)
 end`))
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` group "g"
  setup boot()
  test "reads", booted
      assert booted is Ok(_)
  end
 end`))
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` //! assert boot() is Ok(_)
 fn helper() -> Int
     1
 end`))
	const refused = "entry-point boot cannot be called or captured as an ordinary function; a `group` calls it on its `boot` line, and a test may call it to check the Result it returns"
	expectErrorT(t, buildProjectForDefaultConfig(prefix+` fn again() -> Result<App, String>
     boot()
 end`), refused)
	expectErrorT(t, buildProjectForDefaultConfig(prefix+` test "captures"
  f = boot
  assert f() is Ok(_)
 end`), refused)
}

// A test's call of an entry boot runs inside boot, so a boot that creates a
// supervisor may be called there; the same call in a helper is refused as
// a boot call, not as a supervisor made outside boot.
func TestBootOnly_ATestMayCallABootThatCreatesASupervisor(t *testing.T) {
	prefix := `
 import std::supervisors.Supervisor
 struct App
     context: Context
 work: Supervisor
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root(), work: Supervisor.new(max_running: 1)})
 end
 fn main()
 end
 `
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+` test "boots"
     assert boot() is Ok(_)
 end`))
	result := buildProjectForDefaultConfig(prefix + ` fn again() -> Result<App, String>
     boot()
 end`)
	expectErrorT(t, result, "a test may call it to check the Result it returns")
	for _, err := range result {
		if strings.Contains(err.Message, "creates a supervisor") {
			t.Fatalf("a boot call is reported as a supervisor made outside boot: %s", err.Message)
		}
	}
}

// A group's `boot` line runs before any field is published, so its argument
// may read none.
func TestAppFieldsBootLineArgumentReadsNoField(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App
     context: Context
     n: Int
 end
 fn boot(_startup: Startup) -> Result<App, String>
     Ok(App{context: Context.root(), n: 1})
 end
 fn main()
 end
 fn st() -> Startup
     Startup{args: ["${App.n}"]}
 end
 group "g"
  boot boot(st())
  test "reads"
      assert App.n == 1
  end
 end`), "boot runs before any application field is published, but code it runs reads `App.n` (line 12)")
}

// A group's `boot` line names an entry boot and nothing else.
func TestAppFieldsGroupBootMustNameAnEntryBoot(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct Life
     life: Context
     n: Int
 end
 fn make(_startup: Startup) -> Life
     Life{life: Context.root(), n: 1}
 end
 fn main()
 end
 `+startupFn+`
 group "custom"
  boot make(st())
  test "read"
      assert True
  end
 end
 `), "`boot` in a `group` calls an entry's boot, the `fn boot` of a file that defines `fn main`, such as `boot server.boot(startup)`; `make` is not one")
	// A `fn boot` in a file without `fn main` is not an entry boot either.
	root := appScopeProject(t, map[string]string{
		"lib.nomi": `pub struct Life
    life: Context
    n: Int
end
pub fn boot() -> Result<Life, String>
    Ok(Life{life: Context.root(), n: 1})
end`,
		"lib_test.nomi": `import lib
group "custom"
 boot lib.boot()
 test "read"
     assert True
 end
end`,
	})
	_, errs := appScopeAnalyze(t, root, "lib_test.nomi", nil)
	expectErrorT(t, errs, "`lib.boot` is not one")
}

// Each test group boots its own entry's application, of any type.
func TestAppFieldsEachGroupBootsItsOwnApplication(t *testing.T) {
	files := map[string]string{
		"services.nomi": `pub struct Services
    execution: Context
    port: Int
end
pub fn boot() -> Result<Services, String>
    Ok(Services{execution: Context.root(), port: 3})
end
pub fn read() -> Int
    Services.port
end
fn main()
    _ = read()
end`,
		"other.nomi": `pub struct Other
    life: Context
    text: String
end
pub fn boot() -> Result<Other, String>
    Ok(Other{life: Context.root(), text: "yes"})
end
fn main()
    _ = Other.text
end`,
	}
	head := `import
 services
 services.Services
 other
 other.Other
end
`
	files["app_test.nomi"] = head + `group "services"
 boot services.boot()
 test "reads"
  assert Services.port == 3
  assert services.read() == 3
 end
end
group "other"
 boot other.boot()
 test "reads"
     assert Other.text == "yes"
 end
end
`
	root := appScopeProject(t, files)
	_, errs := appScopeAnalyze(t, root, "app_test.nomi", nil)
	expectNoErrorsT(t, errs)

	files["app_test.nomi"] = head + `group "other"
 boot other.boot()
 test "reads"
     assert services.read() == 3
 end
end
`
	root = appScopeProject(t, files)
	_, errs = appScopeAnalyze(t, root, "app_test.nomi", nil)
	expectErrorT(t, errs, "this test boots `Other`, but code it runs reads `Services.port` (services.nomi:9)")

	files["app_test.nomi"] = head + `group "other"
 boot other.boot()
 setup services.read()
 test "reads"
     assert True
 end
end
`
	root = appScopeProject(t, files)
	_, errs = appScopeAnalyze(t, root, "app_test.nomi", nil)
	expectErrorT(t, errs, "this group's setup boots `Other`, but code it runs reads `Services.port` (services.nomi:9)")
}

// A helper's read names one type, so the boot of every test that runs it must
// return that type; reads of two types under one boot are an error there.
func TestAppFieldsHelpersNameTheirType(t *testing.T) {
	files := map[string]string{
		"a.nomi": `pub struct A
    context: Context
    n: Int
end
pub fn boot() -> Result<A, String>
    Ok(A{context: Context.root(), n: 1})
end
fn main()
end`,
		"b.nomi": `pub struct B
    context: Context
    n: Int
end
pub fn boot() -> Result<B, String>
    Ok(B{context: Context.root(), n: 2})
end
fn main()
end`,
		"helpers.nomi": `import
 a.A
 b.B
end
pub fn read() -> Int
    A.n
end
pub fn both() -> Int
    A.n + B.n
end`,
	}
	for _, c := range []struct{ entry, call, want string }{
		{"a", "read", ""},
		{"b", "read", "this test boots `B`, but code it runs reads `A.n` (helpers.nomi:6)"},
		{"a", "both", "code this test runs reads `A.n` (helpers.nomi:9) and `B.n` (helpers.nomi:9); one boot publishes one application type"},
	} {
		files["app_test.nomi"] = "import\n " + c.entry + "\n helpers\nend\n" +
			"group \"g\"\n boot " + c.entry + ".boot()\n test \"reads\"\n  assert helpers." + c.call + "() > 0\n end\nend\n"
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
		"settings.nomi": ` pub struct Settings
     context:Context
port:Int
 end`,
		"reader.nomi": `import settings.Settings
pub fn read() -> Int
    Settings.port
end`,
		"main.nomi": `import
    settings.Settings reader
end
 fn boot() -> Result<Settings, String>
     Ok(Settings{context:Context.root(),port:3})
 end
 fn main()
     _ = reader.read()
 end`,
	}
	root := appScopeProject(t, files)
	_, errs := appScopeAnalyze(t, root, "main.nomi", nil)
	expectNoErrorsT(t, errs)
	expectNoErrorsT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"))
}

// A file the entry loads knows the type, also when it or a file it loads
// carries `//!` tests that read nothing.
func TestAppFieldsImportedTypeBesideAttachedTests(t *testing.T) {
	main := `import
    settings.Settings reader
end
 fn boot() -> Result<Settings, String>
     Ok(Settings{context:Context.root(),port:3})
 end
 fn main()
     _ = reader.read()
 end`
	settings := ` pub struct Settings
     context:Context
port:Int
 end`
	helper := `//! assert double(2) == 4
//
pub fn double(n: Int) -> Int
    n * 2
end
`
	for name, reader := range map[string]string{
		"a loaded file has a //! test": `import
 helper
 settings.Settings
end
pub fn read() -> Int
    helper.double(Settings.port)
end
`,
		"the file has a //! test": `import settings.Settings
pub fn read() -> Int
    double(Settings.port)
end

//! assert double(2) == 4
//
pub fn double(n: Int) -> Int
    n * 2
end
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
		"settings.nomi": ` pub struct Settings
     context:Context
port:Int
 end`,
		"reader.nomi": `import settings.Settings
pub fn read() -> Int
    Settings.port
end`,
		"twice.nomi": `import reader
pub fn twice() -> Int
    reader.read() * 2
end`,
		"main.nomi": `import
    settings.Settings twice
end
 fn boot() -> Result<Settings, String>
     Ok(Settings{context:Context.root(),port:3})
 end
 fn main()
     _ = twice.twice()
 end`,
	})
	expectNoErrorsT(t, appScopeCheckSibling(t, root, "main.nomi", "twice"))
	expectNoErrorsT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"))
}

// A `//!` test has no boot, so one that reaches a read of an application
// field is rejected at the test.
func TestAppFieldsAttachedTestCannotReachAField(t *testing.T) {
	root := appScopeProject(t, map[string]string{
		"settings.nomi": ` pub struct Settings
     context:Context
port:Int
 end`,
		"reader.nomi": `import settings.Settings

//! assert read() == 3
//
pub fn read() -> Int
    Settings.port
end
`,
		"main.nomi": `import
    settings.Settings reader
end
 fn boot() -> Result<Settings, String>
     Ok(Settings{context:Context.root(),port:3})
 end
 fn main()
     _ = reader.read()
 end`,
	})
	expectErrorT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"),
		"this test has no boot, but code it runs reads `Settings.port` (reader.nomi:6)")
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
	lib := loadStd()
	_, siblings, siblingNodes := analysis.BuildProjectFromEntry(entryPath, nodes, lib.Primitives, lib.Modules, lib.Files, root, appScopeLoader(nil))
	fa := siblings[key]
	if fa == nil {
		t.Fatalf("%s.nomi was not loaded: %v", key, siblings)
	}
	errs := append(append([]analysis.TypeError{}, fa.TypeErrors...), analysis.CheckTypes(fa, siblingNodes[key])...)
	return append(errs, analysis.CheckAppRoots(fa, siblingNodes[key])...)
}

// Without a boot anywhere in the project, Settings is no application type,
// so `Settings.port` is not an application-field read, and the error says so.
func TestAppFieldsNoProgramBootHasNoFields(t *testing.T) {
	root := appScopeProject(t, map[string]string{
		"settings.nomi": ` pub struct Settings
     context:Context
port:Int
 end`,
		"reader.nomi": `import settings.Settings
pub fn read() -> Int
    Settings.port
end`,
		"main.nomi": `import reader
 fn main()
     _ = reader.read()
 end`,
	})
	expectErrorT(t, appScopeCheckSibling(t, root, "main.nomi", "reader"), "`Settings.port` reads an application field only when an entry boot returns `Settings`, and no `fn boot` in this project does")
}

// Code that reads no field may run under a boot, and a group's tests read
// the fields its boot publishes.
func TestAppFieldsUnreadByMainWithCustomTests(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Life
     life: Context
     n: Int
 end
 fn read() -> Int
     1
 end
 fn boot() -> Result<Life, String>
     Ok(Life{life:Context.root(),n:1})
 end
 fn main()
     _ = read()
 end
 group "custom"
  boot boot()
  test "read"
      assert Life.n == read()
  end
 end
 `))
}

// A test binds its group's setup value with an irrefutable pattern only.
func TestAppFieldsTestPatternIsIrrefutable(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 group "g"
  setup Some(1)
  test "x", Some(n)
      assert n == 1
  end
 end
 `), "a test's pattern must match every setup value")
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 group "g"
  setup (Some(1), "a")
  test "x", (n, s)
   assert n == Some(1)
   assert s == "a"
  end
 end
 `))
}

func TestAppFieldsInterfaceReplacement(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 interface Logger
     fn log(logger: self, msg: String) -> Unit
 end
 struct Console end
 struct Audit end
 impl Logger for Console
     fn log(_logger: Console, msg: String) -> Unit
         _ = msg
     end
 end
 impl Logger for Audit
     fn log(_logger: Audit, msg: String) -> Unit
         _ = msg
     end
 end
 struct App
     life: Context
 logger: Logger
 end
 fn boot() -> Result<App, String>
     Ok(App{life:Context.root(),logger:Console{}})
 end
 fn greet()
     Logger.log(App.logger,"hello")
 end
 fn main()
  with App.logger = Audit{}
  greet()
 end
 `))
}

// A `.Variant` override takes its enum from the field's type.
func TestAppFieldsReplacementSeesTheFieldType(t *testing.T) {
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 enum Level
     Info
 Debug
 end
 struct App
     context: Context
 level: Level
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root(), level: .Info})
 end
 fn main()
  with App.level = .Debug
  _ = App.level
 end
 `))
}

func TestAppFieldsWithTargetsAnApplicationField(t *testing.T) {
	prefix := `
 struct App
     context: Context
     n: Int
 end
 struct Point
     x: Int
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root(), n: 1})
 end
 `
	expectErrorT(t, buildProjectForDefaultConfig(prefix+`fn main()
 with Point.x = 1
 _ = 1
 end`),
		"`with` replaces an application field, such as `MyApp.logger`; `Point.x` is not one")
	// Two lines may replace one field; the later one wins.
	expectNoErrorsT(t, buildProjectForDefaultConfig(prefix+`fn main()
 with App.n = 1
 with App.n = App.n + 1
 _ = App.n
 end`))
}

// `App.port` always names the field of an application type, so the type may
// not declare an inherent member of a field's name.
func TestAppFieldsTypeMayNotShadowAField(t *testing.T) {
	expectErrorT(t, buildProjectForDefaultConfig(`
 struct App
     context: Context
     port: Int
 end
 impl App
     fn port() -> Int
         1
     end
 end
 fn boot() -> Result<App, String>
     Ok(App{context: Context.root(), port: 1})
 end
 fn main()
     _ = App.port
 end
 `), "`App` is an application type, so `App.port` reads its field `port`; rename this member")
	// A type no boot returns is not an application type, and its
	// owner-qualified members are unchanged, Context field or not.
	expectNoErrorsT(t, buildProjectForDefaultConfig(`
 struct Point
     context: Context
 x: Int
 end
 impl Point
     fn x() -> Int
         1
     end
 end
 fn main()
     _ = Point.x()
 end
 `))
}

func TestAppFieldsMetadata(t *testing.T) {
	root := appScopeProject(t, map[string]string{"main.nomi": `
 struct App
     execution: Context
     n: Int
 end
 fn boot() -> Result<App, String>
     Ok(App{execution:Context.root(),n:3})
 end
 fn main()
 with App.n = 4
 _ = App.execution
 end
 `})
	fa, errs := appScopeAnalyze(t, root, "main.nomi", nil)
	expectNoErrorsT(t, errs)
	if len(fa.Program().AppTypes) != 1 || fa.Program().AppTypes[0].Name != "App" {
		t.Fatalf("application types: %v", fa.Program().AppTypes)
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

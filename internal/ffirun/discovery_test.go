package ffirun

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stageSourceBindingProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bindingDir := filepath.Join(root, "binding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir binding: %v", err)
	}
	mustWriteHelper(t, filepath.Join(bindingDir, "binding.go"), `package taggedbinding

import "strings"

type Box struct {
	Label string
}

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}

func AddOne(n int64) int64 {
	return n + 1
}

func AddCounts(items map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range items {
		out[k] = v + 1
	}
	return out
}

type Address struct {
	City string
}

type User struct {
	Name    string
	Age     int64
	Address Address
}

func Birthday(user User) User {
	user.Age++
	return user
}
`)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module example.com/binding/source

go 1.26.3
`)
	mustWriteHelper(t, filepath.Join(root, "go.mod"), `module testproject

go 1.26.3

require example.com/binding/source v0.0.0

replace example.com/binding/source => ./binding
`)
	mustWriteHelper(t, filepath.Join(root, "nomi.toml"), `[module]
name = "testproject"
`)
	return root
}

func TestDiscovery_SourceBindings(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

opaque type RawBox go binding.Box

fn echo_upper(s: String): String go binding.EchoUpper

fn add_one(n: Int): Int go binding.AddOne
`)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 package, got %d: %+v", len(got), got)
	}
	if got[0].ImportPath != "example.com/binding/source" {
		t.Fatalf("ImportPath: got %q", got[0].ImportPath)
	}
	if len(got[0].Types) != 1 {
		t.Fatalf("types: got %+v", got[0].Types)
	}
	gotType := got[0].Types[0]
	if gotType.Key != "RawBox" || gotType.TypeName != "Box" || gotType.Declaration != "host type RawBox" {
		t.Fatalf("type binding: got %+v", gotType)
	}
	wantExports := []struct {
		key  string
		name string
		decl string
	}{
		{key: "add_one", name: "AddOne", decl: "host fn add_one(n: Int): Int"},
		{key: "echo_upper", name: "EchoUpper", decl: "host fn echo_upper(s: String): String"},
	}
	if len(got[0].Exports) != len(wantExports) {
		t.Fatalf("exports: got %+v", got[0].Exports)
	}
	for i, want := range wantExports {
		gotExport := got[0].Exports[i]
		if gotExport.Key != want.key || gotExport.FuncName != want.name || gotExport.Declaration != want.decl {
			t.Fatalf("export %d: got %+v", i, gotExport)
		}
	}
}

func TestDiscovery_SourceBindingsFromGoPackageEntry(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

opaque type RawBox go binding.Box

fn echo_upper(s: String): String go binding.EchoUpper
`)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 package, got %d: %+v", len(got), got)
	}
	if got[0].ImportPath != "example.com/binding/source" || got[0].Owner != "binding" {
		t.Fatalf("unexpected package: %+v", got[0])
	}
	if len(got[0].Types) != 1 || got[0].Types[0].TypeName != "Box" {
		t.Fatalf("type binding: %+v", got[0].Types)
	}
	if len(got[0].Exports) != 1 || got[0].Exports[0].FuncName != "EchoUpper" {
		t.Fatalf("exports: %+v", got[0].Exports)
	}
}

func TestDiscovery_GoSelectorMapSignature(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn add_counts(items: Map<String, Int>): Map<String, Int> go binding.AddCounts
`)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || len(got[0].Exports) != 1 {
		t.Fatalf("expected one export, got %+v", got)
	}
	exp := got[0].Exports[0]
	if len(exp.Params) != 1 || exp.Params[0].TypeAnnotation == nil || exp.Params[0].TypeAnnotation.TypeString() != "Map<String, Int>" {
		t.Fatalf("Params: got %+v", exp.Params)
	}
	if exp.ReturnType == nil || exp.ReturnType.TypeString() != "Map<String, Int>" {
		t.Fatalf("ReturnType: got %v", exp.ReturnType)
	}
}

func TestDiscovery_GoSelectorStructSignature(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

struct Address {
  city: String
}

struct User {
  name: String
  age: Int
  address: Address
}

fn birthday(user: User): User go binding.Birthday
`)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || len(got[0].Exports) != 1 {
		t.Fatalf("expected one export, got %+v", got)
	}
	exp := got[0].Exports[0]
	if len(exp.Params) != 1 || exp.Params[0].TypeAnnotation == nil || exp.Params[0].TypeAnnotation.TypeString() != "User" {
		t.Fatalf("Params: got %+v", exp.Params)
	}
	if exp.ReturnType == nil || exp.ReturnType.TypeString() != "User" {
		t.Fatalf("ReturnType: got %v", exp.ReturnType)
	}
}

func TestDiscovery_NoSourceBindings(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `fn main() {}
`)
	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 packages, got %d: %+v", len(got), got)
	}
}

func TestPrepareSkipsNestedNomiPackagesOutsideEntryScope(t *testing.T) {
	root := t.TempDir()
	mustWriteHelper(t, filepath.Join(root, "go.mod"), `module parent

go 1.26.3
`)
	parentDir := filepath.Join(root, "tests", "18-ffi-and-dynamic")
	mustWriteHelper(t, filepath.Join(parentDir, "json_test.nomi"), `import std/json

test "plain json test" {
  assert True
}
`)
	fixtureDir := filepath.Join(parentDir, "tagged_ffi_app")
	mustWriteHelper(t, filepath.Join(fixtureDir, "nomi.toml"), `[module]
name = "tagged_ffi_app"
entry_points = ["main"]
`)
	mustWriteHelper(t, filepath.Join(fixtureDir, "ffi.nomi"), `gopkg "taggedffiapp" as ffi

pub fn echo_upper(s: String): String go ffi.EchoUpper
`)

	res, err := Prepare(filepath.Join(parentDir, "json_test.nomi"))
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !res.FastPath {
		t.Fatalf("expected FastPath=true for parent file with no in-scope FFI, got %+v", res)
	}
}

func TestDiscovery_DuplicateSourceBindingsAreDeduped(t *testing.T) {
	root := stageSourceBindingProject(t)
	decl := `gopkg "example.com/binding/source" as binding

fn echo_upper(s: String): String go binding.EchoUpper
`
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), decl)
	mustWriteHelper(t, filepath.Join(root, "main_test.nomi"), decl)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || len(got[0].Exports) != 1 {
		t.Fatalf("expected one deduped export, got %+v", got)
	}
	if got[0].Exports[0].Key != "echo_upper" {
		t.Fatalf("export key: got %+v", got[0].Exports[0])
	}
}

func TestDiscovery_LocalReplacePackageBindings(t *testing.T) {
	root := t.TempDir()
	nomiRoot := filepath.Join(root, "dep")
	if err := os.MkdirAll(nomiRoot, 0o755); err != nil {
		t.Fatalf("mkdir dep: %v", err)
	}
	mustWriteHelper(t, filepath.Join(nomiRoot, "binding.nomi"), `gopkg "example.com/dep" as dep

pub fn ping(): String go dep.Ping
`)
	mustWriteHelper(t, filepath.Join(nomiRoot, "nomi.toml"), `[module]
name = "dep"
`)
	mustWriteHelper(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module app

go 1.26.3

require example.com/dep v0.0.0

replace example.com/dep => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `import dep/binding

fn main() {
  _ = binding.ping()
}
`)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || got[0].ImportPath != "example.com/dep" || len(got[0].Exports) != 1 {
		t.Fatalf("expected dep binding, got %+v", got)
	}
	if got[0].Exports[0].Key != "binding.ping" {
		t.Fatalf("export key: got %+v", got[0].Exports[0])
	}
}

func TestPrepareRejectsExternPackageNotProvidedByGoMod(t *testing.T) {
	root := t.TempDir()
	mustWriteHelper(t, filepath.Join(root, "go.mod"), `module app

go 1.26.3
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/missing/pkg" as missing

fn ping(): String go missing.Ping
`)

	_, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatal("expected Prepare to reject unresolved Go package")
	}
	msg := err.Error()
	wantParts := []string{
		"nomi FFI binding validation failed",
		`main.nomi:1:8`,
		`Go package "example.com/missing/pkg" is not provided`,
	}
	for _, want := range wantParts {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, msg)
		}
	}
}

func TestGoModProvidesImportPathAllowsStandardLibrary(t *testing.T) {
	root := t.TempDir()
	mustWriteHelper(t, filepath.Join(root, "go.mod"), `module app

go 1.26.3
`)

	if !goModProvidesImportPath(root, "strings") {
		t.Fatal("expected Go standard-library package to be allowed without a go.mod require")
	}
}

func TestPrepareAllowsMissingGoSelectorCheckedByGoCompiler(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

opaque type RawBox go binding.Box

fn missing_func(): String go binding.MissingFunc
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject missing selector, got result %+v", res)
	}
	if !strings.Contains(err.Error(), `has no top-level function "MissingFunc"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsGoSelectorSignatureMismatch(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn echo_upper(s: Int): String go binding.EchoUpper

fn add_one(n: Int): String go binding.AddOne
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject signature mismatch, got result %+v", res)
	}
	if !strings.Contains(err.Error(), `parameter 1 "s" projects to String, but Nomi declares Int`) ||
		!strings.Contains(err.Error(), "return projects to Int, but Nomi declares String") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsUnsupportedGoParameterType(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "extra.go"), `package taggedbinding

func UseNamed(v interface{ Name() string }) string {
	return v.Name()
}
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn use_named(value: Dynamic): String go binding.UseNamed
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject unsupported parameter, got result %+v", res)
	}
	msg := err.Error()
	if !strings.Contains(msg, `has unsupported parameter type`) ||
		!strings.Contains(msg, `non-empty interface interface{ Name() string }`) ||
		strings.Contains(msg, `Go has 0 parameters`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsNamedGoInterfaceType(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "extra.go"), `package taggedbinding

type Named interface {
	Name() string
}

func UseNamedValue(v Named) string {
	return v.Name()
}
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn use_named(value: Dynamic): String go binding.UseNamedValue
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject named interface parameter, got result %+v", res)
	}
	msg := err.Error()
	if !strings.Contains(msg, `named Go interface type Named is not automatically converted`) ||
		!strings.Contains(msg, `declare it as an opaque type or normalize it in Go`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsNamedGoFunctionType(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "extra.go"), `package taggedbinding

type Transform func(string) string

func UseTransform(f Transform) string {
	return f("x")
}
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn use_transform(f: (String) -> String): String go binding.UseTransform
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject named function parameter, got result %+v", res)
	}
	msg := err.Error()
	if !strings.Contains(msg, `named Go function type Transform is not automatically converted`) ||
		!strings.Contains(msg, `use an unnamed function parameter in the Go adapter`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsNamedGoChannelType(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "extra.go"), `package taggedbinding

type Updates chan string

func UseUpdates(ch Updates) string {
	return <-ch
}
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn use_updates(value: Dynamic): String go binding.UseUpdates
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject named channel parameter, got result %+v", res)
	}
	msg := err.Error()
	if !strings.Contains(msg, `named Go channel type Updates is not automatically converted`) ||
		!strings.Contains(msg, `declare it as an opaque type or normalize it in Go`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareAllowsNamedGoAliasProjection(t *testing.T) {
	// These two tests reach Prepare, which creates a cache
	// directory. Without the override they create it in the user's
	// real cache root, keyed on a t.TempDir() that go test deletes on
	// the way out — a directory nothing can ever hit again. They were
	// the only source of new orphans in the last week of the 2656
	// measured there.
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "extra.go"), `package taggedbinding

type Alias = string

func EchoAlias(v Alias) Alias {
	return v
}
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn echo_alias(value: String): String go binding.EchoAlias
`)

	if _, err := Prepare(filepath.Join(root, "main.nomi")); err != nil {
		t.Fatalf("Prepare should allow named aliases with supported underlying types: %v", err)
	}
}

func TestPrepareRejectsUnsupportedCallbackReturnShape(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "extra.go"), `package taggedbinding

func BadCallback(f func(string) (string, string)) string {
	value, _ := f("x")
	return value
}
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

fn bad_callback(f: (String) -> Result<String, String>): String go binding.BadCallback
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject callback return shape, got result %+v", res)
	}
	msg := err.Error()
	if !strings.Contains(msg, `callback func(string) (string, string)`) ||
		!strings.Contains(msg, `second return projects to String, expected error`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsStructShapeMismatch(t *testing.T) {
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

struct User {
  name: String
  age: String
  email: String
}

fn birthday(user: User): User go binding.Birthday
`)

	res, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatalf("expected Prepare to reject struct shape mismatch, got result %+v", res)
	}
	msg := err.Error()
	wantParts := []string{
		`parameter 1 "user" struct field "age" projects to Int, but Nomi User declares String`,
		`parameter 1 "user" struct field "address" exists in Go User but not in Nomi User`,
		`parameter 1 "user" struct field "email" exists in Nomi User but not in Go User`,
		`return struct field "age" projects to Int, but Nomi User declares String`,
	}
	for _, want := range wantParts {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, msg)
		}
	}
}

func TestPrepareValidatesStructFieldsWithStructFileImports(t *testing.T) {
	// See TestPrepareAllowsNamedGoAliasProjection: this one reaches
	// Prepare too, and wrote into the real cache root without it.
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "event_struct.go"), `package taggedbinding

import "time"

type Event struct {
	Created time.Time
}
`)
	mustWriteHelper(t, filepath.Join(root, "binding", "event_func.go"), `package taggedbinding

func TouchEvent(e Event) Event {
	return e
}
`)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

struct Event {
  created: Instant
}

fn touch_event(event: Event): Event go binding.TouchEvent
`)

	if _, err := Prepare(filepath.Join(root, "main.nomi")); err != nil {
		t.Fatalf("Prepare should validate struct fields using the struct file imports: %v", err)
	}
}

// hi.nomi and bin/hi.nomi share a base name and declare one binding, and
// each is keyed by its path under the project (hi.upper, bin/hi.upper), so
// discovery keeps an export for each. Either can be the entry, whose own
// declarations the runtime keys bare, so the wrapper rekeys each export for
// its own file, or `nomi run` of that file finds no binding for upper.
func TestDiscovery_SameBaseNameInTwoDirectoriesKeysEach(t *testing.T) {
	root := stageSourceBindingProject(t)
	decl := `gopkg "example.com/binding/source" as binding

fn upper(s: String): String go binding.EchoUpper
`
	top := filepath.Join(root, "hi.nomi")
	nested := filepath.Join(root, "bin", "hi.nomi")
	mustWriteHelper(t, top, decl)
	mustWriteHelper(t, nested, decl)

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 || len(got[0].Exports) != 2 {
		t.Fatalf("expected an export per file, got %+v", got)
	}
	src, err := renderWrapper(root, got)
	if err != nil {
		t.Fatalf("renderWrapper: %v", err)
	}
	want := map[string]string{"bin/hi.upper": nested, "hi.upper": top}
	for _, exp := range got[0].Exports {
		file, ok := want[exp.Key]
		if !ok || exp.SourceFile != file || exp.EntryKey != "upper" || len(exp.AlsoDeclaredIn) != 0 {
			t.Fatalf("export %q from %s (entry key %q, also %v); want keys %v", exp.Key, exp.SourceFile, exp.EntryKey, exp.AlsoDeclaredIn, want)
		}
		call := fmt.Sprintf("nomiExternKey(targetPath, %q, %q, %q)", exp.Key, "upper", file)
		if !strings.Contains(string(src), call) {
			t.Errorf("wrapper does not rekey %s for its own file; want %s in:\n%s", exp.Key, call, src)
		}
	}
}

// BindingModule keys a file by its path under the nearest nomi.toml or go.mod, so two
// files with one base name in different directories cross under different
// keys, and a file at the Go module's root keeps its base name. The key is
// relative: it carries no path of the machine that computed it.
func TestBindingModule_IsThePathUnderTheNearestGoModule(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("app/go.mod", "module app\n")
	write("dep/go.mod", "module dep\n")
	write("app/lib/nomi.toml", "[module]\nname = \"lib\"\n")
	cases := map[string]string{
		"app/lib/x.nomi":      "x",
		"app/lib/sub/y.nomi":  "sub/y",
		"app/ffi.nomi":        "ffi",
		"app/a/util.nomi":     "a/util",
		"app/b/util.nomi":     "b/util",
		"app/bin/hi":          "bin/hi",
		"dep/sqlite.nomi":     "sqlite",
		"dep/inner/conn.nomi": "inner/conn",
		"loose/util.nomi":     "util",
	}
	for rel, want := range cases {
		if got := BindingModule(write(rel, "")); got != want {
			t.Errorf("BindingModule(%s) = %q, want %q", rel, got, want)
		}
	}
}

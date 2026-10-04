package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
)

func TestDocumentManager_SourceGoBindingsAnalyzeCleanly(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	mustWrite("go.mod", `module localbindingtest

go 1.26.3
`)
	mustWrite("nomi.toml", `[module]
name = "localbindingtest"
entry_points = ["main"]
`)
	mustWrite("binding.go", `package localbindingtest

import "strings"

func EchoUpper(s string) string {
	return strings.ToUpper(s)
	}
`)
	mainPath := mustWrite("main.nomi", `import std/io

gopkg "localbindingtest" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(echo_upper("hello"))
}
`)

	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(mainPath)
	doc := dm.Open("file://"+mainPath, string(src))
	if len(doc.Errors) != 0 {
		t.Fatalf("unexpected parse errors: %+v", doc.Errors)
	}
	if docHasTypeErrorContaining(doc, "undefined type or variant 'IO'") {
		t.Fatalf("std/io import after Go package handle was not visible: %+v", doc.Analysis.TypeErrors)
	}
	if docHasTypeErrorContaining(doc, "undefined variable 'echo_upper'") {
		t.Fatalf("source-declared Go binding was not visible: %+v", doc.Analysis.TypeErrors)
	}
}

func docHasTypeErrorContaining(doc *analysis.Document, needle string) bool {
	if doc == nil || doc.Analysis == nil {
		return false
	}
	for _, e := range doc.Analysis.TypeErrors {
		if strings.Contains(e.Message, needle) {
			return true
		}
	}
	return false
}

func docHasParseErrorContaining(doc *analysis.Document, needle string) bool {
	if doc == nil {
		return false
	}
	for _, e := range doc.Errors {
		if strings.Contains(e.Message, needle) {
			return true
		}
	}
	return false
}

// TestDocumentManager_TypeLevelCycleAcrossFiles verifies that the
// DocumentManager (which routes through BuildProject) tolerates
// mutually recursive type definitions across files: type A in a.nomi
// has a field of type B (defined in b.nomi); B has a field of type A.
// Both signatures are well-defined; only the legacy per-file builder's
// strict cycle rejection prevented this from type-checking. Once
// DocumentManager.analyze routes through BuildProject, the cycle
// resolves cleanly because Sweep A registers every file's symbols
// before annotation walking begins.
func TestDocumentManager_TypeLevelCycleAcrossFiles(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	mustWrite("main.nomi", "import {\n  a: A\n  b: B\n}\n\nfn main() { Unit }\n")
	aPath := mustWrite("a.nomi", `import b: B

pub struct A {
  partner: Maybe<B>
}
`)
	mustWrite("b.nomi", `import a: A

pub struct B {
  partner: Maybe<A>
}
`)

	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(aPath)
	dm.Open("file://"+aPath, string(src))

	doc := dm.Get("file://" + aPath)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	for _, e := range doc.Analysis.TypeErrors {
		if e.Message != "" {
			t.Errorf("unexpected type error in cyclic-types file: %s", e.Message)
		}
	}
}

// TestDocumentManager_FunctionSignatureCycleAcrossFiles verifies that
// mutually recursive function signatures across files type-check
// without error once the DocumentManager routes through BuildProject.
// a.bounce calls b.pong and vice versa; both signatures reference each
// other's modules. Sweep A registers all signatures up-front so neither
// file's annotation walk fails to resolve the other.
func TestDocumentManager_FunctionSignatureCycleAcrossFiles(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	mustWrite("main.nomi", "import a\n\nfn main() { io.inspect(a.bounce(0)) }\n")
	aPath := mustWrite("a.nomi", `import b

pub fn bounce(n: Int): Int {
  case n {
    0 -> 0
    _ -> b.pong(n - 1)
  }
}
`)
	mustWrite("b.nomi", `import a

pub fn pong(n: Int): Int {
  case n {
    0 -> 0
    _ -> a.bounce(n - 1)
  }
}
`)

	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(aPath)
	dm.Open("file://"+aPath, string(src))

	doc := dm.Get("file://" + aPath)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	for _, e := range doc.Analysis.TypeErrors {
		if e.Message != "" {
			t.Errorf("unexpected type error in cyclic-fn-sig file: %s", e.Message)
		}
	}
}

// TestDocumentManager_CycleWithCrossFileImpl verifies that the cycle
// resolution preserves cross-file impl conformance: a.nomi defines
// interface I and type A (whose `partner` field is of type b.T), while
// b.nomi defines type T and an impl `I for T`. The impl crosses the
// import cycle but must still register correctly.
func TestDocumentManager_CycleWithCrossFileImpl(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	mustWrite("main.nomi", "import { a: A }\n\nfn main() { Unit }\n")
	aPath := mustWrite("a.nomi", `import b: T

pub interface I {
  fn label(value: self): String
}

pub struct A {
  partner: T
}
`)
	mustWrite("b.nomi", `import a: I

pub struct T {
  name: String
}

impl I for T {
  fn label(value: T): String { value.name }
}
`)

	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(aPath)
	dm.Open("file://"+aPath, string(src))

	doc := dm.Get("file://" + aPath)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	for _, e := range doc.Analysis.TypeErrors {
		if e.Message != "" {
			t.Errorf("unexpected type error in cycle+impl file: %s", e.Message)
		}
	}
}

// TestDocumentManager_AppFieldAccessResolvesAcrossFiles verifies that
// `AppEnv.logger` in a sibling file resolves to the app field's symbol.
func TestDocumentManager_AppFieldAccessResolvesAcrossFiles(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	mustWrite("main.nomi", `import {
  env.{self, AppEnv}
  greeter
}

fn boot(): AppEnv {
  env.load(Context.root())
}

fn main() {
  greeter.greet("World")
}
`)
	mustWrite("env.nomi", `import {
  log: Logger, ProdLogger
}

pub struct AppEnv {
  context: Context
  logger: Logger
}

pub fn load(context: Context): AppEnv {
  AppEnv{context, logger: ProdLogger}
}

`)
	greeterPath := mustWrite("greeter.nomi", `import {
  env.AppEnv
  log: Logger
}

pub fn greet(name: String) {
  Logger.log(AppEnv.logger, "INFO", "hi ${name}")
}
`)
	mustWrite("log.nomi", `pub interface Logger {
  fn log(value: self, level: String, message: String): Unit
}

pub type ProdLogger

impl Logger for ProdLogger {
  fn log(_logger: ProdLogger, _level: String, _message: String): Unit { Unit }
}
`)

	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(greeterPath)
	dm.Open("file://"+greeterPath, string(src))

	doc := dm.Get("file://" + greeterPath)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}

	var fieldSym *analysis.Symbol
	for _, sym := range doc.Analysis.References {
		if sym.Kind == analysis.SymbolField && sym.Name == "logger" {
			fieldSym = sym
			break
		}
	}
	if fieldSym == nil {
		t.Fatal("expected the AppEnv.logger reference to resolve to the app field symbol")
	}
}

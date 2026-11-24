package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/modcache"
)

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

	mustWrite("main.nomi", "import\n  a: A\n  b: B\nend\n\nfn main()\n    Unit\nend\n")
	aPath := mustWrite("a.nomi", `import b: B

pub struct A
  partner: Option<B>
end
`)
	mustWrite("b.nomi", `import a: A

pub struct B
  partner: Option<A>
end
`)

	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(aPath)
	dm.Open(fileuri.FromPath(aPath), string(src))

	doc := dm.Get(fileuri.FromPath(aPath))
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

	mustWrite("main.nomi", "import a\n\nfn main()\n    io.inspect(a.bounce(0))\nend\n")
	aPath := mustWrite("a.nomi", `import b

pub fn bounce(n: Int) -> Int
  case n
    0 -> 0
    _ -> b.pong(n - 1)
  end
end
`)
	mustWrite("b.nomi", `import a

pub fn pong(n: Int) -> Int
  case n
    0 -> 0
    _ -> a.bounce(n - 1)
  end
end
`)

	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(aPath)
	dm.Open(fileuri.FromPath(aPath), string(src))

	doc := dm.Get(fileuri.FromPath(aPath))
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

	mustWrite("main.nomi", "import\n    a: A\nend\n\nfn main()\n    Unit\nend\n")
	aPath := mustWrite("a.nomi", `import b: T

pub interface I
  fn label(value: self) -> String
end

pub struct A
  partner: T
end
`)
	mustWrite("b.nomi", `import a: I

pub struct T
  name: String
end

impl I for T
  fn label(value: T) -> String
      value.name
  end
end
`)

	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(aPath)
	dm.Open(fileuri.FromPath(aPath), string(src))

	doc := dm.Get(fileuri.FromPath(aPath))
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

	mustWrite("main.nomi", `import
  env.{self, AppEnv}
  greeter
end

fn boot() -> Result<AppEnv, String>
  Ok(env.load(Context.root()))
end

fn main()
  greeter.greet("World")
end
`)
	mustWrite("env.nomi", `import
  log: Logger, ProdLogger
end

pub struct AppEnv
  context: Context
  logger: Logger
end

pub fn load(context: Context) -> AppEnv
  AppEnv{context:, logger: ProdLogger}
end

`)
	greeterPath := mustWrite("greeter.nomi", `import
  env.AppEnv
  log: Logger
end

pub fn greet(name: String)
  Logger.log(AppEnv.logger, "INFO", "hi ${name}")
end
`)
	mustWrite("log.nomi", `pub interface Logger
  fn log(value: self, level: String, message: String) -> Unit
end

pub type ProdLogger

impl Logger for ProdLogger
  fn log(_logger: ProdLogger, _level: String, _message: String) -> Unit
      Unit
  end
end
`)

	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(greeterPath)
	dm.Open(fileuri.FromPath(greeterPath), string(src))

	doc := dm.Get(fileuri.FromPath(greeterPath))
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

// TestDocumentManager_AModuleThatNeedsALaterNomiIsOneError: in the
// language server, a project whose [module].nomi names a later release
// than the running one gets one error, with its code, and nothing of its
// files is checked: the import of a module nomi.toml lacks and the call
// of an undefined function report nothing.
func TestDocumentManager_AModuleThatNeedsALaterNomiIsOneError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(modcache.CacheEnv, filepath.Join(tmp, "cache"))
	modcache.SetRelease("v0.5.0")
	t.Cleanup(func() { modcache.SetRelease("") })
	write := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	write("nomi.toml", "[module]\nname = \"app\"\nnomi = \"v0.6.0\"\nentry_points = [\"main\"]\n")
	main := write("main.nomi", "import later\n\nfn main()\n    undefined_here()\nend\n")

	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src, _ := os.ReadFile(main)
	dm.Open(fileuri.FromPath(main), string(src))
	doc := dm.Get(fileuri.FromPath(main))
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analysis to be populated")
	}
	errs := doc.Analysis.TypeErrors
	want := "module app (this project) needs nomi v0.6.0 or later, and this nomi is v0.5.0; install a newer nomi"
	if len(errs) != 1 || errs[0].Code != analysis.NomiVersionCode || errs[0].Message != want {
		t.Fatalf("the document's errors are %+v, want one: %s", errs, want)
	}
}

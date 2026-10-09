package ffirun

import (
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateGolden regenerates golden files in place when set. Use:
//
//	go test ./internal/ffirun -run TestCodegen -update
//
// Standard Go golden-file idiom; the regenerated file is committed
// like any other test fixture.
var updateGolden = flag.Bool("update", false, "regenerate golden wrapper files")

// TestCodegen_RendersOneFFIDep pins the wrapper bytes for a project
// with one FFI dep (the typical sqlite-binding shape). Catches
// accidental template drift (added whitespace, reordered imports,
// changed error message wording) before such drift escapes to
// users — the template hash bakes into hash.json, so any byte-
// level change invalidates every cached wrapper on every machine.
func TestCodegen_RendersOneFFIDep(t *testing.T) {
	pkgs := []DiscoveredPackage{
		{
			ImportPath: "github.com/foo/sqlite",
			Alias:      "sqlite",
			Types: []DiscoveredType{
				{Key: "sqlite.RawConn", TypeName: "Conn", Declaration: "host type RawConn", SourceFile: "/abs/project/sqlite.nomi", SourceLine: 3, SourceCol: 13},
			},
			Exports: []DiscoveredExport{
				{Key: "sqlite.open_raw", FuncName: "OpenRaw", Declaration: "host fn open_raw(path: String): Result<RawConn, String>", SourceFile: "/abs/project/sqlite.nomi", SourceLine: 5, SourceCol: 14},
			},
		},
	}
	got, err := renderWrapper("/abs/project", pkgs)
	if err != nil {
		t.Fatalf("renderWrapper: %v", err)
	}
	goldenPath := filepath.Join("testdata", "wrappers", "one-ffi-dep.golden.go")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Logf("regenerated %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (rerun with -update to regenerate)", err)
	}
	if string(got) != string(want) {
		t.Errorf("wrapper differs from golden.\n--- got ---\n%s\n--- want ---\n%s",
			got, want)
	}
}

// TestCodegen_NoDeps verifies the no-FFI-dep degenerate case: the
// rendered wrapper still parses as valid Go and still runs through
// nomi/vmhost. (The wrapper path never fires for projects with zero FFI
// deps in practice — Prepare short-circuits to FastPath — but if codegen
// is ever called with an empty slice the output must not blow up.)
func TestCodegen_NoDeps(t *testing.T) {
	got, err := renderWrapper("/abs/project", nil)
	if err != nil {
		t.Fatalf("renderWrapper: %v", err)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "wrapper.go", got, 0); err != nil {
		t.Errorf("generated wrapper is not valid Go: %v\nSource:\n%s", err, got)
	}
	src := string(got)
	for _, want := range []string{
		"checkProgramVM(targetPath)",
		"nomivmhost.Check(entryPath, nomivmhost.WithHosts(nomiHostTable(entryPath)))",
		"runProgramVM(targetPath, extraArgs)",
		"runTestFileVM(targetPath, extraArgs, testFormat)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("wrapper missing %q:\n%s", want, src)
		}
	}
	if strings.Contains(src, "NomiRegister") {
		t.Errorf("wrapper still calls NomiRegister:\n%s", src)
	}
}

// TestCodegen_TheProjectRunnerLinksNoFrontEnd: `nomi build`'s runner for an
// FFI project is the wrapper's generated adapters with vmrunner's main, so it
// imports nomi/vmrunner and never nomi/vmhost (which lowers source, and so
// links the front end); std/compiler's hosts only when asked.
func TestCodegen_TheProjectRunnerLinksNoFrontEnd(t *testing.T) {
	pkgs := []DiscoveredPackage{{
		ImportPath: "github.com/foo/sqlite",
		Alias:      "sqlite",
		Exports: []DiscoveredExport{
			{Key: "sqlite.open_raw", FuncName: "OpenRaw", Declaration: "host fn open_raw(path: String): Result<Int, String>",
				SourceFile: "/abs/project/sqlite.nomi", SourceLine: 5, SourceCol: 14, EntryKey: "open_raw"},
		},
	}}
	for _, compiler := range []bool{false, true} {
		got, err := renderMain("/abs/project", pkgs, mainKind{Runner: true, Compiler: compiler})
		if err != nil {
			t.Fatalf("renderMain: %v", err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "runner.go", got, 0); err != nil {
			t.Fatalf("the generated runner is not valid Go: %v\n%s", err, got)
		}
		src := string(got)
		if strings.Contains(src, `"github.com/nomi-language/nomi/vmhost"`) || !strings.Contains(src, `"github.com/nomi-language/nomi/vmrunner"`) ||
			!strings.Contains(src, "nomivmrunner.WithEntryHosts") {
			t.Fatalf("the runner must import nomi/vmrunner and not nomi/vmhost:\n%s", src)
		}
		if strings.Contains(src, `"github.com/nomi-language/nomi/vmrunner/compiler"`) != compiler {
			t.Fatalf("compiler=%v, but the runner's std/compiler import disagrees:\n%s", compiler, src)
		}
	}
}

// TestCodegen_ImportPathAliasing exercises the alias-disambiguation
// rule from discovery: two import paths sharing a final segment get
// numbered aliases (`db1`, `db2`) so the generated import block
// stays a legal Go file. Without disambiguation Go would refuse to
// compile the wrapper (duplicate import name).
func TestCodegen_ImportPathAliasing(t *testing.T) {
	pkgs := []DiscoveredPackage{
		{
			ImportPath: "github.com/foo/db",
			Exports: []DiscoveredExport{
				{Key: "foo_db_ping", FuncName: "Ping"},
			},
		},
		{
			ImportPath: "github.com/bar/db",
			Exports: []DiscoveredExport{
				{Key: "bar_db_ping", FuncName: "Ping"},
			},
		},
	}
	assignAliases(pkgs)
	if pkgs[0].Alias != "db1" || pkgs[1].Alias != "db2" {
		t.Fatalf("aliases: got %q, %q; want db1, db2", pkgs[0].Alias, pkgs[1].Alias)
	}
	got, err := renderWrapper("/abs/project", pkgs)
	if err != nil {
		t.Fatalf("renderWrapper: %v", err)
	}
	src := string(got)
	wantSubs := []string{
		`db1 "github.com/foo/db"`,
		`db2 "github.com/bar/db"`,
		`_ = db1.Ping`,
		`_ = db2.Ping`,
	}
	for _, s := range wantSubs {
		if !strings.Contains(src, s) {
			t.Errorf("wrapper missing %q:\n%s", s, src)
		}
	}
	// Verify the rendered source parses as valid Go.
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "wrapper.go", got, 0); err != nil {
		t.Errorf("generated wrapper is not valid Go: %v\nSource:\n%s", err, src)
	}
}

// TestCodegen_AliasSanitizes covers the hyphen-in-module-path case.
// `github.com/foo/some-binding` would yield "some-binding" as the
// base — illegal Go identifier — so sanitizeIdent maps the hyphen
// to underscore.
func TestCodegen_AliasSanitizes(t *testing.T) {
	pkgs := []DiscoveredPackage{
		{ImportPath: "github.com/foo/some-binding"},
	}
	assignAliases(pkgs)
	if pkgs[0].Alias != "some_binding" {
		t.Errorf("alias: got %q, want some_binding", pkgs[0].Alias)
	}
}

func TestCodegen_RendersTaggedExports(t *testing.T) {
	pkgs := []DiscoveredPackage{
		{
			ImportPath: "example.com/binding/echo",
			Alias:      "echo",
			Types: []DiscoveredType{
				{Key: "FFI.RawBox", TypeName: "Box", Declaration: "host type RawBox"},
			},
			Exports: []DiscoveredExport{
				{Key: "FFI.echo_upper", FuncName: "EchoUpper", Declaration: "host fn echo_upper(s: String): String"},
			},
		},
	}
	got, err := renderWrapper("/abs/project", pkgs)
	if err != nil {
		t.Fatalf("renderWrapper: %v", err)
	}
	src := string(got)
	wantSubs := []string{
		`echo "example.com/binding/echo"`,
		`_ = (*echo.Box)(nil)`,
		`_ = echo.EchoUpper`,
	}
	for _, s := range wantSubs {
		if !strings.Contains(src, s) {
			t.Errorf("wrapper missing %q:\n%s", s, src)
		}
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "wrapper.go", got, 0); err != nil {
		t.Errorf("generated wrapper is not valid Go: %v\nSource:\n%s", err, src)
	}
}

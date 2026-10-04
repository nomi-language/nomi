package analysis_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// TestOrphanRule_RejectsForeignIfaceForForeignType_E2E runs the whole
// BuildProjectWithCache pipeline over testdata/orphan_violator, an on-disk
// project whose go.mod replaces a sibling orphan_dep module and whose entry
// implements `Display` (std) for `Pad` (orphan_dep). It is the on-disk
// counterpart to TestOrphanImpl_CrossModuleRejected, so it also covers
// nomi.toml parsing and project-root discovery.
func TestOrphanRule_RejectsForeignIfaceForForeignType_E2E(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot resolve fixture path")
	}
	analysisDir := filepath.Dir(thisFile)
	fixtureRoot := filepath.Join(analysisDir, "testdata", "orphan_violator")
	entryPath := filepath.Join(fixtureRoot, "main.nomi")

	data, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("read fixture entry %s: %v", entryPath, err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	// Loader mirrors the front end's on-disk import resolver (see
	// internal/frontend's Checker.Loader): module path segments join under
	// projectRoot, suffixed with ".nomi".
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, fixtureRoot, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}

	var orphanErr *analysis.TypeError
	for i := range fa.TypeErrors {
		e := &fa.TypeErrors[i]
		if strings.Contains(e.Message, "orphan impl") {
			orphanErr = e
			break
		}
	}
	if orphanErr == nil {
		t.Fatalf("expected an `orphan impl` TypeError from on-disk orphan_violator fixture, got %d errors: %v",
			len(fa.TypeErrors), fa.TypeErrors)
	}

	// The diagnostic names both foreign sides and the implementing module.
	for _, want := range []string{"Display", "Pad", "orphan_violator"} {
		if !strings.Contains(orphanErr.Message, want) {
			t.Errorf("orphan diagnostic missing %q in message: %s", want, orphanErr.Message)
		}
	}
}

// TestOrphanRule_RejectsForeignIfaceForNonPreludeStdlibType_E2E runs the
// same pipeline over testdata/orphan_violator_nonprelude, whose entry
// implements `Display` for `Date`. Both are declared in std, and `Date` is
// outside the prelude, so the check must find its declaring module through
// the std files the build discovers rather than through the prelude.
func TestOrphanRule_RejectsForeignIfaceForNonPreludeStdlibType_E2E(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot resolve fixture path")
	}
	analysisDir := filepath.Dir(thisFile)
	fixtureRoot := filepath.Join(analysisDir, "testdata", "orphan_violator_nonprelude")
	entryPath := filepath.Join(fixtureRoot, "main.nomi")

	data, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("read fixture entry %s: %v", entryPath, err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, fixtureRoot, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}

	var orphanErr *analysis.TypeError
	for i := range fa.TypeErrors {
		e := &fa.TypeErrors[i]
		if strings.Contains(e.Message, "orphan impl") {
			orphanErr = e
			break
		}
	}
	if orphanErr == nil {
		t.Fatalf("expected an `orphan impl` TypeError from on-disk orphan_violator_nonprelude fixture, got %d errors: %v",
			len(fa.TypeErrors), fa.TypeErrors)
	}

	// The diagnostic names both foreign sides and the implementing module.
	for _, want := range []string{"Display", "Date", "orphan_violator_nonprelude"} {
		if !strings.Contains(orphanErr.Message, want) {
			t.Errorf("orphan diagnostic missing %q in message: %s", want, orphanErr.Message)
		}
	}
}

// TestOrphanRule_ProjectTypeShadowingStdlibInherentReceiver_NoFalsePositive
// is the regression test for detectInherentOrphanImpls' `declaredLocally`
// short-circuit (added with the primitives-move, when the prelude began
// pulling std/int into every build closure).
//
// Failure mode it pins: declaringModuleIndex collapses declarations by BASE
// name with project-wins precedence. A project type sharing a base name with
// a NON-prelude stdlib type (here `PositiveInt`, also declared in
// std/int.nomi — non-prelude names are legal to redeclare) steals the
// stdlib name's mapping, so declModule["PositiveInt"] points at the project
// module. std/int.nomi's own `PositiveInt` type-body functions then compare
// receiver-module ("the project") against implementing-module ("std") and —
// without the declaredLocally short-circuit — are falsely
// flagged orphan, failing EVERY build that declares such a type. The
// short-circuit notices that std/int.nomi itself declares the receiver,
// which makes the impl local by construction regardless of what the
// base-name-collapsed index says.
//
// Fixture shape matters: the project's shadowing type lives in a
// sibling module that carries an explicit stdlib import (`std/maybe`)
// — the same shape as the opaque cross-module tests where the
// regression first surfaced. The explicit stdlib import is what makes
// discovery walk into the std package so std/int (with its inherent
// `impl PositiveInt` block) lands in the build's file set and actually
// reaches detectInherentOrphanImpls; without any explicit std import
// the inherent records never include std/int and the check trivially
// passes regardless of the predicate. (Verified: with the
// declaredLocally argument swapped to nil at the project_build.go call
// site, this test fails with `orphan impl: inherent “impl PositiveInt
// { ... }“ in module "std" ...`; with the predicate wired, it passes.)
func TestOrphanRule_ProjectTypeShadowingStdlibInherentReceiver_NoFalsePositive(t *testing.T) {
	tmp := t.TempDir()
	files := map[string]string{
		"positive.nomi": `

pub opaque type PositiveInt Int

pub fn from_int(n: Int): Maybe<PositiveInt> {
  if n > 0 { Some(PositiveInt(n)) } else { None }
}
`,
		"main.nomi": `import positive: from_int

fn main() {
  p = from_int(3)
}
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(filepath.Join(tmp, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}

	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, "orphan impl") {
			t.Errorf("unexpected orphan diagnostic (declaredLocally short-circuit regressed?): %s", e.Message)
		}
	}
}

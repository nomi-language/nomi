package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// checkUniversalDebugProject builds a single-file project through the
// full BuildProjectWithCache + CheckTypes + FinalizeCoherence pipeline —
// the same path internal/frontend and the LSP use. This is the path that
// surfaces both the `does not implement Debug` interface-bound error
// (checkGenericCall) and the `no impl Debug` missing-impl diagnostic
// (FinalizeCoherence), which is what the universal-Debug change must
// silence. The simpler single-file BuildFileWithStdlib path doesn't build
// a project impl index, so those diagnostics never fire there.
func checkUniversalDebugProject(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "main.nomi"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	tokens := lexer.Lex(src)
	entryNodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		fp := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(fp)
		if err != nil {
			return nil, err
		}
		ft := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(ft)
		return nodes, nil
	}
	entryFA, _, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader)
	if entryFA == nil {
		t.Fatal("BuildProjectWithCache returned nil")
	}
	var errs []analysis.TypeError
	errs = append(errs, entryFA.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(entryFA, entryNodes)...)
	errs = append(errs, analysis.FinalizeCoherence(entryFA)...)
	return errs
}

func expectNoErrs(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("expected no errors, got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
	}
}

func expectErrContaining(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected error containing %q, got %d:\n  %s", substr, len(errs), strings.Join(msgs, "\n  "))
}

// TestUniversalDebug_NoBoundGenericTypeChecks pins Step 2.1: a generic fn
// calling Debug.inspect on a bare, unbounded type variable must type-check
// clean — Debug is universally satisfied, so no `where T: Debug` bound is
// needed. Callers at concrete types (Int / String) must also check.
func TestUniversalDebug_NoBoundGenericTypeChecks(t *testing.T) {
	src := `import std/io

fn show<T>(x: T): String {
    Debug.inspect(x)
}

fn main(): Unit {
    io.print(show(42))
    io.print(show("hi"))
}
`
	expectNoErrs(t, checkUniversalDebugProject(t, src))
}

// TestUniversalDebug_BoundDebugOnNonDebugTypeChecks pins the core of 2.1:
// a `where T: Debug` bound applied at a type that has NO explicit Debug impl
// must type-check clean. Today (pre-change) this errors with
// "P does not implement Debug (required by `where T: Debug`)". Universal
// Debug makes Debug satisfied by every type, so the interface-bound check
// passes.
func TestUniversalDebug_BoundDebugOnNonDebugTypeChecks(t *testing.T) {
	src := `import std/io

struct P { x: Int }

fn show<T>(x: T): String where T: Debug {
    Debug.inspect(x)
}

fn main(): Unit {
    io.print(show(P{x: 1}))
}
`
	expectNoErrs(t, checkUniversalDebugProject(t, src))
}

// TestUniversalDebug_DisplayBoundStillErrors proves the change is
// Debug-only: a `where T: Display` bound at a non-Display type must STILL
// error (Display is opt-in; its bound is real). The diagnostic must name
// Display, not Debug.
func TestUniversalDebug_DisplayBoundStillErrors(t *testing.T) {
	src := `import std/io

struct P { x: Int }

fn show<T>(x: T): String where T: Display {
    Display.to_string(x)
}

fn main(): Unit {
    io.print(show(P{x: 1}))
}
`
	errs := checkUniversalDebugProject(t, src)
	expectErrContaining(t, errs, "Display")
	for _, e := range errs {
		if strings.Contains(e.Message, "implement Debug") {
			t.Errorf("unexpected Debug interface-bound error in Display test: %s", e.Message)
		}
	}
}

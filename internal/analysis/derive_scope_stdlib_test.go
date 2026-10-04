package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// checkPreludeless builds src the way a stdlib module is built: discovered
// under a `std/` key, so it gets NO prelude parent scope and no prelude
// import injection — it sees exactly the names its own imports bring in.
// This is the only context where the derive-arg scope rule can fire (user
// files get all five protocols from the prelude) and where synthesized
// internals genuinely have nothing in scope to lean on. The harness mirrors
// std.Load(): a synthetic entry imports the module under test, a loader
// serves its source, and CheckTypes runs over the post-synthesis node slice
// BuildProjectWithCache returns for it.
func checkPreludeless(t *testing.T, src string) (*analysis.FileAnalysis, []analysis.TypeError) {
	t.Helper()
	const modName = "derivescopetest"

	parse := func(s string) []ast.Node {
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(s))
		return nodes
	}
	base := std.MakeLoader()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		if len(modulePath) > 0 && modulePath[len(modulePath)-1] == modName {
			return parse(src), nil
		}
		return base(projectRoot, modulePath)
	}

	entryNodes := parse("import std/" + modName + ": Point\n")
	_, cache, extendedNodes := analysis.BuildProjectWithCache(
		entryNodes, nil, nil, nil, "", loader,
	)
	fa := cache["std/"+modName]
	if fa == nil {
		t.Fatalf("std/%s not discovered into the project cache", modName)
	}
	var errs []analysis.TypeError
	errs = append(errs, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, extendedNodes["std/"+modName])...)
	return fa, errs
}

func formatErrs(errs []analysis.TypeError) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n  ")
}

// TestDeriveScope_PreludelessAllProtocols pins the import contract of a
// prelude-less file deriving every protocol: the imports cover exactly the
// names the visible text writes — the four `@derive` args (Debug needs
// none) — and nothing else. The synthesized internals (impl headers, the
// compare signature's `Ordering`, Ordering variants / True / False /
// recursion callees in bodies) resolve through the compiler-known route, so
// the build is clean and every (T, Iface) pair materializes. This is the
// shape std/bool.nomi ships with.
func TestDeriveScope_PreludelessAllProtocols(t *testing.T) {
	src := `import {
  std/comparable.Comparable
  std/display.Display
  std/equatable.Equatable
  std/hashable.Hashable
}

pub struct Point {
  x: Int
  y: Int
}
derive Comparable for Point
derive Debug for Point
derive Display for Point
derive Equatable for Point
derive Hashable for Point

pub enum Color {
  Red
  Green
  Blue
}
derive Comparable for Color
derive Debug for Color
derive Display for Color
derive Equatable for Color
derive Hashable for Color
`
	fa, errs := checkPreludeless(t, src)
	if len(errs) > 0 {
		t.Fatalf("expected a clean build, got %d diagnostics:\n  %s", len(errs), formatErrs(errs))
	}
	for _, ty := range []string{"Point", "Color"} {
		for _, iface := range []string{"Comparable", "Debug", "Display", "Equatable", "Hashable"} {
			if !fa.Impls[ty][iface] {
				t.Errorf("%s: (%s) impl not recorded in fa.Impls (got %v)", ty, iface, fa.Impls[ty])
			}
		}
	}
}

// TestDeriveScope_PreludelessMissingImportErrors: dropping one protocol's
// import makes its `@derive` arg error with the exact import to add — at the
// arg's real source position — while the other args stay quiet.
func TestDeriveScope_PreludelessMissingImportErrors(t *testing.T) {
	src := `import {
  std/display.Display
  std/equatable.Equatable
  std/hashable.Hashable
}

pub struct Point {
  x: Int
}
derive Comparable for Point
derive Display for Point
derive Equatable for Point
derive Hashable for Point
`
	_, errs := checkPreludeless(t, src)
	want := "`Comparable` is not in scope — import `std/comparable.Comparable`"
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			found = true
			if analysis.IsSynthesizedLine(e.Line) {
				t.Errorf("derive-arg scope error at synthesized position: %s", e.Error())
			}
		}
		if strings.Contains(e.Message, "is not in scope") && !strings.Contains(e.Message, "Comparable") {
			t.Errorf("unexpected scope error for an imported protocol: %s", e.Error())
		}
	}
	if !found {
		t.Fatalf("expected error containing %q, got %d diagnostics:\n  %s", want, len(errs), formatErrs(errs))
	}
}

// TestDeriveScope_PreludelessHandWrittenImplStillRequiresImport: the
// compiler-known header route covers SYNTHESIZED blocks only. A hand-written
// `impl Display for T` in a prelude-less file still requires `Display` in
// scope, exactly like before.
func TestDeriveScope_PreludelessHandWrittenImplStillRequiresImport(t *testing.T) {
	src := `pub struct P {
  x: Int
}

impl Display for P {
  fn to_string(_value: P): String { "p" }
}
`
	_, errs := checkPreludeless(t, src)
	for _, e := range errs {
		if strings.Contains(e.Message, "undefined interface 'Display'") {
			return
		}
	}
	t.Fatalf("expected \"undefined interface 'Display'\", got %d diagnostics:\n  %s", len(errs), formatErrs(errs))
}

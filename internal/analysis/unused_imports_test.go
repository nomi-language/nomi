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

// findOnlyUnused asserts FindUnusedImports returned exactly one item and
// returns it.
func findOnlyUnused(t *testing.T, items []analysis.UnusedImport) analysis.UnusedImport {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 unused import, got %d: %+v", len(items), items)
	}
	return items[0]
}

func TestFindUnusedImports_WholeModule(t *testing.T) {
	items, _, _ := findUnusedWithStdlib(t, "import std/io\n\nfn main() { Unit }\n")
	u := findOnlyUnused(t, items)
	if u.ItemKind != analysis.UnusedWholeModule {
		t.Errorf("kind: got %v, want UnusedWholeModule", u.ItemKind)
	}
	if u.Name != "io" {
		t.Errorf("name: got %q, want io", u.Name)
	}
	if u.NameIdx != -1 {
		t.Errorf("NameIdx: got %d, want -1", u.NameIdx)
	}
	if u.Stmt == nil {
		t.Error("Stmt should not be nil")
	}
	if !strings.Contains(u.Message, "imported module 'io' is unused") {
		t.Errorf("message: got %q", u.Message)
	}
}

func TestFindUnusedImports_BraceItemSole(t *testing.T) {
	items, _, _ := findUnusedWithStdlib(t, "import std/comparable.{Ordering}\n\nfn main() { Unit }\n")
	u := findOnlyUnused(t, items)
	if u.ItemKind != analysis.UnusedBraceItem {
		t.Errorf("kind: got %v, want UnusedBraceItem", u.ItemKind)
	}
	if u.Name != "Ordering" {
		t.Errorf("name: got %q, want Ordering", u.Name)
	}
	if u.NameIdx != 0 {
		t.Errorf("NameIdx: got %d, want 0", u.NameIdx)
	}
}

func TestFindUnusedImports_BraceItemOneOfSeveral(t *testing.T) {
	src := "import std/comparable.{Comparable, Ordering}\n\nfn f<T>(x: T): T where T: Comparable { x }\n"
	items, _, _ := findUnusedWithStdlib(t, src)
	u := findOnlyUnused(t, items)
	if u.ItemKind != analysis.UnusedBraceItem {
		t.Errorf("kind: got %v, want UnusedBraceItem", u.ItemKind)
	}
	if u.Name != "Ordering" {
		t.Errorf("name: got %q, want Ordering", u.Name)
	}
	if u.NameIdx != 1 {
		t.Errorf("NameIdx: got %d, want 1 (index of Ordering in Names)", u.NameIdx)
	}
}

func TestFindUnusedImports_SelfMarker(t *testing.T) {
	src := "import std/results.Result.{self, Ok}\n\nfn f(): Maybe<Int> { o = Ok(1)\n  None }\n"
	items, _, _ := findUnusedWithStdlib(t, src)
	u := findOnlyUnused(t, items)
	if u.ItemKind != analysis.UnusedSelfMarker {
		t.Errorf("kind: got %v, want UnusedSelfMarker", u.ItemKind)
	}
	// Name carries the binding the `self` item lifts (the parent module/enum
	// name), matching the diagnostic's "binding 'Result'" phrasing.
	if u.Name != "Result" {
		t.Errorf("name: got %q, want Result", u.Name)
	}
	if !strings.Contains(u.Message, "imported name 'self' (binding 'Result') is unused") {
		t.Errorf("message: got %q", u.Message)
	}
}

// buildErrsWithStdlib runs the single-file builder pipeline (the
// BuildFileWithStdlib path, where CheckUnusedImports fires at the tail of
// buildModule) and returns the builder-level TypeErrors. The prelude is the
// parent scope, mirroring how the LSP analyzes a lone document.
func buildErrsWithStdlib(t *testing.T, src string) []analysis.TypeError {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	return fa.TypeErrors
}

// findUnusedWithStdlib builds the single-file analysis and returns the
// structured FindUnusedImports results (plus the parsed nodes), so tests can
// assert on item kind / name / index, not just the rendered diagnostic.
func findUnusedWithStdlib(t *testing.T, src string) ([]analysis.UnusedImport, []ast.Node, *analysis.FileAnalysis) {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	return analysis.FindUnusedImports(fa, nodes), nodes, fa
}

func unusedErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if strings.Contains(e.Message, "is unused") {
			out = append(out, e)
		}
	}
	return out
}

func expectNoUnused(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	for _, e := range unusedErrors(errs) {
		t.Errorf("unexpected unused-import error: %s", e.Error())
	}
}

func expectUnusedContaining(t *testing.T, errs []analysis.TypeError, substr string) analysis.TypeError {
	t.Helper()
	for _, e := range unusedErrors(errs) {
		if strings.Contains(diagText(e), substr) {
			return e
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected unused-import error containing %q, got %d errors:\n  %s",
		substr, len(errs), strings.Join(msgs, "\n  "))
	return analysis.TypeError{}
}

// A dead brace item errors on that item specifically — message and position
// pinned. The sibling item used in the body stays silent.
func TestUnusedImport_DeadBraceItem(t *testing.T) {
	src := `import std/comparable.{Comparable, Ordering}

fn f<T>(x: T): T where T: Comparable { x }
`
	errs := buildErrsWithStdlib(t, src)
	e := expectUnusedContaining(t, errs, "imported name 'Ordering' is unused — remove it from the import")
	if e.Line != 1 {
		t.Errorf("error line: got %d, want 1", e.Line)
	}
	wantCol := strings.Index(src, "Ordering") + 1
	if e.Col != wantCol {
		t.Errorf("error col: got %d, want %d", e.Col, wantCol)
	}
	for _, ue := range unusedErrors(errs) {
		if strings.Contains(ue.Message, "Comparable") {
			t.Errorf("Comparable is used (interface bound) but flagged: %s", ue.Error())
		}
	}
}

// A whole-file import with no qualified access anywhere is dead.
func TestUnusedImport_DeadModuleImport(t *testing.T) {
	errs := buildErrsWithStdlib(t, `import std/io

fn main() { Unit }
`)
	expectUnusedContaining(t, errs, "imported module 'io' is unused — remove the import")
}

// A module import used via member access (`io.print`) is live; one used
// only in a qualified type annotation (`Instant`) is live too.
func TestUnusedImport_ModuleUses(t *testing.T) {
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/io

fn main() { io.print("hi") }
`))
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/instant.Instant

fn f(t: Instant): Instant { t }
`))
}

func TestUnusedImport_DiscardBindingValueUsesImport(t *testing.T) {
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/io

fn main() {
  _ = io.inspect("hi")
}
`))
}

// A dead selective alias errors naming both the item and its binding.
func TestUnusedImport_SelectiveAlias(t *testing.T) {
	errs := buildErrsWithStdlib(t, `import std/maybe.Maybe.{Some as Just, None as Nothing}

fn f(): Maybe<Int> { Just(42) }
`)
	expectUnusedContaining(t, errs, "imported name 'None' (bound as 'Nothing') is unused")
	for _, ue := range unusedErrors(errs) {
		if strings.Contains(ue.Message, "Some") || strings.Contains(ue.Message, "Just") {
			t.Errorf("Just is used but flagged: %s", ue.Error())
		}
	}
}

// `self` in a brace list is its own item: dead when the parent name is
// never referenced, live when it is.
func TestUnusedImport_SelfItem(t *testing.T) {
	errs := buildErrsWithStdlib(t, `import std/results.Result.{self, Ok}

fn f(): Maybe<Int> { o = Ok(1)
  None }
`)
	expectUnusedContaining(t, errs, "imported name 'self' (binding 'Result') is unused")

	expectNoUnused(t, buildErrsWithStdlib(t, `import std/results.Result.{self, Ok}

fn f(): Result<Int, String> { Ok(1) }
`))
}

// Re-exports are used by definition: line-level `export` and per-item
// `export` exempt their items. (The prelude is the canonical consumer of this
// carve-out.)
func TestUnusedImport_ExportIsUse(t *testing.T) {
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/maybe.{Maybe} export
`))
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/maybe.{Maybe export}
`))
}

// A `derive Iface` conformance line counts as a use of the imported protocol
// (derive args are real scope references).
func TestUnusedImport_DeriveArgIsUse(t *testing.T) {
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/equatable.{Equatable}

struct P {
  x: Int

}
derive Equatable for P
`))
}

// Pattern positions count as uses (`Some(v)` in a case arm references the
// drill-through import).
func TestUnusedImport_PatternIsUse(t *testing.T) {
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/maybe.Maybe.{Some, None}

fn f(m: Maybe<Int>): Int {
  case m {
    Some(v) -> v
    None -> 0
  }
}
`))
}

// A typed-literal tag (`Date"..."`) counts as a use of the imported tag
// type, even though the tag reference is recorded as a proxy symbol.
func TestUnusedImport_TypedLiteralTagIsUse(t *testing.T) {
	expectNoUnused(t, buildErrsWithStdlib(t, `import std/calendar.Date

fn main() {
  d = Date"2024-01-15"
  Unit
}
`))
}

// Drill-through granularity: the brace items are what's checked; using one
// variant doesn't keep an unused sibling alive.
func TestUnusedImport_DrillThroughPerItem(t *testing.T) {
	errs := buildErrsWithStdlib(t, `import std/maybe.Maybe.{Some, None}

fn f(): Maybe<Int> { Some(1) }
`)
	expectUnusedContaining(t, errs, "imported name 'None' is unused")
	for _, ue := range unusedErrors(errs) {
		if strings.Contains(ue.Message, "'Some'") {
			t.Errorf("Some is used but flagged: %s", ue.Error())
		}
	}
}

// Every stdlib module must itself be clean: std.Load() runs the project
// pipeline (and with it CheckUnusedImports) over each std/*.nomi, but
// discards diagnostics — this guard surfaces any unused import that sneaks
// into the stdlib.
func TestUnusedImport_StdlibIsClean(t *testing.T) {
	lib := std.Load()
	for name, fa := range lib.Files {
		for _, e := range unusedErrors(fa.TypeErrors) {
			t.Errorf("std/%s.nomi: %s", name, e.Error())
		}
	}
}

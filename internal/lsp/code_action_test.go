package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// codeActionFixture analyzes src the way the LSP does for a lone document and
// returns the content, nodes, analysis, and the diagnostics the client would
// hold (with the unused-import code tagged on).
func codeActionFixture(t *testing.T, src string) (string, []ast.Node, *analysis.FileAnalysis, []protocol.Diagnostic) {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	diags := typeErrorsToDiagnostics(fa.TypeErrors)
	return src, nodes, fa, diags
}

// onlyUnusedDiags filters down to the unused-import diagnostics so a test can
// feed exactly those to the handler (mirroring a client narrowing by range).
func onlyUnusedDiags(diags []protocol.Diagnostic) []protocol.Diagnostic {
	var out []protocol.Diagnostic
	for _, d := range diags {
		if d.Code != nil && d.Code.Value == analysis.UnusedImportCode {
			out = append(out, d)
		}
	}
	return out
}

func positionToOffset(content string, p protocol.Position) int {
	line, col := uint32(0), uint32(0)
	for i := 0; i < len(content); i++ {
		if line == p.Line && col == p.Character {
			return i
		}
		if content[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return len(content)
}

func applyTextEdit(content string, e protocol.TextEdit) string {
	start := positionToOffset(content, e.Range.Start)
	end := positionToOffset(content, e.Range.End)
	return content[:start] + e.NewText + content[end:]
}

// runFix asserts that the fixture produces exactly one quick-fix and returns
// the source after applying its single TextEdit.
func runFix(t *testing.T, src string) string {
	t.Helper()
	content, nodes, fa, diags := codeActionFixture(t, src)
	unused := onlyUnusedDiags(diags)
	if len(unused) != 1 {
		t.Fatalf("expected exactly 1 unused-import diagnostic, got %d", len(unused))
	}
	actions := buildRemoveUnusedActions(content, nodes, fa, "file:///t.nomi", unused)
	if len(actions) != 1 {
		t.Fatalf("expected exactly 1 code action, got %d", len(actions))
	}
	a := actions[0]
	if a.Title != "Remove unused import" {
		t.Errorf("title: got %q", a.Title)
	}
	if a.Kind == nil || *a.Kind != protocol.CodeActionKindQuickFix {
		t.Errorf("kind: got %v, want quickfix", a.Kind)
	}
	if a.Edit == nil {
		t.Fatal("action has no edit")
	}
	edits := a.Edit.Changes["file:///t.nomi"]
	if len(edits) != 1 {
		t.Fatalf("expected exactly 1 text edit, got %d", len(edits))
	}
	return applyTextEdit(content, edits[0])
}

func TestCodeAction_RemoveWholeModule(t *testing.T) {
	got := runFix(t, "import std/io\n\nfn main() { Unit }\n")
	want := "\nfn main() { Unit }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeAction_RemoveAliasedName(t *testing.T) {
	got := runFix(t, "import std/lists.List as Lists\n\nfn main() { Unit }\n")
	want := "\nfn main() { Unit }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeAction_RemoveSoleBraceItem(t *testing.T) {
	got := runFix(t, "import std/comparable.{Ordering}\n\nfn main() { Unit }\n")
	want := "\nfn main() { Unit }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeAction_RemoveBraceItem_PrecedingComma(t *testing.T) {
	// Ordering (second item) is dead; Comparable (first) is used.
	got := runFix(t, "import std/comparable.{Comparable, Ordering}\n\nfn f<T>(x: T): T where T: Comparable { x }\n")
	want := "import std/comparable.Comparable\n\nfn f<T>(x: T): T where T: Comparable { x }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeAction_RemoveBraceItem_FollowingComma(t *testing.T) {
	// Ordering (first item) is dead; Comparable (second) is used.
	got := runFix(t, "import std/comparable.{Ordering, Comparable}\n\nfn f<T>(x: T): T where T: Comparable { x }\n")
	want := "import std/comparable.Comparable\n\nfn f<T>(x: T): T where T: Comparable { x }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeAction_RemoveSelfMarker(t *testing.T) {
	src := "import std/results.Result.{self, Ok}\n\nfn f(): Maybe<Int> { o = Ok(1)\n  None }\n"
	got := runFix(t, src)
	want := "import std/results.Result.{Ok}\n\nfn f(): Maybe<Int> { o = Ok(1)\n  None }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeAction_RemoveLoneBraceItemBesideSelf(t *testing.T) {
	// self (Result) is used as a type; Ok is dead and the only Names entry,
	// so the fix collapses to a `{self}`-only import.
	src := "import std/results.Result.{self, Ok}\n\nfn f(r: Result<Int, String>): Result<Int, String> { r }\n"
	got := runFix(t, src)
	want := "import std/results.Result.{self}\n\nfn f(r: Result<Int, String>): Result<Int, String> { r }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- grouped `import { ... }` block form ----------------------------------
//
// The dominant shape in real Nomi code: a single `import { ... }` block with
// several child statements. The fixer must edit one child surgically and leave
// the block and its siblings intact.

// (a) One-of-several brace items inside a grouped child: Ordering is dead, so
// the child collapses to `std/comparable.Comparable`; the block, its braces,
// and the sibling `std/io` child are untouched.
func TestCodeAction_Block_BraceItemInChild(t *testing.T) {
	src := "import {\n" +
		"  std/comparable.{Comparable, Ordering}\n" +
		"  std/io\n" +
		"}\n\n" +
		"fn f<T>(x: T): T where T: Comparable { x }\n\n" +
		"fn main() { io.print(\"hi\") }\n"
	got := runFix(t, src)
	want := "import {\n" +
		"  std/comparable.Comparable\n" +
		"  std/io\n" +
		"}\n\n" +
		"fn f<T>(x: T): T where T: Comparable { x }\n\n" +
		"fn main() { io.print(\"hi\") }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// (b) Whole-child removal: the `std/io` child is an unused module import;
// only its line is deleted, the surrounding block and the `std/comparable`
// child are preserved.
func TestCodeAction_Block_WholeChildRemoval(t *testing.T) {
	src := "import {\n" +
		"  std/comparable.Comparable\n" +
		"  std/io\n" +
		"}\n\n" +
		"fn f<T>(x: T): T where T: Comparable { x }\n"
	got := runFix(t, src)
	want := "import {\n" +
		"  std/comparable.Comparable\n" +
		"}\n\n" +
		"fn f<T>(x: T): T where T: Comparable { x }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// (c) Degenerate case: the removed import is the ONLY child of the block. The
// fixer deletes just the child's line, leaving an empty “ block.
// We pin that behavior here: it is a known, acceptable edge — an empty block is
// inert (it binds nothing) and an unused-whole-block scenario is vanishingly
// rare in practice (real blocks always carry several children). Collapsing the
// empty block would require the fixer to reach past the child statement to the
// owning ImportBlock node, which the surgical per-statement edit deliberately
// does not do. If this ever becomes a nuisance, that is the change to make.
func TestCodeAction_Block_SoleChildLeavesEmptyBlock(t *testing.T) {
	src := "import {\n" +
		"  std/io\n" +
		"}\n\n" +
		"fn main() { Unit }\n"
	got := runFix(t, src)
	want := "import {\n" +
		"}\n\n" +
		"fn main() { Unit }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The client echoes diagnostics back in the codeAction request, and glsp's
// IntegerOrString drops the Code value on JSON round-trip. Simulate that
// (Code present but Value nil) and confirm the fix is still offered — the
// position match against FindUnusedImports is what carries it.
func TestCodeAction_SurvivesDroppedCode(t *testing.T) {
	content, nodes, fa, diags := codeActionFixture(t, "import std/io\n\nfn main() { Unit }\n")
	unused := onlyUnusedDiags(diags)
	if len(unused) != 1 {
		t.Fatalf("expected 1 unused diagnostic, got %d", len(unused))
	}
	// Mimic the post-round-trip state: a non-nil Code whose Value was lost.
	unused[0].Code = &protocol.IntegerOrString{Value: nil}
	actions := buildRemoveUnusedActions(content, nodes, fa, "file:///t.nomi", unused)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action despite dropped code, got %d", len(actions))
	}
}

// A diagnostic with a different, non-empty code at an import position must not
// receive the remove-unused fix.
func TestCodeAction_RejectsForeignCode(t *testing.T) {
	content, nodes, fa, diags := codeActionFixture(t, "import std/io\n\nfn main() { Unit }\n")
	unused := onlyUnusedDiags(diags)
	if len(unused) != 1 {
		t.Fatalf("expected 1 unused diagnostic, got %d", len(unused))
	}
	unused[0].Code = &protocol.IntegerOrString{Value: "some-other-code"}
	actions := buildRemoveUnusedActions(content, nodes, fa, "file:///t.nomi", unused)
	if len(actions) != 0 {
		t.Errorf("expected no action for a foreign code, got %d", len(actions))
	}
}

// --- add import -------------------------------------------------------------

// addImportFixture analyzes src through the full pipeline (BuildTypes +
// CheckTypes) so the "undefined variable" diagnostic the add fix matches
// against is present, then returns the content/nodes/analysis and the LSP
// diagnostics the client would hold.
func addImportFixture(t *testing.T, src string) (string, []ast.Node, *analysis.FileAnalysis, []protocol.Diagnostic) {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	all := append([]analysis.TypeError(nil), fa.TypeErrors...)
	all = append(all, analysis.BuildTypes(fa, nodes)...)
	all = append(all, analysis.CheckTypes(fa, nodes)...)
	return src, nodes, fa, typeErrorsToDiagnostics(all)
}

// runAddFix asserts the fixture produces exactly one "Add import" action and
// returns the source after applying its single edit.
func runAddFix(t *testing.T, src string) string {
	t.Helper()
	content, nodes, fa, diags := addImportFixture(t, src)
	actions := buildAddImportActions(content, nodes, fa, "file:///t.nomi", diags)
	if len(actions) != 1 {
		t.Fatalf("expected exactly 1 add-import action, got %d", len(actions))
	}
	a := actions[0]
	if a.Kind == nil || *a.Kind != protocol.CodeActionKindQuickFix {
		t.Errorf("kind: got %v, want quickfix", a.Kind)
	}
	if a.Edit == nil {
		t.Fatal("action has no edit")
	}
	edits := a.Edit.Changes["file:///t.nomi"]
	if len(edits) != 1 {
		t.Fatalf("expected exactly 1 text edit, got %d", len(edits))
	}
	return applyTextEdit(content, edits[0])
}

// No existing imports: a new statement is inserted at the top, with a blank
// line separating it from the code.
func TestCodeAction_AddImport_NoExistingImports(t *testing.T) {
	got := runAddFix(t, "fn main() {\n  io.inspect(42)\n}\n")
	want := "import std/io\n\nfn main() {\n  io.inspect(42)\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An unrelated existing import: the new statement is inserted right after the
// last import line (canonical re-sorting is left to fmt / organize).
func TestCodeAction_AddImport_AfterExisting(t *testing.T) {
	got := runAddFix(t, "import std/lists.List\n\nfn main() {\n  io.inspect(42)\n}\n")
	want := "import std/lists.List\nimport std/io\n\nfn main() {\n  io.inspect(42)\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Existing selective import of the same module: merge the missing member into it.
func TestCodeAction_AddImport_SelfMerge(t *testing.T) {
	got := runAddFix(t, "import std/calendar.Date\n\nfn f(_t: Time, _d: Date): Bool {\n  True\n}\n")
	want := "import std/calendar.{Date, Time}\n\nfn f(_t: Time, _d: Date): Bool {\n  True\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Imports already in a grouped block: the new import joins the block as an
// entry (appended unsorted; `nomi fmt`/organize sort it), preserving the block
// style rather than splitting into a flat statement the formatter never merges.
func TestCodeAction_AddImport_JoinsBlock(t *testing.T) {
	got := runAddFix(t, "import {\n  std/lists.List\n}\n\nfn main() {\n  io.inspect(42)\n}\n")
	want := "import {\n  std/lists.List\n  std/io\n}\n\nfn main() {\n  io.inspect(42)\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A type-qualified use of an unimported stdlib type adds the owner import.
func TestCodeAction_AddModule_NoImports(t *testing.T) {
	got := runAddFix(t, "fn main() {\n  Duration.seconds(5)\n}\n")
	want := "import std/duration.Duration\n\nfn main() {\n  Duration.seconds(5)\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A type-owner import joins an existing block as an entry.
func TestCodeAction_AddModule_JoinsBlock(t *testing.T) {
	got := runAddFix(t, "import {\n  std/io\n}\n\nfn main() {\n  io.inspect(Duration.seconds(5))\n}\n")
	want := "import {\n  std/io\n  std/duration.Duration\n}\n\nfn main() {\n  io.inspect(Duration.seconds(5))\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A second type from the same module merges into the existing selective import.
func TestCodeAction_AddType_MergesIntoSelective(t *testing.T) {
	got := runAddFix(t, "import std/calendar.Date\n\nfn f(_t: Time, _d: Date): Bool {\n  True\n}\n")
	want := "import std/calendar.{Date, Time}\n\nfn f(_t: Time, _d: Date): Bool {\n  True\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A nested type reached through an imported module needs no extra import.
func TestCodeAction_AddType_NoneWhenNamespaceImported(t *testing.T) {
	content, nodes, fa, diags := addImportFixture(t, "import std/io\n\nfn f(_e: io.Error): Unit {\n  io.print(\"x\")\n}\n")
	actions := buildAddImportActions(content, nodes, fa, "file:///t.nomi", diags)
	if len(actions) != 0 {
		t.Errorf("expected no add-import actions when module is already imported, got %d", len(actions))
	}
}

// Merging a second type into a selective import that lives inside a block edits
// just that entry, leaving the block structure intact.
func TestCodeAction_AddType_MergesIntoSelectiveInBlock(t *testing.T) {
	got := runAddFix(t, "import {\n  std/calendar.Date\n}\n\nfn f(_t: Time, _d: Date): Bool {\n  True\n}\n")
	want := "import {\n  std/calendar.{Date, Time}\n}\n\nfn f(_t: Time, _d: Date): Bool {\n  True\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Nothing to add when the module is already imported.
func TestCodeAction_AddImport_NoneWhenImported(t *testing.T) {
	content, nodes, fa, diags := addImportFixture(t, "import std/io\n\nfn main() {\n  io.inspect(42)\n}\n")
	actions := buildAddImportActions(content, nodes, fa, "file:///t.nomi", diags)
	if len(actions) != 0 {
		t.Errorf("expected no add-import actions when already imported, got %d", len(actions))
	}
}

func TestCodeAction_IgnoresNonUnusedDiagnostics(t *testing.T) {
	content := "fn bad(): Int { \"x\" }\n"
	tokens := lexer.Lex(content)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	diags := typeErrorsToDiagnostics(fa.TypeErrors)
	actions := buildRemoveUnusedActions(content, nodes, fa, "file:///t.nomi", diags)
	if len(actions) != 0 {
		t.Errorf("expected no code actions for a non-unused diagnostic, got %d", len(actions))
	}
}

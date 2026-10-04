package lsp

import (
	"testing"
)

// runOrganize builds the organize-imports action for src and returns the source
// after applying it, plus whether an action was offered at all.
func runOrganize(t *testing.T, src string) (string, bool) {
	t.Helper()
	content, nodes, fa, _ := codeActionFixture(t, src)
	a := buildOrganizeImportsAction(content, nodes, fa, "file:///t.nomi")
	if a == nil {
		return content, false
	}
	if a.Kind == nil || *a.Kind != "source.organizeImports" {
		t.Errorf("kind: got %v, want source.organizeImports", a.Kind)
	}
	edits := a.Edit.Changes["file:///t.nomi"]
	if len(edits) != 1 {
		t.Fatalf("expected exactly 1 edit, got %d", len(edits))
	}
	return applyTextEdit(content, edits[0]), true
}

// Removes unused items across statements and within a selective list at once.
// Names are deliberately non-prelude so what is removed is removed for being
// unused, not for duplicating a prelude binding.
func TestOrganize_RemovesUnused(t *testing.T) {
	src := "import std/timer\n" +
		"import std/calendar.{Date, Time}\n\n" +
		"fn f(d: Date): Date { d }\n"
	got, ok := runOrganize(t, src)
	if !ok {
		t.Fatal("expected an organize action")
	}
	want := "import std/calendar.Date\n\n" +
		"fn f(d: Date): Date { d }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Sorts imports that are all used but out of canonical order.
func TestOrganize_SortsUsed(t *testing.T) {
	src := "import std/io\n" +
		"import std/duration.Duration\n\n" +
		"fn f(_d: Duration): Unit { io.print(\"x\") }\n"
	got, ok := runOrganize(t, src)
	if !ok {
		t.Fatal("expected an organize action")
	}
	want := "import std/duration.Duration\n" +
		"import std/io\n\n" +
		"fn f(_d: Duration): Unit { io.print(\"x\") }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Prunes a grouped block down to one entry and collapses it to the bare form.
func TestOrganize_BlockPrunesAndCollapses(t *testing.T) {
	src := "import {\n" +
		"  std/calendar.{Date, Time}\n" +
		"  std/io\n" +
		"}\n\n" +
		"fn f(d: Date): Date { d }\n"
	got, ok := runOrganize(t, src)
	if !ok {
		t.Fatal("expected an organize action")
	}
	want := "import std/calendar.Date\n\n" +
		"fn f(d: Date): Date { d }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Idempotent: organizing an already-canonical file offers no action.
func TestOrganize_NoActionWhenCanonical(t *testing.T) {
	src := "import std/io\n\nfn main() { io.print(\"x\") }\n"
	if _, ok := runOrganize(t, src); ok {
		t.Error("expected no action for an already-canonical file")
	}
}

func TestOrganize_DrilledFunctionImportUsedBare(t *testing.T) {
	src := "import std/io.inspect\n\nfn main() { inspect(42) }\n"
	if _, ok := runOrganize(t, src); ok {
		t.Error("expected no action when a drilled function import is used bare")
	}
}

func TestOrganize_DrilledFunctionImportUsedInPipe(t *testing.T) {
	src := "import std/io.inspect\n\nfn main() { 42 |> inspect() }\n"
	if _, ok := runOrganize(t, src); ok {
		t.Error("expected no action when a drilled function import is used as a pipe stage")
	}
}

// Idempotence end to end: running organize on its own output is a no-op.
func TestOrganize_IdempotentOnOutput(t *testing.T) {
	src := "import std/lists\n" +
		"import std/comparable.{Comparable, Ordering}\n\n" +
		"fn f<T>(x: T): T where T: Comparable { x }\n"
	once, ok := runOrganize(t, src)
	if !ok {
		t.Fatal("expected a first organize action")
	}
	if _, ok := runOrganize(t, once); ok {
		t.Errorf("organize was not idempotent; second pass still offered an action on:\n%s", once)
	}
}

// Bails when a non-import declaration is interleaved between imports: the
// whole-region replace would otherwise silently delete it. Nomi resolves
// whole-program, so an import after another declaration is legal.
func TestOrganize_BailsOnInterleavedCode(t *testing.T) {
	src := "import std/io\n\n" +
		"fn helper(): Int { 1 }\n\n" +
		"import std/lists\n\n" +
		"fn main() { io.print(\"x\") l = lists.length([1]) }\n"
	if _, ok := runOrganize(t, src); ok {
		t.Error("expected no action when a non-import declaration is between imports")
	}
}

// Bails (no action) when a comment sits inside the import region, rather than
// clobbering it with a whole-region replace.
func TestOrganize_BailsOnCommentInRegion(t *testing.T) {
	src := "import std/lists\n" +
		"import std/io // keep me\n\n" +
		"fn main() { io.print(\"x\") }\n"
	if _, ok := runOrganize(t, src); ok {
		t.Error("expected no action when a comment is inside the import region")
	}
}

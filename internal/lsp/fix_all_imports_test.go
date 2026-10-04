package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// runFixAll builds the source.fixAll action for src and returns the source after
// applying it, plus whether an action was offered.
func runFixAll(t *testing.T, src string) (string, bool) {
	t.Helper()
	content, nodes, fa, _ := codeActionFixture(t, src)
	a := buildFixAllImportsAction(content, nodes, fa, "file:///t.nomi")
	if a == nil {
		return content, false
	}
	if a.Kind == nil || *a.Kind != "source.fixAll" {
		t.Errorf("kind: got %v, want source.fixAll", a.Kind)
	}
	edits := a.Edit.Changes["file:///t.nomi"]
	if len(edits) != 1 {
		t.Fatalf("expected exactly 1 edit, got %d", len(edits))
	}
	return applyTextEdit(content, edits[0]), true
}

// The headline: one pass removes an unused import AND adds a missing one.
func TestFixAll_RemovesAndAdds(t *testing.T) {
	src := "import std/lists.List\n\n" +
		"fn main() {\n  io.inspect(42)\n}\n"
	got, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import std/io\n\n" +
		"fn main() {\n  io.inspect(42)\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// No existing imports: fixAll inserts the missing import at the top.
func TestFixAll_AddOnlyNoImports(t *testing.T) {
	got, ok := runFixAll(t, "fn main() {\n  io.inspect(42)\n}\n")
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import std/io\n\nfn main() {\n  io.inspect(42)\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The missing type's file already has a selective import, so merge into it
// rather than adding a duplicate statement.
func TestFixAll_SelfMerge(t *testing.T) {
	src := "import std/calendar.Date\n\n" +
		"fn f(_t: Time, _d: Date): Bool {\n  True\n}\n"
	got, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import std/calendar.{Date, Time}\n\n" +
		"fn f(_t: Time, _d: Date): Bool {\n  True\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// With nothing missing, fixAll behaves like organize: prune + canonicalize.
func TestFixAll_RemoveLikeOrganizeWhenNothingMissing(t *testing.T) {
	src := "import {\n" +
		"  std/lists.List\n" +
		"  std/io\n" +
		"}\n\n" +
		"fn main() { io.print(\"x\") }\n"
	got, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import std/io\n\nfn main() { io.print(\"x\") }\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// `Duration` (not a prelude name, so the import is load-bearing) is referenced
// only from an attached extern-test body. That reference still counts, so
// fixAll must leave the import alone.
func TestFixAll_KeepsImportUsedOnlyByAttachedExternTest(t *testing.T) {
	src := "import std/duration.Duration\n\n" +
		"pub host type Thing\n\n" +
		"//! assert Duration.as_seconds(Duration.seconds(1)) == 1\n" +
		"pub host fn value(): Int\n"
	if _, ok := runFixAll(t, src); ok {
		t.Fatal("expected no fixAll action when every import is used")
	}
}

func TestFixAll_KeepsImportUsedByFileFunction(t *testing.T) {
	src := "import std/duration.Duration\n\n" +
		"pub host type Thing\n\n" +
		"fn one_second(value: Thing): Duration {\n" +
		"  Duration.seconds(1)\n" +
		"}\n"
	if _, ok := runFixAll(t, src); ok {
		t.Fatal("expected no fixAll action when every import is used")
	}
}

// The round-trip both halves give: usage removed → import removed; usage
// present without import → import added. Together these make the
// comment-out-to-test loop lossless under fixAll-on-save.
func TestFixAll_RoundTrip(t *testing.T) {
	removed, ok := runFixAll(t, "import std/io\n\nfn main() {\n  Unit\n}\n")
	if !ok {
		t.Fatal("expected removal direction to act")
	}
	if strings.Contains(removed, "import std/io") {
		t.Errorf("unused io import should be removed:\n%s", removed)
	}
	added, ok := runFixAll(t, "fn main() {\n  io.inspect(1)\n}\n")
	if !ok {
		t.Fatal("expected add direction to act")
	}
	if !strings.Contains(added, "import std/io") {
		t.Errorf("missing io import should be added:\n%s", added)
	}
}

// Self-merge into a selective import that lives inside a grouped block.
func TestFixAll_SelfMergeInBlock(t *testing.T) {
	src := "import {\n" +
		"  std/calendar.Date\n" +
		"}\n\n" +
		"fn f(t: Time, d: Date): Bool {\n" +
		"  Iter.count([1])\n" +
		"  True\n" +
		"}\n"
	got, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import std/calendar.{Date, Time}\n\n" +
		"fn f(t: Time, d: Date): Bool {\n" +
		"  Iter.count([1])\n" +
		"  True\n" +
		"}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Fix-all adds a missing import into an existing block, keeping the block's
// load-bearing entry. `Duration` is deliberately not a prelude name, so it
// survives the redundant-prelude prune and the block stays a block.
func TestFixAll_AddJoinsExistingBlock(t *testing.T) {
	src := "import {\n" +
		"    std/duration.Duration\n" +
		"}\n\n" +
		"fn main() {\n" +
		"    d = Duration.seconds(1)\n" +
		"    io.inspect(Duration.as_seconds(d))\n" +
		"}\n"
	got, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import {\n" +
		"    std/duration.Duration\n" +
		"    std/io\n" +
		"}\n\n" +
		"fn main() {\n" +
		"    d = Duration.seconds(1)\n" +
		"    io.inspect(Duration.as_seconds(d))\n" +
		"}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Several missing modules added in one pass, sorted canonically.
func TestFixAll_MultipleMissing(t *testing.T) {
	src := "fn main() {\n  io.inspect(42)\n  Iter.count([1])\n}\n"
	got, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import std/io\n\n" +
		"fn main() {\n  io.inspect(42)\n  Iter.count([1])\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Removing a redundant prelude import must not be undone by the add half of
// the same pass, or fixAll-on-save would oscillate: strip `import std/iter.Iter`
// on one save, re-add it on the next, forever. Auto-import declines the name
// because the prelude already resolves it, so the second pass offers nothing.
func TestFixAll_RedundantPreludeRemovalIsIdempotent(t *testing.T) {
	src := "import std/iter.Iter\n\n" +
		"fn main() {\n  io.inspect(Iter.count([1, 2]))\n}\n"
	once, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import std/io\n\n" +
		"fn main() {\n  io.inspect(Iter.count([1, 2]))\n}\n"
	if once != want {
		t.Fatalf("first pass: got %q, want %q", once, want)
	}
	if twice, ok := runFixAll(t, once); ok {
		t.Errorf("second pass should be a no-op, got %q", twice)
	}
}

// The ambiguity filter: a type name with two candidate modules (same position)
// is dropped from the auto/fixAll set; an unambiguous one is kept.
func TestUnambiguousMissing(t *testing.T) {
	p1 := analysis.Pos{Line: 1, Col: 1}
	p2 := analysis.Pos{Line: 2, Col: 1}
	got := unambiguousMissing([]analysis.MissingImport{
		{Qualifier: "Foo", ModulePath: "std/a", Member: "Foo", Pos: p1},
		{Qualifier: "Foo", ModulePath: "std/b", Member: "Foo", Pos: p1},
		{Qualifier: "Duration", ModulePath: "std/duration", Member: "Duration", Pos: p2},
	})
	if len(got) != 1 || got[0].ImportSpec() != "std/duration.Duration" {
		t.Errorf("want only the unambiguous std/duration.Duration, got %+v", got)
	}
}

// fixAll adds a missing stdlib owner import, joining the block.
func TestFixAll_AddsModuleIntoBlock(t *testing.T) {
	src := "import {\n    std/io\n}\n\nfn main() {\n    io.inspect(Duration.seconds(5))\n}\n"
	got, ok := runFixAll(t, src)
	if !ok {
		t.Fatal("expected a fixAll action")
	}
	want := "import {\n    std/duration.Duration\n    std/io\n}\n\nfn main() {\n    io.inspect(Duration.seconds(5))\n}\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// fixAll inherits organize's region-safety bails: a non-import declaration
// between imports means decline rather than clobber.
func TestFixAll_BailsOnInterleavedCode(t *testing.T) {
	src := "import std/io\n\n" +
		"fn helper(): Int { 1 }\n\n" +
		"import std/lists\n\n" +
		"fn main() {\n  io.print(\"x\")\n  io.print(List.head([1]))\n}\n"
	if _, ok := runFixAll(t, src); ok {
		t.Error("expected no fixAll action when a declaration is interleaved between imports")
	}
}

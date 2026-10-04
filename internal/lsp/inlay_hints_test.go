package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

func getInlayHints(t *testing.T, src string) []InlayHint {
	t.Helper()
	lib := std.Load()
	tokens := lexer.Lex(src)
	nodes, errs := parser.ParseWithRecovery(tokens)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	// BuildProject (not BuildFileWithStdlib) so the project impl index is
	// populated — type-promoted stdlib methods like `String.length` resolve
	// through ProjectImpls, which the single-file builder doesn't assemble.
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "/tmp", nil)
	analysis.CheckTypes(fa, nodes)
	return collectInlayHints(fa, nodes)
}

func filterByKind(hints []InlayHint, kind InlayHintKind) []InlayHint {
	var out []InlayHint
	for _, h := range hints {
		if h.Kind != nil && *h.Kind == kind {
			out = append(out, h)
		}
	}
	return out
}

func TestInlayHintBindingType(t *testing.T) {
	src := `fn main() {
    x = 42
    name = "hello"
}`
	hints := getInlayHints(t, src)
	found := filterByKind(hints, InlayHintKindType)
	if len(found) != 2 {
		t.Fatalf("expected 2 type hints, got %d: %+v", len(found), found)
	}
	if found[0].Label != ": Int" {
		t.Errorf("expected ': Int', got %q", found[0].Label)
	}
	if found[1].Label != ": String" {
		t.Errorf("expected ': String', got %q", found[1].Label)
	}
}

func TestInlayHintParamNames(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn main() { add(1, 2) }`
	hints := getInlayHints(t, src)
	found := filterByKind(hints, InlayHintKindParameter)
	if len(found) != 2 {
		t.Fatalf("expected 2 param hints, got %d: %+v", len(found), found)
	}
	if found[0].Label != "x:" {
		t.Errorf("expected 'x:', got %q", found[0].Label)
	}
	if found[1].Label != "y:" {
		t.Errorf("expected 'y:', got %q", found[1].Label)
	}
}

func TestInlayHintSkipsNamedArgs(t *testing.T) {
	src := `fn greet(name: String): String { name }
fn main() { greet(name: "Alice") }`
	hints := getInlayHints(t, src)
	found := filterByKind(hints, InlayHintKindParameter)
	if len(found) != 0 {
		t.Fatalf("expected 0 param hints for named args, got %d: %+v", len(found), found)
	}
}

func TestInlayHintNested(t *testing.T) {
	src := `fn add(x: Int, y: Int): Int { x + y }
fn main() {
    result = add(1, add(2, 3))
}`
	hints := getInlayHints(t, src)
	paramHints := filterByKind(hints, InlayHintKindParameter)
	// outer: x:, y: + inner: x:, y: = 4
	if len(paramHints) != 4 {
		t.Fatalf("expected 4 param hints, got %d: %+v", len(paramHints), paramHints)
	}
	typeHints := filterByKind(hints, InlayHintKindType)
	// result: Int
	if len(typeHints) != 1 {
		t.Fatalf("expected 1 type hint, got %d: %+v", len(typeHints), typeHints)
	}
	if typeHints[0].Label != ": Int" {
		t.Errorf("expected ': Int', got %q", typeHints[0].Label)
	}
}

func TestInlayHintLambdaParams(t *testing.T) {
	src := `fn apply(f: (Int) -> Int, x: Int): Int { f(x) }
fn main() { apply(|n| n + 1, 5) }`
	hints := getInlayHints(t, src)
	typeHints := filterByKind(hints, InlayHintKindType)
	foundLambda := false
	for _, h := range typeHints {
		if h.Label == ": Int" && h.Position.Line == 1 {
			foundLambda = true
		}
	}
	if !foundLambda {
		t.Errorf("expected lambda param type hint ': Int' on line 2, got: %+v", typeHints)
	}
}

func TestCheckTypesStdlibCallArgMismatch(t *testing.T) {
	// String.length takes (s: String): Int — passing an Int should error
	src := `import std/strings.String
fn main() { String.length(42) }`
	lib := std.Load()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "/tmp", nil)
	checkErrs := analysis.CheckTypes(fa, nodes)

	found := false
	for _, e := range checkErrs {
		if strings.Contains(e.Message, "expected String") && strings.Contains(e.Message, "got Int") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected type error for String.length(42), got: %v", checkErrs)
	}
}

func TestInlayHintStdlibCallParams(t *testing.T) {
	src := `import std/strings.String
fn main() { String.length("hello") }`
	hints := getInlayHints(t, src)
	paramHints := filterByKind(hints, InlayHintKindParameter)
	// String.length has param "s" — should show "s:" hint
	foundS := false
	for _, h := range paramHints {
		if h.Label == "s:" {
			foundS = true
		}
	}
	if !foundS {
		t.Errorf("expected param hint 's:' for String.length, got: %+v", paramHints)
	}
}

func TestInlayHintWrappedInModuleCall(t *testing.T) {
	// Tests that hints work inside io.Inspect() etc.
	src := `import std/io
fn add(x: Int, y: Int): Int { x + y }
fn main() {
    io.inspect(add(1, 2))
}`
	hints := getInlayHints(t, src)
	paramHints := filterByKind(hints, InlayHintKindParameter)
	// add(1, 2) → x:, y: + io.inspect(...) → value: = 3
	if len(paramHints) != 3 {
		t.Fatalf("expected 3 param hints inside module call, got %d: %+v", len(paramHints), paramHints)
	}
}

package analysis

import (
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// An error in mapped code takes the span map's hint ahead of the
// checker's own: the macro's hint is about what the user wrote (the
// column a field reads), the checker's about the generated code (an impl
// for the type a generated call bounds).
func TestRelocate_TheMacrosHintLeadsTheCheckers(t *testing.T) {
	e := &macroExpansion{
		decl:   &ast.KeywordDecl{},
		file:   "queries.nomi",
		source: "runtime.text(row)",
		spans: []MacroSpanMap{{
			Start: 0, End: len("runtime.text(row)"),
			Span: MacroSpan{File: "queries.nomi", Line: 4, Col: 12, EndLine: 4, EndCol: 17},
			Hint: "`email` is TEXT, so the field should be `String`",
		}},
	}
	te := TypeError{Line: 1, Col: 1, Message: "Int does not implement SqlText", Hints: []string{"write an `impl SqlText for Int` block"}}
	got := e.relocate(te)
	want := []string{"`email` is TEXT, so the field should be `String`", "write an `impl SqlText for Int` block"}
	if !slices.Equal(got.Hints, want) {
		t.Errorf("hints %q, want %q", got.Hints, want)
	}
	if got.Line != 4 || got.Col != 12 {
		t.Errorf("at %d:%d, want 4:12", got.Line, got.Col)
	}
}

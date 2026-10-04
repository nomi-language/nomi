package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A keyword that opens a block inserts its shape, its later lines relative
// to the keyword's line.
func TestCompletion_KeywordShapes(t *testing.T) {
	tests := []struct {
		name, src, label, text string
	}{
		{"fn at the top level", cursorMark + "\n", "fn", "fn ${1:name}(${2}): ${3:Unit} {\n    $0\n}"},
		{"struct", "str" + cursorMark + "\n", "struct", "struct ${1:Name} {\n    $0\n}"},
		{"enum", "en" + cursorMark + "\n", "enum", "enum ${1:Name} {\n    $0\n}"},
		{"impl", "im" + cursorMark + "\n", "impl", "impl ${1:Type} {\n    $0\n}"},
		{"test", "te" + cursorMark + "\n", "test", "test \"${1:name}\" {\n    assert $0\n}"},
		{"tests", "te" + cursorMark + "\n", "tests", "tests \"${1:name}\" {\n    $0\n}"},
		{"if at a statement", "fn f(n: Int) {\n    i" + cursorMark + "\n}\n", "if", "if ${1:condition} {\n    $0\n}"},
		{"if else at a statement", "fn f(n: Int) {\n    i" + cursorMark + "\n}\n", "if else", "if ${1:condition} {\n    ${2}\n} else {\n    $0\n}"},
		{"case in an operand", "fn f(n: Int): Int {\n    x = ca" + cursorMark + "\n    x\n}\n", "case", "case ${1:value} {\n    $0\n}"},
		{"if in an operand", "fn f(n: Int): Int {\n    x = i" + cursorMark + "\n    x\n}\n", "if else", "if ${1:condition} {\n    ${2}\n} else {\n    $0\n}"},
		{"a nested fn", "fn f() {\n    f" + cursorMark + "\n}\n", "fn", "fn ${1:name}(${2}): ${3:Unit} {\n    $0\n}"},
		{"a deeper line", "fn f(n: Int) {\n    if n > 0 {\n        ca" + cursorMark + "\n    }\n}\n", "case", "case ${1:value} {\n    $0\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := completeWith(t, true, tt.src)
			it := mustItem(t, items, tt.label)
			if te := textEditOf(t, it); te.NewText != tt.text {
				t.Fatalf("%s inserts %q, want %q", tt.label, te.NewText, tt.text)
			}
			if it.InsertTextFormat == nil || *it.InsertTextFormat != protocol.InsertTextFormatSnippet {
				t.Error("not a snippet")
			}
			if it.InsertTextMode == nil || *it.InsertTextMode != protocol.InsertTextModeAdjustIndentation {
				t.Error("a multi-line snippet must ask the client to indent its later lines")
			}
		})
	}
}

// One item per keyword: the shape replaces the bare keyword.
func TestCompletion_KeywordShapeReplacesTheKeyword(t *testing.T) {
	items := completeWith(t, true, "fn f(n: Int) {\n    i"+cursorMark+"\n}\n")
	n := 0
	for _, it := range items {
		if it.Label == "if" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one `if` item, got %d", n)
	}
	labelsBefore(t, items, "if", "if else")
}

// Without snippet support a keyword inserts itself, and `if else` is not
// offered.
func TestCompletion_KeywordShapesFallBackToTheKeyword(t *testing.T) {
	items := completeWith(t, false, "fn f(n: Int) {\n    i"+cursorMark+"\n}\n")
	if te := textEditOf(t, mustItem(t, items, "if")); te.NewText != "if" {
		t.Fatalf("if inserts %q, want the bare keyword", te.NewText)
	}
	labelsExclude(t, items, "if else")
	items = completeWith(t, false, cursorMark+"\n")
	if te := textEditOf(t, mustItem(t, items, "fn")); te.NewText != "fn" {
		t.Fatalf("fn inserts %q, want the bare keyword", te.NewText)
	}
}

// Where a multi-line shape cannot be written, the keyword is bare: a `//!`
// line, a string's interpolation, and a pipe stage (`|> if`, which takes
// the piped value).
func TestCompletion_KeywordShapesOnlyWhereTheyFit(t *testing.T) {
	for _, src := range []string{
		"//! x = i" + cursorMark + "\n//! assert x\npub fn double(n: Int): Int {\n    n * 2\n}\n",
		"fn f(n: Int): String {\n    \"${i" + cursorMark + "}\"\n}\n",
		"fn f(n: Int): Int {\n    n |> i" + cursorMark + "\n}\n",
	} {
		items := completeWith(t, true, src)
		if te := textEditOf(t, mustItem(t, items, "if")); te.NewText != "if" {
			t.Errorf("if inserts %q, want the bare keyword\n%s", te.NewText, src)
		}
		labelsExclude(t, items, "if else")
	}
}

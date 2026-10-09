package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A file cannot be re-exported, in any spelling: a plain file import, an
// aliased one, one inside an import block, or a brace list whose `self` is
// the file. The error sits at the `export` and names the file.
func TestFileReExportIsAParseError(t *testing.T) {
	cases := []struct {
		src, file, hint string
		line, col       int
	}{
		{"import leaf export", "leaf", "list the items to re-export, as in `import leaf.{name, Type} export`, or have importers import `leaf` directly", 1, 13},
		{"import leaf as lf export", "leaf", "or have importers import `leaf` directly", 1, 19},
		{"import {\n    std/io\n    lib/leaf export\n}", "leaf", "`import lib/leaf.{name, Type} export`", 3, 14},
		{"import std/io.{self, IOError} export", "io", "remove `self` to re-export only the listed items, or have importers import `std/io` directly", 1, 31},
		{"import std/io.{self} export", "io", "remove `self`", 1, 22},
	}
	for _, tc := range cases {
		err := parseError(t, tc.src)
		var pe ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("%q: error is %T, want ParseError", tc.src, err)
		}
		if want := "`" + tc.file + "` is a file, and a file cannot be re-exported"; pe.Message != want {
			t.Errorf("%q: message %q, want %q", tc.src, pe.Message, want)
		}
		if len(pe.Hints) != 1 || !strings.Contains(pe.Hints[0], tc.hint) {
			t.Errorf("%q: hints %q, want one containing %q", tc.src, pe.Hints, tc.hint)
		}
		if pe.Line != tc.line || pe.Col != tc.col {
			t.Errorf("%q: error at %d:%d, want %d:%d (the `export`)", tc.src, pe.Line, pe.Col, tc.line, tc.col)
		}
	}
}

// Re-exporting items stays valid: one item, a brace list, per-item `export`,
// and an owner type's `self`.
func TestItemReExportsStillParse(t *testing.T) {
	for _, src := range []string{
		"import add.Add export",
		"import std/comparable.{Comparable, Ordering} export",
		"import calc.{add export, helper, subtract export as minus}",
		"import bool.Bool.{self, False, True} export",
		"import {\n    leaf.{add, double} export\n    parser_lib.greet export\n}",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

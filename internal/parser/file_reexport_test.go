package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A file cannot be re-exported, in any spelling: a plain file import, an
// aliased one, one inside an import block, or a brace list whose `self` is
// the file. The error sits at the `pub` and names the file.
func TestFileReExportIsAParseError(t *testing.T) {
	cases := []struct {
		src, file, hint string
		line, col       int
	}{
		{"pub import leaf", "leaf", "list the items to re-export, as in `pub import leaf.{name, Type}`, or have importers import `leaf` directly", 1, 1},
		{"pub import leaf as lf", "leaf", "or have importers import `leaf` directly", 1, 1},
		{"pub import {\n    std/comparable.Ordering\n    lib/leaf\n}", "leaf", "`pub import lib/leaf.{name, Type}`", 1, 1},
		{"pub import std/io.{self, IOError}", "io", "remove `self` to re-export only the listed items, or have importers import `std/io` directly", 1, 1},
		{"pub import std/io.{self}", "io", "remove `self`", 1, 1},
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
			t.Errorf("%q: error at %d:%d, want %d:%d (the `pub`)", tc.src, pe.Line, pe.Col, tc.line, tc.col)
		}
	}
}

// `pub import` re-exports every item it selects: one item, a brace list, an
// aliased item, an owner type's `self`, and every entry of a block. A plain
// import re-exports nothing.
func TestPubImportMarksEveryEntry(t *testing.T) {
	cases := []struct {
		src     string
		entries int
		pub     bool
	}{
		{"pub import add.Add", 1, true},
		{"pub import std/comparable.{Comparable, Ordering}", 1, true},
		{"pub import calc.{add, subtract as minus}", 1, true},
		{"pub import bool.Bool.{self, False, True}", 1, true},
		{"pub import {\n    leaf.{add, double}\n    parser_lib.greet\n}", 2, true},
		{"import {\n    leaf.{add, double}\n    parser_lib.greet\n}", 2, false},
		{"import calc.{add, helper}", 1, false},
		{"pub import calc: add, Shape.{Circle}", 2, true},
	}
	for _, tc := range cases {
		nodes, err := Parse(lexer.Lex(tc.src))
		if err != nil {
			t.Errorf("%q: %v", tc.src, err)
			continue
		}
		if len(nodes) != 1 {
			t.Fatalf("%q: %d nodes, want 1", tc.src, len(nodes))
		}
		var entries []*ast.ImportStmt
		switch v := nodes[0].(type) {
		case *ast.ImportStmt:
			entries = []*ast.ImportStmt{v}
		case *ast.ImportBlock:
			if v.Pub != tc.pub {
				t.Errorf("%q: block Pub = %v, want %v", tc.src, v.Pub, tc.pub)
			}
			entries = v.Entries
		default:
			t.Fatalf("%q: node is %T", tc.src, nodes[0])
		}
		if len(entries) != tc.entries {
			t.Errorf("%q: %d entries, want %d", tc.src, len(entries), tc.entries)
		}
		for _, e := range entries {
			if e.Pub != tc.pub {
				t.Errorf("%q: entry %v Pub = %v, want %v", tc.src, e.ModulePath, e.Pub, tc.pub)
			}
		}
	}
}

// `pub` before `import` parses as a re-export, not a misplaced visibility
// keyword.
func TestPubBeforeImportParses(t *testing.T) {
	if _, err := Parse(lexer.Lex("pub import std/comparable.Ordering\nfn f(): Int { 1 }")); err != nil {
		t.Fatal(err)
	}
}

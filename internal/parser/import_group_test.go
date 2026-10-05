package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// Braces right after a `/` would group paths, which the language does not
// do: braces after a `.` select names from one file, and several files go in
// an import block. The error is at the `{`, and its hint spells the paths
// written as that block.
func TestGroupedImportPath_IsAnErrorAtTheBrace(t *testing.T) {
	const msg = "braces select names from one file, as in `std/regex.{Regex, Match}`"
	cases := []struct {
		src       string
		line, col int
		block     []string
	}{
		{"import std/{io, regex.Regex}\n", 1, 12, []string{"std/io", "std/regex.Regex"}},
		{"import http/{request as req, response}\n", 1, 13, []string{"http/request as req", "http/response"}},
		{"import std/{calendar.{Date, Months}, io}\n", 1, 12, []string{"std/calendar.{Date, Months}", "std/io"}},
		{"import a/b/{\n    c\n    d/e\n}\n", 1, 12, []string{"a/b/c", "a/b/d/e"}},
		{"import {\n    std/{io, iter}\n}\n", 2, 9, []string{"std/io", "std/iter"}},
		{"import std/{\n    calendar.{\n        Date\n        Months\n    }\n}\n", 1, 12, []string{"std/calendar.{Date, Months}"}},
		// An entry the hint cannot spell gets the fixed example.
		{"import std/{io, 1}\n", 1, 12, []string{"std/io", "std/regex.Regex"}},
	}
	for _, tc := range cases {
		_, errs := ParseWithRecovery(lexer.Lex(tc.src))
		if len(errs) == 0 {
			t.Errorf("%q parses", tc.src)
			continue
		}
		e := errs[0]
		hint := "to import several files, list each in an import block:\nimport {\n    " +
			strings.Join(tc.block, "\n    ") + "\n}"
		if e.Line != tc.line || e.Col != tc.col || e.Message != msg || len(e.Hints) != 1 || e.Hints[0] != hint {
			t.Errorf("%q: got %d:%d %q hints %q\nwant %d:%d %q hint %q", tc.src, e.Line, e.Col, e.Message, e.Hints, tc.line, tc.col, msg, hint)
		}
	}
}

// Braces after a `.` still select names.
func TestGroupedImportPath_SelectorsStayValid(t *testing.T) {
	for _, src := range []string{
		"import std/calendar.{Date, Months}\n",
		"import std/calendar.Disambiguation.{self, Earlier}\n",
		"import {\n    std/io\n    std/regex.Regex\n}\n",
	} {
		if _, errs := ParseWithRecovery(lexer.Lex(src)); len(errs) != 0 {
			t.Errorf("%q: %v", src, errs)
		}
	}
}

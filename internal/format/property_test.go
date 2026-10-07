package format

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// collectNomiFiles returns every .nomi file under tests/ and std/, walked
// recursively so the corpus's numbered category folders and their project
// subdirectories are all covered.
func collectNomiFiles(t *testing.T) []string {
	t.Helper()
	roots := []string{
		"../../tests",
		"../../std",
	}
	var files []string
	for _, r := range roots {
		err := filepath.WalkDir(r, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // a root may not exist (e.g. stdlib); skip it
			}
			if !d.IsDir() && strings.HasSuffix(path, ".nomi") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no tests or stdlib files found")
	}
	return files
}

// TestProperty_Idempotent verifies that applying Format twice produces the
// same result as applying it once. Runs on every .nomi file under
// tests/ and stdlib/.
func TestProperty_Idempotent(t *testing.T) {
	files := collectNomiFiles(t)
	for _, f := range files {
		f := f
		name := strings.TrimPrefix(f, "../")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			once, err := Format(string(src))
			if err != nil {
				t.Skipf("parse error (not a formatter bug): %v", err)
			}
			twice, err := Format(once)
			if err != nil {
				t.Fatalf("reformat parse error (formatter produced unparseable output!): %v\n--- once ---\n%s", err, once)
			}
			if once != twice {
				t.Errorf("non-idempotent.\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
		})
	}
}

// TestProperty_SemanticallyEquivalent verifies that Parse(Format(src)) is
// AST-equivalent to Parse(src) (modulo trivia, positions, and doc-comment
// text). The formatter must never change program meaning.
//
// Because Format applies import sorting and type-body item ordering, the
// comparison is done against the same canonicalized Parse(src) rather than
// Parse(src) directly. These are part of the formatter's canonical output, and
// ignoring them would produce false positives on files whose source order is
// accepted but not canonical.
func TestProperty_SemanticallyEquivalent(t *testing.T) {
	files := collectNomiFiles(t)
	for _, f := range files {
		f := f
		name := strings.TrimPrefix(f, "../")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parser.Parse(lexer.Lex(string(src))); err != nil {
				t.Skipf("parse error on source (not a formatter bug): %v", err)
			}
			formatted, err := Format(string(src))
			if err != nil {
				t.Fatalf("Format returned error: %v", err)
			}
			if err := SameMeaning(string(src), formatted); err != nil {
				t.Errorf("formatting changed program meaning: %v\n--- formatted source ---\n%s", err, formatted)
			}
		})
	}
}

// TestProperty_SemanticallyEquivalentShapes is TestProperty_SemanticallyEquivalent
// over SOURCES rather than corpus files, for shapes whose formatted spelling is
// ambiguous with a different construct.
//
// It exists because the corpus cannot reach these. The corpus is already
// formatted, so a shape the formatter mangles is committed in its mangled form
// and then round-trips perfectly: the input that triggers the bug is exactly the
// input no formatted file contains. That is how a one-field anonymous punned
// literal went unnoticed. `nomi fmt` re-emitted `{n: n}` as `{n}`, which
// re-parses as a BLOCK whose value is `n` — not a one-field anonymous struct —
// so the formatter changed program meaning, the strongest class of formatter
// bug. It had already broken a stdlib function in the shipped tree, and what
// found it was an attached `//!` test on that function, not any test in this
// package.
//
// The check is the ROUND TRIP, not the emitted text, deliberately. Pinning the
// output string would accept a future change that re-broke meaning while
// happening to keep the same spelling; an AST comparison cannot.
func TestProperty_SemanticallyEquivalentShapes(t *testing.T) {
	const preamble = `struct Box {
  n: Int
  label: String
}

fn probe(box: Box, n: Int, label: String): Box {
`
	for _, tc := range []struct {
		name string
		body string
	}{
		// The regression. One field, anonymous, punned: `{n}` is a block.
		{"one field anonymous punned", "  Struct.update(box, {n: n})\n"},
		// Two fields is unambiguous — a block has no comma-separated
		// statements — so punning must still be applied here.
		{"two fields anonymous punned", "  Struct.update(box, {n: n, label: label})\n"},
		// A type name disambiguates at any arity, including one.
		{"one field nominal punned", "  Box{n: n, label: label}\n"},
		// Not a pun, so never at risk; present so a fix that keyed on arity
		// alone rather than on punning would show up here.
		{"one field anonymous not punned", "  Struct.update(box, {n: 1})\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := preamble + tc.body + "}\n"
			if _, err := parser.Parse(lexer.Lex(src)); err != nil {
				t.Fatalf("source did not parse (fix the fixture, not the formatter): %v", err)
			}
			formatted, err := Format(src)
			if err != nil {
				t.Fatalf("Format returned error: %v", err)
			}
			if err := SameMeaning(src, formatted); err != nil {
				t.Errorf("formatting changed program meaning: %v\n--- source ---\n%s\n--- formatted ---\n%s", err, src, formatted)
			}
			twice, err := Format(formatted)
			if err != nil {
				t.Fatalf("reformat parse error (formatter produced unparseable output): %v\n--- formatted ---\n%s", err, formatted)
			}
			if twice != formatted {
				t.Errorf("non-idempotent.\n--- once ---\n%s\n--- twice ---\n%s", formatted, twice)
			}
		})
	}
}

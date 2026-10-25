package parser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A keyword declaration's tagged body is a typed literal's raw form, so the
// typed-literal injection (tree-sitter-nomi/queries/injections.scm, which
// Zed copies and Helix and Neovim generate from) highlights it in the
// language its tag names, qualified keyword or bare. A `message` body keeps
// its MessageFormat 2 injection.
func TestTreeSitterInjectsTaggedKeywordBodies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the tree-sitter CLI")
	}
	runCLI := treeSitterCLI(t)
	src := "import sqlkit\n\n" +
		"pub sqlkit.query! orders(user_id: Int) Sql`\n    SELECT id\n    FROM orders\n    `\n\n" +
		"query! names Sql`SELECT name FROM users`\n\n" +
		"message hi `Hi {$name}`\n"
	path := filepath.Join(t.TempDir(), "injections.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI("query", "queries/injections.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	for _, want := range []string{
		"injection.language, start: (2, 39), end: (2, 42), text: `Sql`",
		"injection.content, start: (2, 43), end: (5, 4)",
		"injection.language, start: (7, 13), end: (7, 16), text: `Sql`",
		"injection.content, start: (7, 17), end: (7, 39), text: `SELECT name FROM users`",
		"injection.content, start: (9, 12), end: (9, 22), text: `Hi {$name}`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the injection query does not capture %q:\n%s", want, out)
		}
	}

	// The head reads as a macro, `!` included, ahead of the plain keyword
	// rule (highlights.scm lists specific patterns first); the qualifier is
	// a module and `message` stays a keyword.
	out, err = runCLI("query", "queries/highlights.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	for _, want := range []string{
		"module, start: (2, 4), end: (2, 10), text: `sqlkit`",
		"function.macro, start: (2, 11), end: (2, 16), text: `query`",
		"function.macro, start: (2, 16), end: (2, 17), text: `!`",
		"function.macro, start: (7, 0), end: (7, 5), text: `query`",
		"function.macro, start: (7, 5), end: (7, 6), text: `!`",
		"keyword, start: (9, 0), end: (9, 7), text: `message`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the highlight query does not capture %q:\n%s", want, out)
		}
	}
}

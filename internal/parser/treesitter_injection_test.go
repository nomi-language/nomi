package parser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A `phrase` body is MessageFormat 2, which the injection query
// (tree-sitter-nomi/queries/injections.scm, which Zed copies and Helix and
// Neovim generate from) hands to the editor's `mf2` grammar, and its
// keyword highlights as a keyword.
func TestTreeSitterInjectsPhraseBodies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the tree-sitter CLI")
	}
	runCLI := treeSitterCLI(t)
	src := "phrase hi `Hi {$name}`\n"
	path := filepath.Join(t.TempDir(), "injections.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI("query", "queries/injections.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	if want := "injection.content, start: (0, 11), end: (0, 21), text: `Hi {$name}`"; !strings.Contains(out, want) {
		t.Errorf("the injection query does not capture %q:\n%s", want, out)
	}
	out, err = runCLI("query", "queries/highlights.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	if want := "keyword, start: (0, 0), end: (0, 6), text: `phrase`"; !strings.Contains(out, want) {
		t.Errorf("the highlight query does not capture %q:\n%s", want, out)
	}
}

// An `Html` literal's text is handed to the editor's `html` grammar, in
// each form, by the static rule ahead of the generic dynamic one, which
// would capture the tag itself as the language's name.
func TestTreeSitterInjectsHtmlLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the tree-sitter CLI")
	}
	runCLI := treeSitterCLI(t)
	src := "fn f(n: Int) -> Html {\n    Html\"<b class='x'>${n}</b>\"\n}\n\nfn g() -> Html {\n    Html\"\"\"<i>ok</i>\"\"\"\n}\n"
	path := filepath.Join(t.TempDir(), "html.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI("query", "queries/injections.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	for _, want := range []string{"text: `<b class='x'>`", "text: `<i>ok</i>`"} {
		if !strings.Contains(out, want) {
			t.Errorf("the injection query does not capture %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "injection.language") {
		t.Errorf("an Html literal's tag is captured as a language name, not the static `html`:\n%s", out)
	}
}

// A tagged literal with `${...}` slots is one injected document: one match
// of the injection query captures every body chunk around the slots, and
// the literal as Zed's `@injection.host`. A match per chunk would parse
// each chunk alone, so the HTML after a slot would start mid-attribute.
func TestTreeSitterInjectsASlottedLiteralAsOneDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the tree-sitter CLI")
	}
	runCLI := treeSitterCLI(t)
	src := "fn menu(avatar: String, initial: String) -> Html {\n" +
		"    Html\"\"\"\n" +
		"    <span class=\"a\">\n" +
		"      <img src=\"${avatar}\" alt=\"\" class=\"b\">\n" +
		"      <span class=\"c\">${initial}</span>\n" +
		"    </span>\n" +
		"    \"\"\"\n" +
		"}\n\n" +
		"fn row(id: Int, name: String) -> Sql {\n" +
		"    Sql\"SELECT * FROM users WHERE id = ${id} AND name = ${name}\"\n" +
		"}\n"
	path := filepath.Join(t.TempDir(), "slots.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI("query", "queries/injections.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	// The CLI prints each match as a `pattern:` line and its captures.
	matches := strings.Split(out, "  pattern: ")[1:]
	cases := []struct {
		name   string
		chunks []string
	}{
		{"Html", []string{"start: (1, 11), end: (3, 16)", "start: (3, 25), end: (4, 22)", "start: (4, 32), end: (6, 4)"}},
		{"Sql", []string{"text: `SELECT * FROM users WHERE id = `", "text: ` AND name = `"}},
	}
	for _, c := range cases {
		var found []string
		for _, m := range matches {
			if strings.Contains(m, c.chunks[0]) {
				found = append(found, m)
			}
		}
		if len(found) != 1 {
			t.Errorf("%s: %d matches capture the first chunk, want 1:\n%s", c.name, len(found), out)
			continue
		}
		m := found[0]
		if got := strings.Count(m, "injection.content"); got != len(c.chunks) {
			t.Errorf("%s: the match captures %d content nodes, want %d:\n%s", c.name, got, len(c.chunks), m)
		}
		for _, chunk := range c.chunks {
			if !strings.Contains(m, chunk) {
				t.Errorf("%s: the match does not capture %q:\n%s", c.name, chunk, m)
			}
		}
		if !strings.Contains(m, "injection.host") {
			t.Errorf("%s: the match does not capture the literal as @injection.host:\n%s", c.name, m)
		}
	}
}

package parser_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A `message` body is MessageFormat 2, which the injection query
// (tree-sitter-nomi/queries/injections.scm, which Zed copies and Helix and
// Neovim generate from) hands to the editor's `mf2` grammar, and its
// keyword highlights as a keyword.
func TestTreeSitterInjectsMessageBodies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the tree-sitter CLI")
	}
	runCLI := treeSitterCLI(t)
	src := "message hi `Hi {$name}`\n"
	path := filepath.Join(t.TempDir(), "injections.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI("query", "queries/injections.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	if want := "injection.content, start: (0, 12), end: (0, 22), text: `Hi {$name}`"; !strings.Contains(out, want) {
		t.Errorf("the injection query does not capture %q:\n%s", want, out)
	}
	out, err = runCLI("query", "queries/highlights.scm", path)
	if err != nil {
		t.Fatalf("tree-sitter query: %v\n%s", err, out)
	}
	if want := "keyword, start: (0, 0), end: (0, 7), text: `message`"; !strings.Contains(out, want) {
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

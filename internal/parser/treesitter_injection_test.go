package parser_test

import (
	"fmt"
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

// Each std tag with an embedded language hands its literal's text, in each
// form, to the editor's lowercase grammar name, set by a static rule: Helix
// matches a captured `Sql` against no language, and searches its injection
// regexes inside a tag such as `Url` (R) or `Cron` (RON). A tag with no
// embedded language injects nothing.
func TestTreeSitterInjectsTypedLiteralsByGrammarName(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the tree-sitter CLI")
	}
	runCLI := treeSitterCLI(t)
	query, err := os.ReadFile(filepath.Join("..", "..", "tree-sitter-nomi", "queries", "injections.scm"))
	if err != nil {
		t.Fatal(err)
	}
	languages := injectionPatternLanguages(string(query))
	cases := []struct{ tag, body, lang string }{
		{"Sql", "SELECT 1", "sql"},
		{"Html", "<b>x</b>", "html"},
		{"Toml", "a = 1", "toml"},
		{"Regex", "a+b", "regex"},
		{"Url", "https://x.test/", ""},
		{"Cron", "0 * * * *", ""},
		{"Locale", "en-US", ""},
		{"Route", "/users", ""},
	}
	for _, c := range cases {
		forms := []struct{ name, src string }{
			{"string", c.tag + "\"" + c.body + "\""},
			{"triple", c.tag + "\"\"\"" + c.body + "\"\"\""},
			{"raw", c.tag + "`" + c.body + "`"},
		}
		for _, f := range forms {
			path := filepath.Join(t.TempDir(), "lit.nomi")
			if err := os.WriteFile(path, []byte("fn f()\n    x = "+f.src+"\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := runCLI("query", "queries/injections.scm", path)
			if err != nil {
				t.Fatalf("tree-sitter query: %v\n%s", err, out)
			}
			matches := strings.Split(out, "  pattern: ")[1:]
			if c.lang == "" {
				if len(matches) != 0 {
					t.Errorf("%s %s: a tag with no embedded language is injected:\n%s", c.tag, f.name, out)
				}
				continue
			}
			if len(matches) != 1 {
				t.Errorf("%s %s: %d matches, want 1:\n%s", c.tag, f.name, len(matches), out)
				continue
			}
			m := matches[0]
			if want := "text: `" + c.body + "`"; !strings.Contains(m, want) {
				t.Errorf("%s %s: the match does not capture %q:\n%s", c.tag, f.name, want, m)
			}
			if strings.Contains(m, "injection.language") {
				t.Errorf("%s %s: the tag is captured as the language name:\n%s", c.tag, f.name, m)
			}
			var ix int
			if _, err := fmt.Sscanf(m, "%d", &ix); err != nil || ix >= len(languages) {
				t.Fatalf("%s %s: no pattern index in %q", c.tag, f.name, m)
			}
			if languages[ix] != c.lang {
				t.Errorf("%s %s: pattern %d sets injection.language %q, want %q", c.tag, f.name, ix, languages[ix], c.lang)
			}
		}
	}
}

// injectionPatternLanguages returns, per top-level pattern of an injection
// query, the language its `#set! injection.language` names, or "".
func injectionPatternLanguages(query string) []string {
	var langs []string
	depth, start := 0, 0
	inString := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch {
		case inString:
			if ch == '\\' {
				i++
			} else if ch == '"' {
				inString = false
			}
		case ch == ';':
			for i < len(query) && query[i] != '\n' {
				i++
			}
		case ch == '"':
			inString = true
		case ch == '(':
			if depth == 0 {
				start = i
			}
			depth++
		case ch == ')':
			depth--
			if depth == 0 {
				lang := ""
				pattern := query[start : i+1]
				if j := strings.Index(pattern, `injection.language "`); j >= 0 {
					rest := pattern[j+len(`injection.language "`):]
					lang = rest[:strings.IndexByte(rest, '"')]
				}
				langs = append(langs, lang)
			}
		}
	}
	return langs
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
	src := "fn menu(avatar: String, initial: String) -> Html\n" +
		"    Html\"\"\"\n" +
		"    <span class=\"a\">\n" +
		"      <img src=\"${avatar}\" alt=\"\" class=\"b\">\n" +
		"      <span class=\"c\">${initial}</span>\n" +
		"    </span>\n" +
		"    \"\"\"\n" +
		"}\n\n" +
		"fn row(id: Int, name: String) -> Sql\n" +
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

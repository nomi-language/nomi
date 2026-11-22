package docscheck

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/doctest"
)

// The site's block renderer, which gives each fence language its form.
const fenceRenderer = "tour/src/lib/rehype-shiki.mjs"

// githubOnlyFenceLanguages are the fence languages a file the site never
// renders may use beyond the renderer's, each with its reason. GitHub
// renders these files, and highlights these languages.
var githubOnlyFenceLanguages = map[string]map[string]string{
	"editors/nvim/README.md":   {"lua": "Neovim's configuration is Lua"},
	"tests/exercism/README.md": {"markdown": "it shows the head of an exercise's NOTES.md"},
}

// TestEveryFenceHasALanguageTheSiteRenders fails for a fenced block in an
// authored Markdown file, or in a std `///` or `//#` doc comment, that has
// no info string, or whose language rehype-shiki.mjs gives no form: such a
// block renders as unstyled text on the site, without the highlighting,
// file tree or terminal form its kind has. The known languages are read
// from the renderer itself: the keys of its SHIKI_LANGS table and each
// `language-<name>` class it tests for. A new form is added there, and a
// file only GitHub renders may use a language listed in
// githubOnlyFenceLanguages. A fence other than ```nomi-run carries no words
// after its language, since the renderer reads none; a ```nomi-run fence
// carries only the words doctest.InfoWords names.
//
// The pages generated from these sources (the cheat sheet's page, the
// examples, the spec, style, tooling and reference pages) are not read.
func TestEveryFenceHasALanguageTheSiteRenders(t *testing.T) {
	root, files := trackedFilesMatching(t, "*.md", "std/*.nomi", "std/**/*.nomi")
	known := rendererFenceLanguages(t, root)
	checked := 0
	for _, rel := range files {
		if generatedMarkdown(rel) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		markdown := string(src)
		if strings.HasSuffix(rel, ".nomi") {
			markdown = doctest.DocCommentMarkdown(markdown)
		}
		for _, b := range doctest.Fences(markdown) {
			checked++
			switch {
			case b.Lang == "":
				t.Errorf("%s:%d: a fence with no language; give it the one its block is (%s names the forms)", rel, b.Line, fenceRenderer)
			case !known[b.Lang] && githubOnlyFenceLanguages[rel][b.Lang] == "":
				t.Errorf("%s:%d: ```%s is not a fence language %s renders; use one it does, or add its form there", rel, b.Line, b.Lang, fenceRenderer)
			case b.Lang == "nomi-run" && len(b.UnknownInfo()) > 0:
				t.Errorf("%s:%d: ```nomi-run carries %q, which is none of %s", rel, b.Line, b.UnknownInfo(), strings.Join(doctest.InfoWords(), ", "))
			case b.Lang != "nomi-run" && len(b.Info) > 0:
				t.Errorf("%s:%d: ```%s carries %q, which nothing reads; only ```nomi-run takes words after its language", rel, b.Line, b.Lang, b.Info)
			}
		}
	}
	if checked < 1000 {
		t.Errorf("checked %d fences; the scan is not reading the repository", checked)
	}
	t.Logf("checked %d fences against the languages %s renders", checked, fenceRenderer)
}

// generatedMarkdown reports whether a tracked Markdown file is written by
// a generator from a source this test reads: the cheat sheet's page (from
// docs/cheatsheet.md), the examples pages (from tests/exercism), and test
// fixtures.
func generatedMarkdown(rel string) bool {
	return rel == "tour/src/content/docs/cheat-sheet.md" ||
		strings.HasPrefix(rel, "tour/src/content/docs/examples/") ||
		strings.HasPrefix(rel, "testdata/") || strings.Contains(rel, "/testdata/")
}

var (
	shikiTable    = regexp.MustCompile(`(?s)const SHIKI_LANGS = \{(.*?)\n\};`)
	shikiKey      = regexp.MustCompile(`(?m)^\s*([a-z][a-z0-9-]*):`)
	languageClass = regexp.MustCompile(`hasClass\(code, "language-([a-z][a-z0-9-]*)"\)`)
)

// rendererFenceLanguages answers the fence languages rehype-shiki.mjs gives
// a form: its SHIKI_LANGS keys and the `language-<name>` classes it tests.
func rendererFenceLanguages(t *testing.T, root string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, fenceRenderer))
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	table := shikiTable.FindSubmatch(src)
	if table == nil {
		t.Fatalf("%s: no SHIKI_LANGS table; update rendererFenceLanguages to read the renderer", fenceRenderer)
	}
	for _, m := range shikiKey.FindAllSubmatch(table[1], -1) {
		known[string(m[1])] = true
	}
	for _, m := range languageClass.FindAllSubmatch(src, -1) {
		known[string(m[1])] = true
	}
	for _, want := range []string{"nomi", "nomi-run", "nomi-solution", "console", "tree", "gomod", "text", "toml", "go", "sh"} {
		if !known[want] {
			names := make([]string, 0, len(known))
			for k := range known {
				names = append(names, k)
			}
			slices.Sort(names)
			t.Fatalf("%s: read the languages %v, without %q; update rendererFenceLanguages to read the renderer", fenceRenderer, names, want)
		}
	}
	return known
}

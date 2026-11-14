package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/format"
)

// pluralActions are the "Add ... plural forms" actions at line (0-based)
// of the document at uri.
func pluralActions(t *testing.T, s *Server, uri string, line uint32) []protocol.CodeAction {
	t.Helper()
	pos := protocol.Position{Line: line, Character: 0}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Range:        protocol.Range{Start: pos, End: pos},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := res.([]protocol.CodeAction)
	var out []protocol.CodeAction
	for _, a := range actions {
		if strings.HasPrefix(a.Title, "Add ") && strings.Contains(a.Title, "plural forms") {
			if a.Kind == nil || *a.Kind != protocol.CodeActionKindRefactorRewrite {
				t.Errorf("%q is not a refactor.rewrite", a.Title)
			}
			out = append(out, a)
		}
	}
	return out
}

func applyOnly(t *testing.T, content, uri string, a protocol.CodeAction) string {
	t.Helper()
	edits := a.Edit.Changes[protocol.DocumentUri(uri)]
	if len(edits) != 1 {
		t.Fatalf("%q has %d edits, want 1", a.Title, len(edits))
	}
	return applyTextEdit(content, edits[0])
}

// "Add plural forms" rewrites a phrase or noun with no `.match` to select on
// an Int parameter, with the source locale's categories, as `nomi fmt`
// writes it, and the result checks.
func TestCodeAction_AddPluralForms(t *testing.T) {
	english := "[module]\nname = \"shop\"\n"
	polish := "[module]\nname = \"shop\"\n\n[i18n]\nsource_locale = \"pl\"\nlocales = [\"pl\"]\n"
	for _, c := range []struct {
		name, manifest, src string
		line                uint32
		titles              []string
		want                string
	}{
		{"english", english, "/// The cart.\npub phrase cart_items(count: Int, who: String) `{$who} has {$count} items`\n", 1,
			[]string{"Add plural forms for `$count`"},
			"/// The cart.\npub phrase cart_items(count: Int, who: String) `\n    .input {$count :integer}\n    .match $count\n    one {{{$who} has {$count} items}}\n    * {{{$who} has {$count} items}}\n    `\n"},
		{"polish", polish, "pub phrase rzeczy(count: Int) `{$count} rzeczy`\n", 0,
			[]string{"Add plural forms for `$count`"},
			"pub phrase rzeczy(count: Int) `\n    .input {$count :integer}\n    .match $count\n    one {{{$count} rzeczy}}\n    few {{{$count} rzeczy}}\n    many {{{$count} rzeczy}}\n    * {{{$count} rzeczy}}\n    `\n"},
		{"a noun", english, "pub noun book `book`\n", 0,
			[]string{"Add plural forms for `$count`"},
			"pub noun book `\n    .input {$count :integer}\n    .match $count\n    one {{book}}\n    * {{book}}\n    `\n"},
		{"a multi-line body keeps its input", english, "pub phrase n(count: Int) `\n    .input {$count :number}\n    {{{$count} left}}\n    `\n", 2,
			[]string{"Add plural forms for `$count`"},
			"pub phrase n(count: Int) `\n    .input {$count :number}\n    .match $count\n    one {{{$count} left}}\n    * {{{$count} left}}\n    `\n"},
		{"two Int parameters", english, "pub phrase two(a: Int, b: Int) `{$a} and {$b}`\n", 0,
			[]string{"Add plural forms for `$a`", "Add plural forms for `$b`"}, ""},
		{"already matched", english, "pub phrase m(count: Int) `.input {$count :integer} .match $count one {{one}} * {{many}}`\n", 0, nil, ""},
		{"no Int parameter", english, "pub phrase hi(name: String) `Hi, {$name}`\n", 0, nil, ""},
		{"off the declaration", english, "pub phrase cart(count: Int) `{$count} items`\n\nfn other() -> Int\n    1\nend\n", 2, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, uri := openProject(t, map[string]string{"nomi.toml": c.manifest, "shop.nomi": c.src}, "shop.nomi")
			actions := pluralActions(t, s, uri, c.line)
			var titles []string
			for _, a := range actions {
				titles = append(titles, a.Title)
			}
			if strings.Join(titles, "|") != strings.Join(c.titles, "|") {
				t.Fatalf("actions %q, want %q", titles, c.titles)
			}
			if c.want == "" {
				return
			}
			got := applyOnly(t, c.src, uri, actions[0])
			if got != c.want {
				t.Fatalf("result:\n%s\nwant:\n%s", got, c.want)
			}
			if formatted, err := format.Format(got); err != nil || formatted != got {
				t.Errorf("nomi fmt changes the result (%v):\n%s", err, formatted)
			}
			s2, uri2 := openProject(t, map[string]string{"nomi.toml": c.manifest, "shop.nomi": got}, "shop.nomi")
			if errs := s2.docs.Snapshot(uri2).Analysis.TypeErrors; len(errs) > 0 {
				t.Errorf("the result does not check: %v", errs)
			}
		})
	}
}

// In a catalog it rewrites the translation under the cursor with the
// catalog locale's categories.
func TestCodeAction_AddPluralFormsInACatalog(t *testing.T) {
	files := map[string]string{
		"nomi.toml":            "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n",
		"main.nomi":            "import\n    std::io\n    cart\nend\n\nfn main()\n    io.print(Phrase.format(cart.items(3), Locale\"pl\"))\nend\n",
		"cart.nomi":            "pub phrase items(count: Int) `{$count} items`\n",
		"locales/pl/cart.toml": "[items]\nsource = '''{$count} items'''\ntext = '''{$count} rzeczy'''\n",
	}
	root := writeProject(t, files)
	s, _ := openProjectAt(t, root, "cart.nomi")
	catPath := filepath.Join(root, "locales", "pl", "cart.toml")
	catURI := pathToURI(catPath)
	actions := pluralActions(t, s, catURI, 2)
	if len(actions) != 1 || actions[0].Title != "Add Polish plural forms for `$count`" {
		t.Fatalf("actions %+v, want the Polish one", actions)
	}
	got := applyOnly(t, files["locales/pl/cart.toml"], catURI, actions[0])
	want := "[items]\nsource = '''{$count} items'''\ntext = '''\n.input {$count :integer}\n.match $count\none {{{$count} rzeczy}}\nfew {{{$count} rzeczy}}\nmany {{{$count} rzeczy}}\n* {{{$count} rzeczy}}\n'''\n"
	if got != want {
		t.Fatalf("result:\n%s\nwant:\n%s", got, want)
	}
	if len(pluralActions(t, s, catURI, 1)) != 0 {
		t.Errorf("offered on the `source` line")
	}
	if err := os.WriteFile(catPath, []byte(got), 0o644); err != nil {
		t.Fatal(err)
	}
	requireClean(t, root)
}

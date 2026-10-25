package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nounsProject is a project with nouns and the messages that agree with
// them in one file, a noun of another file that a message must also agree
// with, and their Polish catalogs.
func nounsProject(tb testing.TB) incrementalProject {
	dir := tb.TempDir()
	book := ".input {$count :integer} .match $count one {{book}} * {{books}}"
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n",
		"locales/pl/shop.toml": "[book]\nsource = '''" + book + "'''\ngender = \"feminine\"\n" +
			"nominative = '''.input {$count :integer} .match $count one {{książka}} few {{książki}} many {{książek}} * {{książki}}'''\n" +
			"genitive = '''.input {$count :integer} .match $count one {{książki}} few {{książek}} many {{książek}} * {{książki}}'''\n\n" +
			"[has_shipped]\nsource = '''Your {$product :nomi:noun} has shipped.'''\n" +
			"text = '''.input {$product :nomi:gender} .match $product feminine {{Twoja {$product :nomi:noun} została wysłana.}} inanimate {{Twój {$product :nomi:noun} został wysłany.}} * {{Twoja przesyłka została wysłana.}}'''\n\n" +
			"[sold_out]\nsource = '''No {$product :nomi:noun count=2} left.'''\ntext = '''Brak {$product :nomi:noun case=genitive count=5}.'''\n",
		"locales/pl/stock.toml": "[laptop]\nsource = '''laptop'''\ngender = \"inanimate\"\nnominative = '''laptop'''\ngenitive = '''laptopa'''\n",
	}
	p := incrementalProject{name: "nouns", entry: "main", dir: dir, texts: map[string]string{
		"main":  "import {\n    std/io\n    std/locale.Locale\n    std/message.Message\n    shop\n    stock\n}\n\nfn main() {\n    io.print(Message.format(shop.has_shipped(stock.laptop), Locale`pl`))\n}\n",
		"shop":  "import std/message.Noun\n\npub noun book `" + book + "`\n\npub message has_shipped(product: Noun) `Your {$product :nomi:noun} has shipped.`\n\npub message sold_out(product: Noun) `No {$product :nomi:noun count=2} left.`\n",
		"stock": "pub noun laptop `laptop`\n",
	}}
	for key, text := range p.texts {
		files[key+".nomi"] = text
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			tb.Fatal(err)
		}
		if strings.HasSuffix(name, ".toml") && name != "nomi.toml" {
			p.texts[name] = text
		}
	}
	return p
}

// nounsEdits edit a noun's catalog entry (its gender, a form a message asks
// for) in its own file's catalog and in another file's, a message's
// selector, and a noun's text. Each catalog edit keeps every signature, so
// it rebuilds the reading file's bodies alone, and the agreement check,
// which reads every file, answers as a full check does.
var nounsEdits = []editStep{
	replace("catalog body only: another file's noun becomes personal", "locales/pl/stock.toml", `"inanimate"`, `"personal"`),
	restore("catalog body only: back", "locales/pl/stock.toml"),
	replace("catalog body only: a form a message asks for removed", "locales/pl/stock.toml", "genitive = '''laptopa'''\n", ""),
	restore("catalog body only: the form back", "locales/pl/stock.toml"),
	replace("catalog body only: a gender arm removed", "locales/pl/shop.toml", " inanimate {{Twój {$product :nomi:noun} został wysłany.}}", ""),
	restore("catalog body only: the arm back", "locales/pl/shop.toml"),
	replace("catalog body only: a noun's gender", "locales/pl/shop.toml", `"feminine"`, `"neuter"`),
	restore("catalog body only: its gender back", "locales/pl/shop.toml"),
	replace("noun body only: its text, now stale", "stock", "`laptop`", "`notebook`"),
	restore("noun body only: back", "stock"),
}

func TestIncrementalMatchesFullWithNouns(t *testing.T) {
	p := nounsProject(t)
	if errs := fullCheck(p, p.texts).Errors; len(errs["main"])+len(errs["shop"])+len(errs["stock"]) > 0 {
		t.Fatalf("the project does not check: %v", errs)
	}
	runChecked(t, p, nounsEdits, newManager().RecheckWithOutcome)
}

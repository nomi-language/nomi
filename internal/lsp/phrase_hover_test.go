package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Hover inside the MessageFormat 2 of a `phrase` or `noun` body, and of a
// catalog entry's strings.

const coinsNomi = "" +
	"pub noun quarter `\n" +
	"    .input {$count :integer}\n" +
	"    .match $count\n" +
	"    one {{quarter}}\n" +
	"    * {{quarters}}\n" +
	"    `\n\n" +
	"/// One kind of coin and how many, such as \"3 quarters\".\n" +
	"pub phrase coin_count(\n" +
	"    coin: Noun,\n" +
	"    // How many coins.\n" +
	"    count: Int,\n" +
	") `{$count} {$coin :nomi:noun count=$count}`\n\n" +
	"pub phrase items(count: Int) `\n" +
	"    .local $n = {$count :integer}\n" +
	"    .match $n\n" +
	"    one {{{$n} item}}\n" +
	"    * {{{$n} items}}\n" +
	"    `\n\n" +
	"pub phrase shipped(product: Noun) `Your {$product :nomi:noun} has shipped.`\n\n" +
	"pub phrase left(count: Int) `.input {$count :integer} .match $count 0 {{none left}} one {{one left}} * {{{$count} left}}`\n"

const coinsPolish = "" +
	"[quarter]\n" +
	"source = '''.input {$count :integer} .match $count one {{quarter}} * {{quarters}}'''\n" +
	"gender = \"feminine\"\n" +
	"nominative = '''.input {$count :integer} .match $count one {{cwierc}} few {{cwierci}} many {{cwierci}} * {{cwierci}}'''\n\n" +
	"[coin_count]\n" +
	"source = '''{$count} {$coin :nomi:noun count=$count}'''\n" +
	"text = '''{$count} {$coin :nomi:noun case=genitive count=$count}'''\n\n" +
	"[items]\n" +
	"source = '''.local $n = {$count :integer} .match $n one {{{$n} item}} * {{{$n} items}}'''\n" +
	"text = '''\n.local $n = {$count :integer}\n.match $n\none {{{$n} rzecz}}\nfew {{{$n} rzeczy}}\nmany {{{$n} rzeczy}}\n* {{{$n} rzeczy}}\n'''\n\n" +
	"[shipped]\n" +
	"source = '''Your {$product :nomi:noun} has shipped.'''\n" +
	"text = '''\n.input {$product :nomi:gender}\n.match $product\nfeminine {{Twoja {$product :nomi:noun} wyslana.}}\n* {{Wyslano {$product :nomi:noun}.}}\n'''\n"

func coinsProject() map[string]string {
	return map[string]string{
		"nomi.toml":             "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n",
		"main.nomi":             "import\n    std::io\n    coins\nend\n\nfn main()\n    io.print(Phrase.format(coins.coin_count(coins.quarter, 3), Locale\"pl\"))\nend\n",
		"coins.nomi":            coinsNomi,
		"locales/pl/coins.toml": coinsPolish,
	}
}

type mf2HoverCase struct {
	name string
	// at is the text the hover is over, found after the first after.
	after, at string
	want      []string
}

func runMF2HoverCases(t *testing.T, uri, text string, s *Server, cases []mf2HoverCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from := strings.Index(text, c.after)
			if from < 0 {
				t.Fatalf("no %q in the text", c.after)
			}
			i := strings.Index(text[from:], c.at)
			if i < 0 {
				t.Fatalf("no %q after %q", c.at, c.after)
			}
			got := phraseHoverText(t, s, uri, offsetPosition(text, from+i+1))
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("the hover lacks %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestPhraseHover_InASourceBody(t *testing.T) {
	s, uri := openProject(t, coinsProject(), "coins.nomi")
	runMF2HoverCases(t, uri, coinsNomi, s, []mf2HoverCase{
		{"a phrase's parameter", "pub phrase coin_count", "$coin", []string{"```nomi\ncoin: Noun\n```", "A parameter of `coin_count`."}},
		{"a parameter's comment", ") `", "$count", []string{"```nomi\ncount: Int\n```", "How many coins."}},
		{"a noun's count", "pub noun quarter", "$count", []string{"```nomi\ncount: Int\n```",
			"How many: a phrase that writes `quarter` passes it with `count=` on `:nomi:noun`, as in `{$coin :nomi:noun count=$n}`, and 1 when it names none.",
			"Annotated by `.input {$count :integer}`."}},
		{"a local", ".match $n", "$n", []string{"```\n.local $n = {$count :integer}\n```\n\nA variable this text declares."}},
		{"a standard function", "pub noun quarter", ":integer", []string{"```\n:integer\n```", "Writes a number as a whole number",
			"Takes `Int`.", "Under `.match` it selects by the number's plural category in the locale", "- `select`: How the value selects"}},
		{"a function of Nomi's", "pub phrase coin_count", ":nomi:noun", []string{"```\n:nomi:noun\n```",
			"`:nomi:noun` is one of Nomi's own functions, in the `nomi` namespace: MessageFormat 2 reserves names without a namespace for its standard functions.",
			"It selects nothing under `.match`.", "- `case`: The grammatical case", "- `count`: How many of the noun"}},
		{"an option", "pub phrase coin_count", "count=", []string{"`count`, an option of `:nomi:noun`\n\nHow many of the noun"}},
		{"a plural key", "pub noun quarter", "one", []string{"`one` is a plural category in English (`en`): `$count` takes this arm for 1."}},
		{"the catch-all", "pub noun quarter", "* {{", []string{"`*` is the catch-all key: the arm `$count` takes when no other key matches it.",
			"In English (`en`) it covers `other` (0, 2, 3, 4, …)."}},
		{"an exact key", "pub phrase left", "0 {{", []string{"`0` matches `$count` exactly when it is 0, ahead of its plural category."}},
		{"the catch-all beside an exact key", "pub phrase left", "* {{", []string{"In English (`en`) it covers `other` (0, 2, 3, 4, …)."}},
		{"a keyword", "pub noun quarter", ".match", []string{"`.match $name ...` picks one variant"}},
		{"a keyword .local", "pub phrase items", ".local", []string{"`.local $name = {expression}`"}},
	})
	// Text a pattern writes has no hover of its own.
	if h := phraseBodyHoverAt(t, s, uri, coinsNomi, "quarters"); h != "" {
		t.Errorf("text has a hover: %s", h)
	}
}

func phraseBodyHoverAt(t *testing.T, s *Server, uri, text, at string) string {
	t.Helper()
	h, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: offsetPosition(text, strings.Index(text, at)+2),
	}})
	if err != nil || h == nil {
		return ""
	}
	return h.Contents.(protocol.MarkupContent).Value
}

func TestPhraseHover_InACatalog(t *testing.T) {
	s, uri := openProject(t, coinsProject(), "coins.nomi")
	root := strings.TrimSuffix(uriToPath(uri), "coins.nomi")
	catURI := pathToURI(filepath.Join(root, "locales", "pl", "coins.toml"))
	runMF2HoverCases(t, catURI, coinsPolish, s, []mf2HoverCase{
		{"a noun form's count", "nominative = ", "$count", []string{"a phrase that writes `quarter` passes it with `count=`"}},
		{"a Polish plural key", "[items]\nsource", "few", []string{"`few` is a plural category in Polish (`pl`): `$n` takes this arm for 2–4, 22–24, 32–34, …"}},
		{"a Polish key in a noun's form", "nominative = ", "many", []string{"`many` is a plural category in Polish (`pl`): `$count` takes this arm for 0, 5–21, 25–31, …"}},
		{"the catch-all with every arm", "few {{{$n}", "* {{", []string{"Every plural category of Polish (`pl`) that a whole number is in has an arm here, so `*` covers none."}},
		{"the source text's key is English", "[items]\nsource", "one", []string{"`one` is a plural category in English (`en`)"}},
		{"the case option lists the cases", "text = '''{$count}", "case=", []string{"`case`, an option of `:nomi:noun`", "The cases of Polish: `nominative`, `genitive`"}},
		{"a phrase's parameter", "text = '''{$count}", "$coin", []string{"```nomi\ncoin: Noun\n```"}},
		{"a gender key", "[shipped]", "feminine", []string{"`feminine` is a gender of Polish nouns: the arm `$product` takes when its noun is feminine in this locale."}},
		{"a gender selector's catch-all", "[shipped]", "* {{", []string{"It covers `animate`, `inanimate`, `personal`, `neuter`."}},
		{"a gender function", "[shipped]", ":nomi:gender", []string{"Selects by a `Noun`'s grammatical gender", "Under `.match` it selects by the noun's gender in the locale"}},
	})
	// Outside the MF2's tokens, the entry's own hover.
	if got := phraseHoverText(t, s, catURI, offsetPosition(coinsPolish, strings.Index(coinsPolish, "[coin_count]")+2)); !strings.Contains(got, "phrase coin_count(coin: Noun, count: Int)") {
		t.Errorf("the entry's hover is gone:\n%s", got)
	}
}

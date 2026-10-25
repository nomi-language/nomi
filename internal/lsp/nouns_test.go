package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Nouns in the editor: hover on a noun, completion of a noun entry's keys,
// its gender and the cases after `case=`, the gender keys of a selector,
// go to implementations, and the quick fixes that add a missing gender arm
// and a missing form.

const orderNouns = "import std/message.Noun\n\n" +
	"/// A book, a product category.\n" +
	"pub noun book `.input {$count :integer} .match $count one {{book}} * {{books}}`\n\n" +
	"pub noun laptop `.input {$count :integer} .match $count one {{laptop}} * {{laptops}}`\n\n" +
	"pub noun phone_case `phone case`\n\n" +
	"pub message has_shipped(product: Noun) `Your {$product :nomi:noun} has shipped.`\n\n" +
	"pub message sold_out(product: Noun) `No {$product :nomi:noun count=2} left.`\n"

const polishOrder = "[book]\nsource = '''.input {$count :integer} .match $count one {{book}} * {{books}}'''\ngender = \"feminine\"\n" +
	"nominative = '''.input {$count :integer} .match $count one {{książka}} few {{książki}} many {{książek}} * {{książki}}'''\n" +
	"genitive = '''.input {$count :integer} .match $count one {{książki}} few {{książek}} many {{książek}} * {{książki}}'''\n\n" +
	"[laptop]\nsource = '''.input {$count :integer} .match $count one {{laptop}} * {{laptops}}'''\ngender = \"inanimate\"\n" +
	"nominative = '''.input {$count :integer} .match $count one {{laptop}} few {{laptopy}} many {{laptopów}} * {{laptopa}}'''\n" +
	"genitive = '''.input {$count :integer} .match $count one {{laptopa}} few {{laptopów}} many {{laptopów}} * {{laptopa}}'''\n\n" +
	"[phone_case]\nsource = '''phone case'''\ngender = \"neuter\"\nnominative = '''etui'''\ngenitive = '''etui'''\n\n" +
	"[has_shipped]\nsource = '''Your {$product :nomi:noun} has shipped.'''\ntext = '''\n.input {$product :nomi:gender}\n.match $product\n" +
	"feminine {{Twoja {$product :nomi:noun} została wysłana.}}\ninanimate {{Twój {$product :nomi:noun} został wysłany.}}\n" +
	"neuter {{Twoje {$product :nomi:noun} zostało wysłane.}}\n* {{Twoja przesyłka została wysłana.}}\n'''\n\n" +
	"[sold_out]\nsource = '''No {$product :nomi:noun count=2} left.'''\ntext = '''Brak {$product :nomi:noun case=genitive count=5}.'''\n"

func nounProject() map[string]string {
	return map[string]string{
		"nomi.toml":             "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n",
		"main.nomi":             "import {\n    std/io\n    std/locale.Locale\n    std/message.Message\n    order\n}\n\nfn main() {\n    io.print(Message.format(order.has_shipped(order.book), Locale`pl`))\n}\n",
		"order.nomi":            orderNouns,
		"locales/pl/order.toml": polishOrder,
	}
}

// nounDiagnostics opens the project's Nomi files in a fresh server and
// answers what it publishes for the Polish catalog.
func nounDiagnostics(t *testing.T, root string) []protocol.Diagnostic {
	t.Helper()
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	for _, f := range []string{"order.nomi", "main.nomi"} {
		uri := pathToURI(filepath.Join(root, f))
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		s.docs.Open(uri, string(data))
		s.publish(s.notify, uri)
	}
	var out []protocol.Diagnostic
	for uri, ds := range published() {
		if strings.HasSuffix(uriToPath(uri), filepath.Join("locales", "pl", "order.toml")) {
			out = append(out, ds...)
		}
	}
	return out
}

func TestNouns_TheProjectChecks(t *testing.T) {
	root := writeProject(t, nounProject())
	if ds := nounDiagnostics(t, root); len(ds) > 0 {
		t.Fatalf("the catalog has diagnostics: %+v", ds)
	}
}

// A noun's hover, at its declaration and where it is passed, shows it as
// declared, its doc comment, and its gender and forms in each locale.
func TestNouns_HoverShowsTheFormsInEachLocale(t *testing.T) {
	files := nounProject()
	s, mainURI := openProject(t, files, "main.nomi")
	main := files["main.nomi"]
	use := messageHoverText(t, s, mainURI, offsetPosition(main, strings.Index(main, "order.book")+7))
	root := strings.TrimSuffix(uriToPath(mainURI), "main.nomi")
	orderURI := pathToURI(filepath.Join(root, "order.nomi"))
	s.docs.Open(orderURI, orderNouns)
	decl := messageHoverText(t, s, orderURI, offsetPosition(orderNouns, strings.Index(orderNouns, "noun book")+6))
	for name, text := range map[string]string{"use": use, "declaration": decl} {
		for _, want := range []string{
			"noun book: Noun",
			"A book, a product category.",
			"**Forms by locale**",
			"`en` English, the source: nominative\n\n```\n.input {$count :integer} .match $count one {{book}} * {{books}}\n```",
			"`pl` Polish, feminine\n\nnominative\n\n```\n.input {$count :integer} .match $count one {{książka}}",
			"genitive\n\n```\n.input {$count :integer} .match $count one {{książki}}",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the hover on the %s lacks %q:\n%s", name, want, text)
			}
		}
	}
	// Go to implementations lists the noun's catalog entry.
	impl, err := s.textDocumentImplementation(nil, &protocol.ImplementationParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(mainURI)}, Position: offsetPosition(main, strings.Index(main, "order.book")+7),
	}})
	if err != nil {
		t.Fatal(err)
	}
	locs, _ := impl.([]protocol.Location)
	if len(locs) != 1 || !strings.HasSuffix(string(locs[0].URI), "locales/pl/order.toml") || locs[0].Range.Start.Line != 0 {
		t.Errorf("implementations of a noun: %+v, want its Polish entry", impl)
	}
}

// Completion in a noun's entry offers the keys it lacks, the language's
// genders in its `gender`, the cases after `case=`, and a gender
// selector's keys: the genders the nouns have first.
func TestNouns_CompletionInACatalog(t *testing.T) {
	files := nounProject()
	// laptop's entry lacks its gender and genitive, with a line for a key.
	draft := strings.Replace(polishOrder, "gender = \"inanimate\"\n", "\n", 1)
	draft = strings.Replace(draft, "genitive = '''.input {$count :integer} .match $count one {{laptopa}} few {{laptopów}} many {{laptopów}} * {{laptopa}}'''\n", "", 1)
	draft = strings.Replace(draft, "gender = \"neuter\"", "gender = \"\"", 1)
	draft = strings.Replace(draft, "case=genitive", "case=", 1)
	draft = strings.Replace(draft, "neuter {{Twoje", "\nneuter {{Twoje", 1)
	files["locales/pl/order.toml"] = draft
	s, mainURI := openProject(t, files, "main.nomi")
	root := strings.TrimSuffix(uriToPath(mainURI), "main.nomi")
	uri := pathToURI(filepath.Join(root, "locales/pl/order.toml"))
	s.docs.SetInputOverlay(uriToPath(uri), draft)

	keyLine := strings.Index(draft, "laptops}}'''\n\n") + len("laptops}}'''\n")
	if got := strings.Join(catalogCompletionLabels(t, s, uri, offsetPosition(draft, keyLine)), " "); got != "gender genitive dative accusative instrumental vocative locative" {
		t.Errorf("at a noun entry's key: %q, want its gender, then the case its messages ask for, then the others", got)
	}
	if got := strings.Join(catalogCompletionLabels(t, s, uri, offsetPosition(draft, strings.Index(draft, "gender = \"\"")+10)), " "); got != "feminine animate inanimate personal neuter" {
		t.Errorf("in `gender`: %q, want the genders the nouns have first", got)
	}
	if got := catalogCompletionLabels(t, s, uri, offsetPosition(draft, strings.Index(draft, "case=")+5)); len(got) != 7 || got[0] != "nominative" || got[1] != "genitive" {
		t.Errorf("after `case=`: %v, want Polish's seven cases", got)
	}
	arm := strings.Index(draft, "\n\nneuter {{Twoje") + 1
	if got := strings.Join(catalogCompletionLabels(t, s, uri, offsetPosition(draft, arm)), " "); got != "feminine animate inanimate personal neuter *" {
		t.Errorf("at a gender selector's keys: %q, want the gender a noun has, then the language's others", got)
	}
}

// A message that selects on a noun's gender and lacks an arm for a gender
// the program's nouns have gets it from the arms quick fix, and a noun
// that lacks a form its locale's messages ask for gets one from its own.
func TestNouns_QuickFixesAddTheGenderArmAndTheForm(t *testing.T) {
	files := nounProject()
	short := strings.Replace(polishOrder, "neuter {{Twoje {$product :nomi:noun} zostało wysłane.}}\n", "", 1)
	short = strings.Replace(short, "genitive = '''.input {$count :integer} .match $count one {{laptopa}} few {{laptopów}} many {{laptopów}} * {{laptopa}}'''\n", "", 1)
	files["locales/pl/order.toml"] = short
	root := writeProject(t, files)
	ds := nounDiagnostics(t, root)
	var messages []string
	for _, d := range ds {
		messages = append(messages, d.Message)
	}
	for _, want := range []string{
		"pl: `has_shipped` has no `neuter` arm; the noun `order.phone_case` is neuter in Polish",
		"pl: `laptop` has no `genitive` form, which `sold_out` asks for",
	} {
		if !strings.Contains(strings.Join(messages, "\n"), want) {
			t.Fatalf("diagnostics %q lack %q", messages, want)
		}
	}
	s, _ := openProjectAt(t, root, "order.nomi")
	uri := pathToURI(filepath.Join(root, "locales/pl/order.toml"))
	applyEdits(t, root, actionTitled(t, messageCodeActions(t, s, uri, catalogPointAt(short, "inanimate {{Tw"), nil), "Give the pl translation of `has_shipped` the arms Polish needs").Edit)
	data, _ := os.ReadFile(uriToPath(uri))
	text := string(data)
	if !strings.Contains(text, "neuter {{Twoja przesyłka została wysłana.}}\n* {{") {
		t.Errorf("the neuter arm was not added from the catch-all:\n%s", text)
	}
	s.docs.SetInputOverlay(uriToPath(uri), text)
	applyEdits(t, root, actionTitled(t, messageCodeActions(t, s, uri, catalogPointAt(text, "[laptop]"), nil), "Give the pl translation of `laptop` the `genitive` form its messages ask for").Edit)
	data, _ = os.ReadFile(uriToPath(uri))
	if !strings.Contains(string(data), "genitive = '''.input {$count :integer} .match $count one {{laptop}} few {{laptopy}} many {{laptopów}} * {{laptopa}}'''") {
		t.Errorf("the genitive was not added as a copy of the nominative:\n%s", data)
	}
	if ds := nounDiagnostics(t, root); len(ds) > 0 {
		t.Fatalf("after the fixes the catalog has diagnostics: %+v", ds)
	}
}

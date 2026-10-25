package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// The shop's cart messages: an Int plural, an enum selector and a list of
// names, translated into Polish and Portuguese; Portugal's Portuguese falls
// back to Portuguese, and German is a partial locale.
const shopCart = `/// Where an order is.
pub enum Status {
    Pending
    Shipped
    Delivered
}

/// How many items the cart holds.
pub message cart_items(count: Int) ` + "`" + `
    .input {$count :integer}
    .match $count
    one {{{$count} item in your cart}}
    * {{{$count} items in your cart}}
    ` + "`" + `

/// Where the customer's order is.
pub message order_status(status: Status) ` + "`" + `
    .input {$status :string}
    .match $status
    Pending {{Your order is waiting to ship}}
    Shipped {{Your order is on its way}}
    Delivered {{Your order was delivered}}
    * {{Your order is on its way}}
    ` + "`" + `
`

const cartItemsSource = ".input {$count :integer}\n.match $count\none {{{$count} item in your cart}}\n* {{{$count} items in your cart}}\n"

const orderStatusSource = ".input {$status :string}\n.match $status\nPending {{Your order is waiting to ship}}\nShipped {{Your order is on its way}}\nDelivered {{Your order was delivered}}\n* {{Your order is on its way}}\n"

const polishCart = "[cart_items]\nsource = '''\n" + cartItemsSource + "'''\ntext = '''\n.input {$count :integer}\n.match $count\none {{{$count} produkt w koszyku}}\nfew {{{$count} produkty w koszyku}}\nmany {{{$count} produktów w koszyku}}\n* {{{$count} produktu w koszyku}}\n'''\n\n" +
	"[order_status]\nsource = '''\n" + orderStatusSource + "'''\ntext = '''\n.input {$status :string}\n.match $status\nPending {{Zamówienie czeka na wysyłkę}}\nShipped {{Zamówienie zostało wysłane}}\nDelivered {{Zamówienie zostało dostarczone}}\n* {{Zamówienie zostało wysłane}}\n'''\n"

const portugueseCart = "[cart_items]\nsource = '''\n" + cartItemsSource + "'''\ntext = '''\n.input {$count :integer}\n.match $count\none {{{$count} item no carrinho}}\nmany {{{$count} de itens no carrinho}}\n* {{{$count} itens no carrinho}}\n'''\n\n" +
	"[order_status]\nsource = '''\n" + orderStatusSource + "'''\ntext = '''\n.input {$status :string}\n.match $status\nPending {{O pedido aguarda envio}}\nShipped {{O pedido está a caminho}}\nDelivered {{O pedido foi entregue}}\n* {{O pedido está a caminho}}\n'''\n"

func shopProject() map[string]string {
	return map[string]string{
		"nomi.toml":            "[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\", \"pt\", \"pt-PT\"]\npartial_locales = [\"de\"]\n",
		"main.nomi":            "import {\n    std/io\n    std/locale.Locale\n    std/message.Message\n    cart\n}\n\nfn main() {\n    io.print(Message.format(cart.cart_items(3), Locale`pl`))\n    io.print(Message.format(cart.order_status(.Shipped), Locale`pl`))\n}\n",
		"cart.nomi":            shopCart,
		"locales/pl/cart.toml": polishCart,
		"locales/pt/cart.toml": portugueseCart,
		"locales/de/cart.toml": "[cart_items]\nsource = '''{$count :integer} items'''\ntext = '''{$count :integer} Artikel'''\n",
	}
}

// diagnosticsOf opens every Nomi file of the project at root in a fresh
// server and answers what it publishes, by path from root.
func diagnosticsOf(t *testing.T, root string) map[string][]protocol.Diagnostic {
	t.Helper()
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	for _, f := range []string{"cart.nomi", "main.nomi"} {
		uri := pathToURI(filepath.Join(root, f))
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		s.docs.Open(uri, string(data))
		s.publish(s.notify, uri)
	}
	out := map[string][]protocol.Diagnostic{}
	for uri, ds := range published() {
		if len(ds) > 0 {
			rel, _ := filepath.Rel(root, uriToPath(uri))
			out[filepath.ToSlash(rel)] = ds
		}
	}
	return out
}

func requireClean(t *testing.T, root string) {
	t.Helper()
	if ds := diagnosticsOf(t, root); len(ds) > 0 {
		for f, d := range ds {
			for _, x := range d {
				t.Errorf("%s:%d: %s", f, x.Range.Start.Line+1, x.Message)
			}
		}
		t.FailNow()
	}
}

// applyEdits applies a workspace edit to the files under root.
func applyEdits(t *testing.T, root string, edit *protocol.WorkspaceEdit) {
	t.Helper()
	if edit == nil {
		t.Fatal("the action has no edit")
	}
	apply := func(uri string, edits []protocol.TextEdit) {
		path := uriToPath(uri)
		if !strings.HasPrefix(path, root) {
			t.Fatalf("the edit writes %s, outside the project", path)
		}
		data, _ := os.ReadFile(path)
		text := string(data)
		sort.SliceStable(edits, func(i, j int) bool {
			return offsetOfPosition(text, edits[i].Range.Start) > offsetOfPosition(text, edits[j].Range.Start)
		})
		for _, e := range edits {
			text = text[:offsetOfPosition(text, e.Range.Start)] + e.NewText + text[offsetOfPosition(text, e.Range.End):]
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for uri, edits := range edit.Changes {
		apply(string(uri), edits)
	}
	for _, change := range edit.DocumentChanges {
		switch c := change.(type) {
		case protocol.CreateFile:
			if _, err := os.Stat(uriToPath(string(c.URI))); err == nil {
				t.Fatalf("the edit creates %s, which exists", c.URI)
			}
			apply(string(c.URI), nil)
		case protocol.TextDocumentEdit:
			var edits []protocol.TextEdit
			for _, e := range c.Edits {
				edits = append(edits, e.(protocol.TextEdit))
			}
			apply(string(c.TextDocument.URI), edits)
		default:
			t.Fatalf("unexpected document change %T", change)
		}
	}
}

func messageHoverText(t *testing.T, s *Server, uri string, pos protocol.Position) string {
	t.Helper()
	h, err := s.textDocumentHover(nil, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos,
	}})
	if err != nil || h == nil {
		t.Fatalf("no hover at %+v: %v", pos, err)
	}
	return h.Contents.(protocol.MarkupContent).Value
}

// A message's hover shows its signature, its doc comment and its text in
// each locale, saying which locale's text a locale with no current
// translation of its own shows.
func TestMessages_HoverShowsTheTextInEachLocale(t *testing.T) {
	files := shopProject()
	s, mainURI := openProject(t, files, "main.nomi")
	main := files["main.nomi"]
	call := messageHoverText(t, s, mainURI, offsetPosition(main, strings.Index(main, "cart_items")+2))
	root := strings.TrimSuffix(uriToPath(mainURI), "main.nomi")
	cartURI := pathToURI(filepath.Join(root, "cart.nomi"))
	s.docs.Open(cartURI, files["cart.nomi"])
	decl := messageHoverText(t, s, cartURI, offsetPosition(shopCart, strings.Index(shopCart, "cart_items")+2))
	for name, text := range map[string]string{"call": call, "declaration": decl} {
		for _, want := range []string{
			"cart_items(count: Int) -> Message",
			"How many items the cart holds.",
			"**Text by locale**",
			"`en` English, the source\n\n```\n.input {$count :integer}\n.match $count\none {{{$count} item in your cart}}\n* {{{$count} items in your cart}}\n```",
			"`pl` Polish\n\n```\n.input {$count :integer}\n.match $count\none {{{$count} produkt w koszyku}}\nfew {{{$count} produkty w koszyku}}",
			"`pt` Portuguese\n\n```\n.input {$count :integer}",
			"`pt-PT` Portuguese: no entry; shows the `pt` text",
			"`de` German (partial): stale, translated from an older English text; shows the `en` text",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("the hover on the %s lacks %q:\n%s", name, want, text)
			}
		}
	}
}

// Hover in a catalog shows the message an entry translates, and for a
// stale one the text it was translated from beside the text now.
func TestMessages_CatalogHoverComparesTheSourceTexts(t *testing.T) {
	files := shopProject()
	s, mainURI := openProject(t, files, "main.nomi")
	root := strings.TrimSuffix(uriToPath(mainURI), "main.nomi")
	de := files["locales/de/cart.toml"]
	got := messageHoverText(t, s, pathToURI(filepath.Join(root, "locales/de/cart.toml")), offsetPosition(de, strings.Index(de, "text")))
	for _, want := range []string{
		"```nomi\nmessage cart_items(count: Int)\n```",
		"How many items the cart holds.",
		"This translation was made from an older English text.",
		"It was translated from:\n\n```\n{$count :integer} items\n```",
		"The text now:\n\n```\n.input {$count :integer}\n.match $count\none {{{$count} item in your cart}}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the catalog hover lacks %q:\n%s", want, got)
		}
	}
	pl := files["locales/pl/cart.toml"]
	if got := messageHoverText(t, s, pathToURI(filepath.Join(root, "locales/pl/cart.toml")), offsetPosition(pl, strings.Index(pl, "[order_status]")+2)); !strings.Contains(got, "This translation is current") || !strings.Contains(got, "message order_status(status: Status)") {
		t.Errorf("the hover on a current entry:\n%s", got)
	}
}

func catalogCompletionLabels(t *testing.T, s *Server, uri string, pos protocol.Position) []string {
	t.Helper()
	res, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos,
	}})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := res.(*protocol.CompletionList)
	if list == nil {
		return nil
	}
	items := append([]protocol.CompletionItem(nil), list.Items...)
	sort.SliceStable(items, func(i, j int) bool { return *items[i].SortText < *items[j].SortText })
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

// Completion in a catalog offers the file's messages in a table's header,
// the message's parameters after `$`, and at the start of a variant the
// keys its selector takes in the catalog's locale.
func TestMessages_CompletionInACatalog(t *testing.T) {
	files := shopProject()
	// The Polish catalog is being written: `order_status` is not there yet,
	// and `cart_items` has its `one` arm.
	draft := "[cart_items]\nsource = '''\n" + cartItemsSource + "'''\ntext = '''\n.input {$count :integer}\n.match $count\none {{{$count} produkt w koszyku}}\n\n* {{{$} produktu w koszyku}}\n'''\n\n[\n"
	files["locales/pl/cart.toml"] = draft
	s, mainURI := openProject(t, files, "main.nomi")
	root := strings.TrimSuffix(uriToPath(mainURI), "main.nomi")
	uri := pathToURI(filepath.Join(root, "locales/pl/cart.toml"))
	s.docs.SetInputOverlay(uriToPath(uri), draft)

	if got := catalogCompletionLabels(t, s, uri, offsetPosition(draft, len(draft)-1)); strings.Join(got, " ") != "order_status" {
		t.Errorf("in a new table's header: %v, want the message with no entry yet", got)
	}
	if got := catalogCompletionLabels(t, s, uri, offsetPosition(draft, strings.Index(draft, "{$}")+2)); strings.Join(got, " ") != "count" {
		t.Errorf("after `$`: %v, want the message's parameter", got)
	}
	line := strings.Index(draft, "\n\n* {{") + 1
	if got := catalogCompletionLabels(t, s, uri, offsetPosition(draft, line)); strings.Join(got, " ") != "few many one *" {
		t.Errorf("at a variant's keys: %v, want Polish's missing categories first, then `one` and `*`", got)
	}

	// An enum selector's keys are its variants.
	enumDraft := polishCart[:strings.Index(polishCart, "Pending {{Zam")] + "\n" + polishCart[strings.Index(polishCart, "Pending {{Zam"):]
	s.docs.SetInputOverlay(uriToPath(uri), enumDraft)
	at := strings.Index(enumDraft, "\nPending {{Zam")
	if got := catalogCompletionLabels(t, s, uri, offsetPosition(enumDraft, at)); strings.Join(got, " ") != "Pending Shipped Delivered *" {
		t.Errorf("at an enum selector's keys: %v, want its variants", got)
	}

	// A TOML file that is no catalog gets nothing.
	if got := catalogCompletionLabels(t, s, pathToURI(filepath.Join(root, "nomi.toml")), protocol.Position{Line: 0, Character: 1}); got != nil {
		t.Errorf("nomi.toml completes %v", got)
	}
}

func messageCodeActions(t *testing.T, s *Server, uri string, rng protocol.Range, diags []protocol.Diagnostic) []protocol.CodeAction {
	t.Helper()
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Range:        rng,
		Context:      protocol.CodeActionContext{Diagnostics: diags},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := res.([]protocol.CodeAction)
	return actions
}

func actionTitled(t *testing.T, actions []protocol.CodeAction, title string) protocol.CodeAction {
	t.Helper()
	for _, a := range actions {
		if a.Title == title {
			return a
		}
	}
	var titles []string
	for _, a := range actions {
		titles = append(titles, a.Title)
	}
	t.Fatalf("no action %q among %q", title, titles)
	return protocol.CodeAction{}
}

func catalogPointAt(text, marker string) protocol.Range {
	p := offsetPosition(text, strings.Index(text, marker))
	return protocol.Range{Start: p, End: p}
}

// A stale translation's quick fix records the current source, as `nomi
// messages accept` does, and the project then checks clean.
func TestMessages_QuickFixMarksAStaleTranslationCurrent(t *testing.T) {
	files := shopProject()
	stale := strings.Replace(polishCart, "source = '''\n"+cartItemsSource, "source = '''\n{$count :integer} items\n", 1)
	files["locales/pl/cart.toml"] = stale
	root := writeProject(t, files)
	ds := diagnosticsOf(t, root)["locales/pl/cart.toml"]
	if len(ds) != 1 || !strings.HasPrefix(ds[0].Message, "pl: `cart_items` was translated from an older English text") {
		t.Fatalf("the stale catalog's diagnostics are %+v", ds)
	}
	s, _ := openProjectAt(t, root, "cart.nomi")
	uri := pathToURI(filepath.Join(root, "locales/pl/cart.toml"))
	a := actionTitled(t, messageCodeActions(t, s, uri, ds[0].Range, ds), "Mark the pl translation of `cart_items` current")
	if len(a.Diagnostics) != 1 {
		t.Errorf("the action names %d diagnostics, want the stale one", len(a.Diagnostics))
	}
	applyEdits(t, root, a.Edit)
	data, _ := os.ReadFile(uriToPath(uri))
	if string(data) != polishCart {
		t.Errorf("the catalog after the fix:\n%s\nwant\n%s", data, polishCart)
	}
	requireClean(t, root)
}

// A translation that lacks a plural category's arm, or an enum variant's,
// gets them from its quick fix, copied from the catch-all.
func TestMessages_QuickFixAddsTheMissingArms(t *testing.T) {
	files := shopProject()
	short := strings.Replace(polishCart, "few {{{$count} produkty w koszyku}}\nmany {{{$count} produktów w koszyku}}\n", "", 1)
	short = strings.Replace(short, "Delivered {{Zamówienie zostało dostarczone}}\n", "", 1)
	files["locales/pl/cart.toml"] = short
	root := writeProject(t, files)
	ds := diagnosticsOf(t, root)["locales/pl/cart.toml"]
	var messages []string
	for _, d := range ds {
		messages = append(messages, d.Message)
	}
	want := []string{
		"pl: `cart_items` has no `few` arm; 2, 3 and 4 select `few` in Polish",
		"pl: `cart_items` has no `many` arm; 0, 5 and 6 select `many` in Polish",
		"pl: `order_status` has no `Delivered` arm; `.match $status` needs an arm for every variant of `Status`",
	}
	if strings.Join(messages, "\n") != strings.Join(want, "\n") {
		t.Fatalf("diagnostics %q, want %q", messages, want)
	}
	s, _ := openProjectAt(t, root, "cart.nomi")
	uri := pathToURI(filepath.Join(root, "locales/pl/cart.toml"))
	applyEdits(t, root, actionTitled(t, messageCodeActions(t, s, uri, ds[0].Range, ds), "Give the pl translation of `cart_items` the arms Polish needs").Edit)
	data, _ := os.ReadFile(uriToPath(uri))
	text := string(data)
	if !strings.Contains(text, "one {{{$count} produkt w koszyku}}\nfew {{{$count} produktu w koszyku}}\nmany {{{$count} produktu w koszyku}}\n* {{{$count} produktu w koszyku}}") {
		t.Errorf("the arms were not added from the catch-all:\n%s", text)
	}
	s.docs.SetInputOverlay(uriToPath(uri), text)
	applyEdits(t, root, actionTitled(t, messageCodeActions(t, s, uri, catalogPointAt(text, "Shipped {{Zam"), nil), "Give the pl translation of `order_status` the arms Polish needs").Edit)
	requireClean(t, root)
}

// A listed locale with no translation of a message gets one from the quick
// fix on the declaration: the source text as `source`, and as `text` with
// the arms the locale needs, in the catalog, which it writes when there is
// none.
func TestMessages_QuickFixAddsAMissingTranslation(t *testing.T) {
	for _, tc := range []struct {
		name, polish string
		create       bool
	}{
		{"into the catalog", polishCart[:strings.Index(polishCart, "[order_status]")], false},
		{"into a new catalog", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := shopProject()
			if tc.create {
				delete(files, "locales/pl/cart.toml")
			} else {
				files["locales/pl/cart.toml"] = tc.polish
			}
			root := writeProject(t, files)
			ds := diagnosticsOf(t, root)["cart.nomi"]
			var missing []protocol.Diagnostic
			for _, d := range ds {
				if strings.HasPrefix(d.Message, "pl: `order_status` has no translation") {
					missing = append(missing, d)
				}
			}
			if len(missing) != 1 {
				t.Fatalf("cart.nomi's diagnostics are %+v, want `order_status` missing in pl", ds)
			}
			s, cartURI := openProjectAt(t, root, "cart.nomi")
			s.createFiles = tc.create
			a := actionTitled(t, messageCodeActions(t, s, cartURI, missing[0].Range, missing), "Add the pl translation of `order_status` to locales/pl/cart.toml")
			if len(a.Diagnostics) != 1 {
				t.Errorf("the action names %d diagnostics, want the missing translation", len(a.Diagnostics))
			}
			applyEdits(t, root, a.Edit)
			if tc.create {
				// cart_items, into the catalog the fix wrote, with every
				// arm Polish needs.
				s, cartURI := openProjectAt(t, root, "cart.nomi")
				applyEdits(t, root, actionTitled(t, messageCodeActions(t, s, cartURI, catalogPointAt(shopCart, "cart_items"), nil), "Add the pl translation of `cart_items` to locales/pl/cart.toml").Edit)
			}
			data, _ := os.ReadFile(filepath.Join(root, "locales/pl/cart.toml"))
			text := string(data)
			if !strings.Contains(text, "[order_status]\nsource = '''\n"+orderStatusSource+"'''\ntext = '''\n"+orderStatusSource+"'''\n") {
				t.Errorf("the written entry:\n%s", text)
			}
			if tc.create && !strings.Contains(text, "one {{{$count} item in your cart}}\nfew {{{$count} items in your cart}}\nmany {{{$count} items in your cart}}\n* {{{$count} items in your cart}}") {
				t.Errorf("the new cart_items entry lacks Polish's arms:\n%s", text)
			}
			requireClean(t, root)
		})
	}

	// A client that cannot create files is offered no fix for a catalog
	// that does not exist.
	files := shopProject()
	delete(files, "locales/pl/cart.toml")
	root := writeProject(t, files)
	s, cartURI := openProjectAt(t, root, "cart.nomi")
	for _, a := range messageCodeActions(t, s, cartURI, catalogPointAt(shopCart, "order_status"), nil) {
		if strings.HasPrefix(a.Title, "Add the pl translation") {
			t.Errorf("offered %q to a client that cannot create the catalog", a.Title)
		}
	}
}

// openProjectAt opens the file open of the project written at root.
func openProjectAt(t *testing.T, root, open string) (*Server, string) {
	t.Helper()
	s := NewServer()
	s.docs.SetWorkspaceRoot(root)
	uri := pathToURI(filepath.Join(root, open))
	data, err := os.ReadFile(filepath.Join(root, open))
	if err != nil {
		t.Fatal(err)
	}
	s.docs.Open(uri, string(data))
	if snap := s.docs.Snapshot(uri); snap == nil || snap.Analysis == nil {
		t.Fatalf("%s: no analysis", open)
	}
	return s, uri
}

// An editor that attaches by language sends every TOML buffer; one that is
// no catalog is not read as an input, so its edits recheck nothing, while
// a catalog's buffer is what the analysis reads.
func TestMessages_OnlyCatalogBuffersAreInputs(t *testing.T) {
	files := shopProject()
	files["config/settings.toml"] = "a = 1\n"
	root := writeProject(t, files)
	s, _ := openProjectAt(t, root, "cart.nomi")
	open := func(rel, text string) {
		uri := pathToURI(filepath.Join(root, rel))
		if err := s.textDocumentDidOpen(nil, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: protocol.DocumentUri(uri), Text: text}}); err != nil {
			t.Fatal(err)
		}
	}
	open("config/settings.toml", "a = 2\n")
	if got, _ := s.docs.InputText(filepath.Join(root, "config/settings.toml")); got != "a = 1\n" || s.inputOpen(pathToURI(filepath.Join(root, "config/settings.toml"))) {
		t.Errorf("a TOML file that is no catalog was taken as an input: %q", got)
	}
	open("locales/pl/cart.toml", "# edited\n"+polishCart)
	if got, _ := s.docs.InputText(filepath.Join(root, "locales/pl/cart.toml")); got != "# edited\n"+polishCart {
		t.Errorf("the catalog's buffer is not what the analysis reads: %q", got)
	}
}

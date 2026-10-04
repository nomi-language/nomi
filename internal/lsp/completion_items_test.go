package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// completeWith runs one completion with the client's snippet support set.
func completeWith(t *testing.T, snippets bool, src string) []protocol.CompletionItem {
	t.Helper()
	s := NewServer()
	s.snippetSupport = snippets
	return completeIn(t, s, "file:///completion_items.nomi", src).Items
}

func mustItem(t *testing.T, items []protocol.CompletionItem, label string) protocol.CompletionItem {
	t.Helper()
	it := findItem(items, label)
	if it == nil {
		t.Fatalf("no item %q; got %v", label, itemLabels(items))
	}
	return *it
}

func textEditOf(t *testing.T, it protocol.CompletionItem) protocol.TextEdit {
	t.Helper()
	te, ok := it.TextEdit.(protocol.TextEdit)
	if !ok {
		t.Fatalf("item %q has no TextEdit: %#v", it.Label, it.TextEdit)
	}
	return te
}

const greetDecls = `/// Greets someone by name.
fn greet(name: String, punct: String = "!"): String {
    "hi ${name}${punct}"
}

fn now(): Int {
    1
}
`

func TestInitialize_CompletionCapabilities(t *testing.T) {
	s := NewServer()
	snippets := true
	params := &protocol.InitializeParams{}
	params.Capabilities.TextDocument = &protocol.TextDocumentClientCapabilities{
		Completion: &protocol.CompletionClientCapabilities{},
	}
	params.Capabilities.TextDocument.Completion.CompletionItem = &struct {
		SnippetSupport          *bool                 `json:"snippetSupport,omitempty"`
		CommitCharactersSupport *bool                 `json:"commitCharactersSupport,omitempty"`
		DocumentationFormat     []protocol.MarkupKind `json:"documentationFormat,omitempty"`
		DeprecatedSupport       *bool                 `json:"deprecatedSupport,omitempty"`
		PreselectSupport        *bool                 `json:"preselectSupport,omitempty"`
		TagSupport              *struct {
			ValueSet []protocol.CompletionItemTag `json:"valueSet"`
		} `json:"tagSupport,omitempty"`
		InsertReplaceSupport *bool `json:"insertReplaceSupport,omitempty"`
		ResolveSupport       *struct {
			Properties []string `json:"properties"`
		} `json:"resolveSupport,omitempty"`
		InsertTextModeSupport *struct {
			ValueSet []protocol.InsertTextMode `json:"valueSet"`
		} `json:"insertTextModeSupport,omitempty"`
	}{SnippetSupport: &snippets}
	res, err := s.initialize(nil, params)
	if err != nil {
		t.Fatal(err)
	}
	caps := res.(initializeResult).Capabilities
	cp := caps.CompletionProvider
	if cp == nil || cp.ResolveProvider == nil || !*cp.ResolveProvider {
		t.Fatalf("completion provider = %#v, want resolveProvider", caps.CompletionProvider)
	}
	if strings.Join(cp.TriggerCharacters, " ") != ". > { } \" ` : ," {
		t.Fatalf("trigger characters = %v, want . > { \" ` :", cp.TriggerCharacters)
	}
	if !s.snippetSupport {
		t.Fatal("the client's snippetSupport was not read")
	}
}

// A `>`, `{`, quote or `:` trigger opens a list only where one fits;
// everywhere else the handler answers nothing.
func TestCompletion_TriggerCharacters(t *testing.T) {
	tests := []struct {
		name, trigger, src string
		open               bool
	}{
		{"pipe", ">", "fn f(xs: List<Int>): Int {\n    xs |>" + cursorMark + "\n}\n", true},
		{"arrow", ">", "fn f(c: Bool): Int {\n    case c {\n        True ->" + cursorMark + "\n    }\n}\n", false},
		{"comparison", ">", "fn f(n: Int): Bool {\n    n >" + cursorMark + "\n}\n", false},
		{"struct literal", "{", "struct P {\n    x: Int\n}\n\nfn f(): P {\n    P{" + cursorMark + "}\n}\n", true},
		{"block", "{", "fn f() {" + cursorMark + "\n}\n", false},
		{"struct literal, brace auto-paired", "}", "struct P {\n    x: Int\n}\n\nfn f(): P {\n    P{" + cursorMark + "}\n}\n", true},
		{"struct pattern, brace auto-paired", "}", "struct P {\n    x: Int\n}\n\nfn f(p: P): Int {\n    case p {\n        P{" + cursorMark + "} -> 0\n    }\n}\n", true},
		{"struct literal's next field", ",", "struct P {\n    x: Int\n    y: Int\n}\n\nfn f(): P {\n    P{x: 1," + cursorMark + "}\n}\n", true},
		{"struct pattern's next field", ",", "struct P {\n    x: Int\n    y: Int\n}\n\nfn f(p: P): Int {\n    case p {\n        P{x," + cursorMark + "} -> 0\n    }\n}\n", true},
		{"comma in a call", ",", "fn g(a: Int, b: Int): Int {\n    a\n}\n\nfn f(): Int {\n    g(1," + cursorMark + ")\n}\n", false},
		{"comma in a list", ",", "fn f(): List<Int> {\n    [1," + cursorMark + "]\n}\n", false},
		{"closing a block", "}", "fn f() {\n    x = 1\n}" + cursorMark + "\n", false},
		{"auto-paired block", "}", "fn f() {" + cursorMark + "}\n", false},
		{"anonymous block after a name", "{", "fn f(): Int {\n    case n{" + cursorMark + "}\n}\n", false},
		{"typed literal", `"`, "import std/calendar.Date\n\nfn f() {\n    _ = Date\"" + cursorMark + "\"\n}\n", true},
		{"plain string", `"`, "fn f() {\n    _ = \"" + cursorMark + "\"\n}\n", false},
		{"parameter annotation", ":", "struct Game {\n    turn: Int\n}\n\nfn slay(game:" + cursorMark + ") {}\n", true},
		{"binding annotation", ":", "fn f() {\n    n:" + cursorMark + "\n}\n", true},
		{"return type", ":", "fn f():" + cursorMark + "\n", true},
		{"struct field declaration", ":", "struct P {\n    x:" + cursorMark + "\n}\n", true},
		{"struct literal field", ":", "struct P {\n    x: Int\n}\n\nfn f(): P {\n    P{x:" + cursorMark + "}\n}\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, pos := splitCursor(t, tt.src)
			s := NewServer()
			uri := "file:///trigger.nomi"
			s.docs.Open(uri, content)
			trigger := tt.trigger
			res, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
					Position:     pos,
				},
				Context: &protocol.CompletionContext{
					TriggerKind:      protocol.CompletionTriggerKindTriggerCharacter,
					TriggerCharacter: &trigger,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if opened := len(completionItemsOf(t, res)) > 0; opened != tt.open {
				t.Fatalf("list opened = %v, want %v (%v)", opened, tt.open, res)
			}
		})
	}
}

func TestCompletion_DetailIsTheHoverSignature(t *testing.T) {
	items := complete(t, "fn f(s: String): String {\n    String.tri"+cursorMark+"\n}\n")
	it := mustItem(t, items, "trim")
	if it.Detail == nil || *it.Detail != "fn trim(s: String): String" {
		t.Fatalf("detail = %v, want the signature", it.Detail)
	}
	items = complete(t, greetDecls+"fn f(): String {\n    gre"+cursorMark+"\n}\n")
	it = mustItem(t, items, "greet")
	if it.Detail == nil || *it.Detail != `fn greet(name: String, punct: String = "!"): String` {
		t.Fatalf("detail = %q", *it.Detail)
	}
	if it.Documentation != nil {
		t.Fatal("documentation is sent in the list; it belongs to resolve")
	}
}

func TestCompletion_FieldDetailIsItsType(t *testing.T) {
	src := "struct Box<T> {\n    /// What the box holds.\n    value: T\n}\n\nfn f(b: Box<Int>): Int {\n    b." + cursorMark + "\n}\n"
	s := NewServer()
	items := completeIn(t, s, "file:///box.nomi", src).Items
	it := mustItem(t, items, "value")
	if it.Detail == nil || *it.Detail != "value: Int" {
		t.Fatalf("detail = %v, want value: Int", it.Detail)
	}
	resolved, err := s.completionItemResolve(nil, &it)
	if err != nil {
		t.Fatal(err)
	}
	if doc, ok := resolved.Documentation.(protocol.MarkupContent); !ok || !strings.Contains(doc.Value, "What the box holds.") {
		t.Fatalf("resolved documentation = %#v, want the field's doc comment", resolved.Documentation)
	}
}

func TestCompletion_ResolveAddsDocumentation(t *testing.T) {
	s := NewServer()
	items := completeIn(t, s, "file:///resolve.nomi", greetDecls+"fn f(): String {\n    gre"+cursorMark+"\n}\n").Items
	it := mustItem(t, items, "greet")
	if it.Data == nil {
		t.Fatal("item carries no resolve data")
	}
	stale := it
	resolved, err := s.completionItemResolve(nil, &it)
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := resolved.Documentation.(protocol.MarkupContent)
	if !ok || doc.Kind != protocol.MarkupKindMarkdown || !strings.Contains(doc.Value, "Greets someone by name.") {
		t.Fatalf("resolved documentation = %#v", resolved.Documentation)
	}

	// An item from an older list resolves to itself.
	completeIn(t, s, "file:///resolve.nomi", greetDecls+"fn f(): String {\n    no"+cursorMark+"\n}\n")
	again, _ := s.completionItemResolve(nil, &stale)
	if again.Documentation != nil {
		t.Fatal("a stale item resolved against the newer list")
	}
}

func TestCompletion_InsertText(t *testing.T) {
	tests := []struct {
		name     string
		snippets bool
		src      string
		label    string
		text     string
		snippet  bool
	}{
		{"required parameters only", true, greetDecls + "fn f(): String {\n    gre" + cursorMark + "\n}\n", "greet", "greet(${1:name})$0", true},
		{"no parameters", true, greetDecls + "fn f(): Int {\n    no" + cursorMark + "\n}\n", "now", "now()$0", true},
		{"owner function", true, "fn f(s: String): String {\n    String.tri" + cursorMark + "\n}\n", "trim", "trim(${1:s})$0", true},
		{"plain text, required parameters", false, greetDecls + "fn f(): String {\n    gre" + cursorMark + "\n}\n", "greet", "greet", false},
		{"plain text, no parameters", false, greetDecls + "fn f(): Int {\n    no" + cursorMark + "\n}\n", "now", "now()", false},
		{"parenthesis already written", true, greetDecls + "fn f(): String {\n    gre" + cursorMark + "(\"a\")\n}\n", "greet", "greet", false},
		{"a binding is not called", true, "fn f(): Int {\n    total = 1\n    tot" + cursorMark + "\n}\n", "total", "total", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := mustItem(t, completeWith(t, tt.snippets, tt.src), tt.label)
			te := textEditOf(t, it)
			if te.NewText != tt.text {
				t.Fatalf("insert text = %q, want %q", te.NewText, tt.text)
			}
			isSnippet := it.InsertTextFormat != nil && *it.InsertTextFormat == protocol.InsertTextFormatSnippet
			if isSnippet != tt.snippet {
				t.Fatalf("snippet format = %v, want %v", isSnippet, tt.snippet)
			}
		})
	}
}

func TestCompletion_TextEditReplacesTheWord(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		label      string
		start, end uint32 // UTF-16 columns on the cursor's line
	}{
		{"typed prefix", "fn f(): Int {\n    total = 1\n    tot" + cursorMark + "\n}\n", "total", 4, 7},
		{"word continues past the cursor", "fn f(): Int {\n    total = 1\n    to" + cursorMark + "tl\n}\n", "total", 4, 8},
		{"non-ASCII earlier on the line", "fn f(): String {\n    total = \"é\"\n    x = \"ééé\" + tot" + cursorMark + "\n}\n", "total", 16, 19},
		{"predicate name typed through its ?", "fn empty?(s: String): Bool {\n    s == \"\"\n}\n\nfn f(): Bool {\n    empty?" + cursorMark + "\n}\n", "empty?", 4, 10},
		{"predicate name before its ?", "fn empty?(s: String): Bool {\n    s == \"\"\n}\n\nfn f(): Bool {\n    emp" + cursorMark + "ty?\n}\n", "empty?", 4, 10},
		{"after a dot", "fn f(s: String): String {\n    String.tr" + cursorMark + "\n}\n", "trim", 11, 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, pos := splitCursor(t, tt.src)
			it := mustItem(t, complete(t, tt.src), tt.label)
			te := textEditOf(t, it)
			r := te.Range
			if r.Start.Line != pos.Line || r.End.Line != pos.Line || r.Start.Character != tt.start || r.End.Character != tt.end {
				t.Fatalf("range = %d:%d-%d:%d, want %d:%d-%d", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character, pos.Line, tt.start, tt.end)
			}
		})
	}
}

// A field accepted straight after the `,` brings the space after it.
func TestCompletion_FieldAfterACommaGetsItsSpace(t *testing.T) {
	src := "struct P {\n    x: Int\n    y: Int\n}\n\nfn f(): P {\n    P{x: 1," + cursorMark + "}\n}\n"
	items := complete(t, src)
	te := textEditOf(t, mustItem(t, items, "y"))
	if !strings.HasPrefix(te.NewText, " y") {
		t.Fatalf("insert = %q, want it to start with the space after the comma", te.NewText)
	}
	spaced := complete(t, "struct P {\n    x: Int\n    y: Int\n}\n\nfn f(): P {\n    P{x: 1, "+cursorMark+"}\n}\n")
	if te := textEditOf(t, mustItem(t, spaced, "y")); strings.HasPrefix(te.NewText, " ") {
		t.Fatalf("insert = %q after a space, want no second space", te.NewText)
	}
}

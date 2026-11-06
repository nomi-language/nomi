package lsp

import (
	"encoding/json"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestCompletion_DestructuringParamPlaceholder: a parameter written as a
// pattern is offered by its pattern, never by the parser's binding slot.
func TestCompletion_DestructuringParamPlaceholder(t *testing.T) {
	items := completeWith(t, true, "import std::calendar.{Date, Days}\n\nfn f(d: Date) -> Date {\n    Date.ad"+cursorMark+"\n}\n")
	it := mustItem(t, items, "add")
	te := textEditOf(t, it)
	t.Logf("insert: %s", te.NewText)
	if strings.Contains(te.NewText, "__destr") || !strings.Contains(te.NewText, "(n)}") {
		t.Errorf("insert = %q, want the parameter's pattern as its placeholder", te.NewText)
	}
}

// TestSignatureHelp_DestructuringParamLabel: signature help labels a
// pattern parameter by its pattern, as hover does.
func TestSignatureHelp_DestructuringParamLabel(t *testing.T) {
	s := NewServer()
	uri := "file:///sig_destructure.nomi"
	src := "type Days Int\n\nfn add_days(lhs: Int, Days(n)) -> Int {\n    lhs + n\n}\n\nfn main() {\n    add_days(1, " + cursorMark + ")\n}\n"
	text, pos := splitCursor(t, src)
	s.docs.Open(uri, text)
	help, err := s.textDocumentSignatureHelp(nil, &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil || help == nil {
		t.Fatalf("signature help = %v, %v", help, err)
	}
	sig := help.Signatures[0]
	b, _ := json.Marshal(sig)
	if strings.Contains(string(b), "__destr") {
		t.Fatalf("signature help leaks the binding slot: %s", b)
	}
	if got := sig.Parameters[1].Label; got != "Days(n)" {
		t.Errorf("second parameter label = %v, want Days(n)", got)
	}
}

// TestSignatureHelp_TypeOnlyParamLabel: a parameter written as its type
// alone, `fn stats(Request)`, is labelled and shown by its type, not as
// `_: Request`, and a call's placeholder for it is the type.
func TestSignatureHelp_TypeOnlyParamLabel(t *testing.T) {
	s := NewServer()
	uri := "file:///sig_type_only.nomi"
	src := "struct Request {\n    path: String\n}\n\nfn stats(Request, n: Int) -> Int {\n    n\n}\n\nfn main() {\n    stats(" + cursorMark + ")\n}\n"
	text, pos := splitCursor(t, src)
	s.docs.Open(uri, text)
	help, err := s.textDocumentSignatureHelp(nil, &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil || help == nil {
		t.Fatalf("signature help = %v, %v", help, err)
	}
	sig := help.Signatures[0]
	if sig.Label != "fn stats(Request, n: Int) -> Int" {
		t.Errorf("signature label = %q, want fn stats(Request, n: Int) -> Int", sig.Label)
	}
	if got := sig.Parameters[0].Label; got != "Request" {
		t.Errorf("first parameter label = %v, want Request", got)
	}
}

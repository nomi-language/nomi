package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const paramDefaultsSrc = "fn search(query: String, limit = 10, mode = \"fast\") -> String\n    \"${query}${limit}${mode}\"\nend\n\nfn main()\n    _ = search(\"a\")\nend\n"

// A parameter whose type comes from its default shows the type in hover,
// in signature help and as an inlay hint after its name.
func TestHoverParamTypeFromDefault(t *testing.T) {
	got := hoverAtMark(t, strings.Replace(paramDefaultsSrc, "fn search", "fn ▮search", 1))
	if !strings.Contains(got, "fn search(query: String, limit: Int = 10, mode: String = \"fast\") -> String") {
		t.Errorf("hover = %q, want the inferred parameter types", got)
	}
}

func TestSignatureHelpParamTypeFromDefault(t *testing.T) {
	content, pos := splitCursor(t, strings.Replace(paramDefaultsSrc, "search(\"a\")", "search(\"a\", ‸)", 1))
	s := NewServer()
	uri := "file:///param_defaults.nomi"
	s.docs.Open(uri, content)
	res, err := s.textDocumentSignatureHelp(nil, &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil || res == nil || len(res.Signatures) != 1 {
		t.Fatalf("signature help = %#v, %v", res, err)
	}
	sig := res.Signatures[0]
	if !strings.Contains(sig.Label, "limit: Int = 10") {
		t.Errorf("label = %q, want limit: Int", sig.Label)
	}
	if len(sig.Parameters) != 3 || sig.Parameters[1].Label != "limit: Int" {
		t.Errorf("parameters = %#v, want limit: Int second", sig.Parameters)
	}
}

func TestInlayHintParamTypeFromDefault(t *testing.T) {
	hints := filterByKind(getInlayHints(t, paramDefaultsSrc), InlayHintKindType)
	var labels []string
	for _, h := range hints {
		labels = append(labels, h.Label)
	}
	if len(labels) < 2 || labels[0] != ": Int" || labels[1] != ": String" {
		t.Fatalf("type hints = %q, want `: Int` and `: String` first", labels)
	}
}

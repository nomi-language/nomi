package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// listCommentSource is a call whose argument list holds comments, before
// the first argument, after one's comma and after the last.
const listCommentSource = `fn add(a: Int, b: Int) -> Int {
    a + b
}

fn double(n: Int) -> Int {
    n * 2
}

fn main() {
    total = add(
        // the base
        1,
        double(2), // the step
        // the end
    )
    _ = total
}
`

// A comment in an argument list leaves the arguments to the language
// server as they are without one: hover and semantic tokens find the call
// after the comments, and completion works on a line between them.
func TestListComment_HoverTokensAndCompletionInACommentedArgumentList(t *testing.T) {
	uri := "file:///list_comment.nomi"
	s := NewServer()
	s.docs.Open(uri, listCommentSource)

	// `double` on line 13 (0-based 12), column 9 (0-based 8).
	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: 12, Character: 9},
		},
	})
	if err != nil || hover == nil {
		t.Fatalf("no hover on `double` in the argument list: %v", err)
	}
	if mc, ok := hover.Contents.(protocol.MarkupContent); !ok || !strings.Contains(mc.Value, "fn double(n: Int) -> Int") {
		t.Errorf("hover on `double` = %+v", hover.Contents)
	}

	toks, err := s.textDocumentSemanticTokensFull(nil, &protocol.SemanticTokensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
	})
	if err != nil || toks == nil {
		t.Fatalf("no semantic tokens: %v", err)
	}
	found := false
	line, col := uint32(0), uint32(0)
	for i := 0; i+4 < len(toks.Data); i += 5 {
		if toks.Data[i] > 0 {
			line += toks.Data[i]
			col = toks.Data[i+1]
		} else {
			col += toks.Data[i+1]
		}
		if line == 12 && col == 8 && toks.Data[i+2] == 6 {
			found = true
			if toks.Data[i+3] != 0 {
				t.Errorf("`double` in the argument list is token type %d, want 0 (function)", toks.Data[i+3])
			}
		}
	}
	if !found {
		t.Errorf("no semantic token on `double` in the argument list: %v", toks.Data)
	}

	src := strings.Replace(listCommentSource, "        // the end\n", "        // the end\n        dou"+cursorMark+"\n", 1)
	items := completeIn(t, NewServer(), "file:///list_comment_completion.nomi", src).Items
	labelsInclude(t, items, "double")
}

package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A postfix `!` leaves the expression before it as it was: hover and
// go-to-definition on a call's function, signature help inside its argument
// list and completion of an argument answer as they do without the `!`.
const unwrapSource = `import std/io

/// Halves an even number.
fn half(n: Int) -> Result<Int, String> {
  if n % 2 == 0 {
    Ok(n / 2)
  } else {
    Err("odd")
  }
}

fn main() {
  count = 4
  io.inspect(half(count)!)
  io.inspect(Map.get(%{"a" => 1}, "a")!)
}
`

func openUnwrapProject(t *testing.T, text string) (*Server, string) {
	t.Helper()
	return openProject(t, map[string]string{
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi": text,
	}, "main.nomi")
}

func TestUnwrap_HoverAndDefinitionNameTheFunction(t *testing.T) {
	s, uri := openUnwrapProject(t, unwrapSource)
	at := offsetPosition(unwrapSource, strings.Index(unwrapSource, "half(count)!")+1)
	hover, _ := hoverAt(t, unwrapSource, at)
	if !strings.Contains(hover, "fn half(n: Int) -> Result<Int, String>") || !strings.Contains(hover, "Halves an even number.") {
		t.Errorf("hover on `half` in `half(count)!` = %q, want half's signature and doc", hover)
	}
	if loc := definitionAt(t, s, uri, at); loc.Range.Start.Line != 3 {
		t.Errorf("definition of `half` in `half(count)!` = line %d, want line 3 (the fn)", loc.Range.Start.Line)
	}
	get := offsetPosition(unwrapSource, strings.Index(unwrapSource, "get(")+1)
	if hover, _ = hoverAt(t, unwrapSource, get); !strings.Contains(hover, "fn get(") {
		t.Errorf("hover on `get` in `Map.get(...)!` = %q, want Map.get's signature", hover)
	}
}

func TestUnwrap_SignatureHelpAndCompletion(t *testing.T) {
	s, uri := openUnwrapProject(t, unwrapSource)
	res, err := s.textDocumentSignatureHelp(nil, &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: 13, Character: uint32(len("  io.inspect(half(co"))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || len(res.Signatures) != 1 || !strings.Contains(res.Signatures[0].Label, "half(n: Int)") {
		t.Fatalf("signature help in `half(co` = %+v, want half's signature", res)
	}
	src := strings.Replace(unwrapSource, "half(count)!", "half(cou"+cursorMark+")!", 1)
	text, pos := splitCursor(t, src)
	s.docs.SetText(uri, text)
	mustItem(t, completeAt(t, s, uri, pos), "count")
}

// An unwrapped pipe stage's hover writes the `!` and names the unwrapped
// result.
func TestUnwrap_PipeStageHover(t *testing.T) {
	runPipeHoverCases(t, []pipeHoverCase{
		{"an unwrapped stage", `fn main() {
  n = "4" ▮|> String.to_int()!
  io.inspect(n)
}
`, "`String` → `String.to_int(s)!` → `Int`\n\n```nomi\nfn to_int(s: String) -> Maybe<Int>\n```\n\nThe piped value fills `s`."},
	})
}

package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// spelledTokens are the semantic tokens of text as `line:col text type`,
// 1-based, with the protocol's UTF-16 column, one per line.
func spelledTokens(text string, data []protocol.UInteger) string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, tok := range decodeSemanticTokenData(data) {
		line := lines[tok.line]
		start := byteColOnLine(line, uint32(tok.col)) - 1
		end := byteColOnLine(line, uint32(tok.col+tok.length)) - 1
		out = append(out, fmt.Sprintf("%d:%d %s %s", tok.line+1, tok.col+1, line[start:end], tokenTypes[tok.tokenType]))
	}
	return strings.Join(out, "\n")
}

func phraseTokensOf(t *testing.T, src string) string {
	t.Helper()
	s, uri := openProject(t, map[string]string{"main.nomi": src}, "main.nomi")
	snap := s.docs.Snapshot(uri)
	return spelledTokens(src, encodeSemanticTokens(snap.Analysis, snap.Nodes, snap.Content))
}

// The MF2 in a phrase's or noun's body gets tokens for its syntax, placed in
// the source through the body's indentation; its text gets none, so it
// keeps the string color.
func TestSemanticTokens_PhraseBodies(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"single line", "pub phrase hello(name: String) `Hi, {$name}!`\n",
			"1:12 hello function\n1:18 name parameter\n1:24 String type\n1:37 { operator\n1:38 $name parameter\n1:43 } operator"},
		{"multi-line with a match", "pub phrase cart_items(count: Int) `\n    .input {$count :integer}\n    .match $count\n    one {{{$count} item}}\n    * {{{$count} items}}\n    `\n",
			"1:12 cart_items function\n1:23 count parameter\n1:30 Int type\n" +
				"2:5 .input keyword\n2:12 { operator\n2:13 $count parameter\n2:20 :integer function\n2:28 } operator\n" +
				"3:5 .match keyword\n3:12 $count parameter\n" +
				"4:5 one enumMember\n4:9 {{ operator\n4:11 { operator\n4:12 $count parameter\n4:18 } operator\n4:24 }} operator\n" +
				"5:5 * enumMember\n5:7 {{ operator\n5:9 { operator\n5:10 $count parameter\n5:16 } operator\n5:23 }} operator"},
		{"a noun and a phrase taking it", "pub noun book `.input {$count :integer} .match $count one {{book}} * {{books}}`\n\npub phrase sold(product: Noun) `{$product :nomi:noun case=nominative count=2} sold`\n",
			"1:10 book variable\n1:16 .input keyword\n1:23 { operator\n1:24 $count parameter\n1:31 :integer function\n1:39 } operator\n" +
				"1:41 .match keyword\n1:48 $count parameter\n1:55 one enumMember\n1:59 {{ operator\n1:65 }} operator\n" +
				"1:68 * enumMember\n1:70 {{ operator\n1:77 }} operator\n" +
				"3:12 sold function\n3:17 product parameter\n3:26 Noun struct\n" +
				"3:33 { operator\n3:34 $product parameter\n3:43 :nomi:noun function\n" +
				"3:54 case property\n3:58 = operator\n3:59 nominative enumMember\n3:70 count property\n3:75 = operator\n3:76 2 number\n3:77 } operator"},
		{"a syntax error keeps what parsed", "pub phrase broken(count: Int) `{$count :integer} {oops`\n",
			"1:12 broken function\n1:19 count parameter\n1:26 Int type\n1:32 { operator\n1:33 $count parameter\n1:40 :integer function\n1:48 } operator\n1:50 { operator\n1:51 oops enumMember"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := phraseTokensOf(t, c.src); got != c.want {
				t.Errorf("tokens:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// A catalog's strings get the same tokens, from the catalog's buffer.
func TestSemanticTokens_CatalogStrings(t *testing.T) {
	files := phraseProject()
	files["locales/pl/cart.toml"] = currentCatalog
	s, uri := openProject(t, files, "cart.nomi")
	root := strings.TrimSuffix(uriToPath(uri), "cart.nomi")
	catURI := pathToURI(filepath.Join(root, "locales", "pl", "cart.toml"))
	res, err := s.textDocumentSemanticTokensFull(nil, &protocol.SemanticTokensParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(catURI)}})
	if err != nil || res == nil {
		t.Fatalf("no tokens: %v", err)
	}
	want := "2:20 { operator\n2:21 $name parameter\n2:26 } operator\n3:18 { operator\n3:19 $name parameter\n3:24 } operator"
	if got := spelledTokens(currentCatalog, res.Data); got != want {
		t.Errorf("tokens:\n%s\nwant:\n%s", got, want)
	}
}

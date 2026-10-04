package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// BenchmarkCompletion measures one completion request in a project of 100
// files of 10 public functions each. A prefixed expression position walks
// every tracked project file for import candidates; that walk reads each
// file's analyzed scope and never analyzes anything.
//
//	go test ./internal/lsp -run '^$' -bench BenchmarkCompletion -benchmem
func BenchmarkCompletion(b *testing.B) {
	files := map[string]string{}
	for i := range 100 {
		var src strings.Builder
		fmt.Fprintf(&src, "pub struct Thing%d {\n    n: Int\n}\n\n", i)
		for j := range 10 {
			fmt.Fprintf(&src, "pub fn fn_%d_%d(t: Thing%d): Int {\n    t.n\n}\n\n", i, j, i)
		}
		files[fmt.Sprintf("pkg/file%d.nomi", i)] = src.String()
	}
	var body strings.Builder
	for i := range 200 {
		fmt.Fprintf(&body, "    v%d = %d\n", i, i)
	}
	files["main.nomi"] = "fn main() {\n}\n"
	dir := writeProject(b, files)
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.IndexWorkspace(context.Background())
	uri := "file://" + filepath.Join(dir, "main.nomi")

	cases := map[string]string{
		"prefixed expression": "fn main() {\n" + body.String() + "    fn_4" + cursorMark + "\n}\n",
		"owner member":        "fn main() {\n" + body.String() + "    String." + cursorMark + "\n}\n",
		"dot variant":         "fn f(): Ordering {\n" + body.String() + "    ." + cursorMark + "\n}\n",
		"parameter type":      "fn main() {\n" + body.String() + "}\n\nfn sum(list" + cursorMark,
		"expected type":       "import std/calendar.Date\n\nfn main() {\n" + body.String() + "    d: Date = " + cursorMark + "\n}\n",
		"generic call member": "struct P {\n    n: Int\n}\n\nfn f(ps: List<P>, p: P): Int {\n" + body.String() + "    Maybe.with_default(List.head(ps), p)." + cursorMark + "\n}\n",
	}
	for name, src := range cases {
		content, pos := splitCursor(b, src)
		s.docs.Open(uri, content)
		params := &protocol.CompletionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
				Position:     pos,
			},
		}
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, err := s.textDocumentCompletion(nil, params); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// generatedFile is a file of n documented functions, each with a `//!`
// attached test and a three-stage pipeline: 2,705 lines for n = 300.
func generatedFile(n int) string {
	var b strings.Builder
	b.WriteString("pub struct User {\n    name: String\n    age: Int\n    active?: Bool\n}\n")
	for i := range n {
		fmt.Fprintf(&b, "\n/// Function %d.\n", i)
		fmt.Fprintf(&b, "//! assert f%d([User{name: \"a\", age: %d, active?: True}]) == [\"a\"]\n", i, i)
		fmt.Fprintf(&b, "pub fn f%d(users: List<User>): List<String> {\n", i)
		b.WriteString("    users\n    |> Iter.filter(.active?)\n    |> Iter.map(.name)\n    |> Iter.to_list()\n}\n")
	}
	return b.String()
}

// lspProbe is appended to each benchmarked file: completion runs after
// `it`, and one documentHighlight on the local `items`.
const lspProbe = "\nfn lsp_probe(items: List<Int>): Int {\n    n = List.length(items)\n    it" + cursorMark + "\n}\n"

// requestBenchFile is one file the request benchmarks open, with the
// cursor of the position requests.
type requestBenchFile struct {
	name    string
	s       *Server
	uri     string
	content string
	cursor  protocol.Position
	// local is `items` in lspProbe; wide is the name of the file's first
	// struct, which the large file names 600 times.
	local, wide protocol.Position
	lines       uint32
}

// requestBenchFiles opens the generated large file and a real 523-line
// corpus file (with its helper file beside it), each ending in lspProbe.
func requestBenchFiles(b testing.TB) []requestBenchFile {
	b.Helper()
	dir := filepath.Join("..", "..", "tests", "07-structs-and-enums", "struct_spread")
	small, err := os.ReadFile(filepath.Join(dir, "struct_spread_test.nomi"))
	if err != nil {
		b.Fatal(err)
	}
	helpers, err := os.ReadFile(filepath.Join(dir, "test_helpers.nomi"))
	if err != nil {
		b.Fatal(err)
	}
	var out []requestBenchFile
	for _, f := range []struct {
		name, src string
		extra     map[string]string
	}{
		{"523_lines", string(small), map[string]string{"test_helpers.nomi": string(helpers)}},
		{"2705_lines", generatedFile(300), nil},
	} {
		content, pos := splitCursor(b, f.src+lspProbe)
		files := map[string]string{"main_test.nomi": content}
		for k, v := range f.extra {
			files[k] = v
		}
		s, uri := openProject(b, files, "main_test.nomi")
		out = append(out, requestBenchFile{
			name: f.name, s: s, uri: uri, content: content, cursor: pos,
			local: offsetPosition(content, strings.LastIndex(content, "(items)")+1),
			wide:  offsetPosition(content, strings.Index(content, "struct ")+len("struct ")),
			lines: uint32(strings.Count(content, "\n") + 1),
		})
	}
	return out
}

// openProject writes files as a project and opens the one named open in
// a new server, analyzed.
func openProject(t testing.TB, files map[string]string, open string) (*Server, string) {
	t.Helper()
	root := writeProject(t, files)
	s := NewServer()
	s.docs.SetWorkspaceRoot(root)
	uri := pathToURI(filepath.Join(root, open))
	s.docs.Open(uri, files[open])
	if snap := s.docs.Snapshot(uri); snap == nil || snap.Analysis == nil {
		t.Fatalf("%s: no analysis", open)
	}
	return s, uri
}

func (f requestBenchFile) inlayParams(start, end uint32) *InlayHintParams {
	p := &InlayHintParams{}
	p.TextDocument.URI = f.uri
	p.Range.Start.Line = start
	p.Range.End.Line = end
	return p
}

// offsetPosition is the position of a byte offset whose line holds only
// ASCII before it.
func offsetPosition(content string, off int) protocol.Position {
	start := strings.LastIndex(content[:off], "\n") + 1
	return protocol.Position{Line: uint32(strings.Count(content[:off], "\n")), Character: uint32(off - start)}
}

func (f requestBenchFile) at(p protocol.Position) protocol.TextDocumentPositionParams {
	return protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(f.uri)},
		Position:     p,
	}
}

// BenchmarkLSPRequests measures one request of each per-keystroke or
// per-cursor-rest kind on an analyzed 523-line corpus file and a generated
// 2,705-line file.
//
//	go test ./internal/lsp -run '^$' -bench BenchmarkLSPRequests -benchmem
func BenchmarkLSPRequests(b *testing.B) {
	for _, f := range requestBenchFiles(b) {
		doc := protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(f.uri)}
		mid := f.lines / 2
		requests := []struct {
			name string
			run  func() error
		}{
			{"documentHighlight_local", func() error {
				_, err := f.s.textDocumentDocumentHighlight(nil, &protocol.DocumentHighlightParams{TextDocumentPositionParams: f.at(f.local)})
				return err
			}},
			{"documentHighlight_struct", func() error {
				_, err := f.s.textDocumentDocumentHighlight(nil, &protocol.DocumentHighlightParams{TextDocumentPositionParams: f.at(f.wide)})
				return err
			}},
			{"documentSymbol", func() error {
				_, err := f.s.textDocumentDocumentSymbol(nil, &protocol.DocumentSymbolParams{TextDocument: doc})
				return err
			}},
			{"codeLens", func() error {
				_, err := f.s.textDocumentCodeLens(nil, &protocol.CodeLensParams{TextDocument: doc})
				return err
			}},
			{"inlayHint_visible", func() error {
				_, err := f.s.textDocumentInlayHint(nil, f.inlayParams(mid, mid+60))
				return err
			}},
			{"inlayHint_whole", func() error {
				_, err := f.s.textDocumentInlayHint(nil, f.inlayParams(0, f.lines))
				return err
			}},
			{"semanticTokens", func() error {
				_, err := f.s.textDocumentSemanticTokensFull(nil, &protocol.SemanticTokensParams{TextDocument: doc})
				return err
			}},
			{"completion", func() error {
				_, err := f.s.textDocumentCompletion(nil, &protocol.CompletionParams{TextDocumentPositionParams: f.at(f.cursor)})
				return err
			}},
		}
		for _, r := range requests {
			b.Run(f.name+"/"+r.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if err := r.run(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

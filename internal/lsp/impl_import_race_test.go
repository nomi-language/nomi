package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sourcegraph/jsonrpc2"
)

// TestRPC_ImplImportCheckWhileSiblingIsRead edits a document with pending
// impl-only imports (decls.nomi imports back and hop for their impls only),
// so every background build of it type-checks the project's other files
// for checkDocImplImports, while hover, definition, completion, semantic
// tokens, symbols and inlay hints read hop.nomi, which is open too. Run
// under -race: the build must not write to anything those requests read.
//
// It passed before builds stopped loading open siblings' installed nodes:
// the builds wrote ImplIface and ResolvedEnum into hop's nodes, but no
// request reads those fields. The write itself is pinned by
// analysis.TestDocumentManager_BuildLeavesOtherDocumentsNodesAlone.
func TestRPC_ImplImportCheckWhileSiblingIsRead(t *testing.T) {
	fixture := filepath.Join("..", "irbuild", "testdata", "sibimpl_cycle")
	files := map[string]string{"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"}
	for _, name := range []string{"main.nomi", "decls.nomi", "back.nomi", "hop.nomi"} {
		src, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(src)
	}
	// A dot variant in hop: checking hop records its enum on the node.
	files["hop.nomi"] += "\nfn order(): Ordering {\n    .Less\n}\n"
	dir := writeProject(t, files)
	declsURI := "file://" + filepath.Join(dir, "decls.nomi")
	hopURI := "file://" + filepath.Join(dir, "hop.nomi")

	s := NewServer()
	s.sched.delay = 1 // analyze each edit at once, to overlap builds with requests
	c := startRPC(t, s, dir)
	c.open(hopURI, files["hop.nomi"])
	c.open(declsURI, files["decls.nomi"])
	c.waitDiagnostics(declsURI)
	if snap := s.docs.Snapshot(declsURI); snap == nil || snap.Analysis == nil || len(snap.Analysis.ImplImports) == 0 {
		t.Fatal("decls.nomi has no pending impl-only import; the test does not reach checkDocImplImports")
	}

	hopLines := strings.Split(files["hop.nomi"], "\n")
	at := func(needle string) map[string]any {
		for i, l := range hopLines {
			if j := strings.Index(l, needle); j >= 0 {
				return map[string]any{"line": i, "character": j + 1}
			}
		}
		t.Fatalf("hop.nomi has no %q", needle)
		return nil
	}
	doc := map[string]any{"uri": hopURI}
	requests := []struct {
		method string
		params map[string]any
	}{
		{"textDocument/hover", map[string]any{"textDocument": doc, "position": at("Other.beta")}},
		{"textDocument/hover", map[string]any{"textDocument": doc, "position": at("Thing):")}},
		{"textDocument/hover", map[string]any{"textDocument": doc, "position": at(".Less")}},
		{"textDocument/definition", map[string]any{"textDocument": doc, "position": at("Other.beta")}},
		{"textDocument/completion", map[string]any{"textDocument": doc, "position": at("decls.other")}},
		{"textDocument/semanticTokens/full", map[string]any{"textDocument": doc}},
		{"textDocument/documentSymbol", map[string]any{"textDocument": doc}},
		{"textDocument/inlayHint", map[string]any{"textDocument": doc, "range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": len(hopLines), "character": 0}}}},
	}

	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				r := requests[i%len(requests)]
				var out any
				if err := c.conn.Call(ctx, r.method, r.params, &out); err != nil {
					var rpcErr *jsonrpc2.Error
					if errorsAs(err, &rpcErr) && rpcErr.Code == codeContentModified {
						continue
					}
					t.Errorf("%s: %v", r.method, err)
					return
				}
			}
		}()
	}
	for i := range 30 {
		c.change(declsURI, i+2, files["decls.nomi"]+fmt.Sprintf("\n// edit %d\n", i))
		c.waitDiagnostics(declsURI)
	}
	close(stop)
	wg.Wait()
}

func errorsAs(err error, target **jsonrpc2.Error) bool {
	e, ok := err.(*jsonrpc2.Error)
	if ok {
		*target = e
	}
	return ok
}

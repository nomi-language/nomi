package lsp

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/sourcegraph/jsonrpc2"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestRPC_AClientThatStopsReadingBlocksNoLock is a client that reads the
// first diagnostics and then stops reading. The server's next publication
// cannot be written until the client reads again, and it must not hold
// publishMu while it waits: a didClose takes publishMu, and it runs on the
// goroutine that reads the client's messages, so every later message would
// wait too.
func TestRPC_AClientThatStopsReadingBlocksNoLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	text := "fn main() {\n}\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := fileuri.FromPath(path)

	s := NewServer()
	s.sched.delay = time.Millisecond
	server, client := net.Pipe()
	go s.ServeStream(server)
	release := make(chan struct{})
	first := make(chan struct{}, 1)
	conn := jsonrpc2.NewConn(context.Background(), jsonrpc2.NewBufferedStream(client, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(_ context.Context, _ *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
			if req.Method == string(protocol.ServerTextDocumentPublishDiagnostics) {
				select {
				case first <- struct{}{}:
				default:
				}
				// The client stops reading here until the test ends.
				<-release
			}
			return nil, nil
		}))
	t.Cleanup(func() {
		close(release)
		conn.Close()
	})
	var res any
	if err := conn.Call(context.Background(), "initialize", map[string]any{
		"rootUri":      fileuri.FromPath(dir),
		"capabilities": map[string]any{},
	}, &res); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	notify := func(method string, params any) {
		t.Helper()
		if err := conn.Notify(context.Background(), method, params); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	notify("initialized", struct{}{})
	notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(uri), LanguageID: "nomi", Version: 1, Text: text,
	}})
	select {
	case <-first:
	case <-time.After(30 * time.Second):
		t.Fatal("no diagnostics published")
	}

	// The edit's analysis publishes into a pipe nobody reads.
	notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": 2},
		"contentChanges": []map[string]any{{"text": "fn main() {\n    x\n}\n"}},
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s.sched.publishMu.TryLock() {
			done := s.sched.published[uri] >= 2
			s.sched.publishMu.Unlock()
			if done {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the edit's diagnostics were not published, or their publication holds publishMu while the client does not read")
		}
		time.Sleep(5 * time.Millisecond)
	}

	notify("textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}})
	deadline = time.Now().Add(10 * time.Second)
	for s.docs.IsOpen(uri) {
		if time.Now().After(deadline) {
			t.Fatal("didClose did not close the document: closeDocument is waiting for publishMu")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

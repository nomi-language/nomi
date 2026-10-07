package lsp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sourcegraph/jsonrpc2"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const internalErrorText = "internal language server error: planted panic"

// plantPanic makes every boundary named in where panic until the test
// ends, and captures the server's panic log.
func plantPanic(t *testing.T, where ...string) *bytes.Buffer {
	t.Helper()
	hook := func(at string) {
		for _, w := range where {
			if at == w {
				panic("planted panic")
			}
		}
	}
	faultHook.Store(&hook)
	var log bytes.Buffer
	panicLogMu.Lock()
	saved := panicLog
	panicLog = &log
	panicLogMu.Unlock()
	t.Cleanup(func() {
		faultHook.Store(nil)
		panicLogMu.Lock()
		panicLog = saved
		panicLogMu.Unlock()
	})
	return &log
}

func logText(log *bytes.Buffer) string {
	panicLogMu.Lock()
	defer panicLogMu.Unlock()
	return log.String()
}

// waitMessage waits for a publication for uri with a diagnostic containing
// text.
func (c *rpcClient) waitMessage(uri, text string) published {
	c.t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case p := <-c.diags:
			if p.uri != uri {
				continue
			}
			for _, m := range p.msgs {
				if strings.Contains(m, text) {
					return p
				}
			}
		case <-timeout:
			c.t.Fatalf("no diagnostic containing %q published for %s", text, uri)
		}
	}
}

func (c *rpcClient) hover(uri string) (*protocol.Hover, error) {
	var hover *protocol.Hover
	err := c.conn.Call(context.Background(), "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 9, "character": 17},
	}, &hover)
	return hover, err
}

// TestRecover_RequestHandlerPanic: a panic in a request handler answers
// that request with an InternalError, is logged with its stack, and the
// server goes on answering.
func TestRecover_RequestHandlerPanic(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)
	log := plantPanic(t, "textDocument/hover")

	for range 2 {
		_, err := c.hover(uri)
		var rpcErr *jsonrpc2.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc2.CodeInternalError {
			t.Fatalf("panicking hover answered %v, want error code %d", err, jsonrpc2.CodeInternalError)
		}
		if !strings.Contains(rpcErr.Message, internalErrorText) || !strings.Contains(rpcErr.Message, "https://github.com/nomi-language/nomi/issues") {
			t.Fatalf("error message = %q", rpcErr.Message)
		}
	}
	if got := logText(log); !strings.Contains(got, "recovered panic in textDocument/hover: planted panic") || !strings.Contains(got, "goroutine ") {
		t.Fatalf("log = %q, want the panic and its stack", got)
	}

	var symbols []protocol.DocumentSymbol
	if err := c.conn.Call(context.Background(), "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	}, &symbols); err != nil {
		t.Fatalf("documentSymbol after the panic: %v", err)
	}
	faultHook.Store(nil)
	if hover, err := c.hover(uri); err != nil || hover == nil {
		t.Fatalf("hover after the panic = %v, %v", hover, err)
	}
}

// TestRecover_NotificationPanic: a panic in a notification handler, which
// runs on the connection's read goroutine, does not end the connection.
func TestRecover_NotificationPanic(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, text)
	c.waitDiagnostics(uri)
	log := plantPanic(t, "textDocument/didChange")

	c.change(uri, 2, text+"\n")
	if hover, err := c.hover(uri); err != nil || hover == nil {
		t.Fatalf("hover after a panicking didChange = %v, %v", hover, err)
	}
	if got := logText(log); !strings.Contains(got, "recovered panic in textDocument/didChange") {
		t.Fatalf("log = %q", got)
	}
}

// TestRecover_AnalysisPanic: a panic in a document's background analysis
// publishes one internal-error diagnostic, and the next edit, analyzed
// without the panic, publishes the document's own diagnostics again.
func TestRecover_AnalysisPanic(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	plantPanic(t, "analysis")

	c.open(uri, text)
	p := c.waitMessage(uri, internalErrorText)
	if p.n != 1 || !strings.Contains(p.msgs[0], "https://github.com/nomi-language/nomi/issues") {
		t.Fatalf("published %q, want one internal-error diagnostic", p.msgs)
	}

	faultHook.Store(nil)
	c.change(uri, 2, text+"\nfn broken(): Int { \"no\" }\n")
	p = c.waitErrors(uri)
	for _, m := range p.msgs {
		if strings.Contains(m, "internal language server error") {
			t.Fatalf("the analysis after the panic published %q", p.msgs)
		}
	}
	if hover, err := c.hover(uri); err != nil || hover == nil {
		t.Fatalf("hover after the panic = %v, %v", hover, err)
	}
}

// TestRecover_LoweringPanic: a panic around an open document's lowering
// run becomes one diagnostic at the top of the file.
func TestRecover_LoweringPanic(t *testing.T) {
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	plantPanic(t, "lowering")

	c.open(uri, text)
	c.waitMessage(uri, internalErrorText)
	if hover, err := c.hover(uri); err != nil || hover == nil {
		t.Fatalf("hover after the panic = %v, %v", hover, err)
	}
}

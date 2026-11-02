package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/lsp"
	"github.com/sourcegraph/jsonrpc2"
)

// lspClient is a client of an in-process language server, over a pipe, so
// the answers are the ones an editor gets. A file a tool names is opened in
// it from the disk, and changed again when the disk changes.
type lspClient struct {
	conn *jsonrpc2.Conn

	mu   sync.Mutex
	docs map[string]*openDoc // by URI
}

type openDoc struct {
	version int
	text    string
}

func startLSP(root string) (*lspClient, error) {
	serverSide, clientSide := net.Pipe()
	go lsp.NewServer().ServeStream(serverSide)
	c := &lspClient{docs: map[string]*openDoc{}}
	c.conn = jsonrpc2.NewConn(context.Background(),
		jsonrpc2.NewBufferedStream(clientSide, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(c.fromServer))
	var res json.RawMessage
	if err := c.conn.Call(context.Background(), "initialize", map[string]any{
		"processId":    os.Getpid(),
		"rootUri":      fileuri.FromPath(root),
		"capabilities": map[string]any{},
	}, &res); err != nil {
		return nil, fmt.Errorf("starting the language server: %w", err)
	}
	if err := c.conn.Notify(context.Background(), "initialized", struct{}{}); err != nil {
		return nil, err
	}
	return c, nil
}

// fromServer answers the server's requests to its client. It publishes
// diagnostics and asks for settings and progress tokens; the tools read
// diagnostics through `nomi check`, so each is answered with nothing.
func (c *lspClient) fromServer(_ context.Context, _ *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
	if req.Method == "workspace/configuration" && req.Params != nil {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(*req.Params, &p)
		return make([]any, len(p.Items)), nil
	}
	return nil, nil
}

func (c *lspClient) close() { c.conn.Close() }

// sync opens path in the server, or sends its new text if the disk changed
// since, and answers its URI and text.
func (c *lspClient) sync(ctx context.Context, path string) (string, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", "", err
	}
	text := string(data)
	uri := fileuri.FromPath(abs)
	c.mu.Lock()
	defer c.mu.Unlock()
	doc, ok := c.docs[uri]
	switch {
	case !ok:
		c.docs[uri] = &openDoc{version: 1, text: text}
		err = c.conn.Notify(ctx, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "nomi", "version": 1, "text": text,
		}})
	case doc.text != text:
		doc.version++
		doc.text = text
		err = c.conn.Notify(ctx, "textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": doc.version},
			"contentChanges": []map[string]any{{"text": text}},
		})
	}
	return uri, text, err
}

// position is an LSP position: 0-based line, UTF-16 character.
type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

// toLSP turns a 1-based line and 1-based byte column of text into an LSP
// position.
func toLSP(text string, line, col int) (position, error) {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return position{}, fmt.Errorf("line %d is outside the file, which has %d lines", line, len(lines))
	}
	l := lines[line-1]
	if col < 1 || col > len(l)+1 {
		return position{}, fmt.Errorf("column %d is outside line %d, which has %d bytes", col, line, len(l))
	}
	return position{Line: line - 1, Character: utf16Len(l[:col-1])}, nil
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// fromLSP turns an LSP position in text back into a 1-based line and byte
// column.
func fromLSP(text string, p position) (int, int) {
	lines := strings.Split(text, "\n")
	if p.Line >= len(lines) {
		return p.Line + 1, 1
	}
	l, units, i := lines[p.Line], 0, 0
	for i < len(l) && units < p.Character {
		r, size := utf8.DecodeRuneInString(l[i:])
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
		i += size
	}
	return p.Line + 1, i + 1
}

// location is a span in a file, 1-based, columns in bytes, end exclusive.
type location struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"end_line"`
	EndCol  int    `json:"end_col"`
}

// locations reads a definition or references result: a Location, a list of
// them, or a list of LocationLinks.
func locations(raw json.RawMessage) ([]location, error) {
	type lspLocation struct {
		URI                  string    `json:"uri"`
		Range                *lspRange `json:"range"`
		TargetURI            string    `json:"targetUri"`
		TargetSelectionRange *lspRange `json:"targetSelectionRange"`
	}
	var list []lspLocation
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return nil, nil
	case strings.HasPrefix(trimmed, "["):
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
	default:
		var one lspLocation
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		list = []lspLocation{one}
	}
	out := []location{}
	texts := map[string]string{}
	for _, l := range list {
		uri, r := l.URI, l.Range
		if l.TargetURI != "" {
			uri, r = l.TargetURI, l.TargetSelectionRange
		}
		if r == nil {
			continue
		}
		path := fileuri.ToPath(uri)
		text, ok := texts[path]
		if !ok {
			data, _ := os.ReadFile(path)
			text = string(data)
			texts[path] = text
		}
		line, col := fromLSP(text, r.Start)
		endLine, endCol := fromLSP(text, r.End)
		out = append(out, location{File: path, Line: line, Col: col, EndLine: endLine, EndCol: endCol})
	}
	return out, nil
}

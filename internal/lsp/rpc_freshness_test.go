package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/sourcegraph/jsonrpc2"
)

// TestRPC_EveryRequestThatReadsItsDocumentIsListed sends every request
// the server dispatches, the fields protocol.Handler sets and lsp317's
// methods, over the RPC path to an analyzed open document. A handler reads
// its document through requestDoc, which panics for a method with no
// requestFreshness entry, so a request that reads its document's analysis
// without the wait its freshness gives fails here with that panic as an
// InternalError. Every listed method must be one the server dispatches.
//
// The params are one object every request's params decode from, so each
// handler is reached: a request answered InvalidParams or MethodNotFound
// fails too.
func TestRPC_EveryRequestThatReadsItsDocumentIsListed(t *testing.T) {
	methods := glspMethods(t)
	dir, uri, text := datesFixture(t)
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, text)
	waitAnalyzed(t, s, uri)

	var requests []string
	h := reflect.ValueOf(&s.handler).Elem()
	for i := 0; i < h.NumField(); i++ {
		f := h.Field(i)
		// A notification's function returns only an error; initialize ran
		// in startRPC.
		if f.Kind() != reflect.Func || f.IsNil() || f.Type().NumOut() != 2 {
			continue
		}
		name := h.Type().Field(i).Name
		method, ok := methods["Method"+name]
		if !ok {
			t.Fatalf("protocol.Handler.%s has no Method%s constant in glsp", name, name)
		}
		if method != "initialize" {
			requests = append(requests, method)
		}
	}
	for method := range lsp317 {
		requests = append(requests, method)
	}

	rng := map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 1}}
	params := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": 0},
		"range":        rng,
		"context": map[string]any{
			"diagnostics": []any{}, "includeDeclaration": true, "triggerKind": 1,
			"isRetrigger": false, "frameId": 1, "stoppedLocation": rng,
		},
		"newName": "renamed",
		"options": map[string]any{"tabSize": 4, "insertSpaces": true},
		"item":    map[string]any{"uri": uri, "name": "x", "kind": 12, "range": rng, "selectionRange": rng},
		"query":   "",
		"files":   []any{},
	}
	sent := map[string]bool{}
	for _, method := range requests {
		sent[method] = true
		var res json.RawMessage
		err := c.conn.Call(context.Background(), method, params, &res)
		var rpcErr *jsonrpc2.Error
		if errors.As(err, &rpcErr) {
			switch rpcErr.Code {
			case jsonrpc2.CodeInternalError, jsonrpc2.CodeInvalidParams, jsonrpc2.CodeMethodNotFound:
				t.Errorf("%s: %v", method, rpcErr.Message)
			}
		} else if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	for method := range requestFreshness {
		if !sent[method] {
			t.Errorf("requestFreshness lists %s, which the server does not dispatch", method)
		}
	}
	t.Logf("%d requests sent, %d of them listed", len(requests), len(requestFreshness))
}

// glspMethods maps the name of each Method constant of glsp's
// protocol_3_16 to its value, read from the package's source, where
// Method<Field> is the method protocol.Handler's field <Field> answers.
func glspMethods(t *testing.T) map[string]string {
	t.Helper()
	pkg, err := build.Import("github.com/tliron/glsp/protocol_3_16", ".", build.FindOnly)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(pkg.Dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]string{}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Method") || i >= len(vs.Values) {
						continue
					}
					call, ok := vs.Values[i].(*ast.CallExpr)
					if !ok || len(call.Args) != 1 {
						continue
					}
					if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil {
							methods[name.Name] = v
						}
					}
				}
			}
		}
	}
	if len(methods) == 0 {
		t.Fatalf("no Method constants in %s", pkg.Dir)
	}
	return methods
}

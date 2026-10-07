package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// scriptFFIFiles is an extensionless `#!` script in bin/ that binds Go
// through the go.mod above it and imports a .nomi file beside it. The
// main.nomi at the top is another program: the CLI roots the script at
// bin/, so that main.nomi must not become the script's project root.
var scriptFFIFiles = map[string]string{
	"go.mod":    "module scriptbinding\n\ngo 1.27.0\n",
	"main.nomi": "fn main() {}\n",
	"binding.go": `package scriptbinding

import "strings"

type Box struct {
	Label string
}

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}

func MakeBox(label string) *Box {
	return &Box{Label: label}
}

func BoxLabel(box *Box) string {
	return box.Label
}
`,
	"bin/helper.nomi": "pub fn answer(): Int {\n  42\n}\n",
	"bin/hi": `#!/usr/bin/env nomi
import {
  std/io
  helper
}

gopkg "scriptbinding" as ffi

opaque type RawBox go ffi.Box

fn echo_upper(s: String): String go ffi.EchoUpper

fn make_box(label: String): RawBox go ffi.MakeBox

fn box_label(box: RawBox): String go ffi.BoxLabel

fn main() {
  io.print(echo_upper("hello"))
  io.print(box_label(make_box("boxed")))
  io.print(helper.answer())
}
`,
}

// positionOf is the position just after the first occurrence of marker in
// text, or at its start when after is false.
func positionOf(t *testing.T, text, marker string, after bool) protocol.Position {
	t.Helper()
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("marker %q not in the text", marker)
	}
	if after {
		i += len(marker)
	}
	line := strings.Count(text[:i], "\n")
	col := i - (strings.LastIndex(text[:i], "\n") + 1)
	return protocol.Position{Line: uint32(line), Character: uint32(col)}
}

// An extensionless script with Go bindings gets what a .nomi file gets in
// the editor: no false diagnostics from the front end or the lowering run,
// hover and completion on a bound name, and go-to-definition from the Go
// symbol to its Go source through the nearest go.mod above the script.
func TestRPC_ExtensionlessScriptGoBindings(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	dir := t.TempDir()
	for name, text := range scriptFFIFiles {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	text := scriptFFIFiles["bin/hi"]
	uri := "file://" + filepath.Join(dir, "bin", "hi")
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, text)
	if p := c.waitDiagnostics(uri); p.n != 0 {
		t.Fatalf("the script's analysis reports %v; want none", p.msgs)
	}
	if d := safeLoweringDiagnostics(uri, text); len(d) != 0 {
		t.Fatalf("the script's lowering reports %+v; want none", d)
	}

	var hover protocol.Hover
	if err := c.conn.Call(context.Background(), "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": positionOf(t, text, `echo_upper("hello")`, false),
	}, &hover); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(hover.Contents)
	if !strings.Contains(string(b), "fn echo_upper(s: String): String") {
		t.Errorf("hover = %s, want the binding's signature", b)
	}

	w, _ := c.completion(uri, positionOf(t, text, `io.print(echo_`, true), "")
	labels, _ := c.labels(w)
	labelsHave(t, labels, "echo_upper")

	var loc protocol.Location
	if err := c.conn.Call(context.Background(), "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": positionOf(t, text, "EchoUpper", false),
	}, &loc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(loc.URI), "/binding.go") || loc.Range.Start.Line != 8 {
		t.Errorf("definition of ffi.EchoUpper = %+v; want binding.go line 9", loc)
	}
}

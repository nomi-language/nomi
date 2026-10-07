package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/ffirun"
)

const stdFFIText = `import std/io

gopkg "strings" as strings
gopkg "math" as math

fn upper(s: String): String go strings.ToUpper

fn sqrt(x: Float): Float go math.Sqrt

fn main() {
  io.print(upper("hello"))
  io.print(sqrt(2.0))
}
`

// A file binding Go standard library packages, with no go.mod above it, gets
// no diagnostics from the front end or the lowering run, hover on a bound
// name, and go-to-definition from the Go symbol into the toolchain's source.
// `math.Sqrt` resolves in the math package's own directory, not to math/big's
// `(*Float).Sqrt` method below it.
func TestRPC_StdLibraryGoBindings(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	dir := t.TempDir()
	if _, found := ffirun.GoModRoot(filepath.Join(dir, "main.nomi")); found {
		t.Skip("a go.mod above the temp directory")
	}
	path := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(path, []byte(stdFFIText), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := "file://" + path
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, stdFFIText)
	if p := c.waitDiagnostics(uri); p.n != 0 {
		t.Fatalf("analysis reports %v; want none", p.msgs)
	}
	if d := safeLoweringDiagnostics(uri, stdFFIText); len(d) != 0 {
		t.Fatalf("lowering reports %+v; want none", d)
	}

	var hover protocol.Hover
	if err := c.conn.Call(context.Background(), "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri}, "position": positionOf(t, stdFFIText, `upper("hello")`, false),
	}, &hover); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(hover.Contents)
	if !strings.Contains(string(b), "fn upper(s: String): String") {
		t.Errorf("hover = %s, want the binding's signature", b)
	}

	for symbol, wantFile := range map[string]string{
		"ToUpper": filepath.Join("strings", "strings.go"),
		"Sqrt":    filepath.Join("math", "sqrt.go"),
	} {
		var loc protocol.Location
		if err := c.conn.Call(context.Background(), "textDocument/definition", map[string]any{
			"textDocument": map[string]any{"uri": uri}, "position": positionOf(t, stdFFIText, symbol, false),
		}, &loc); err != nil {
			t.Fatal(err)
		}
		stdDir, _ := ffirun.StdPackageDir("strings")
		want := filepath.Join(filepath.Dir(stdDir), wantFile)
		if string(loc.URI) != "file://"+want {
			t.Errorf("definition of %s = %+v; want %s", symbol, loc, want)
		}
	}
}

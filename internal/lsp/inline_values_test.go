package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

const inlineValueSrc = `import std/io

fn scale(xs: List<Int>, by: Int) -> List<Int> {
    factor = by * 2
    _ = factor
    xs |> Iter.map(fn x -> x * factor) |> Iter.to_list()
}

fn main() {
    nums = [1, 2]
    total = nums |> Iter.count()
    io.print("${scale(nums, total)}")
}
`

// inlineValuesFor analyzes src and answers the lookups for the whole file
// while paused on line stop, as "line:col name", 1-based.
func inlineValuesFor(t *testing.T, src string, stop int) []string {
	t.Helper()
	lib := std.Load()
	nodes, errs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "/tmp", nil)
	analysis.CheckTypes(fa, nodes)
	var out []string
	for _, v := range inlineValues(fa, nodes, 1, strings.Count(src, "\n")+1, stop) {
		out = append(out, fmt.Sprintf("%d:%d %s", v.Range.Start.Line+1, v.Range.Start.Character+1, v.VariableName))
	}
	return out
}

// Paused in a function: each parameter and local of that function, once per
// line, from its first line to the paused line, lambda parameters included;
// nothing of another function, and nothing after the paused line.
func TestInlineValues_LookUpTheLocalsOfThePausedFunction(t *testing.T) {
	got := strings.Join(inlineValuesFor(t, inlineValueSrc, 6), ", ")
	want := "3:10 xs, 3:25 by, 4:5 factor, 4:14 by, 5:9 factor, 6:5 xs, 6:23 x, 6:32 factor"
	if got != want {
		t.Fatalf("paused on line 6:\n got %s\nwant %s", got, want)
	}
	got = strings.Join(inlineValuesFor(t, inlineValueSrc, 11), ", ")
	want = "10:5 nums, 11:5 total, 11:13 nums"
	if got != want {
		t.Fatalf("paused on line 11:\n got %s\nwant %s", got, want)
	}
	if got := inlineValuesFor(t, inlineValueSrc, 1); len(got) != 0 {
		t.Fatalf("paused outside every function: %v", got)
	}
}

// The server answers textDocument/inlineValue over RPC, in UTF-16 columns.
func TestInlineValues_AnsweredOverRPC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scale.nomi")
	if err := os.WriteFile(path, []byte(inlineValueSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := fileuri.FromPath(path)
	s := NewServer()
	c := startRPC(t, s, dir)
	c.open(uri, inlineValueSrc)
	c.waitDiagnostics(uri)
	var got []InlineValueVariableLookup
	err := c.conn.Call(context.Background(), "textDocument/inlineValue", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range":        map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 12, "character": 0}},
		"context": map[string]any{"frameId": 1, "stoppedLocation": map[string]any{
			"start": map[string]any{"line": 10, "character": 4}, "end": map[string]any{"line": 10, "character": 4}}},
	}, &got)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range got {
		names = append(names, fmt.Sprintf("%d:%d %s", v.Range.Start.Line+1, v.Range.Start.Character+1, v.VariableName))
	}
	if strings.Join(names, ", ") != "10:5 nums, 11:5 total, 11:13 nums" {
		t.Fatalf("inline values: %v", names)
	}
}

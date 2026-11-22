package lsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// effectDbgMain holds a `dbg` in main, in a function nothing calls, in a
// generic function nothing instantiates and in a test, and an `assert`, a
// `todo` and a `?`, which stop or leave and act on nothing.
const effectDbgMain = `import std::io

fn draft(n: Int) -> Int
    dbg n
end

fn first<T>(xs: List<T>) -> Maybe<T>
    xs |> dbg |> List.first()
end

fn later(n: Int) -> Result<Int, String>
    m = Ok(n)?
    if m > 0 then
        Ok(m)
    else
        todo
    end
end

fn main()
    io.print("${dbg 1 + 1}")
    _ = later(1)
end

test "two"
    assert dbg 2 == 2
end
`

// The keyword of a `dbg` is the call that prints: it carries the effect
// modifier, as the callee of a call that acts does, and its hover keeps the
// keyword's text and ends with what it reaches, in a function main
// reaches, one nothing calls, a generic one nothing instantiates and a
// test, under a list that names only std::io, since no list names a `dbg`
// and every list allows it. The other keywords that stop or leave carry
// no modifier and no line.
func TestSemanticTokens_EffectModifierOnADbg(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\n"
	dir := writeProject(t, map[string]string{"main.nomi": effectDbgMain, "nomi.toml": manifest})
	s, c := startEffectServer(t, dir, map[string]string{"main.nomi": effectDbgMain})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	for _, needle := range []string{"dbg n", "dbg |>", "dbg 1", "dbg 2"} {
		waitReachAt(t, s, c, uri, effectDbgMain, needle, "Has effects: `dbg`.")
		var hover protocol.Hover
		if err := c.conn.Call(context.Background(), "textDocument/hover", map[string]any{
			"textDocument": map[string]any{"uri": uri}, "position": positionOf(t, effectDbgMain, needle, false),
		}, &hover); err != nil {
			t.Fatal(err)
		}
		var markup protocol.MarkupContent
		raw, _ := json.Marshal(hover.Contents)
		_ = json.Unmarshal(raw, &markup)
		if !strings.Contains(markup.Value, "Debug rendering") {
			t.Errorf("the hover on %q lost the keyword's text: %q", needle, markup.Value)
		}
	}
	for _, needle := range []string{"assert", "todo", "?\n"} {
		waitReachAt(t, s, c, uri, effectDbgMain, needle)
	}
	toks := semanticTokensOf(t, c, uri)
	for _, tc := range []struct {
		needle, word string
		want         bool
	}{
		{"dbg n", "dbg", true},
		{"fn draft", "draft", true},
		{"dbg |>", "dbg", true},
		{"fn first", "first", true},
		{"dbg 1", "dbg", true},
		{"fn main", "main", true},
		{"dbg 2", "dbg", true},
		{"assert", "assert", false},
		{"todo", "todo", false},
		{"fn later", "later", false},
	} {
		if got := effectAt(t, toks, effectDbgMain, tc.needle, tc.word); got != tc.want {
			t.Errorf("%q in %q: effect is %v, want %v", tc.word, tc.needle, got, tc.want)
		}
	}
}

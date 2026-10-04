package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

const bindingElseSrc = `enum LoadError {
  NotFound
  Broken(String)
}

fn load(id: Int): Result<String, LoadError> {
  Err(.NotFound)
}

fn name(id: Int): Result<String, String> {
  Ok(user) = load(id) else {
    Err(.NotFound) -> return Err("none")
    Err(err) -> return Err("broken: ${Debug.inspect(err)}")
  }
  Ok("${user}!")
}

fn size(r: Result<(Int, Int), String>): Int {
  Ok((width, height)) = r else { (80, 24) }
  width * height
}
`

// posOf is the 1-based position of the nth (0-based) occurrence of word on
// the line containing marker.
func posOf(t *testing.T, src, marker, word string, nth int) analysis.Pos {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		col := -1
		rest := line
		offset := 0
		for k := 0; k <= nth; k++ {
			j := strings.Index(rest, word)
			if j < 0 {
				t.Fatalf("%q has no occurrence %d of %q", line, nth, word)
			}
			col = offset + j
			offset = col + len(word)
			rest = line[offset:]
		}
		return analysis.Pos{Line: i + 1, Col: col + 1}
	}
	t.Fatalf("no line contains %q", marker)
	return analysis.Pos{}
}

// A name a binding's pattern binds, and one an else arm binds, resolve from
// their reads to their definitions and hover with their types.
func TestBindingElse_HoverAndDefinition(t *testing.T) {
	lib := std.Load()
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(bindingElseSrc))
	file := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	typeErrs := analysis.BuildTypes(file, nodes)
	checkErrs := analysis.CheckTypes(file, nodes)
	if len(typeErrs)+len(checkErrs) > 0 {
		t.Fatalf("unexpected type errors: %v %v", typeErrs, checkErrs)
	}
	for _, tc := range []struct {
		name     string
		use, def analysis.Pos
		hover    string
	}{
		{"pattern name", posOf(t, bindingElseSrc, "${user}!", "user", 0), posOf(t, bindingElseSrc, "Ok(user) = load", "user", 0), "```nomi\nuser: String\n```"},
		{"arm name", posOf(t, bindingElseSrc, "broken: ${", "err", 0), posOf(t, bindingElseSrc, "Err(err) ->", "err", 0), "```nomi\nerr: LoadError\n```"},
		{"fallback-bound name", posOf(t, bindingElseSrc, "width * height", "width", 0), posOf(t, bindingElseSrc, "Ok((width, height))", "width", 0), "```nomi\nwidth: Int\n```"},
	} {
		sym := file.SymbolAt(tc.use)
		if sym == nil {
			t.Errorf("%s: no symbol at %v", tc.name, tc.use)
			continue
		}
		if sym.Pos != tc.def {
			t.Errorf("%s: resolves to %v, want the definition at %v", tc.name, sym.Pos, tc.def)
		}
		if got := renderHover(sym); got != tc.hover {
			t.Errorf("%s: hover %q, want %q", tc.name, got, tc.hover)
		}
	}
}

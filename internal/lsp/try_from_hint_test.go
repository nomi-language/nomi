package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

const tryFromHintSrc = `enum DiskError
    Full
end

enum ApiError
    Disk DiskError
end

impl From<DiskError> for ApiError
    fn from(e: DiskError) -> ApiError
        ApiError.Disk(e)
    end
end

fn disk() -> Result<Int, DiskError>
    Err(.Full)
end

fn count() -> Result<Int, ApiError>
    n = disk()?
    m = disk()?
    Ok(n + m)
end

fn same() -> Result<Int, DiskError>
    n = disk()?
    Ok(n)
end
`

// A `?` that converts its error through a `From` impl shows the impl as an
// inlay hint after the `?`, and its hover says so. A `?` whose error types
// agree shows neither.
func TestTryFrom_HintAndHoverNameTheImpl(t *testing.T) {
	lib := loadStd()
	nodes, errs := parser.ParseWithRecovery(lexer.Lex(tryFromHintSrc))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, "/tmp", nil)
	if terrs := analysis.CheckTypes(fa, nodes); len(terrs) > 0 {
		t.Fatalf("the front end rejects a `?` the From impl converts: %v", terrs)
	}
	var got []string
	for _, h := range collectInlayHints(fa, nodes) {
		if strings.HasPrefix(h.Label, "From<") {
			got = append(got, h.Label)
			if h.Position.Line != 19 && h.Position.Line != 20 {
				t.Errorf("the hint %q is on line %d, want the converting `?`s on lines 20 and 21", h.Label, h.Position.Line+1)
			}
			if h.Position.Character != 15 || !h.PaddingLeft {
				t.Errorf("the hint %q is at character %d, want 15, after the `?`, padded", h.Label, h.Position.Character)
			}
		}
	}
	if len(got) != 2 || got[0] != "From<DiskError>" || got[1] != "From<DiskError>" {
		t.Fatalf("conversion hints %q, want From<DiskError> at each of the two converting `?`s", got)
	}

	converting := fa.SymbolAt(analysis.Pos{Line: 20, Col: 15})
	if converting == nil || converting.Kind != analysis.SymbolTryOp {
		t.Fatalf("no `?` symbol at 20:15: %+v", converting)
	}
	want := "```nomi\n?: Result<Int, DiskError> -> Int\n```\n\nOn `Err: DiskError`, unwinds to `fn count`.\n\n" +
		"Converts the error with `impl From<DiskError> for ApiError`: returns `Err(ApiError.from(e))`."
	if hover := renderHover(converting); hover != want {
		t.Errorf("hover on a converting `?`:\n%s\nwant:\n%s", hover, want)
	}
	plain := fa.SymbolAt(analysis.Pos{Line: 26, Col: 15})
	if plain == nil || plain.Kind != analysis.SymbolTryOp {
		t.Fatalf("no `?` symbol at 26:15: %+v", plain)
	}
	if hover := renderHover(plain); strings.Contains(hover, "Converts") {
		t.Errorf("hover on a `?` whose error types agree names a conversion:\n%s", hover)
	}
}

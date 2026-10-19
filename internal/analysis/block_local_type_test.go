package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// Two blocks each declaring a struct `Point` with different fields: a field
// read or a literal's field label resolves to the field of the `Point` in
// scope at the use, in every fresh analysis. Go randomizes map order per map,
// so an answer picked by walking the file's definitions in map order shows up
// as a different answer on some of the runs.
func TestBlockLocalType_SameNamedStructsResolveFieldsToTheDeclarationInScope(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "irbuild", "testdata", "block_local_types.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	// The use (line:col of the field name) and the field it names.
	want := map[analysis.Pos]analysis.Pos{
		{Line: 19, Col: 16}: {Line: 15, Col: 9}, // `x` of `#Point{x: 3, y: 4}` in first
		{Line: 20, Col: 7}:  {Line: 15, Col: 9}, // `p.x`
		{Line: 20, Col: 13}: {Line: 16, Col: 9}, // `p.y`
		{Line: 28, Col: 12}: {Line: 25, Col: 9}, // `name` of `#Point{name: "second"}` in second
		{Line: 28, Col: 28}: {Line: 25, Col: 9}, // `.name`
	}
	lib := std.Load()
	for run := 1; run <= 40; run++ {
		nodes, perrs := parser.ParseWithRecovery(lexer.Lex(string(src)))
		if len(perrs) > 0 {
			t.Fatalf("the fixture does not parse: %v", perrs)
		}
		fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
		analysis.AttachStdlibProjectImpls(fa, lib.Files)
		errs := append(analysis.BuildTypes(fa, nodes), analysis.CheckTypes(fa, nodes)...)
		if len(errs) > 0 {
			t.Fatalf("run %d: the fixture does not check: %v", run, errs)
		}
		for use, decl := range want {
			sym := fa.References[use]
			for sym != nil && sym.Resolved != nil {
				sym = sym.Resolved
			}
			if sym == nil || sym.Kind != analysis.SymbolField || sym.Pos.Line != decl.Line || sym.Pos.Col != decl.Col {
				t.Fatalf("run %d: the field at %d:%d resolves to %+v, want the field declared at %d:%d", run, use.Line, use.Col, sym, decl.Line, decl.Col)
			}
		}
	}
}

// A type declared in a block names a type in the annotations of that block
// and its inner blocks, and nowhere else.
func TestBlockLocalType_IsKnownToAnnotationsInItsBlockOnly(t *testing.T) {
	errs := checkWithStdlib(`
fn main() {
  inner = {
    type Meters Int
    m: Meters = Meters(3)
    f = fn d: Meters = Meters(1) -> case d { Meters(n) -> n }
    g: (Meters) -> Int = fn Meters(n) -> n
    _ = {
      n: Meters = m
      n
    }
    f(m) + g(m)
  }
  _ = inner
  outside: Meters = 1
  _ = outside
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		`15:12 unknown type "Meters"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

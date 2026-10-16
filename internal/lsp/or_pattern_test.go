package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// orPatternSource binds `r` in both alternatives of an `or` pattern: line
// 9, columns 13 and 27 are the two sites, and column 33 the use.
const orPatternSource = `enum Shape {
  Circle {r: Float}
  Square {r: Float}
  Point
}

fn radius(s: Shape): Float {
  case s {
    .Circle{r} or .Square{r} -> r
    .Point -> 0.0
  }
}
`

var orSites = []analysis.Pos{{Line: 9, Col: 13}, {Line: 9, Col: 27}}

func TestOrPattern_HoverOnEitherSiteAndTheUse(t *testing.T) {
	file := checkedFile(orPatternSource)
	for _, pos := range append(append([]analysis.Pos{}, orSites...), analysis.Pos{Line: 9, Col: 33}) {
		sym := file.SymbolAt(pos)
		if sym == nil {
			t.Fatalf("no symbol at %v", pos)
		}
		if got, want := renderHover(sym), "```nomi\nr: Float\n```"; got != want {
			t.Errorf("hover at %v:\ngot  %q\nwant %q", pos, got, want)
		}
	}
}

func TestOrPattern_DefinitionIsEverySite(t *testing.T) {
	uri := "file:///or_pattern_definition.nomi"
	s := NewServer()
	s.docs.Open(uri, orPatternSource)
	for _, at := range []protocol.Position{{Line: 8, Character: 32}, {Line: 8, Character: 26}} {
		result, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: at}})
		if err != nil {
			t.Fatal(err)
		}
		locs, ok := result.([]protocol.Location)
		if !ok || len(locs) != 2 {
			t.Fatalf("definition at %v: want both sites, got %#v", at, result)
		}
		for i, loc := range locs {
			if int(loc.Range.Start.Line)+1 != orSites[i].Line || int(loc.Range.Start.Character)+1 != orSites[i].Col {
				t.Errorf("site %d: got %v, want %v", i, loc.Range.Start, orSites[i])
			}
		}
	}
}

func TestOrPattern_RenameEditsEverySiteAndTheUse(t *testing.T) {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(orPatternSource))
	fa := buildFile(nodes)
	sym := fa.SymbolAt(analysis.Pos{Line: 9, Col: 33})
	if sym == nil {
		t.Fatal("no symbol at the use of r")
	}
	target := sym
	if target.Resolved != nil {
		target = target.Resolved
	}
	edits := buildRenameEdits(fa, target, symbolIdentity(sym), "radius")
	if len(edits) != 3 {
		t.Fatalf("got %d edits, want 3 (both sites and the use): %+v", len(edits), edits)
	}
	cols := map[uint32]bool{}
	for _, e := range edits {
		if e.Range.Start.Line != 8 {
			t.Errorf("edit %+v: want line 9", e.Range)
		}
		cols[e.Range.Start.Character] = true
	}
	for _, c := range []uint32{12, 26, 32} {
		if !cols[c] {
			t.Errorf("no edit at 9:%d: %+v", c+1, edits)
		}
	}
}

func TestOrPattern_SemanticTokensMarkEverySiteAVariable(t *testing.T) {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(orPatternSource))
	fa := buildFile(nodes)
	data := encodeSemanticTokens(fa, nodes, orPatternSource)
	line, col := 1, 1
	found := 0
	for i := 0; i+4 < len(data); i += 5 {
		line += int(data[i])
		if data[i] > 0 {
			col = int(data[i+1]) + 1
		} else {
			col += int(data[i+1])
		}
		for _, site := range orSites {
			if line == site.Line && col == site.Col {
				found++
				if tokenTypes[data[i+3]] != "variable" {
					t.Errorf("token at %v: type %s, want variable", site, tokenTypes[data[i+3]])
				}
			}
		}
	}
	if found != len(orSites) {
		t.Errorf("tokens at %d of the %d sites", found, len(orSites))
	}
}

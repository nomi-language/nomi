package lsp

import (
	"encoding/json"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestSemanticTokens_BasicFunction(t *testing.T) {
	src := "fn double(n: Int): Int { n + n }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)

	data := encodeSemanticTokens(fa, nodes)

	if len(data)%5 != 0 {
		t.Fatalf("encoded data length %d is not a multiple of 5", len(data))
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data")
	}

	// Decode back to semanticTokens and find "double"
	type decoded struct {
		line, col, length int
		tokenType         uint32
		modifiers         uint32
	}
	var toks []decoded
	line, col := 1, 1
	for i := 0; i+4 < len(data); i += 5 {
		deltaLine := int(data[i])
		deltaStart := int(data[i+1])
		length := int(data[i+2])
		tt := data[i+3]
		mod := data[i+4]

		line += deltaLine
		if deltaLine > 0 {
			col = int(deltaStart) + 1 // 0-based back to 1-based
		} else {
			col += deltaStart
		}
		toks = append(toks, decoded{line, col, length, tt, mod})
	}

	// Find "double" — it's at line 1, col 4, length 6
	found := false
	for _, tok := range toks {
		if tok.line == 1 && tok.col == 4 && tok.length == 6 {
			found = true
			if tok.tokenType != 0 {
				t.Errorf("expected tokenType 0 (function), got %d", tok.tokenType)
			}
			if tok.modifiers != 0 {
				t.Errorf("expected no modifiers on function definition, got %d", tok.modifiers)
			}
		}
	}
	if !found {
		t.Errorf("did not find 'double' token at expected position; decoded tokens: %v", toks)
	}
}

// TestSemanticTokens_AttachedTestTokensCarryTheModifier pins that a token on
// a `//!` line keeps its type and gains the attachedTest modifier (the editors
// dim attached tests through it), and that no token outside one carries it:
// the declaration under the test, the function body, and the `//` line
// between two prompt groups.
func TestSemanticTokens_AttachedTestTokensCarryTheModifier(t *testing.T) {
	src := `struct Label {
  text: String
}

//! assert ready_label() == "[ready]"
//
//! label = ready_label()
//! assert label == "[ready]"
fn ready_label(): String {
  "[ready]"
}

impl Label {
  //! assert Label.wrap("x").text == "[x]"
  fn wrap(text: String): Label {
    Label{text: "[${text}]"}
  }
}
`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)
	if sym := fa.References[analysis.Pos{Line: 5, Col: 12}]; sym == nil || sym.Name != "ready_label" {
		t.Fatalf("expected attached test call to keep analysis reference, got %#v", sym)
	}
	toks := decodeSemanticTokenData(encodeSemanticTokens(fa, nodes))

	// 1-based (line, col) of each identifier, and whether it is on a `//!` line.
	want := []struct {
		line, col int
		name      string
		attached  bool
		tokenType uint32
	}{
		{5, 12, "ready_label", true, 0},
		{7, 5, "label", true, 7},
		{7, 13, "ready_label", true, 0},
		{8, 12, "label", true, 7},
		{9, 4, "ready_label", false, 0},
		{14, 14, "Label", true, 1},
		{15, 6, "wrap", false, 0},
		{15, 11, "text", false, 6},
		{16, 21, "text", false, 6},
	}
	for _, w := range want {
		var got *decodedSemanticToken
		for i := range toks {
			if toks[i].line == w.line-1 && toks[i].col == w.col-1 {
				got = &toks[i]
			}
		}
		if got == nil {
			t.Errorf("no token for %q at %d:%d; tokens: %+v", w.name, w.line, w.col, toks)
			continue
		}
		if got.length != len(w.name) || got.tokenType != w.tokenType {
			t.Errorf("%q at %d:%d: length %d type %d, want length %d type %d", w.name, w.line, w.col, got.length, got.tokenType, len(w.name), w.tokenType)
		}
		if has := got.modifiers&modAttachedTest != 0; has != w.attached {
			t.Errorf("%q at %d:%d: attachedTest modifier = %v, want %v", w.name, w.line, w.col, has, w.attached)
		}
	}
	for _, tok := range toks {
		if tok.line == 5 && tok.modifiers&modAttachedTest != 0 {
			t.Errorf("token on the `//` line between prompt groups carries attachedTest: %+v", tok)
		}
	}
}

// TestSemanticTokens_LegendNamesAttachedTest pins the modifier's legend name
// and bit: editors match the name (Zed's semantic_token_rules.json, Neovim's
// @lsp.mod.attachedTest), and the encoder sets the bit at that name's index.
func TestSemanticTokens_LegendNamesAttachedTest(t *testing.T) {
	for i, name := range tokenModifiers {
		if name == "attachedTest" {
			if 1<<i != modAttachedTest {
				t.Fatalf("attachedTest is legend index %d but modAttachedTest is %d", i, modAttachedTest)
			}
			return
		}
	}
	t.Fatalf("legend %v has no attachedTest modifier", tokenModifiers)
}

func TestSemanticTokens_SourceGoBindingDoesNotPaintImportLine(t *testing.T) {
	root := stageGoFFIDefinitionProject(t)
	mainPath := filepath.Join(root, "main.nomi")
	src := `import std/io

gopkg "example.com/binding/ffi" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(echo_upper("fixture"))
}
`
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	s := NewServer()
	uri := pathToURI(mainPath)
	doc := s.docs.Open(uri, src)
	data := encodeSemanticTokens(doc.Analysis, doc.Nodes)
	toks := decodeSemanticTokenData(data)

	for _, tok := range toks {
		if tok.line == 0 {
			t.Fatalf("semantic token leaked into import line: %+v; all tokens: %+v", tok, toks)
		}
	}
}

func TestSemanticTokens_AllKinds(t *testing.T) {
	src := `struct Point { x: Int; y: Int }
enum Color { Red; Green; Blue }
fn move(p: Point): Point { p }`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)

	data := encodeSemanticTokens(fa, nodes)

	if len(data)%5 != 0 {
		t.Fatalf("encoded data length %d is not a multiple of 5", len(data))
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty encoded data for multi-kind source")
	}
}

func TestSemanticTokens_EmptyFile(t *testing.T) {
	fa := &analysis.FileAnalysis{
		Definitions: map[analysis.Pos]*analysis.Symbol{},
		References:  map[analysis.Pos]*analysis.Symbol{},
	}

	data := encodeSemanticTokens(fa, nil)

	if len(data) != 0 {
		t.Fatalf("expected empty data for empty file, got %d elements", len(data))
	}
	got, err := json.Marshal(protocol.SemanticTokens{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"data":[]`) {
		t.Fatalf("an empty file must send `\"data\":[]` (Neovim crashes on null), got %s", got)
	}
}

type decodedSemanticToken struct {
	line, col, length int
	tokenType         uint32
	modifiers         uint32
}

func decodeSemanticTokenData(data []protocol.UInteger) []decodedSemanticToken {
	var toks []decodedSemanticToken
	line, col := 0, 0
	for i := 0; i+4 < len(data); i += 5 {
		deltaLine := int(data[i])
		deltaStart := int(data[i+1])
		length := int(data[i+2])
		tt := data[i+3]
		mod := data[i+4]

		line += deltaLine
		if deltaLine > 0 {
			col = deltaStart
		} else {
			col += deltaStart
		}
		toks = append(toks, decodedSemanticToken{line, col, length, tt, mod})
	}
	return toks
}

// TestStructDestructure_PunningHasNoFieldCollision verifies that a punning
// struct destructure (`{x, y} = point`) registers exactly one symbol per
// binding position — the SymbolBinding — and does not also register a
// SymbolField at the same position. The two used to coexist: the analyzer's
// builder put a SymbolBinding in Definitions and the checker put a
// SymbolField in References at the identical position. Go's randomized map
// iteration meant `x` and `y` rendered with different semantic-token kinds
// from one LSP run to the next.
func TestStructDestructure_PunningHasNoFieldCollision(t *testing.T) {
	src := `fn main() {
  point = {x: 10, y: 20}
  {x, y} = point
}
`
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)

	// Locate the binding positions for `x` and `y` on line 3.
	xPos := analysis.Pos{Line: 3, Col: 4}
	yPos := analysis.Pos{Line: 3, Col: 7}

	xDef, hasXDef := fa.Definitions[xPos]
	if !hasXDef {
		t.Fatalf("expected SymbolBinding definition for `x` at %v", xPos)
	}
	if xDef.Kind != analysis.SymbolBinding {
		t.Errorf("`x` definition: kind=%v, want SymbolBinding", xDef.Kind)
	}
	yDef, hasYDef := fa.Definitions[yPos]
	if !hasYDef {
		t.Fatalf("expected SymbolBinding definition for `y` at %v", yPos)
	}
	if yDef.Kind != analysis.SymbolBinding {
		t.Errorf("`y` definition: kind=%v, want SymbolBinding", yDef.Kind)
	}

	// The bug: a SymbolField reference was also registered at the same
	// position. After the fix, the punning case skips this registration
	// (the binding's hover info already covers it).
	if ref, ok := fa.References[xPos]; ok {
		t.Errorf("`x` should not have a colliding reference; got kind=%v name=%s", ref.Kind, ref.Name)
	}
	if ref, ok := fa.References[yPos]; ok {
		t.Errorf("`y` should not have a colliding reference; got kind=%v name=%s", ref.Kind, ref.Name)
	}
}

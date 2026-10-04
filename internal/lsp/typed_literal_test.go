package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// typedLiteralProject is a small two-module fixture: main.nomi imports
// the `Joiner` tag type and uses `Joiner"..."`. Reused across LSP tests
// so each can focus on a single behaviour.
type typedLiteralProject struct {
	mainSrc   string
	joinerSrc string
}

func newTypedLiteralProject() typedLiteralProject {
	return typedLiteralProject{
		mainSrc: `import {
  std/io
  joiner: Joiner
}

fn main() {
  name = "Alice"
  msg = Joiner"hi ${name}"
  io.print(msg)
}
`,
		joinerSrc: `import std/literals.{Fragment, Literal}

pub type Joiner

impl Literal for Joiner {
  fn from_fragments(fragments: List<Fragment<Display>>): String {
    Iter.reduce(fragments, |acc = "", frag|
      case frag {
        .Static(s) -> acc + s
        .Dynamic(v) -> acc + Display.to_string(v)
      }
    )
  }
}

`,
	}
}

// buildTaggedProject builds a multi-file analysis using the in-memory
// loader pattern. Returns the entry-file FileAnalysis.
func buildTaggedProject(t *testing.T, p typedLiteralProject) *analysis.FileAnalysis {
	t.Helper()
	lib := std.Load()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		key := strings.Join(modulePath, "/")
		var src string
		switch key {
		case "joiner":
			src = p.joinerSrc
		default:
			return nil, nil
		}
		toks := lexer.Lex(src)
		nodes, _ := parser.ParseWithRecovery(toks)
		return nodes, nil
	}
	tokens := lexer.Lex(p.mainSrc)
	mainNodes, _ := parser.ParseWithRecovery(tokens)
	fa := analysis.BuildProject(mainNodes, lib.Primitives, lib.Modules, lib.Files, "/p", loader)
	if fa == nil {
		t.Fatal("BuildProject returned nil")
	}
	analysis.CheckTypes(fa, mainNodes)
	return fa
}

// TestTypedLiteral_LexerTagPosition probes that the lexer's tagged
// string token Line/Col is at the opening quote, not the tag identifier.
// Confirms the assumption used by hover/definition tag-position math
// (tag column = token column - len(tag)).
func TestTypedLiteral_LexerTagPosition(t *testing.T) {
	src := `Sql"SELECT 1"`
	toks := lexer.Lex(src)
	if len(toks) < 1 {
		t.Fatal("no tokens")
	}
	if toks[0].Line != 1 || toks[0].Col != 4 {
		t.Errorf("expected token at line 1 col 4 (the opening quote), got line %d col %d", toks[0].Line, toks[0].Col)
	}
	if toks[0].Tag != "Sql" {
		t.Errorf("expected tag 'Sql', got %q", toks[0].Tag)
	}
}

// Verify a Reference is registered at the tag identifier position
// (column 9 in main.nomi line 8: `  msg = Joiner"hi ${name}"`).
func TestTypedLiteral_ReferenceAtTagIdentifier(t *testing.T) {
	p := newTypedLiteralProject()
	fa := buildTaggedProject(t, p)
	// In `  msg = Joiner"hi ${name}"`, "Joiner" starts at col 9 (1-based).
	tagPos := analysis.Pos{Line: 8, Col: 9}
	ref := fa.References[tagPos]
	if ref == nil {
		t.Fatalf("expected a Reference at line 8 col 9 (start of `Joiner` tag); got nil. References at line 8: %v", refsAtLine(fa, 8))
	}
}

// Hover on the tag identifier should return the handler's signature.
func TestTypedLiteral_HoverOnTag(t *testing.T) {
	p := newTypedLiteralProject()
	uri := "file:///p/main.nomi"

	s := NewServer()
	// Open joiner.nomi first so it's in the doc manager (its URI must
	// match BuildProject's view).
	s.docs.Open("file:///p/joiner.nomi", p.joinerSrc)
	s.docs.Open(uri, p.mainSrc)

	// Cursor on `Joiner` tag identifier — line 8 (1-based), col 9 ⇒
	// LSP zero-based line 7, char 8.
	params := &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: 7, Character: 8},
		},
	}
	res, err := s.textDocumentHover(nil, params)
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if res == nil {
		t.Fatal("expected hover content for tag identifier, got nil")
	}
	mc, ok := res.Contents.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("expected MarkupContent, got %T", res.Contents)
	}
	if !strings.Contains(mc.Value, "from_fragments") || !strings.Contains(mc.Value, "Fragment") {
		t.Errorf("expected hover to show the from_fragments handler signature; got: %s", mc.Value)
	}
}

// Hover on a slot variable (the `name` inside `${name}`) should return
// the variable's binding info, just like normal expression hover.
func TestTypedLiteral_HoverOnSlotVariable(t *testing.T) {
	p := newTypedLiteralProject()
	uri := "file:///p/main.nomi"

	s := NewServer()
	s.docs.Open("file:///p/joiner.nomi", p.joinerSrc)
	s.docs.Open(uri, p.mainSrc)

	// `${name}` body in `  msg = Joiner"hi ${name}"`. The `${` starts at
	// col 19 (after `Joiner"hi `), `name` starts at col 21. LSP zero-based
	// line 7, char 20.
	params := &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: 7, Character: 20},
		},
	}
	res, err := s.textDocumentHover(nil, params)
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if res == nil {
		t.Fatal("expected hover content for slot variable, got nil")
	}
	mc, ok := res.Contents.(protocol.MarkupContent)
	if !ok {
		t.Fatalf("expected MarkupContent, got %T", res.Contents)
	}
	if !strings.Contains(mc.Value, "name") {
		t.Errorf("expected hover to mention 'name'; got: %s", mc.Value)
	}
}

// Goto-definition on the tag identifier jumps into joiner.nomi (the tag
// type / its Literal handler).
func TestTypedLiteral_GotoDefinitionOnTag(t *testing.T) {
	p := newTypedLiteralProject()
	uri := "file:///p/main.nomi"

	s := NewServer()
	s.docs.Open("file:///p/joiner.nomi", p.joinerSrc)
	s.docs.Open(uri, p.mainSrc)

	params := &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: 7, Character: 8},
		},
	}
	res, err := s.textDocumentDefinition(nil, params)
	if err != nil {
		t.Fatalf("definition error: %v", err)
	}
	if res == nil {
		t.Fatal("expected goto-definition target for tag identifier, got nil")
	}
	loc, ok := res.(*protocol.Location)
	if !ok {
		t.Fatalf("expected *protocol.Location, got %T", res)
	}
	if !strings.Contains(string(loc.URI), "joiner.nomi") {
		t.Errorf("expected jump into joiner.nomi, got %s", loc.URI)
	}
}

// Diagnostic for tag-not-in-scope fires at the tag identifier position
// (so editors highlight just the tag, not the entire literal).
func TestTypedLiteral_DiagnosticTagNotInScope(t *testing.T) {
	src := `fn main() {
  q = Nope"hi"
}
`
	dm := analysis.NewDocumentManager()
	doc := dm.Open("file:///p/main.nomi", src)
	if doc.Analysis == nil {
		t.Fatal("expected analysis")
	}
	found := false
	for _, e := range doc.Analysis.TypeErrors {
		if strings.Contains(e.Message, "not in scope") && strings.Contains(e.Message, "Nope") {
			found = true
			// `Nope` starts at col 7 (1-based) on line 2.
			if e.Line != 2 || e.Col != 7 {
				t.Errorf("expected diagnostic at line 2 col 7, got line %d col %d", e.Line, e.Col)
			}
		}
	}
	if !found {
		t.Fatalf("expected tag-not-in-scope diagnostic; got: %v", doc.Analysis.TypeErrors)
	}
}

// Diagnostic for slot-type mismatch fires at the slot expression's
// position, not the tag site.
func TestTypedLiteral_DiagnosticSlotMismatch(t *testing.T) {
	// `Joiner` requires Display on slots; a function value has no
	// Display impl, so this is a slot-type mismatch.
	mainSrc := `import joiner.{Joiner}

fn helper(x: Int): Int { x + 1 }

fn main() {
  msg = Joiner"x = ${helper}"
}
`
	joinerSrc := `import std/literals.{Fragment, Literal}

pub type Joiner

impl Literal for Joiner {
  fn from_fragments(fragments: List<Fragment<Display>>): String {
    Iter.reduce(fragments, |acc = "", _frag| acc)
  }
}

`
	lib := std.Load()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		if strings.Join(modulePath, "/") == "joiner" {
			toks := lexer.Lex(joinerSrc)
			ns, _ := parser.ParseWithRecovery(toks)
			return ns, nil
		}
		return nil, nil
	}
	tokens := lexer.Lex(mainSrc)
	mainNodes, _ := parser.ParseWithRecovery(tokens)
	fa := analysis.BuildProject(mainNodes, lib.Primitives, lib.Modules, lib.Files, "/p", loader)
	if fa == nil {
		t.Fatal("BuildProject returned nil")
	}
	checkErrs := analysis.CheckTypes(fa, mainNodes)
	all := append([]analysis.TypeError{}, fa.TypeErrors...)
	all = append(all, checkErrs...)
	found := false
	for _, e := range all {
		if strings.Contains(strings.ToLower(e.Message), "display") {
			found = true
			// The slot `${helper}` has `helper` starting on line 6 of
			// mainSrc. Diagnostic should land at the slot.
			if e.Line != 6 {
				t.Errorf("expected slot-mismatch diagnostic on line 6, got line %d (msg: %s)", e.Line, e.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected slot-type mismatch diagnostic; got %d errors:\n%v", len(all), all)
	}
}

// Inlay hints walk into typed-literal slot expressions so a nested
// call inside `${expr}` produces parameter hints, mirroring how slot
// expressions get type-checked. Static parts have no expressions and
// produce no hints.
func TestTypedLiteral_InlayHintsWalkSlots(t *testing.T) {
	mainSrc := `import joiner.{Joiner}

fn add(x: Int, y: Int): Int { x + y }

fn main() {
  msg = Joiner"sum: ${add(1, 2)}"
}
`
	joinerSrc := `import std/literals.{Fragment, Literal}

pub type Joiner

impl Literal for Joiner {
  fn from_fragments(fragments: List<Fragment<Display>>): String {
    Iter.reduce(fragments, |acc = "", _frag| acc)
  }
}

`
	lib := std.Load()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		if strings.Join(modulePath, "/") == "joiner" {
			toks := lexer.Lex(joinerSrc)
			ns, _ := parser.ParseWithRecovery(toks)
			return ns, nil
		}
		return nil, nil
	}
	tokens := lexer.Lex(mainSrc)
	mainNodes, _ := parser.ParseWithRecovery(tokens)
	fa := analysis.BuildProject(mainNodes, lib.Primitives, lib.Modules, lib.Files, "/p", loader)
	analysis.CheckTypes(fa, mainNodes)
	hints := collectInlayHints(fa, mainNodes)
	// `add(1, 2)` inside `${...}` should produce two parameter hints
	// (`x:`, `y:`) — without the TaggedString case in inlay_hints.go's
	// walker, the slot would be skipped and no hints would emit.
	got := 0
	for _, h := range hints {
		if h.Kind != nil && *h.Kind == InlayHintKindParameter {
			got++
		}
	}
	if got != 2 {
		t.Errorf("expected 2 parameter inlay hints inside the typed-literal slot, got %d: %+v", got, hints)
	}
}

// refsAtLine collects all reference positions on a given source line
// for diagnostic messages.
func refsAtLine(fa *analysis.FileAnalysis, line int) []analysis.Pos {
	var out []analysis.Pos
	for pos := range fa.References {
		if pos.Line == line {
			out = append(out, pos)
		}
	}
	return out
}

package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestFindNomiFiles(t *testing.T) {
	dir := t.TempDir()
	// Create files
	os.WriteFile(filepath.Join(dir, "main.nomi"), []byte("fn main() {}"), 0644)
	os.WriteFile(filepath.Join(dir, "math.nomi"), []byte("fn double(n: Int): Int { n + n }"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not nomi"), 0644)
	// Create subdirectory with a .nomi file
	os.MkdirAll(filepath.Join(dir, "utils"), 0755)
	os.WriteFile(filepath.Join(dir, "utils", "helpers.nomi"), []byte("fn helper(): Int { 1 }"), 0644)
	// Create hidden directory that should be skipped
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	os.WriteFile(filepath.Join(dir, ".git", "config.nomi"), []byte("ignored"), 0644)

	files := findNomiFiles(dir)
	sort.Strings(files)

	expected := []string{
		filepath.Join(dir, "main.nomi"),
		filepath.Join(dir, "math.nomi"),
		filepath.Join(dir, "utils", "helpers.nomi"),
	}
	sort.Strings(expected)

	if len(files) != len(expected) {
		t.Fatalf("expected %d files, got %d: %v", len(expected), len(files), files)
	}
	for i, f := range files {
		if f != expected[i] {
			t.Errorf("file %d: expected %s, got %s", i, expected[i], f)
		}
	}
}

func TestFindReferences_SameFile(t *testing.T) {
	src := "fn double(n: Int): Int { n + n }\nfn main() { double(1) }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)

	// "double" at line 2, col 17 is a reference to the function
	sym := fa.SymbolAt(analysis.Pos{Line: 2, Col: 17})
	if sym == nil {
		t.Fatal("expected symbol at reference site")
	}

	// Count references (pointer equality since same analysis)
	var refs []analysis.Pos
	for pos, s := range fa.References {
		if s == sym {
			refs = append(refs, pos)
		}
	}
	if len(refs) != 1 {
		t.Errorf("expected 1 reference, got %d", len(refs))
	}

	// Also check definition is findable
	var defs []analysis.Pos
	for pos, s := range fa.Definitions {
		if s == sym {
			defs = append(defs, pos)
		}
	}
	if len(defs) != 1 {
		t.Errorf("expected 1 definition, got %d", len(defs))
	}
}

func TestFindReferences_CrossFile(t *testing.T) {
	dir := t.TempDir()
	mathFile := filepath.Join(dir, "math.nomi")
	mainFile := filepath.Join(dir, "main.nomi")

	os.WriteFile(mathFile, []byte("/// Doubles it.\npub fn double(n: Int): Int { n + n }"), 0644)
	os.WriteFile(mainFile, []byte("import math\nfn main() { math.double(21) }"), 0644)

	dm := analysis.NewDocumentManager()
	mainURI := "file://" + mainFile

	// Open main file
	doc := dm.Open(mainURI, "import math\nfn main() { math.double(21) }")

	// Find "double" reference in main.nomi.
	sym := doc.Analysis.SymbolAt(analysis.Pos{Line: 2, Col: 18})
	if sym == nil {
		t.Fatal("expected double symbol")
	}

	// Use the matching logic
	id := symbolIdentity(sym)
	if id.Name != "double" {
		t.Errorf("expected name double, got %s", id.Name)
	}

	// Analyze math.nomi and check for definition match
	mathSnap := dm.Analyzed("file://" + mathFile)
	if mathSnap == nil || mathSnap.Analysis == nil {
		t.Fatal("expected analysis for math.nomi")
	}
	mathFA := mathSnap.Analysis

	var found bool
	for _, s := range mathFA.Definitions {
		if matchesIdentity(s, id) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected to find double definition in math.nomi")
	}
}

func TestFindReferences_IncludeDeclaration(t *testing.T) {
	src := "fn greet(): String { \"hi\" }\nfn main() { greet() }"
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	fa := buildFile(nodes)

	sym := fa.SymbolAt(analysis.Pos{Line: 1, Col: 8})
	if sym == nil {
		t.Fatal("expected Greet definition symbol")
	}

	// Count definitions
	defCount := 0
	for _, s := range fa.Definitions {
		if s == sym {
			defCount++
		}
	}
	// Count references
	refCount := 0
	for _, s := range fa.References {
		if s == sym {
			refCount++
		}
	}

	if defCount != 1 {
		t.Errorf("expected 1 definition, got %d", defCount)
	}
	if refCount != 1 {
		t.Errorf("expected 1 reference, got %d", refCount)
	}
}

// TestFindReferences_DeriveSkipsSynthBand probes a file with
// `derive Equatable for Point` and a call `Equatable.equal?(p1, p2)`, then runs
// Find References on `equals` at the call site. The synthesizer registers a synthetic `equals`
// FuncDef in the synth-band (line >= 1<<30); without filtering, those phantom
// definitions surface as Locations the editor jumps to past EOF. The handler
// must skip them.
func TestFindReferences_DeriveSkipsSynthBand(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.nomi")
	src := `import std/equatable.{Equatable}

pub struct Point {
  x: Int
  y: Int
}

derive Equatable for Point

fn main() {
  p1 = Point{x: 1, y: 2}
  p2 = Point{x: 1, y: 2}
  result = Equatable.equal?(p1, p2)
  result
}
`
	os.WriteFile(mainFile, []byte(src), 0644)

	s := NewServer()
	uri := "file://" + mainFile
	s.docs.Open(uri, src)

	// `equal?` at the call site sits on line 13; the identifier starts
	// at column 22 ("  result = Equatable.equal?(p1, p2)"). Click inside
	// `equal?` (line 13 — zero-based line 12, char 23).
	params := &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: 12, Character: 23},
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	}

	locs, err := s.textDocumentReferences(nil, params)
	if err != nil {
		t.Fatalf("references request returned error: %v", err)
	}
	if len(locs) == 0 {
		t.Fatal("expected at least one Location for `equals`")
	}
	for _, loc := range locs {
		// LSP positions are 0-based; the synth-band is at line >= 1<<30
		// (1-based) which is >= (1<<30)-1 zero-based — both the same
		// order of magnitude. Anything past, say, 1<<20 is unmistakably
		// synth-band.
		if loc.Range.Start.Line >= (1 << 20) {
			t.Errorf("expected real-position Location, got synth-band at line %d (URI=%s)", loc.Range.Start.Line, loc.URI)
		}
	}
}

func TestFindReferences_SelectiveImportFollowsResolved(t *testing.T) {
	dir := t.TempDir()
	modelsFile := filepath.Join(dir, "models.nomi")
	mainFile := filepath.Join(dir, "main.nomi")

	os.WriteFile(modelsFile, []byte("struct User { name: String }"), 0644)
	os.WriteFile(mainFile, []byte("import models.{User}\nfn main() { User{name: \"Alice\"} }"), 0644)

	dm := analysis.NewDocumentManager()
	mainURI := "file://" + mainFile
	doc := dm.Open(mainURI, "import models.{User}\nfn main() { User{name: \"Alice\"} }")

	// Find User on the import line — should have Resolved pointing to real symbol
	sym := doc.Analysis.SymbolAt(analysis.Pos{Line: 1, Col: 17})
	if sym == nil {
		t.Fatal("expected User symbol on import line")
	}
	if sym.Resolved == nil {
		t.Fatal("expected Resolved pointer for selective import")
	}

	id := symbolIdentity(sym)
	if id.Name != "User" {
		t.Errorf("expected identity name User, got %s", id.Name)
	}
	if id.File == "" {
		t.Error("expected identity to have source file")
	}
}

func TestFindReferences_InterfaceMethodIncludesImplDeclarations(t *testing.T) {
	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.nomi")
	src := "interface Stepper {\n" +
		"  fn next(value: self): Int\n" +
		"  open fn steps_between(start: self, end: self): Int {\n" +
		"    0\n" +
		"  }\n" +
		"}\n" +
		"\n" +
		"struct counter { n: Int }\n" +
		"struct Code { n: Int }\n" +
		"\n" +
		"impl Stepper for counter {\n" +
		"  fn next(value: self): Int { value.n + 1 }\n" +
		"  fn steps_between(start: self, end: self): Int { end.n - start.n }\n" +
		"}\n" +
		"\n" +
		"impl Stepper for Code {\n" +
		"  fn next(value: self): Int { value.n + 1 }\n" +
		"  fn steps_between(start: self, end: self): Int { end.n - start.n }\n" +
		"}\n"
	if err := os.WriteFile(mainFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	s := NewServer()
	uri := "file://" + mainFile
	s.docs.Open(uri, src)

	params := &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			// Cursor on `steps_between` in the interface body.
			Position: protocol.Position{Line: 2, Character: 12},
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	}

	locs, err := s.textDocumentReferences(nil, params)
	if err != nil {
		t.Fatalf("references request returned error: %v", err)
	}
	wantLines := map[uint32]bool{
		2:  false, // interface declaration
		12: false, // counter impl
		17: false, // Code impl
	}
	for _, loc := range locs {
		if _, ok := wantLines[loc.Range.Start.Line]; ok {
			wantLines[loc.Range.Start.Line] = true
		}
	}
	for line, found := range wantLines {
		if !found {
			t.Fatalf("expected references to include zero-based line %d, got %+v", line, locs)
		}
	}
}

func TestFindReferences_StdlibInterfaceMethodIncludesStdlibImplDeclarations(t *testing.T) {
	discreteFile := filepath.Join("..", "..", "std", "discrete.nomi")
	srcBytes, err := os.ReadFile(discreteFile)
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	idx := strings.Index(src, "steps_between")
	if idx < 0 {
		t.Fatal("expected std/discrete.nomi to declare steps_between")
	}
	line := uint32(strings.Count(src[:idx], "\n"))
	lineStart := strings.LastIndex(src[:idx], "\n") + 1
	char := uint32(idx - lineStart)

	absDiscrete, err := filepath.Abs(discreteFile)
	if err != nil {
		t.Fatal(err)
	}
	uri := "file://" + absDiscrete
	s := NewServer()
	s.docs.Open(uri, src)

	locs, err := s.textDocumentReferences(nil, &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: line, Character: char},
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatalf("references request returned error: %v", err)
	}
	wantFiles := map[string]bool{
		"/std/int.nomi":        false,
		"/std/codepoints.nomi": false,
	}
	for _, loc := range locs {
		for suffix := range wantFiles {
			if strings.HasSuffix(string(loc.URI), suffix) {
				wantFiles[suffix] = true
			}
		}
	}
	for suffix, found := range wantFiles {
		if !found {
			t.Fatalf("expected references to include %s, got %+v", suffix, locs)
		}
	}
}

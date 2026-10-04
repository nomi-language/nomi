package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// buildMissing analyzes src with the stdlib wired in and returns the
// FindMissingImports result.
func buildMissing(t *testing.T, src string) []analysis.MissingImport {
	t.Helper()
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	_ = analysis.BuildTypes(fa, nodes)
	_ = analysis.CheckTypes(fa, nodes)
	return analysis.FindMissingImports(fa, nodes)
}

func TestFindMissingImports_SuggestsStdlibNamespace(t *testing.T) {
	got := buildMissing(t, "fn main() {\n  io.inspect(42)\n}\n")
	if len(got) != 1 {
		t.Fatalf("want 1 missing import, got %d: %+v", len(got), got)
	}
	m := got[0]
	if m.Qualifier != "io" || m.ModulePath != "std/io" || m.Member != "" {
		t.Errorf("got %+v, want {Qualifier:io ModulePath:std/io Member:<empty>}", m)
	}
	if m.Pos.Line != 2 || m.Pos.Col != 3 {
		t.Errorf("pos = %+v, want {2 3}", m.Pos)
	}
}

func TestFindMissingImports_AlreadyImported(t *testing.T) {
	got := buildMissing(t, "import std/io\n\nfn main() {\n  io.inspect(42)\n}\n")
	if len(got) != 0 {
		t.Errorf("want no suggestions when already imported, got %+v", got)
	}
}

func TestFindMissingImports_ResolvesToLocalDefNoSuggestion(t *testing.T) {
	// A local `fn io` makes `io` resolve, so no import is suggested (and no
	// alias machinery is needed — a collision means the name already binds).
	got := buildMissing(t, "fn io(): Int {\n  5\n}\n\nfn main() {\n  io.inspect(42)\n}\n")
	if len(got) != 0 {
		t.Errorf("want no suggestions when qualifier resolves locally, got %+v", got)
	}
}

func TestFindMissingImports_UnknownQualifierNoSuggestion(t *testing.T) {
	got := buildMissing(t, "fn main() {\n  ioo.inspect(42)\n}\n")
	if len(got) != 0 {
		t.Errorf("want no suggestions for unknown qualifier, got %+v", got)
	}
}

func TestFindMissingImports_LowercaseModuleQualifierSuggestsNamespace(t *testing.T) {
	got := buildMissing(t, "fn main() {\n  Json.decode(\"{}\")\n}\n")
	if len(got) != 1 || got[0].ModulePath != "std/json" || got[0].Member != "Json" {
		t.Errorf("want std/json.Json import suggestion, got %+v", got)
	}
}

// Regression for the reflection-walker blind spot: a qualifier used only inside
// a struct-valued container (case branch, struct literal, map literal) must
// still be detected. These containers are []StructValue, not []Node, so the
// walker has to recurse into struct elements, not just Node elements.
func TestFindMissingImports_InsideCaseBody(t *testing.T) {
	got := buildMissing(t, "fn main() {\n  case 1 {\n    _ -> io.inspect(42)\n  }\n}\n")
	if len(got) != 1 || got[0].ModulePath != "std/io" {
		t.Fatalf("want std/io suggestion inside case body, got %+v", got)
	}
}

func TestFindMissingImports_InsideStructLiteral(t *testing.T) {
	got := buildMissing(t, "fn main() {\n  x = {a: io.inspect(42)}\n  x\n}\n")
	if len(got) != 1 || got[0].ModulePath != "std/io" {
		t.Fatalf("want std/io suggestion inside struct literal, got %+v", got)
	}
}

func TestFindMissingImports_InsideMapLiteral(t *testing.T) {
	got := buildMissing(t, "fn main() {\n  m = {1 => io.inspect(42)}\n  m\n}\n")
	if len(got) != 1 || got[0].ModulePath != "std/io" {
		t.Fatalf("want std/io suggestion inside map literal, got %+v", got)
	}
}

// Type-qualified use of an unimported stdlib type -> type import suggestion.
func TestFindMissingImports_SuggestsStdlibModule(t *testing.T) {
	got := buildMissing(t, "fn main() {\n  Duration.seconds(3)\n}\n")
	if len(got) != 1 {
		t.Fatalf("want 1 suggestion, got %d: %+v", len(got), got)
	}
	m := got[0]
	if m.Qualifier != "Duration" || m.ModulePath != "std/duration" || m.Member != "Duration" {
		t.Errorf("got %+v, want {Duration std/duration Duration}", m)
	}
	if m.ImportSpec() != "std/duration.Duration" {
		t.Errorf("ImportSpec = %q, want std/duration.Duration", m.ImportSpec())
	}
}

// An unresolved type in an annotation position is detected too.
func TestFindMissingImports_TypeInAnnotation(t *testing.T) {
	got := buildMissing(t, "fn f(_d: Duration): Bool {\n  True\n}\n")
	if len(got) != 1 || got[0].ModulePath != "std/duration" || got[0].Member != "Duration" {
		t.Fatalf("want std/duration.Duration for the annotation, got %+v", got)
	}
}

func TestFindMissingImports_ModuleAlreadyImported(t *testing.T) {
	got := buildMissing(t, "import std/duration.Duration\n\nfn main() {\n  Duration.seconds(1)\n}\n")
	if len(got) != 0 {
		t.Errorf("want none when duration is imported, got %+v", got)
	}
}

// A prelude type resolves, so it is never suggested even though a stdlib module
// exports it.
func TestFindMissingImports_PreludeTypeNoSuggestion(t *testing.T) {
	got := buildMissing(t, "fn f(): Result<Int, String> {\n  Ok(1)\n}\n")
	if len(got) != 0 {
		t.Errorf("want none for prelude Result, got %+v", got)
	}
}

// A locally-declared type that happens to share a stdlib name must not be
// flagged (definition positions are excluded; uses of it resolve locally).
func TestFindMissingImports_LocalTypeDefinitionNoSuggestion(t *testing.T) {
	got := buildMissing(t, "struct Set {\n  n: Int\n}\n\nfn main() {\n  Set{n: 1}\n}\n")
	if len(got) != 0 {
		t.Errorf("want none for a locally-defined Set, got %+v", got)
	}
}

func TestFindMissingImports_SelfMergeCandidate(t *testing.T) {
	// Importing one calendar member does not bind every sibling declaration, so
	// a suggestion is still produced for the missing type in the same file.
	got := buildMissing(t, "import std/calendar.Date\n\nfn f(_t: Time, _d: Date): Bool {\n  True\n}\n")
	if len(got) != 1 || got[0].ModulePath != "std/calendar" || got[0].Member != "Time" {
		t.Fatalf("want std/calendar.Time suggestion, got %+v", got)
	}
}

package analysis_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
)

// Opening a stdlib-classified file (immediate-parent dir "std") in the editor
// must still resolve *inferred* binding types, so hover shows e.g. `ns: Int`
// for a distinct-destructure binding rather than a bare name.
//
// Regression: document.go's stdlib branch ran BuildFileWithStdlib + CheckTypes
// but skipped Sweep C (BuildTypes) — which the project branches get for free
// via BuildProjectFromEntry. Without it the checker can't see the distinct
// type's Inner, so `Dur(n) = x` left `n` untyped and hover showed just `n`.
func TestDocumentManager_StdlibFileTypesDistinctDestructure(t *testing.T) {
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, nil)

	src := "import std/int.{Int}\n\nopaque type Dur Int\n\nfn unwrap(x: Dur): Int {\n  Dur(n) = x\n  n\n}"
	uri := "file:///path/to/std/dur.nomi"
	dm.Open(uri, src)

	doc := dm.Get(uri)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}

	var found bool
	for _, sym := range doc.Analysis.Definitions {
		if sym.Name == "n" && sym.Kind == analysis.SymbolBinding {
			found = true
			if sym.Type == nil {
				t.Fatal("binding `n` from `Dur(n) = x` has no type — hover would show a bare name")
			}
			if got := sym.Type.String(); got != "Int" {
				t.Fatalf("binding `n` type = %q, want Int", got)
			}
		}
	}
	if !found {
		t.Fatal("never saw a SymbolBinding named `n`")
	}
}

func TestDocumentManager_StdlibFileAllowsTopLevelImplForLocalType(t *testing.T) {
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)

	src := `import std/strings: String

pub interface Show {
  fn show(value: self): String
}

pub opaque type Thing Int

impl Show for Thing {
  fn show(_value: Thing): String {
    "thing"
  }
}
`
	uri := "file:///path/to/std/local_impl.nomi"
	dm.Open(uri, src)

	doc := dm.Get(uri)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}
	if len(doc.Analysis.TypeErrors) != 0 {
		t.Fatalf("local type impl block should be allowed, got %v", doc.Analysis.TypeErrors)
	}
}

// Regression: std/json.nomi uses imported type names behind same-named
// variants (`variant String String`) and also calls those imported types
// (`Int.to_string`). Opening it directly in the editor must not report false
// unused-import or bare-variant diagnostics for String/Int/Float.
func TestDocumentManager_StdlibFileJsonDiagnostics(t *testing.T) {
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test file")
	}
	jsonPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "std", "json.nomi")
	src, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}

	uri := "file://" + filepath.Clean(jsonPath)
	dm.Open(uri, string(src))

	doc := dm.Get(uri)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}

	var diagnostics []string
	for _, err := range doc.Analysis.TypeErrors {
		diagnostics = append(diagnostics, err.Error())
	}
	if len(diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics in std/json.nomi:\n%s", strings.Join(diagnostics, "\n"))
	}
}

func TestDocumentManager_StdlibFileCalendarCivilUnitDiagnostics(t *testing.T) {
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test file")
	}
	calendarPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "std", "calendar.nomi")
	src, err := os.ReadFile(calendarPath)
	if err != nil {
		t.Fatal(err)
	}

	uri := "file://" + filepath.Clean(calendarPath)
	dm.Open(uri, string(src))

	doc := dm.Get(uri)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}

	var diagnostics []string
	for _, err := range doc.Analysis.TypeErrors {
		diagnostics = append(diagnostics, err.Error())
	}
	if len(diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics in std/calendar.nomi:\n%s", strings.Join(diagnostics, "\n"))
	}
}

func TestDocumentManager_StdlibFileCalendarCivilUnitQualifiedTypeReferences(t *testing.T) {
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test file")
	}
	calendarPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "std", "calendar.nomi")
	srcBytes, err := os.ReadFile(calendarPath)
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)

	uri := "file://" + filepath.Clean(calendarPath)
	dm.Open(uri, src)

	doc := dm.Get(uri)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}

	refPos, ok := posInSource(src, "impl Add<Years, Date>", "Years")
	if !ok {
		t.Fatal("could not locate Years in calendar impl header")
	}

	sym := doc.Analysis.SymbolAt(refPos)
	if sym == nil {
		t.Fatal("expected hover/go-to-def symbol at Years in Years")
	}
	if sym.Name != "Years" || sym.Kind != analysis.SymbolType {
		t.Fatalf("symbol at Years = %s/%v, want Years/%v", sym.Name, sym.Kind, analysis.SymbolType)
	}
}

func posInSource(src, containing, token string) (analysis.Pos, bool) {
	blockOffset := strings.Index(src, containing)
	if blockOffset < 0 {
		return analysis.Pos{}, false
	}
	tokenOffset := strings.Index(containing, token)
	if tokenOffset < 0 {
		return analysis.Pos{}, false
	}
	offset := blockOffset + tokenOffset
	pos := analysis.Pos{Line: 1, Col: 1}
	for _, r := range src[:offset] {
		if r == '\n' {
			pos.Line++
			pos.Col = 1
		} else {
			pos.Col++
		}
	}
	return pos, true
}

func containsDiagnostic(diagnostics []string, needle string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, needle) {
			return true
		}
	}
	return false
}

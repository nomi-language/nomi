package analysis_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"
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
	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, nil)

	src := "import std::int.{Int}\n\nopaque type Dur Int\n\nfn unwrap(x: Dur) -> Int\n  Dur(n) = x\n  n\nend"
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
	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)

	src := `import std::strings: String

pub interface Show
  fn show(value: self) -> String
end

pub opaque type Thing Int

impl Show for Thing
  fn show(_value: Thing) -> String
    "thing"
  end
end
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
	lib := loadStd()
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

	uri := fileuri.FromPath(filepath.Clean(jsonPath))
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
	lib := loadStd()
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

	uri := fileuri.FromPath(filepath.Clean(calendarPath))
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
	lib := loadStd()
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

	uri := fileuri.FromPath(filepath.Clean(calendarPath))
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

// stdSubtypeDoc is a std file that declares a subtype, a validated struct
// and a validated opaque type, and calls the functions the front end writes
// for each (subtype_synthesis.go), as std's own modules do.
const stdSubtypeDoc = `import
    std::convert.TryFrom
    std::int.Int
    std::option.Option
end

pub subtype Positive Int when fn n -> n > 0

pub struct Span
    low: Int
    high: Int
    when fn span -> span.low <= span.high
end

pub opaque type Even Int when fn n -> n % 2 == 0

pub fn one() -> Positive
    Positive.unchecked(1)
end

pub fn positive(n: Int) -> Option<Positive>
    Positive.try_from(n)
end

pub fn span(low: Int, high: Int) -> Option<Span>
    Span.try_from({low:, high:})
end

pub fn unit_span() -> Span
    Span.unchecked({low: 0, high: 1})
end

pub fn even(n: Int) -> Option<Even>
    Even.try_from(n)
end

pub fn zero() -> Even
    Even.unchecked(0)
end
`

// A std file opened as a document is checked with the impl blocks the front
// end writes for its declarations, as `nomi check` checks it: a call of a
// subtype's `unchecked` or `try_from` in the declaring file resolves, and
// each written function has its type, which hover shows.
func TestDocumentManager_StdlibFileCallsTheFunctionsWrittenForItsTypes(t *testing.T) {
	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	doc := dm.Open("file:///path/to/std/checked.nomi", stdSubtypeDoc)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}
	if len(doc.Errors) != 0 {
		t.Fatalf("the fixture does not parse: %v", doc.Errors)
	}
	if len(doc.Analysis.TypeErrors) != 0 {
		t.Fatalf("a std document that calls the functions written for its own types reports:\n%v", doc.Analysis.TypeErrors)
	}
	for _, want := range []struct{ typ, fn, sig string }{
		{"Positive", "try_from", "(Int) -> Option<Positive>"},
		{"Positive", "unchecked", "(Int) -> Positive"},
		{"Span", "try_from", "({low: Int, high: Int}) -> Option<Span>"},
		{"Span", "unchecked", "({low: Int, high: Int}) -> Span"},
		{"Even", "unchecked", "(Int) -> Even"},
	} {
		sym := doc.Analysis.LookupTypeMethod(want.typ, want.fn)
		if sym == nil {
			t.Errorf("%s.%s: no function", want.typ, want.fn)
			continue
		}
		if sym.Type == nil || sym.Type.String() != want.sig {
			t.Errorf("%s.%s: type %v, want %s", want.typ, want.fn, sym.Type, want.sig)
		}
	}
	// Each call names the written function, for hover and go-to-definition.
	for _, call := range []string{"Positive.unchecked(1)", "Positive.try_from(n)", "Span.try_from(", "Span.unchecked(", "Even.try_from(n)", "Even.unchecked(0)"} {
		name := "unchecked"
		if strings.Contains(call, "try_from") {
			name = "try_from"
		}
		pos, ok := posInSource(stdSubtypeDoc, call, name)
		if !ok {
			t.Fatalf("could not locate %s", call)
		}
		sym := doc.Analysis.SymbolAt(pos)
		if sym == nil || sym.Name != name || sym.Type == nil {
			t.Errorf("%s: the symbol at %s is %+v, want the typed function", call, name, sym)
		}
	}
}

// A subtype's rule is checked inside the `try_from` written for it, so a
// std document reports a rule that does not check, at the rule.
func TestDocumentManager_StdlibFileChecksASubtypesRule(t *testing.T) {
	lib := loadStd()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	src := strings.Replace(stdSubtypeDoc, "when fn n -> n > 0", `when fn n -> n > "0"`, 1)
	doc := dm.Open("file:///path/to/std/checked.nomi", src)
	if doc == nil || doc.Analysis == nil {
		t.Fatal("expected analyzed document")
	}
	want, ok := posInSource(src, `n > "0"`, `n > "0"`)
	if !ok {
		t.Fatal("could not locate the rule")
	}
	if len(doc.Analysis.TypeErrors) == 0 {
		t.Fatal("a std document whose subtype's rule compares an Int with a String reports nothing")
	}
	e := doc.Analysis.TypeErrors[0]
	if len(doc.Analysis.TypeErrors) != 1 || e.Message != "comparison type mismatch: Int vs String" || e.Line != want.Line {
		t.Fatalf("want one error at the rule (line %d), `comparison type mismatch: Int vs String`, got:\n%v", want.Line, doc.Analysis.TypeErrors)
	}
}

// Every std module opened as a document, as go-to-definition into std opens
// one, reports no error: the std the server was built with checks, and a
// document's analysis of one of its files is that check's.
func TestDocumentManager_EveryStdModuleOpensWithNoError(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test file")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	var paths []string
	err := filepath.WalkDir(filepath.Join(repo, "std"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(path) == ".nomi" {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("found no std module")
	}
	lib := loadStd()
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		dm := analysis.NewDocumentManager()
		dm.SetEditorFolder(repo)
		dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
		doc := dm.Open(fileuri.FromPath(path), string(src))
		rel, _ := filepath.Rel(repo, path)
		if doc == nil || doc.Analysis == nil {
			t.Errorf("%s: no analysis", rel)
			continue
		}
		if len(doc.Errors) > 0 || len(doc.Analysis.TypeErrors) > 0 {
			t.Errorf("%s as a document reports:\n%v\n%v", rel, doc.Errors, doc.Analysis.TypeErrors)
		}
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

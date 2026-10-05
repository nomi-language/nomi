package lsp

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Duplicate derive entries are lowering-time errors. They must
// surface as LSP diagnostics (parity with `nomi run`), not be silently dropped.
func TestLoweringErrorsSurfaceAsDiagnostics(t *testing.T) {
	dm := analysis.NewDocumentManager()
	src := "struct Dog {\n" +
		"  name: String\n" +
		"}\n" +
		"\n" +
		"derive Equatable for Dog\n" +
		"derive Equatable for Dog\n"
	doc := dm.Open("file:///test.nomi", src)
	if doc.Analysis == nil {
		t.Fatal("expected analysis to be non-nil")
	}
	count := 0
	for _, e := range doc.Analysis.TypeErrors {
		if strings.Contains(e.Message, "duplicate `derive Equatable for Dog`") {
			count++
		}
	}
	if count == 0 {
		t.Fatalf("expected a diagnostic about duplicate `derive Equatable for Dog`, got %d type errors: %+v",
			len(doc.Analysis.TypeErrors), doc.Analysis.TypeErrors)
	}
	if count > 1 {
		t.Fatalf("expected the duplicate-conformance diagnostic exactly once, got %d", count)
	}
}

func TestDuplicateTestNamesSurfaceAsDiagnostics(t *testing.T) {
	dm := analysis.NewDocumentManager()
	src := `tests "math" {
  test "adds" {
    assert True
  }
}

tests "math" {
  test "adds" {
    assert True
  }
}
`
	doc := dm.Open("file:///test.nomi", src)
	if doc.Analysis == nil {
		t.Fatal("expected analysis to be non-nil")
	}
	for _, e := range doc.Analysis.TypeErrors {
		if strings.Contains(e.Message, `duplicate test name "math / adds"`) &&
			strings.Contains(e.Message, "first declared at line 2") {
			return
		}
	}
	t.Fatalf("expected duplicate test-name diagnostic, got: %+v", doc.Analysis.TypeErrors)
}

func TestParseErrorsToDiagnostics(t *testing.T) {
	errs := []parser.ParseError{
		{Line: 5, Col: 10, Message: "expected ')'"},
		{Line: 12, Col: 1, Message: "unexpected token"},
	}
	diags := parseErrorsToDiagnostics(errs)
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d", len(diags))
	}
	// 1-based to 0-based conversion
	if diags[0].Range.Start.Line != 4 {
		t.Errorf("expected line 4, got %d", diags[0].Range.Start.Line)
	}
	if diags[0].Range.Start.Character != 9 {
		t.Errorf("expected char 9, got %d", diags[0].Range.Start.Character)
	}
	if diags[0].Message != "expected ')'" {
		t.Errorf("wrong message: %s", diags[0].Message)
	}
	if *diags[0].Severity != protocol.DiagnosticSeverityError {
		t.Error("expected error severity")
	}
}

func TestParseErrorsToDiagnostics_Empty(t *testing.T) {
	diags := parseErrorsToDiagnostics(nil)
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics, got %d", len(diags))
	}
}

func TestAttachedTestDiagnosticsAllowOrdinaryAssert(t *testing.T) {
	content := strings.Join([]string{
		"//! assert 1 == 1",
		"fn ready(): Bool { True }",
	}, "\n")
	nodes, parseErrs := parser.ParseWithRecovery(lexer.Lex(content))
	if len(parseErrs) != 0 {
		t.Fatalf("expected no parse errors, got %d: %+v", len(parseErrs), parseErrs)
	}
	diags := buildPublishedDiagnostics("file:///repo/app/main.nomi", content, nodes, parseErrs, nil)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %d: %+v", len(diags), diags)
	}
}

func TestAttachedTestDiagnosticsAllowBlankSeparatedBlocks(t *testing.T) {
	content := strings.Join([]string{
		"//! assert answer() == 42",
		"",
		"//! refute answer() == 41",
		"//!",
		"fn answer(): Int { 42 }",
	}, "\n")
	nodes, parseErrs := parser.ParseWithRecovery(lexer.Lex(content))
	if len(parseErrs) != 0 {
		t.Fatalf("expected no parse errors, got %d: %+v", len(parseErrs), parseErrs)
	}
	diags := buildPublishedDiagnostics("file:///repo/app/main.nomi", content, nodes, parseErrs, nil)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %d: %+v", len(diags), diags)
	}
}

func TestAttachedTestDiagnosticsAllowCommentSeparatedBlocks(t *testing.T) {
	for _, separator := range []string{"///", "//"} {
		t.Run(separator, func(t *testing.T) {
			content := strings.Join([]string{
				"//! assert answer() == 42",
				separator,
				"//! refute answer() == 41",
				"fn answer(): Int { 42 }",
			}, "\n")
			nodes, parseErrs := parser.ParseWithRecovery(lexer.Lex(content))
			if len(parseErrs) != 0 {
				t.Fatalf("expected no parse errors, got %d: %+v", len(parseErrs), parseErrs)
			}
			diags := buildPublishedDiagnostics("file:///repo/app/main.nomi", content, nodes, parseErrs, nil)
			if len(diags) != 0 {
				t.Fatalf("expected no diagnostics, got %d: %+v", len(diags), diags)
			}
		})
	}
}

func TestTypeErrorsToDiagnostics(t *testing.T) {
	errs := []analysis.TypeError{
		{Line: 3, Col: 5, Message: "type mismatch: expected Int, got String"},
	}
	diags := typeErrorsToDiagnostics(errs)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}
	// 1-based to 0-based conversion
	if diags[0].Range.Start.Line != 2 {
		t.Errorf("expected line 2, got %d", diags[0].Range.Start.Line)
	}
	if diags[0].Range.Start.Character != 4 {
		t.Errorf("expected char 4, got %d", diags[0].Range.Start.Character)
	}
	if diags[0].Message != "type mismatch: expected Int, got String" {
		t.Errorf("wrong message: %s", diags[0].Message)
	}
	if *diags[0].Severity != protocol.DiagnosticSeverityError {
		t.Error("expected error severity")
	}
	if *diags[0].Source != "nomi-type" {
		t.Errorf("expected source 'nomi-type', got %s", *diags[0].Source)
	}
}

func TestBuildPublishedDiagnosticsSuppressesExpectedFixtureErrors(t *testing.T) {
	content := strings.Join([]string{
		"fn main() {",
		"  bad = Nope // expect type-error: undefined name 'Nope'",
		"  also_bad = other // expect type-error: undefined name 'other'",
		"}",
	}, "\n")
	errs := []analysis.TypeError{
		{Line: 2, Col: 9, Message: "undefined name 'Nope'"},
		{Line: 3, Col: 14, Message: "undefined name 'other'"},
	}
	diags := buildPublishedDiagnostics("file:///repo/internal/analysis/testdata/orphan_violator/main.nomi", content, nil, nil, errs)
	if len(diags) != 0 {
		t.Fatalf("expected matching fixture diagnostics to be suppressed, got %d: %+v", len(diags), diags)
	}
}

func TestBuildPublishedDiagnosticsReportsFixtureDrift(t *testing.T) {
	content := strings.Join([]string{
		"fn main() {",
		"  bad = Nope // expect type-error: undefined name 'Nope'",
		"}",
	}, "\n")
	errs := []analysis.TypeError{
		{Line: 2, Col: 9, Message: "undefined name 'Surprise'"},
	}
	diags := buildPublishedDiagnostics("file:///repo/internal/analysis/testdata/orphan_violator/main.nomi", content, nil, nil, errs)
	if len(diags) != 2 {
		t.Fatalf("expected one missing-expectation diagnostic and one unexpected diagnostic, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "expected type error did not occur") {
		t.Fatalf("expected first diagnostic to report the missing expected error, got %+v", diags[0])
	}
	if !strings.Contains(diags[1].Message, "undefined name 'Surprise'") {
		t.Fatalf("expected second diagnostic to keep the unexpected analyzer error, got %+v", diags[1])
	}
}

func TestBuildPublishedDiagnosticsDoesNotSuppressNonFixtureErrors(t *testing.T) {
	content := "fn main() { bad = Nope } // expect type-error: undefined name 'Nope'"
	errs := []analysis.TypeError{
		{Line: 1, Col: 19, Message: "undefined name 'Nope'"},
	}
	diags := buildPublishedDiagnostics("file:///repo/app/main.nomi", content, nil, nil, errs)
	if len(diags) != 1 {
		t.Fatalf("expected non-fixture diagnostics to remain visible, got %d: %+v", len(diags), diags)
	}
}

func TestWorkspaceFixtureDiagnosticsAreQuietWhenExpectationsMatch(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetWorkspaceRoot(root)
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)

	// Every .nomi file of the repository outside std/ and dot directories,
	// testdata fixtures included: the workspace scan leaves those out, but
	// an editor that opens one analyzes it the same way.
	var paths []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || path == filepath.Join(root, "std")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".nomi" {
			paths = append(paths, path)
		}
		return nil
	})
	var noisy []string
	for _, path := range paths {
		doc := dm.AnalyzeClosed("file://" + path)
		if doc == nil || doc.Analysis == nil {
			continue
		}
		diags := buildPublishedDiagnostics(doc.URI, doc.Content, doc.Nodes, doc.Errors, doc.Analysis.TypeErrors)
		for _, diag := range diags {
			if diag.Severity != nil && *diag.Severity == protocol.DiagnosticSeverityWarning {
				continue
			}
			noisy = append(noisy, string(doc.URI)+": "+diag.Message)
		}
	}
	if len(noisy) > 0 {
		t.Fatalf("expected workspace fixture diagnostics to publish cleanly, got %d:\n  %s", len(noisy), strings.Join(noisy, "\n  "))
	}
}

func TestStdlibSameOwnerPipeCallPublishesCleanly(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "std", "strings.nomi")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read std/strings.nomi: %v", err)
	}

	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetWorkspaceRoot(root)
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	doc := dm.Open("file://"+path, string(content))
	if doc.Analysis == nil {
		t.Fatal("expected analysis to be non-nil")
	}

	diags := buildPublishedDiagnostics(doc.URI, doc.Content, doc.Nodes, doc.Errors, doc.Analysis.TypeErrors)
	var messages []string
	for _, diag := range diags {
		if diag.Severity != nil && *diag.Severity == protocol.DiagnosticSeverityWarning {
			continue
		}
		messages = append(messages, diag.Message)
	}
	if len(messages) > 0 {
		t.Fatalf("expected no diagnostics, got %d:\n  %s", len(messages), strings.Join(messages, "\n  "))
	}
}

func TestBuildPublishedDiagnosticsReportsDbgWarning(t *testing.T) {
	content := strings.Join([]string{
		"fn main() {",
		"  dbg 42",
		"}",
	}, "\n")
	nodes, parseErrs := parser.ParseWithRecovery(lexer.Lex(content))
	if len(parseErrs) != 0 {
		t.Fatalf("unexpected parse errors: %+v", parseErrs)
	}
	diags := buildPublishedDiagnostics("file:///repo/app/main.nomi", content, nodes, nil, nil)
	if len(diags) != 1 {
		t.Fatalf("expected one dbg warning, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity == nil || *diags[0].Severity != protocol.DiagnosticSeverityWarning {
		t.Fatalf("expected warning severity, got %+v", diags[0].Severity)
	}
	if diags[0].Source == nil || *diags[0].Source != "nomi-dbg" {
		t.Fatalf("expected source nomi-dbg, got %+v", diags[0].Source)
	}
	if diags[0].Code == nil || diags[0].Code.Value != "debug-expression" {
		t.Fatalf("expected debug-expression code, got %+v", diags[0].Code)
	}
	if !strings.Contains(diags[0].Message, "`dbg` is debug-only code") {
		t.Fatalf("unexpected message: %q", diags[0].Message)
	}
	if diags[0].Range.Start.Line != 1 || diags[0].Range.Start.Character != 2 {
		t.Fatalf("unexpected range start: %+v", diags[0].Range.Start)
	}
	if diags[0].Range.End.Line != 1 || diags[0].Range.End.Character != 5 {
		t.Fatalf("unexpected range end: %+v", diags[0].Range.End)
	}
}

// An operator diagnostic starts at the operator, not at column 1 of its line,
// so the editor marks the `-` in `Instant.from_seconds(15) - 5`. The second
// line puts a non-ASCII character before the operator: the published start is
// the operator's UTF-16 column, one less than its byte column.
func TestOperatorDiagnosticsStartAtTheOperator(t *testing.T) {
	src := strings.Join([]string{
		"import std/instant.Instant",
		"",
		"fn main() {",
		"    _ = Instant.from_seconds(15) - 5",
		`    _ = "é" == 1`,
		"}",
	}, "\n")
	uri := "file:///operator_position.nomi"
	s := NewServer()
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis for the document")
	}
	diags := buildPublishedDiagnostics(snap.URI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	want := map[string]protocol.Position{
		"no matching Subtract impl for Instant - Int": {Line: 3, Character: 33},
		"equality type mismatch: String vs Int":       {Line: 4, Character: 12},
	}
	for msg, pos := range want {
		found := false
		for _, d := range diags {
			if !strings.Contains(d.Message, msg) {
				continue
			}
			found = true
			if d.Range.Start != pos {
				t.Errorf("%q starts at %+v, want %+v", msg, d.Range.Start, pos)
			}
		}
		if !found {
			t.Errorf("no diagnostic %q in %+v", msg, diags)
		}
	}
}

func TestTypeErrorsViaDocumentManager(t *testing.T) {
	dm := analysis.NewDocumentManager()
	// This function has a return type mismatch: declared Int, returns String
	doc := dm.Open("file:///test.nomi", `fn bad(): Int { "hello" }`)
	if doc.Analysis == nil {
		t.Fatal("expected analysis to be non-nil")
	}
	if len(doc.Analysis.TypeErrors) == 0 {
		t.Fatal("expected type errors for return type mismatch, got none")
	}
	found := false
	for _, e := range doc.Analysis.TypeErrors {
		if e.Message != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one type error with a non-empty message")
	}
}

func TestDocumentManager_DiscardBindingSuppressesIgnoredExpressionDiagnostic(t *testing.T) {
	dm := analysis.NewDocumentManager()
	path, err := filepath.Abs(filepath.Join("..", "..", "tests", "01-foundations", "block_scoping", "block_scoping_test.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := dm.Open("file://"+path, string(content))
	if doc.Analysis == nil {
		t.Fatal("expected analysis to be non-nil")
	}
	for _, err := range doc.Analysis.TypeErrors {
		if err.Line == 84 && err.Col == 9 && strings.Contains(err.Message, "non-final expression has type Int") {
			t.Fatalf("unexpected ignored-expression diagnostic for discard binding: %s", err)
		}
	}
}

// An imported file's build-phase errors are published on that file, at their
// own position, and the importer publishes nothing for them: no duplicate
// `Debug` impl at a synthesized line from a type declared twice.
func TestImportedFileBuildErrorsPublishOnThatFile(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi":   "import std/io\nimport helper\n\nfn main() {\n  io.print(helper.greet())\n}\n",
		"helper.nomi": "import std/regex\n\nstruct A {\n  x: Int\n}\n\nenum A {\n  One\n}\n\npub fn greet(): String {\n  \"hi\"\n}\n",
	})
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetWorkspaceRoot(dir)
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	published := func(name string) []string {
		path := filepath.Join(dir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := dm.Open("file://"+path, string(content))
		if doc.Analysis == nil {
			t.Fatalf("%s: no analysis", name)
		}
		var got []string
		for _, d := range buildPublishedDiagnostics(doc.URI, doc.Content, doc.Nodes, doc.Errors, doc.Analysis.TypeErrors) {
			got = append(got, fmt.Sprintf("%d:%d: %s", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message))
		}
		return got
	}
	want := []string{
		"7:6: 'A' is already defined in this scope as a type\nhelp: pick a different name",
		"1:12: imported module 'regex' is unused — remove the import",
	}
	if got := published("helper.nomi"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("helper.nomi publishes:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if got := published("main.nomi"); len(got) != 0 {
		t.Errorf("main.nomi publishes its import's errors:\n  %s", strings.Join(got, "\n  "))
	}
}

// An import of a file that does not exist publishes on the import line of the
// file that wrote it, with the misspelling hint, and nowhere else.
func TestMissingImportPublishesOnTheImportLine(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"main.nomi":   "import std/io\nimport helper\nimport lib\n\nfn main() {\n  io.print(helper.greet())\n  lib.go()\n}\n",
		"helper.nomi": "import utlis\n\npub fn greet(): String {\n  utlis.name()\n}\n",
		"utils.nomi":  "pub fn name(): String {\n  \"hi\"\n}\n",
	})
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetWorkspaceRoot(dir)
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	published := func(name string) string {
		path := filepath.Join(dir, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc := dm.Open("file://"+path, string(content))
		if doc.Analysis == nil {
			t.Fatalf("%s: no analysis", name)
		}
		var got []string
		for _, d := range buildPublishedDiagnostics(doc.URI, doc.Content, doc.Nodes, doc.Errors, doc.Analysis.TypeErrors) {
			got = append(got, fmt.Sprintf("%d:%d-%d:%d: %s", d.Range.Start.Line+1, d.Range.Start.Character+1,
				d.Range.End.Line+1, d.Range.End.Character+1, d.Message))
		}
		return strings.Join(got, "\n")
	}
	for _, tc := range []struct{ file, want string }{
		{"main.nomi", "3:8-3:11: no module `lib`: no file lib.nomi in this file's directory"},
		{"helper.nomi", "1:8-1:13: no module `utlis`: no file utlis.nomi in this file's directory\nhelp: did you mean 'utils'?"},
	} {
		if got := published(tc.file); got != tc.want {
			t.Errorf("%s publishes:\n  %s\nwant:\n  %s", tc.file, got, tc.want)
		}
	}
}

// Braces right after a `/` in an import publish at the `{`, with the import
// block the hint spells.
func TestGroupedImportPathPublishesAtTheBrace(t *testing.T) {
	dm := analysis.NewDocumentManager()
	src := "import std/{io, regex.Regex}\n\nfn main() {\n}\n"
	doc := dm.Open("file:///test.nomi", src)
	var got []string
	for _, d := range buildPublishedDiagnostics(doc.URI, doc.Content, doc.Nodes, doc.Errors, nil) {
		got = append(got, fmt.Sprintf("%d:%d: %s", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message))
	}
	want := "1:12: braces select names from one file, as in `std/regex.{Regex, Match}`\n" +
		"help: to import several files, list each in an import block:\nimport {\n    std/io\n    std/regex.Regex\n}"
	if strings.Join(got, "\n---\n") != want {
		t.Errorf("publishes:\n%s\nwant:\n%s", strings.Join(got, "\n---\n"), want)
	}
}

// An import that names nothing but brings in impl blocks the program uses
// publishes nothing, and the structured query the quick fix and organize
// imports read does not offer to remove it. One whose impls nothing uses is
// reported and offered.
func TestImplOnlyImportPublishesWhatTheProgramUses(t *testing.T) {
	fixture := filepath.Join("..", "irbuild", "testdata", "sibimpl_cycle")
	files := map[string]string{"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"}
	for _, name := range []string{"main.nomi", "decls.nomi", "back.nomi", "hop.nomi"} {
		src, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(src)
	}
	// extra.nomi imports hop, whose one impl the program uses, and spare,
	// whose one impl it does not.
	files["spare.nomi"] = "import decls.{Alpha, Other}\n\nimpl Alpha for Other {\n    fn go(x: Other): Int {\n        x.n\n    }\n}\n"
	files["extra.nomi"] = "import {\n    hop\n    spare\n}\n\npub fn one(): Int {\n    1\n}\n"
	files["main.nomi"] = strings.Replace(files["main.nomi"], "    std/io\n", "    std/io\n    extra\n", 1)
	// The derive is there because opening another file type-checks main.nomi
	// as that file's entry, from a tree the editor parsed but did not lower.
	files["main.nomi"] = strings.Replace(files["main.nomi"], "fn main() {\n",
		"derive Equatable for Box\n\nstruct Box {\n    n: Int\n}\n\nfn main() {\n    _ = extra.one()\n    _ = Box{n: 1} == Box{n: 2}\n", 1)
	dir := writeProject(t, files)
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetWorkspaceRoot(dir)
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	open := func(name string) (*analysis.Document, []string) {
		path := filepath.Join(dir, name)
		doc := dm.Open("file://"+path, files[name])
		if doc.Analysis == nil {
			t.Fatalf("%s: no analysis", name)
		}
		var got []string
		for _, d := range buildPublishedDiagnostics(doc.URI, doc.Content, doc.Nodes, doc.Errors, doc.Analysis.TypeErrors) {
			got = append(got, fmt.Sprintf("%d:%d: %s", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message))
		}
		return doc, got
	}
	for _, name := range []string{"main.nomi", "decls.nomi", "back.nomi", "hop.nomi"} {
		doc, got := open(name)
		if len(got) != 0 {
			t.Errorf("%s publishes:\n  %s", name, strings.Join(got, "\n  "))
		}
		if unused := analysis.FindUnusedImports(doc.Analysis, doc.Nodes); len(unused) != 0 {
			t.Errorf("%s offers to remove %s", name, unused[0].Name)
		}
	}
	doc, got := open("extra.nomi")
	want := "3:5: imported module 'spare' is unused — remove the import"
	if strings.Join(got, "\n") != want {
		t.Errorf("extra.nomi publishes:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), want)
	}
	unused := analysis.FindUnusedImports(doc.Analysis, doc.Nodes)
	if len(unused) != 1 || unused[0].Name != "spare" {
		t.Errorf("extra.nomi offers to remove %+v, want spare alone", unused)
	}
}

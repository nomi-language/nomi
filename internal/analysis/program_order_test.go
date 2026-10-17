package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// A program's analysis does not depend on the order its files are checked in
// or on which of them the build starts from. Every file of orderProject
// imports, directly or through another, every other, so each can be the
// entry, as the language server makes the open document its entry. The
// project has the facts a file used to take from another file's check or
// from being the entry: an unannotated `once` read in another file, impls
// used across files, an application field read in another file through a
// dispatch, and diagnostics about the whole program (a duplicate impl, a
// missing impl a derive demands, a test reading an application field with no
// boot).
var orderProject = map[string]string{
	"main.nomi": `import {
    std/io
    shapes.{Shape, Circle}
    report
    settings.Settings
}

pub fn greeting() -> String {
    "hello"
}

pub fn boot() -> Settings {
    Settings{context: Context.root(), tag: "root"}
}

fn main() {
    io.print(report.describe(Circle{radius: shapes.unit}))
    io.print(report.title())
}
`,
	"shapes.nomi": `import {
    report
    settings.Settings
}

pub interface Shape {
    fn area(value: self) -> Int
}

pub struct Circle {
    radius: Int
}

impl Shape for Circle {
    fn area(value: Circle) -> Int {
        value.radius + Iter.count(Settings.tag)
    }
}

pub once unit = report.base * 2
`,
	"report.nomi": `import {
    main
    shapes.{Shape, Circle}
    settings.Settings
}

pub once base = Iter.count(main.greeting())

pub struct Labelled {
    circle: Circle
}

derive Display for Labelled

impl Shape for Circle {
    fn area(value: Circle) -> Int {
        value.radius
    }
}

pub fn describe(shape: Shape) -> String {
    "area ${Shape.area(shape)}"
}

pub fn title() -> String {
    Settings.tag
}

test "area" {
    assert Shape.area(Circle{radius: 1}) > 0
}
`,
	"settings.nomi": `import main

pub struct Settings {
    context: Context

    tag: String
}

pub fn default_tag() -> String {
    main.greeting()
}
`,
}

// orderKeys are orderProject's module keys, sorted.
func orderKeys() []string {
	var keys []string
	for name := range orderProject {
		keys = append(keys, strings.TrimSuffix(name, ".nomi"))
	}
	sort.Strings(keys)
	return keys
}

// checkProgramDump builds the project at root from the file with module key
// entry, as the front end and the language server build a file entry, checks
// every user file in order, runs the checks that read every file, and answers
// each file's dump (analysis.DumpFileForTest) by key, plus the program's own
// diagnostics under "".
func checkProgramDump(t *testing.T, root, entry string, order []string) map[string]string {
	t.Helper()
	lib := std.Shared()
	entryPath := filepath.Join(root, entry+".nomi")
	src, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(string(src)))
	if len(perrs) > 0 {
		t.Fatalf("parse %s: %v", entry, perrs)
	}
	nodes, _ = analysis.LowerDerives(nodes)
	nodes, _ = analysis.SynthesizeDerives(nodes)
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	entryFA, siblings, siblingNodes := analysis.BuildProjectFromEntryWithManifest(
		entryPath, nodes, lib.Primitives, lib.Modules, lib.Files, root, std.MakeLoader(), nil, entry, true)
	files := map[string]*analysis.FileAnalysis{entry: entryFA}
	fileNodes := map[string][]ast.Node{entry: nodes}
	for key, fa := range siblings {
		if analysis.IsStdlibKey(key) {
			continue
		}
		files[key] = fa
		fileNodes[key] = siblingNodes[key]
	}
	if len(files) != len(order) {
		t.Fatalf("entry %s reached %d files, want %d", entry, len(files), len(order))
	}
	errs := map[string][]analysis.TypeError{}
	for _, key := range order {
		errs[key] = append(append([]analysis.TypeError{}, files[key].TypeErrors...), analysis.CheckTypes(files[key], fileNodes[key])...)
	}
	keys := orderKeys()
	var program []analysis.ProgramFile
	for _, key := range keys {
		errs[key] = append(errs[key], analysis.CheckAppRoots(files[key], fileNodes[key])...)
		program = append(program, analysis.ProgramFile{FA: files[key], Nodes: fileNodes[key]})
	}
	implImportErrs := analysis.CheckImplImports(program)
	out := map[string]string{}
	for _, key := range keys {
		out[key] = analysis.DumpFileForTest(files[key], append(errs[key], implImportErrs[files[key]]...), root)
	}
	programErrs := append(append([]analysis.TypeError{}, entryFA.Program().Errors...), analysis.FinalizeCoherence(entryFA)...)
	out[""] = analysis.DumpFileForTest(&analysis.FileAnalysis{}, programErrs, root)
	return out
}

// permutations answers every order of keys.
func permutations(keys []string) [][]string {
	if len(keys) <= 1 {
		return [][]string{append([]string(nil), keys...)}
	}
	var out [][]string
	for i := range keys {
		rest := append(append([]string(nil), keys[:i]...), keys[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]string{keys[i]}, p...))
		}
	}
	return out
}

func TestProgram_AnalysisDoesNotDependOnCheckOrderOrEntry(t *testing.T) {
	root := appScopeProject(t, orderProject)
	keys := orderKeys()
	want := checkProgramDump(t, root, "main", keys)
	// The project must say something in each place it tests.
	for key, fragment := range map[string]string{
		"":       "duplicate impl",
		"report": "this test has no boot",
		"shapes": "def 20:10 unit 11 = Int in /shapes.nomi",
	} {
		if !strings.Contains(want[key], fragment) {
			t.Fatalf("%q's dump has no %q:\n%s", key, fragment, want[key])
		}
	}
	compare := func(what string, got map[string]string, files []string) {
		t.Helper()
		for _, key := range files {
			if got[key] != want[key] {
				t.Errorf("%s: %q differs:\n%s", what, key, firstDifference(want[key], got[key]))
			}
		}
	}
	// The program's own diagnostics as well as every file's analysis.
	for _, order := range permutations(keys) {
		compare(fmt.Sprintf("entry main, check order %v", order), checkProgramDump(t, root, "main", order), append([]string{""}, keys...))
	}
	// Every file's analysis. The program's diagnostics are reported with the
	// entry, as `nomi check` reports them, and name the entry's module as the
	// entry ("<project entry>"), so they are the entry's to word.
	for _, entry := range keys {
		compare("entry "+entry, checkProgramDump(t, root, entry, keys), keys)
	}
}

// firstDifference shows the first line two dumps disagree on.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d\n want %s\n  got %s", i+1, wl, gl)
		}
	}
	return "(none)"
}

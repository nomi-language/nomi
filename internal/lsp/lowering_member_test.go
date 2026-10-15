package lsp

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/projectgen"
)

// memberOrderProject is internal/analysis's orderProject
// (program_order_test.go): four files that import each other, so each can
// be the entry, with an unannotated `once` read in another file, impls
// used across files, an application field read in another file, a
// duplicate impl, and a test reading an application field with no boot.
var memberOrderProject = map[string]string{
	"main.nomi": `import {
    std/io
    shapes.{Shape, Circle}
    report
    settings.Settings
}

pub fn greeting(): String {
    "hello"
}

pub fn boot(): Settings {
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
    fn area(value: self): Int
}

pub struct Circle {
    radius: Int
}

impl Shape for Circle {
    fn area(value: Circle): Int {
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
    fn area(value: Circle): Int {
        value.radius
    }
}

pub fn describe(shape: Shape): String {
    "area ${Shape.area(shape)}"
}

pub fn title(): String {
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

pub fn default_tag(): String {
    main.greeting()
}
`,
}

// memberLibrary is a project whose entry imports a library that imports
// another, where the library has tests and an exported function, its
// dependency a blocker the library's test reaches, and the entry an impl
// for its own type.
var memberLibrary = map[string]string{
	"nomi.toml": entryToml,
	"main.nomi": `import {
    std/io
    lib
}

struct Local {
    n: Int
}

impl Display for Local {
    fn to_string(value: Local): String {
        "local ${value.n}"
    }
}

fn main() {
    io.print(lib.evens([1, 2]))
    io.print(Display.to_string(Local{n: 1}))
}
`,
	"lib.nomi": `import dep

pub fn evens(xs: List<Int>): List<Int> {
    dep.halves(xs)
}

export fn count(xs: List<Int>): Int {
    Iter.count(dep.halves(xs))
}

test "evens" {
    assert evens([2]) == [2]
}
`,
	"dep.nomi": `fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

pub fn halves(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}
`,
}

// memberForeignImpl is a project whose entry implements an interface the
// library declares for a type the library declares, so the library's
// program alone lacks an impl the entry's program has.
var memberForeignImpl = map[string]string{
	"nomi.toml": entryToml,
	"main.nomi": `import {
    std/io
    shapes.{Circle, Shape}
}

impl Shape for Circle {
    fn area(value: Circle): Int {
        value.radius * 3
    }
}

fn main() {
    io.inspect(shapes.total([Circle{radius: 1}]))
}
`,
	"shapes.nomi": `pub interface Shape {
    fn area(value: self): Int
}

pub struct Circle {
    radius: Int
}

pub fn total(circles: List<Circle>): Int {
    Iter.map(circles, |c| Shape.area(c)) |> Iter.sum()
}

test "total" {
    assert total([Circle{radius: 1}]) == 3
}
`,
}

// readTree reads every file under dir, by path relative to dir, leaving
// out Go modules' files.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(path); ext != ".nomi" && filepath.Base(path) != "nomi.toml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// memberProjects are the projects whose documents
// TestLoweringFromTheKeptProgramMatchesTheSource lowers: the corpus
// directories of several files, the order project as written and without
// its duplicate impl, memberLibrary, memberForeignImpl, the dungeon (when
// it is there) and a generated project of about 1k lines.
func memberProjects(t *testing.T) map[string]map[string]string {
	t.Helper()
	projects := map[string]map[string]string{
		"order":                   memberOrderProject,
		"order without its error": withoutDuplicateImpl(memberOrderProject),
		"library":                 memberLibrary,
		"foreign impl":            memberForeignImpl,
	}
	dirs, err := filepath.Glob(filepath.Join("..", "..", "tests", "*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			continue
		}
		files := readTree(t, dir)
		n := 0
		for rel := range files {
			if strings.HasSuffix(rel, ".nomi") {
				n++
			}
		}
		if n < 2 {
			continue
		}
		if _, ok := files["nomi.toml"]; !ok && files["main.nomi"] != "" {
			files["nomi.toml"] = entryToml
		}
		projects[strings.TrimPrefix(filepath.ToSlash(dir), "../../tests/")] = files
	}
	if _, err := os.Stat(dungeonDir); err == nil {
		projects["dungeon"] = readTree(t, dungeonDir)
	}
	gen := projectgen.Generate(projectgen.Config{Seed: 1, Lines: 1000})
	files := map[string]string{"nomi.toml": entryToml}
	for key, text := range gen.Files {
		files[key+".nomi"] = text
	}
	projects["generated 1k"] = files
	// Each project again with loweringProbe appended to its first file
	// other than main.nomi, which gives that file a test whose lowering
	// declines a body.
	for name, files := range maps.Clone(projects) {
		var rels []string
		for rel := range files {
			if strings.HasSuffix(rel, ".nomi") && rel != "main.nomi" {
				rels = append(rels, rel)
			}
		}
		if len(rels) == 0 {
			continue
		}
		sort.Strings(rels)
		probed := maps.Clone(files)
		probed[rels[0]] += loweringProbe
		projects[name+", probe in "+rels[0]] = probed
	}
	return projects
}

// withoutDuplicateImpl is the order project with report's second
// `impl Shape for Circle` removed, so the program checks clean.
func withoutDuplicateImpl(files map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range files {
		out[k] = v
	}
	out["report.nomi"] = strings.Replace(out["report.nomi"], `impl Shape for Circle {
    fn area(value: Circle): Int {
        value.radius
    }
}

`, "", 1)
	return out
}

// Every open document of a project is lowered from the program the server
// keeps when that is the program `nomi check` builds for it, the program's
// entry and each file it reaches alike (analysis.EntryProgram), and
// publishes what lowering its text with its own front end
// (vmhost.CheckLowering) publishes, diagnostic for diagnostic. The
// documents whose analysis is not offered (the order project's, which has
// errors, and the library of memberForeignImpl, whose program alone lacks
// the entry's impl) lower their text.
func TestLoweringFromTheKeptProgramMatchesTheSource(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	projects := memberProjects(t)
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	// Documents, those lowered from an analysis, those of them other than
	// their program's entry, and those with lowering diagnostics, over
	// every project.
	var docs, analyzed, members, blocked, membersBlocked int
	memberDocs := map[string]int{}
	for _, name := range names {
		files := projects[name]
		t.Run(name, func(t *testing.T) {
			dir := writeProject(t, files)
			s := NewServer()
			var rels []string
			for rel := range files {
				if strings.HasSuffix(rel, ".nomi") {
					rels = append(rels, rel)
				}
			}
			sort.Strings(rels)
			for _, rel := range rels {
				s.docs.Open(pathToURI(filepath.Join(dir, rel)), files[rel])
			}
			for _, rel := range rels {
				path := filepath.Join(dir, rel)
				uri := pathToURI(path)
				text := files[rel]
				a, latest := s.loweringAnalysis(uri, text)
				if !latest {
					t.Fatalf("%s: the opened text is not the document's latest", rel)
				}
				want := loweringDiagnostics(path, text, nil)
				docs++
				if len(want) > 0 {
					blocked++
				}
				release := s.docs.View()
				ok := false
				if a != nil {
					_, ok, _ = analyzedlowering.Check(path, text, a)
				}
				got := loweringDiagnostics(path, text, a)
				release()
				if ok {
					analyzed++
					if _, hasMain := files["main.nomi"]; hasMain && rel != "main.nomi" {
						members++
						memberDocs[name]++
						if len(want) > 0 {
							membersBlocked++
						}
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s (lowered from the analysis: %v): from the kept program\n%s\nfrom the source\n%s", rel, ok, renderDiagnostics(got), renderDiagnostics(want))
				}
			}
		})
	}
	t.Logf("%d documents, %d with lowering diagnostics; %d lowered from the kept program, %d of them not its entry, %d of those with lowering diagnostics", docs, blocked, analyzed, members, membersBlocked)
	if membersBlocked < 5 {
		t.Errorf("only %d documents other than their program's entry were lowered from the kept program with lowering diagnostics", membersBlocked)
	}
	for _, name := range []string{"library", "dungeon", "generated 1k"} {
		if _, ok := projects[name]; ok && memberDocs[name] == 0 {
			t.Errorf("%s: no document but the entry was lowered from the kept program", name)
		}
	}
	if memberDocs["foreign impl"] != 0 || memberDocs["order"] != 0 {
		t.Errorf("a document of a program the front end would judge differently was lowered from the kept program: %v", memberDocs)
	}
}

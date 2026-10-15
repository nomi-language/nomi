package lsp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/frontend"
)

const multiManifest = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"

// multiMain imports shapes and calls `shapes.evens`, which the IR builder
// declines (loweringBlocked's shape), and declines a body of its own.
const multiMain = `import std/io
import shapes

fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

fn halves(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}

fn main() {
  io.print(shapes.evens([1, 2]))
  io.print(halves([1, 2]))
  io.inspect(shapes.area(2))
}
`

const multiShapes = `pub fn area(r: Int): Int {
  r * 3
}

fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

pub fn evens(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| skip_odd(x)) |> Iter.to_list()
}
`

// multiRingMain and multiRing import each other.
const multiRingMain = `import std/io
import ring

pub fn base(n: Int): Int {
  n + 1
}

fn main() {
  io.inspect(ring.twice(2))
}
`

const multiRing = `import main

pub fn twice(n: Int): Int {
  main.base(n) * 2
}
`

// multiFileCase is a project, the documents a server holds open in it,
// and the one whose lowering is compared.
type multiFileCase struct {
	name string
	// disk holds the project's files.
	disk map[string]string
	// open are documents opened, with these texts, before doc.
	open map[string]string
	// doc is the document lowered, opened with text (its disk text when
	// "").
	doc, text string
	// after changes the project once doc's analysis is installed.
	after func(t *testing.T, dir string)
	// analyzed is whether the run lowers doc's analysis.
	analyzed bool
	// parsed is how many project files the front end parses to lower doc.
	parsed int
	// blocked is how many lowering diagnostics doc gets.
	blocked int
}

var multiFileCases = []multiFileCase{
	{
		name:     "an importing file",
		disk:     map[string]string{"main.nomi": multiMain, "shapes.nomi": multiShapes},
		doc:      "main.nomi",
		analyzed: true, parsed: 2, blocked: 2,
	},
	{
		name:     "an importing file with unsaved edits",
		disk:     map[string]string{"main.nomi": multiMain, "shapes.nomi": multiShapes},
		doc:      "main.nomi",
		text:     strings.Replace(multiMain, "io.inspect(shapes.area(2))", "io.inspect(shapes.area(3))", 1),
		analyzed: true, parsed: 2, blocked: 2,
	},
	{
		name:     "an imported file open with its disk text",
		disk:     map[string]string{"main.nomi": multiMain, "shapes.nomi": multiShapes},
		open:     map[string]string{"shapes.nomi": multiShapes},
		doc:      "main.nomi",
		analyzed: true, parsed: 2, blocked: 2,
	},
	{
		name: "an imported file edited and not saved",
		disk: map[string]string{"main.nomi": multiMain, "shapes.nomi": multiShapes},
		open: map[string]string{"shapes.nomi": strings.Replace(multiShapes, "r * 3", "r * 4", 1)},
		doc:  "main.nomi",
		// The front end reads shapes.nomi from disk; the analysis read the
		// editor's text.
		analyzed: false, parsed: 2, blocked: 2,
	},
	{
		name: "an imported file changed on disk since the analysis",
		disk: map[string]string{"main.nomi": multiMain, "shapes.nomi": multiShapes},
		doc:  "main.nomi",
		after: func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "shapes.nomi"), strings.Replace(multiShapes, "r * 3", "r * 4", 1))
		},
		analyzed: false, parsed: 2, blocked: 2,
	},
	{
		name: "nomi.toml changed since the analysis",
		disk: map[string]string{"main.nomi": multiMain, "shapes.nomi": multiShapes},
		doc:  "main.nomi",
		after: func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "nomi.toml"), multiManifest+"\n")
		},
		analyzed: false, parsed: 2, blocked: 2,
	},
	{
		name: "the imported file",
		disk: map[string]string{"main.nomi": multiMain, "shapes.nomi": multiShapes},
		doc:  "shapes.nomi",
		// The server analyzes shapes.nomi through main.nomi, and offers
		// the program `nomi check shapes.nomi` builds from that analysis.
		analyzed: true, parsed: 1, blocked: 0,
	},
	{
		name: "an error in another file",
		disk: map[string]string{
			"main.nomi":   multiMain,
			"shapes.nomi": strings.Replace(multiShapes, "r * 3", "r * \"3\"", 1),
		},
		doc:      "main.nomi",
		analyzed: false, parsed: 2, blocked: 0,
	},
	{
		name:     "a cycle",
		disk:     map[string]string{"main.nomi": multiRingMain, "ring.nomi": multiRing},
		doc:      "main.nomi",
		analyzed: true, parsed: 3, blocked: 0,
	},
	{
		name: "a cycle through an entry with unsaved edits",
		disk: map[string]string{"main.nomi": multiRingMain, "ring.nomi": multiRing},
		doc:  "main.nomi",
		// The front end reads main.nomi from disk for ring's import.
		text:     strings.Replace(multiRingMain, "n + 1", "n + 2", 1),
		analyzed: false, parsed: 3, blocked: 0,
	},
	{
		name: "a test file in a cycle",
		disk: map[string]string{
			"main.nomi": multiRingMain + "\ntest \"base\" {\n  assert base(1) == 2\n}\n",
			"ring.nomi": multiRing,
		},
		doc: "main.nomi",
		// ring's import of the test file reaches the file's own analysis,
		// as it does for an entry.
		analyzed: true, parsed: 3, blocked: 0,
	},
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Lowering a document whose program has other project files from the
// server's analysis reports what lowering its text with its own front end
// (vmhost.CheckLowering) reports, and parses no file to do it. Where the
// server's analyses are not of the texts the front end would read, or
// another file has an error, the run lowers the text instead. Lowering
// from the analysis writes nothing any of its files' analyses reach.
func TestLoweringFromAnalysis_ProgramsOfSeveralFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	for _, c := range multiFileCases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"nomi.toml": multiManifest}
			for rel, text := range c.disk {
				files[rel] = text
			}
			dir := writeProject(t, files)
			s := NewServer()
			for rel, text := range c.open {
				s.docs.Open(pathToURI(filepath.Join(dir, rel)), text)
			}
			path := filepath.Join(dir, c.doc)
			uri := pathToURI(path)
			text := c.text
			if text == "" {
				text = c.disk[c.doc]
			}
			s.docs.Open(uri, text)
			a, latest := s.loweringAnalysis(uri, text)
			if !latest {
				t.Fatal("the opened text is not the document's latest")
			}
			if c.after != nil {
				c.after(t, dir)
			}

			before := frontend.FilesParsed()
			start := time.Now()
			want := loweringDiagnostics(path, text, nil)
			fromText := time.Since(start)
			parsedFromText := frontend.FilesParsed() - before

			var prints objectPrints
			if a != nil {
				prints = fingerprint(a)
			}
			ok := false
			if a != nil {
				_, ok, _ = analyzedlowering.Check(context.Background(), path, text, a)
			}
			if ok != c.analyzed {
				t.Fatalf("lowered from the analysis: %v, want %v (analysis offered: %v)", ok, c.analyzed, a != nil)
			}
			before = frontend.FilesParsed()
			start = time.Now()
			got := loweringDiagnostics(path, text, a)
			fromRun := time.Since(start)
			parsedFromRun := frontend.FilesParsed() - before

			if a != nil {
				if changed := prints.diff(fingerprint(a)); len(changed) > 0 {
					t.Errorf("lowering changed objects the analysis reaches, of types:\n  %s", strings.Join(changed, "\n  "))
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("from the analysis\n%s\nfrom the source\n%s", renderDiagnostics(got), renderDiagnostics(want))
			}
			if n := len(notSupported(want)); n != c.blocked {
				t.Errorf("%d lowering diagnostics, want %d:\n%s", n, c.blocked, renderDiagnostics(want))
			}
			if parsedFromText != int64(c.parsed) {
				t.Errorf("the front end parsed %d files, want %d", parsedFromText, c.parsed)
			}
			wantRun := int64(c.parsed)
			if c.analyzed {
				wantRun = 0
			}
			if parsedFromRun != wantRun {
				t.Errorf("the run parsed %d files, want %d", parsedFromRun, wantRun)
			}
			t.Logf("from the text: %d files parsed, %v; the run: %d files parsed, %v", parsedFromText, fromText, parsedFromRun, fromRun)
		})
	}
}

// An edit that changes the entry's interface checks the entry afresh and
// keeps the analysis of the file it imports (a dependents recheck), so the
// program the run lowers holds analyses of two checks. It lowers as the
// source does, and lowering it writes nothing the analyses reach.
func TestLoweringFromAnalysis_AfterADependentsRecheck(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, map[string]string{"nomi.toml": multiManifest, "main.nomi": multiMain, "shapes.nomi": multiShapes})
	s := NewServer()
	path := filepath.Join(dir, "main.nomi")
	uri := pathToURI(path)
	s.docs.Open(pathToURI(filepath.Join(dir, "shapes.nomi")), multiShapes)
	s.docs.Open(uri, multiMain)
	text := strings.Replace(multiMain, "fn halves(xs: List<Int>): List<Int> {", "fn halves(xs: List<Int>, _n: Int = 0): List<Int> {", 1)
	if text == multiMain {
		t.Fatal("the edit does not apply")
	}
	_, dependents, _, _ := s.docs.ProgramChecks()
	s.docs.Update(uri, text)
	if _, after, _, _ := s.docs.ProgramChecks(); after != dependents+1 {
		t.Fatalf("the edit checked %d programs by their dependents, want 1; whole programs because: %v", after-dependents, s.docs.FullCheckReasons())
	}
	a, latest := s.loweringAnalysis(uri, text)
	if a == nil || !latest || len(a.Files) != 1 {
		t.Fatalf("loweringAnalysis = %+v, %v; want main.nomi's program with shapes.nomi", a, latest)
	}
	// From the source first, which marks the shared stdlib's tail calls as any
	// first lowering in a process does.
	want := loweringDiagnostics(path, text, nil)
	prints := fingerprint(a)
	if _, ok, err := analyzedlowering.Check(context.Background(), path, text, a); !ok || err != nil {
		t.Fatalf("lowered from the analysis: %v, %v", ok, err)
	}
	got := loweringDiagnostics(path, text, a)
	if changed := prints.diff(fingerprint(a)); len(changed) > 0 {
		t.Errorf("lowering changed objects the analysis reaches, of types:\n  %s", strings.Join(changed, "\n  "))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("from the analysis\n%s\nfrom the source\n%s", renderDiagnostics(got), renderDiagnostics(want))
	}
	if n := len(notSupported(want)); n != 2 {
		t.Errorf("%d lowering diagnostics, want 2:\n%s", n, renderDiagnostics(want))
	}
}

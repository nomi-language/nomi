package analysis_test

import (
	"flag"
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/nomi-language/nomi/std"
)

// The harness the reductions over stored facts are held to (stored
// facts, stored_facts.go). For a checked program, each project file's
// facts read from the program's check from source must equal its facts
// read from its check against interfaces, and the program's reductions
// over bodies run over the facts, encoded and read back, must report what
// the passes over the program's analyses report: the boot-only calls, the
// application-field roots (when the program declares no application type),
// the whole-file imports and coherence's missing impls, diagnostic for
// diagnostic (StoredFactsForTest).
//
// TestStoredReductionsMatchTheProgram (about 40 seconds) runs over the
// incremental harness's projects and edit sequences, a project that draws
// each reduction's diagnostic (reductionsProject), and every multi-file
// corpus project with its entry edited, and fails when some reduction
// reported nothing over the whole run.
// FuzzStoredReductionsMatchTheProgram mutates and edits as
// FuzzIncrementalMatchesFull does, and ...Sample and ...Rotating run a
// fixed and a daily set. TestStoredFactsSupplyIgnoresModulesBuiltLater
// holds a record's supply to the same answer whatever std modules the
// reading process has built.

// storedDiff checks p's texts and answers how the stored facts or their
// reductions differ from the program's, or "".
func storedDiff(p incrementalProject, texts map[string]string) string {
	dm := newManager()
	full := dm.CheckProgram(p.root(), p.entry, texts)
	fromSource, fromInterfaces, whole, stored := dm.StoredFactsForTest(full)
	keys := make([]string, 0, len(fromSource))
	for key := range fromSource {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		got, ok := fromInterfaces[key]
		if !ok {
			return fmt.Sprintf("%s has no check against interfaces", key)
		}
		if string(got) != string(fromSource[key]) {
			a, _ := analysis.DecodeFileFacts(fromSource[key])
			b, _ := analysis.DecodeFileFacts(got)
			return fmt.Sprintf("%s's facts differ:\nfrom source:     %+v\nfrom interfaces: %+v", key, *a, *b)
		}
	}
	if d := dm.SupplyDiffForTest(full); d != "" {
		return "the supply from interfaces differs from the program's: " + d
	}
	if stored == nil {
		// A program not built over a stdlib base holding every std file
		// (one that declares a primitive a std file resolves) is never
		// reduced over facts: a build relinking it checks it whole.
		return ""
	}
	storedCompared.boot += len(whole.Boot)
	storedCompared.appRoots += len(whole.AppRoots)
	storedCompared.implImports += len(whole.ImplImports)
	storedCompared.coherence += len(whole.Coherence)
	if w, s := renderReductions(whole), renderReductions(stored); w != s {
		return "the reductions over stored facts differ from the program's:\n" + dumpDiff(w, s)
	}
	return ""
}

// storedCompared counts the diagnostics storedDiff compared, by pass.
var storedCompared struct{ boot, appRoots, implImports, coherence int }

// renderReductions prints r for comparison. A file's application-field
// root errors are sorted, since the check finds an entry boot's calls in
// the order of a map.
func renderReductions(r *analysis.Reductions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "application roots exact: %t\n", r.AppExact)
	section := func(name string, m map[string][]analysis.TypeError, sorted bool) {
		keys := make([]string, 0, len(m))
		for key := range m {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines := make([]string, 0, len(m[key]))
			for _, e := range m[key] {
				lines = append(lines, renderError(e))
			}
			if sorted {
				sort.Strings(lines)
			}
			fmt.Fprintf(&b, "%s %s:\n%s", name, key, strings.Join(lines, ""))
		}
	}
	section("boot", r.Boot, false)
	section("app roots", r.AppRoots, true)
	section("impl imports", r.ImplImports, false)
	for _, e := range r.Coherence {
		b.WriteString("coherence " + renderError(e))
	}
	return b.String()
}

func renderError(e analysis.TypeError) string {
	s := fmt.Sprintf("  %s %d:%d-%d:%d %q code=%q hints=%q", e.File, e.Line, e.Col, e.EndLine, e.EndCol, e.Message, e.Code, e.Hints)
	for _, r := range e.Related {
		s += fmt.Sprintf(" related(%+v)", r)
	}
	if e.Fix != nil {
		s += fmt.Sprintf(" fix(%+v)", *e.Fix)
	}
	return s + "\n"
}

// storedEdits checks p after each of fuzzSteps' edits of the file key and
// answers the first difference.
func storedEdits(p incrementalProject, key, src string, seed int64) string {
	texts := map[string]string{}
	for k, t := range p.texts {
		texts[k] = t
	}
	for i, s := range fuzzSteps(key, src, seed) {
		text, ok := s.change(texts[key], p.texts[key])
		if !ok {
			continue
		}
		texts[key] = text
		if diff := storedDiff(p, texts); diff != "" {
			return fmt.Sprintf("%s: after step %d (%s, editing %s): %s", p.name, i+1, s.kind, key, diff)
		}
	}
	return ""
}

// TestStoredReductionsMatchTheProgram holds the stored facts and their
// reductions to the program's over the incremental harness's projects and
// edit sequences and every multi-file corpus project with its entry
// edited.
func TestStoredReductionsMatchTheProgram(t *testing.T) {
	t.Parallel()
	steps := func(t *testing.T, p incrementalProject, edits []editStep) {
		t.Helper()
		texts := map[string]string{}
		for k, v := range p.texts {
			texts[k] = v
		}
		if diff := storedDiff(p, texts); diff != "" {
			t.Fatalf("%s: %s", p.name, diff)
		}
		for i, s := range edits {
			if isInputKey(s.key) {
				continue
			}
			text, ok := s.change(texts[s.key], p.texts[s.key])
			if !ok {
				t.Fatalf("%s: step %d (%s) does not apply to %s", p.name, i+1, s.kind, s.key)
			}
			texts[s.key] = text
			if diff := storedDiff(p, texts); diff != "" {
				t.Fatalf("%s: after step %d (%s, editing %s): %s", p.name, i+1, s.kind, s.key, diff)
			}
		}
	}
	t.Run("reductions", func(t *testing.T) { steps(t, reductionsProject(), reductionsEdits) })
	t.Run("layered", func(t *testing.T) { steps(t, layeredProject(), layeredEdits) })
	t.Run("signal", func(t *testing.T) { steps(t, signalProject(), signalEdits) })
	t.Run("dispatch", func(t *testing.T) { steps(t, dispatchProject(), dispatchEdits) })
	t.Run("local type argument", func(t *testing.T) { steps(t, rateProject(), rateEdits) })
	t.Run("same names in two files", func(t *testing.T) { steps(t, sameNamesProject(), nil) })
	t.Run("order", func(t *testing.T) {
		p := orderIncremental()
		steps(t, p, orderEdits)
		for _, key := range []string{"main", "shapes"} {
			steps(t, p, genericEdits(key, p.texts[key]))
		}
	})
	projects, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			steps(t, p, genericEdits(p.entry, p.texts[p.entry]))
		})
	}
	c := storedCompared
	t.Logf("compared files with boot-only errors %d, application-root errors %d, unused whole-file imports %d, missing impls %d",
		c.boot, c.appRoots, c.implImports, c.coherence)
	if c.boot == 0 || c.appRoots == 0 || c.implImports == 0 || c.coherence == 0 {
		t.Fatalf("some pass reported nothing, so the harness compared nothing of it: %+v", c)
	}
}

// TestStoredFactsSupplyIgnoresModulesBuiltLater checks a program, then
// builds every stdlib module, and holds the program's supply to the one its
// interfaces answer over the larger stdlib. A stdlib built a module at a
// time grows as other programs reach modules, and a record's supply is read
// in processes that built other modules than the one that wrote it.
// container_impls impls a std type, whose impls in modules it never reaches
// (TcpListener's Debug in std/net) the larger stdlib holds.
func TestStoredFactsSupplyIgnoresModulesBuiltLater(t *testing.T) {
	projects, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	const name = "11-interfaces-and-impls/container_impls"
	for _, p := range projects {
		if p.name != name {
			continue
		}
		dm := newManager()
		full := dm.CheckProgram(p.root(), p.entry, p.texts)
		std.Shared().Ensure(std.Shared().ModuleNames()...)
		if d := dm.SupplyDiffForTest(full); d != "" {
			t.Fatalf("%s: the supply from interfaces over every stdlib module differs from the program's: %s", name, d)
		}
		return
	}
	t.Fatalf("no corpus project %s", name)
}

// TestStoredReductionsMatchTheProgramSample runs storedEdits over a fixed
// sample: every file of the first fuzz project and the entry of every
// other second one.
func TestStoredReductionsMatchTheProgramSample(t *testing.T) {
	projects := fuzzProjects(t)
	for i, p := range projects {
		if i != 0 && i%2 != 1 {
			continue
		}
		keys := []string{p.entry}
		if i == 0 {
			keys = keys[:0]
			for key := range p.texts {
				keys = append(keys, key)
			}
			sort.Strings(keys)
		}
		for _, key := range keys {
			if diff := storedEdits(p, key, p.texts[key], int64(hashText(p.name+"/"+key))); diff != "" {
				t.Fatal(diff)
			}
		}
	}
}

// TestStoredReductionsMatchTheProgramRotating runs storedEdits over a
// different handful of edit sequences every UTC day.
func TestStoredReductionsMatchTheProgramRotating(t *testing.T) {
	projects := fuzzProjects(t)
	set := rotation.For(t, "./internal/analysis", 4)
	for _, seed := range set.Seeds() {
		rng := rand.New(rand.NewSource(seed))
		p, key := projectFile(projects, uint(rng.Intn(len(projects))), uint(rng.Intn(1<<16)))
		t.Logf("seed %d edits %s in %s", seed, key, p.name)
		if diff := storedEdits(p, key, p.texts[key], seed); diff != "" {
			t.Fatalf("%s%s", set.Failure(seed), diff)
		}
	}
}

// FuzzStoredReductionsMatchTheProgram edits one file of a multi-file
// project as FuzzIncrementalMatchesFull does and holds the stored facts and
// their reductions to the program's after each edit. Run it with
//
//	go test ./internal/analysis -run '^$' -fuzz '^FuzzStoredReductionsMatchTheProgram$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4
func FuzzStoredReductionsMatchTheProgram(f *testing.F) {
	projects := fuzzProjects(f)
	fuzz := flag.Lookup("test.fuzz")
	fuzzing := fuzz != nil && fuzz.Value.String() != ""
	for i, p := range projects {
		keys := make([]string, 0, len(p.texts))
		for key := range p.texts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for j, key := range keys {
			if (fuzzing || i == 0 && j == 0) && len(p.texts[key]) <= fuzzSeedText {
				f.Add(uint(i), uint(j), p.texts[key])
			}
		}
	}
	f.Fuzz(func(t *testing.T, project, file uint, src string) {
		if !utf8.ValidString(src) || len(src) > 2*fuzzSeedText {
			t.Skip()
		}
		p, key := projectFile(projects, project, file)
		hang := time.AfterFunc(fuzzHang, func() {
			buf := make([]byte, 1<<20)
			panic(fmt.Sprintf("the stored facts of %s in %s took over %v:\n%s", key, p.name, fuzzHang, buf[:runtime.Stack(buf, true)]))
		})
		defer hang.Stop()
		if diff := storedEdits(p, key, src, int64(hashText(src))); diff != "" {
			t.Fatal(diff)
		}
	})
}

// reductionsProject draws a diagnostic from each reduction over bodies: a
// chain of calls to Supervisor.new across two files that main and a
// private function nothing calls reach, a function value naming one, and
// a whole-file import of a file whose impl nothing uses.
func reductionsProject() incrementalProject {
	return incrementalProject{name: "reductions", entry: "main", texts: map[string]string{
		"chain": `import std::supervisors.Supervisor

pub fn deep() -> Int {
    _ = Supervisor.new(max_running: 1)
    1
}

pub fn plain() -> Int {
    2
}
`,
		"lib": `import chain

pub fn make() -> Int {
    chain.plain()
}

fn unused_private() -> Int {
    chain.deep()
}
`,
		"point": `pub struct Point {
    x: Int
}
`,
		"show": `pub interface Show {
    fn show(p: self) -> String
}
`,
		"impls": `import {
    point.{Point}
    show.{Show}
}

impl Show for Point {
    fn show(p: Point) -> String {
        "p${p.x}"
    }
}
`,
		"main": `import {
    std::io
    lib
    impls
    point.{Point}
}

fn main() {
    io.println("${lib.make()}")
    io.println("${Point{x: 1}.x}")
}
`,
	}}
}

var reductionsEdits = []editStep{
	replace("body calls a boot-only function", "lib", "    chain.plain()\n", "    chain.deep()\n"),
	replace("body names a boot-only function as a value", "main", "    io.println(\"${lib.make()}\")\n", "    f = lib.make\n    io.println(\"${f()}\")\n"),
	restore("body back", "main"),
	restore("body back", "lib"),
	replace("body calls Supervisor.new in a lambda", "chain", "pub fn plain() -> Int {\n    2\n", "pub fn plain() -> Int {\n    g = fn -> Supervisor.new(max_running: 2)\n    _ = g\n    2\n"),
	restore("body back", "chain"),
	replace("body calls a boot-only std function", "chain", "import std::supervisors.Supervisor\n", "import {\n    std::supervisors.Supervisor\n    std::jobs.{Worker}\n    std::jobs_testing.{Inline}\n}\n"),
	replace("body calls a boot-only std function", "chain", "pub fn plain() -> Int {\n    2\n", "pub fn plain() -> Int {\n    _ = Worker.new(Inline.new(), 1)\n    2\n"),
	restore("body back", "chain"),
}

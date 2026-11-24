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
// cyclic `once` bindings, the application-field roots (when the program declares no application type),
// the whole-file imports and coherence's missing impls, diagnostic for
// diagnostic (StoredFactsForTest).
//
// TestStoredReductionsMatchTheProgram (about 10 seconds) runs over the
// incremental harness's projects and edit sequences, a project that draws
// each reduction's diagnostic (reductionsProject), and a sample of the
// multi-file corpus projects with their entry edited, every one under
// NOMI_CORPUS_WALKS=full (about a minute), and fails when some reduction
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
	storedCompared.once += len(whole.Once)
	storedCompared.appRoots += len(whole.AppRoots)
	storedCompared.implImports += len(whole.ImplImports)
	storedCompared.coherence += len(whole.Coherence)
	if w, s := renderReductions(whole), renderReductions(stored); w != s {
		return "the reductions over stored facts differ from the program's:\n" + dumpDiff(w, s)
	}
	return ""
}

// storedCompared counts the diagnostics storedDiff compared, by pass.
var storedCompared struct{ boot, once, appRoots, implImports, coherence int }

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
	section("once", r.Once, false)
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
// edit sequences and every eighth multi-file corpus project with its entry
// edited (incrementalStride); NOMI_CORPUS_WALKS=full edits every one.
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
	walk := rotation.CorpusWalk(t, incrementalStride)
	for i, p := range projects {
		if !walk.Takes(i) {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			steps(t, p, genericEdits(p.entry, p.texts[p.entry]))
		})
	}
	c := storedCompared
	t.Logf("compared files with boot-only errors %d, once cycles %d, application-root errors %d, unused whole-file imports %d, missing impls %d",
		c.boot, c.once, c.appRoots, c.implImports, c.coherence)
	if c.boot == 0 || c.once == 0 || c.appRoots == 0 || c.implImports == 0 || c.coherence == 0 {
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

// The missing-impl help read from stored facts names a generic interface's
// type parameters as the program's check does: the demand carries them
// (Recording.IfaceTypeParams), since the reduction over facts has no
// declaration of the interface at hand.
func TestStoredReductions_AGenericInterfaceHelpNamesItsParameters(t *testing.T) {
	p := incrementalProject{name: "generic-help", entry: "main", texts: map[string]string{
		"scale": genericInterfaces,
		"main": `import
    scale
end

struct Other
    x: Int
end

fn main()
    dbg scale.Scale.scale(Other{x: 1}, 2)
    dbg scale.Pair.pair(Other{x: 1}, 2)
end
`,
	}}
	if diff := storedDiff(p, p.texts); diff != "" {
		t.Fatal(diff)
	}
	dm := newManager()
	_, _, _, stored := dm.StoredFactsForTest(dm.CheckProgram(p.root(), p.entry, p.texts))
	if stored == nil {
		t.Fatal("the program is not reduced over stored facts")
	}
	var got []string
	for _, e := range stored.Coherence {
		got = append(got, e.Message+" | "+strings.Join(e.Hints, " | "))
	}
	sort.Strings(got)
	want := []string{
		"no impl of `Pair` for `Other` (required by the call `scale.Pair.pair`) | write an `impl Pair<A, B> for Other` block",
		"no impl of `Scale` for `Other` (required by the call `scale.Scale.scale`) | write an `impl Scale<N> for Other` block",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the reduction over stored facts reports:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A derive over a field whose type names nothing demands no impl of it, in
// the reduction over stored facts as in the check (DemandFact.NoType), and
// a derive's demand of a field beside it still draws its missing impl.
func TestStoredReductions_ADeriveOverAPartOfNoTypeDemandsNothing(t *testing.T) {
	p := incrementalProject{name: "part-of-no-type", entry: "main", texts: map[string]string{
		"changes": `pub struct Thing
    f: (Int) -> Int
end

pub struct Changes
    email: Maybe<String>
    thing: Thing
end

derive Equatable for Changes
`,
		"main": `import
    changes
end

fn main()
    dbg changes.Changes
end
`,
	}}
	if diff := storedDiff(p, p.texts); diff != "" {
		t.Fatal(diff)
	}
	dm := newManager()
	_, _, _, stored := dm.StoredFactsForTest(dm.CheckProgram(p.root(), p.entry, p.texts))
	if stored == nil {
		t.Fatal("the program is not reduced over stored facts")
	}
	var got []string
	for _, e := range stored.Coherence {
		got = append(got, e.Message)
	}
	want := "no impl of `Equatable` for `Thing` (required via `derive Equatable for Changes`, field `thing`)"
	if strings.Join(got, "\n") != want {
		t.Errorf("the reduction over stored facts reports:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// A demand that is no derive's, of a type another file declares with a
// field whose type names nothing, demands nothing, in the reduction over
// stored facts as in the check (DemandFact.NoType), and the same demand of
// a healthy type beside it still draws its missing impl.
func TestStoredReductions_ADemandOfATypeWithAPartOfNoTypeDemandsNothing(t *testing.T) {
	p := incrementalProject{name: "demand-of-no-type", entry: "main", texts: map[string]string{
		"changes": `pub struct Thing
    n: Int
end

pub struct Changes
    email: Nope
end
`,
		"main": `import
    changes.{Changes, Thing}
end

fn main()
    c = Changes{email: 1}
    t = Thing{n: 1}
    _ = c < c
    _ = Comparable.compare(c, c)
    _ = t < t
end
`,
	}}
	if diff := storedDiff(p, p.texts); diff != "" {
		t.Fatal(diff)
	}
	dm := newManager()
	_, _, _, stored := dm.StoredFactsForTest(dm.CheckProgram(p.root(), p.entry, p.texts))
	if stored == nil {
		t.Fatal("the program is not reduced over stored facts")
	}
	var got []string
	for _, e := range stored.Coherence {
		got = append(got, e.Message)
	}
	want := "no impl of `Comparable` for `Thing` (required via ordering operator `<`)"
	if strings.Join(got, "\n") != want {
		t.Errorf("the reduction over stored facts reports:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// A call of std's Worker.new in a lambda and in a test body, with no
// function of the program calling it elsewhere, is reported by the check
// and by the reduction over stored facts alike.
func TestStoredReductions_AStdBootOnlyCallOutsideBootIsReportedByBoth(t *testing.T) {
	p := incrementalProject{name: "std-boot-only", entry: "main", texts: map[string]string{
		"main": `import
    std::io
    std::jobs.Worker
    std::jobs::testing.Inline
end

fn main()
    io.print("hi")
end

pub fn later() -> List<Worker>
    [1, 2] |> Iter.map(fn _ -> Worker.new(Inline.new(), max_running: 2)) |> Iter.to_list()
end

test "a worker"
    queue = Inline.new()
    w = Worker.new(queue, max_running: 2)
    assert Worker.id(w) != ""
end
`,
	}}
	if diff := storedDiff(p, p.texts); diff != "" {
		t.Fatal(diff)
	}
	dm := newManager()
	_, _, whole, stored := dm.StoredFactsForTest(dm.CheckProgram(p.root(), p.entry, p.texts))
	if stored == nil {
		t.Fatal("the program is not reduced over stored facts")
	}
	var got []string
	for _, e := range whole.Boot["main"] {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message[:strings.Index(e.Message, " at jobs.nomi:")]))
	}
	want := []string{
		"12:42 `Worker.new` creates a supervisor (`Supervisor.new`",
		"17:19 `Worker.new` creates a supervisor (`Supervisor.new`",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the check reports:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// reductionsProject draws a diagnostic from each reduction over bodies: a
// chain of calls to Supervisor.new across two files that main and a
// private function nothing calls reach, a function value naming one, and
// a whole-file import of a file whose impl nothing uses. Its edits add a
// `once` whose initializer reaches itself through the other file.
func reductionsProject() incrementalProject {
	return incrementalProject{name: "reductions", entry: "main", texts: map[string]string{
		"chain": `import std::supervisors.Supervisor

pub fn deep() -> Int
    _ = Supervisor.new(max_running: 1)
    1
end

pub fn plain() -> Int
    2
end
`,
		"lib": `import chain

pub fn make() -> Int
    chain.plain()
end

fn unused_private() -> Int
    chain.deep()
end
`,
		"point": `pub struct Point
    x: Int
end
`,
		"show": `pub interface Show
    fn show(p: self) -> String
end
`,
		"impls": `import
    point.{Point}
    show.{Show}
end

impl Show for Point
    fn show(p: Point) -> String
        "p${p.x}"
    end
end
`,
		"main": `import
    std::io
    lib
    impls
    point.{Point}
end

fn main()
    io.println("${lib.make()}")
    io.println("${Point{x: 1}.x}")
end
`,
	}}
}

var reductionsEdits = []editStep{
	replace("body calls a boot-only function", "lib", "    chain.plain()\n", "    chain.deep()\n"),
	replace("body names a boot-only function as a value", "main", "    io.println(\"${lib.make()}\")\n", "    f = lib.make\n    io.println(\"${f()}\")\n"),
	restore("body back", "main"),
	restore("body back", "lib"),
	replace("body calls Supervisor.new in a lambda", "chain", "pub fn plain() -> Int\n    2\n", "pub fn plain() -> Int\n    g = fn -> Supervisor.new(max_running: 2)\n    _ = g\n    2\n"),
	restore("body back", "chain"),
	replace("body calls a boot-only std function", "chain", "import std::supervisors.Supervisor\n", "import\n    std::supervisors.Supervisor\n    std::jobs.{Worker}\n    std::jobs::testing.{Inline}\nend\n"),
	replace("body calls a boot-only std function", "chain", "pub fn plain() -> Int\n    2\n", "pub fn plain() -> Int\n    _ = Worker.new(Inline.new(), 1)\n    2\n"),
	restore("body back", "chain"),
	// A std boot-only function no function of the program calls outside a
	// lambda is boot-only all the same.
	replace("imports a boot-only std function", "chain", "import std::supervisors.Supervisor\n", "import\n    std::supervisors.Supervisor\n    std::jobs.{Worker}\n    std::jobs::testing.{Inline}\nend\n"),
	replace("a lambda calls the boot-only std function", "chain", "pub fn plain() -> Int\n    2\n", "pub fn plain() -> Int\n    g = fn -> Worker.new(Inline.new(), 1)\n    _ = g\n    2\n"),
	replace("a test body calls it", "chain", "pub fn deep() -> Int\n", "test \"a worker\"\n    _ = Worker.new(Inline.new(), 1)\nend\n\npub fn deep() -> Int\n"),
	replace("a body names it as a value", "chain", "    _ = g\n    2\n", "    _ = g\n    h = Worker.new\n    _ = h\n    2\n"),
	restore("body back", "chain"),
	replace("a once calls into another file", "lib", "fn unused_private() -> Int\n", "pub once total: Int = chain.plain()\n\nfn unused_private() -> Int\n"),
	replace("the other file imports it back", "chain", "import std::supervisors.Supervisor\n", "import\n    std::supervisors.Supervisor\n    lib\nend\n"),
	replace("its function reads the once: a cycle", "chain", "pub fn plain() -> Int\n    2\n", "pub fn plain() -> Int\n    lib.total\n"),
	restore("body back", "chain"),
	restore("body back", "lib"),
}

// The reductions over stored facts read the stdlib's demands and supply
// from the base the program is built over, which ReduceFacts takes from an
// analysis of the program and never defaults: with no base, a program's
// demand of a std impl reads as unmet, as `no impl of Display for String`
// for this program's interpolation.
func TestReduceFacts_ReadsTheStdlibOfTheProgram(t *testing.T) {
	p := incrementalProject{name: "std-context", entry: "main", texts: map[string]string{
		"names": "pub fn greet(name: String) -> String\n    \"hello ${name}\"\nend\n",
		"main":  "import\n    names\nend\n\nfn main()\n    dbg names.greet(\"x\")\nend\n",
	}}
	dm := newManager()
	full := dm.CheckProgram(p.root(), p.entry, p.texts)
	requireClean(t, full, "the program")
	red, ok, noStd := analysis.ReduceSourceFactsForTest(full, full.Files[full.Entry])
	if !ok {
		t.Fatal("ReduceFacts refuses a program built over the stdlib's base")
	}
	if !red.Clean(false) {
		t.Fatalf("the reductions over the program's facts find something: %+v", *red)
	}
	if len(noStd) != 1 || !strings.Contains(noStd[0].Message, "no impl of `Display` for `String`") {
		t.Fatalf("the facts reduced with no stdlib find %v, want the missing Display for String an empty stdlib reads; without it this test does not tell a reduction with the stdlib from one without", noStd)
	}
	if _, ok, _ := analysis.ReduceSourceFactsForTest(full, nil); ok {
		t.Fatal("ReduceFacts reduces with no analysis to read the stdlib from")
	}
}

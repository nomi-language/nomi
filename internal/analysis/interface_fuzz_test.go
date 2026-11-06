package analysis_test

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/rotation"
)

// The harness a check against interfaces is held to. Every project file of
// a program is checked against the interfaces of the files it reads
// (DocumentManager.CheckFromInterfacesForTest: its import closure's
// interfaces, written and read back, and the impls it can see of the other
// files), and the program those checks make must dump as a check of every
// file from source does (analysis.DumpProgramForTest), byte for byte.

// fromInterfacesDiff checks p's texts from source and each file against
// interfaces, and answers how the two programs differ, or "". A difference
// a known gap explains (knownInterfaceGaps) is "".
func fromInterfacesDiff(p incrementalProject, texts map[string]string) string {
	want, got := interfaceDumps(p, texts)
	if got == want || knownInterfaceGapFor(want, got) != nil {
		return ""
	}
	return dumpDiff(want, got)
}

// interfaceDumps are the dumps of p's texts checked from source and with
// each file checked against interfaces.
func interfaceDumps(p incrementalProject, texts map[string]string) (want, got string) {
	dm := newManager()
	full := dm.CheckProgram(p.root(), p.entry, texts)
	want = analysis.DumpProgramForTest(full)
	got = analysis.DumpProgramForTest(dm.CheckFromInterfacesForTest(full))
	return want, got
}

// interfaceGap is a known difference between a file checked against
// interfaces and the program checked from source: a break in the rule the
// harness holds. Each is pinned by a reproducer TestKnownInterfaceGaps
// checks still differs; when a fix makes it agree, the test fails, and the
// entry goes in the same change.
type interfaceGap struct {
	name string
	// fix says where the fix belongs.
	fix string
	// match reports whether the two dumps' first differing lines, from
	// source and against interfaces, and the full dump up to them, are
	// this gap's.
	match func(before []string, want, got string) bool
	// reproduce answers the reproducer: a project and its texts.
	reproduce func() (incrementalProject, map[string]string)
}

var knownInterfaceGaps = []interfaceGap{}

// knownInterfaceGapFor is the known gap the first difference of want and
// got is, or nil.
func knownInterfaceGapFor(want, got string) *interfaceGap {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(w) && i < len(g) && w[i] == g[i] {
		i++
	}
	if i >= len(w) || i >= len(g) {
		return nil
	}
	for j := range knownInterfaceGaps {
		if knownInterfaceGaps[j].match(w[:i], w[i], g[i]) {
			return &knownInterfaceGaps[j]
		}
	}
	return nil
}

// TestKnownInterfaceGaps holds each known gap's reproducer to its
// difference.
func TestKnownInterfaceGaps(t *testing.T) {
	for _, gap := range knownInterfaceGaps {
		t.Run(gap.name, func(t *testing.T) {
			p, texts := gap.reproduce()
			want, got := interfaceDumps(p, texts)
			if g := knownInterfaceGapFor(want, got); want == got || g == nil || g.name != gap.name {
				t.Fatalf("this reproducer no longer shows the gap; if it is fixed (%s), remove %q from knownInterfaceGaps", gap.fix, gap.name)
			}
		})
	}
}

// interfaceEdits checks p after each of fuzzSteps' edits of the file key,
// from source and against interfaces, and answers the first difference.
func interfaceEdits(tb testing.TB, p incrementalProject, key, src string, seed int64) string {
	tb.Helper()
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
		if diff := fromInterfacesDiff(p, texts); diff != "" {
			return fmt.Sprintf("%s: after step %d (%s, editing %s) the program checked against interfaces differs from a full check:\n%s",
				p.name, i+1, s.kind, key, diff)
		}
	}
	return ""
}

// FuzzCheckFromInterfacesMatchesFull edits one file of a multi-file
// project (fuzzProjects), as FuzzIncrementalMatchesFull does, and after
// each edit checks every file of the program against interfaces and
// compares the program those checks make with a full check of the same
// texts. Run it with
//
//	go test ./internal/analysis -run '^$' -fuzz '^FuzzCheckFromInterfacesMatchesFull$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4
//
// Under plain `go test` it runs one seed, and
// TestCheckFromInterfacesMatchesFullSample and
// TestCheckFromInterfacesMatchesFullRotating run a fixed and a daily set.
func FuzzCheckFromInterfacesMatchesFull(f *testing.F) {
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
			panic(fmt.Sprintf("checking %s in %s against interfaces took over %v:\n%s", key, p.name, fuzzHang, buf[:runtime.Stack(buf, true)]))
		})
		defer hang.Stop()
		if diff := interfaceEdits(t, p, key, src, int64(hashText(src))); diff != "" {
			t.Fatal(diff)
		}
	})
}

// TestCheckFromInterfacesMatchesFullSample runs interfaceEdits over every
// project as it is, and over a fixed sample of edits: every file of
// orderProject and the entry of every other corpus project.
func TestCheckFromInterfacesMatchesFullSample(t *testing.T) {
	projects := fuzzProjects(t)
	for _, p := range projects {
		if diff := fromInterfacesDiff(p, p.texts); diff != "" {
			t.Fatalf("%s checked against interfaces differs from a full check:\n%s", p.name, diff)
		}
	}
	for i, p := range projects {
		if i != 0 && i%2 != 1 {
			continue
		}
		var keys []string
		if i == 0 {
			for key := range p.texts {
				keys = append(keys, key)
			}
			sort.Strings(keys)
		} else {
			keys = []string{p.entry}
		}
		for _, key := range keys {
			if diff := interfaceEdits(t, p, key, p.texts[key], int64(hashText(p.name+"/"+key))); diff != "" {
				t.Fatal(diff)
			}
		}
	}
}

// interfaceSteps applies steps to p in order and after each checks the
// program from source and against interfaces, failing tb on a difference.
// An edit to an input (a catalog) writes it where the checks read it.
func interfaceSteps(tb testing.TB, p incrementalProject, steps []editStep) {
	tb.Helper()
	texts := map[string]string{}
	for k, t := range p.texts {
		texts[k] = t
	}
	if diff := fromInterfacesDiff(p, texts); diff != "" {
		tb.Fatalf("%s checked against interfaces differs from a full check:\n%s", p.name, diff)
	}
	inputs := map[string]string{}
	for i, s := range steps {
		if isInputKey(s.key) {
			path := filepath.Join(p.dir, filepath.FromSlash(s.key))
			orig, err := os.ReadFile(path)
			if err != nil {
				tb.Fatal(err)
			}
			if _, ok := inputs[path]; !ok {
				inputs[path] = string(orig)
				tb.Cleanup(func() { os.WriteFile(path, orig, 0o644) })
			}
			text, ok := s.change(string(orig), inputs[path])
			if !ok {
				tb.Fatalf("%s: step %d (%s) does not apply", p.name, i+1, s.kind)
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				tb.Fatal(err)
			}
		} else {
			text, ok := s.change(texts[s.key], p.texts[s.key])
			if !ok {
				tb.Fatalf("%s: step %d (%s) does not apply to %s", p.name, i+1, s.kind, s.key)
			}
			texts[s.key] = text
		}
		if diff := fromInterfacesDiff(p, texts); diff != "" {
			tb.Fatalf("%s: after step %d (%s, editing %s) the program checked against interfaces differs from a full check:\n%s",
				p.name, i+1, s.kind, s.key, diff)
		}
	}
}

// TestCheckFromInterfacesMatchesFull checks against interfaces the
// projects and edit sequences TestIncrementalMatchesFull holds rechecks to:
// a file that calls an impl declared in a file it does not import
// (dispatch), impls of std interfaces at a local type (rate), another
// file's functions as iteration callbacks (signal), an application type
// read by files that do not import the entry's boot (app types), message
// declarations and their catalogs, and every multi-file corpus project.
func TestCheckFromInterfacesMatchesFull(t *testing.T) {
	t.Parallel()
	t.Run("layered", func(t *testing.T) { interfaceSteps(t, layeredProject(), layeredEdits) })
	t.Run("signal", func(t *testing.T) { interfaceSteps(t, signalProject(), signalEdits) })
	t.Run("messages", func(t *testing.T) { interfaceSteps(t, messagesProject(t), messagesEdits) })
	t.Run("dispatch", func(t *testing.T) { interfaceSteps(t, dispatchProject(), dispatchEdits) })
	t.Run("local type argument", func(t *testing.T) { interfaceSteps(t, rateProject(), rateEdits) })
	t.Run("same names in two files", func(t *testing.T) {
		interfaceSteps(t, sameNamesProject(), nil)
	})
	t.Run("order", func(t *testing.T) {
		p := orderIncremental()
		interfaceSteps(t, p, orderEdits)
		for _, key := range []string{"main", "shapes"} {
			interfaceSteps(t, p, genericEdits(key, p.texts[key]))
		}
	})
	projects, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		t.Run(p.name, func(t *testing.T) {
			if p.name == "15-app-and-defer/app_field_permission" {
				interfaceSteps(t, p, appTypeEdits)
			}
			interfaceSteps(t, p, genericEdits(p.entry, p.texts[p.entry]))
		})
	}
}

// rotatingInterfaceCount is how many edit sequences
// TestCheckFromInterfacesMatchesFullRotating runs.
const rotatingInterfaceCount = 4

// TestCheckFromInterfacesMatchesFullRotating runs interfaceEdits beside the
// fixed sample, a different handful every UTC day (internal/rotation).
func TestCheckFromInterfacesMatchesFullRotating(t *testing.T) {
	projects := fuzzProjects(t)
	set := rotation.For(t, "./internal/analysis", rotatingInterfaceCount)
	for _, seed := range set.Seeds() {
		rng := rand.New(rand.NewSource(seed))
		p, key := projectFile(projects, uint(rng.Intn(len(projects))), uint(rng.Intn(1<<16)))
		t.Logf("seed %d edits %s in %s", seed, key, p.name)
		if diff := interfaceEdits(t, p, key, p.texts[key], seed); diff != "" {
			t.Fatalf("%s%s", set.Failure(seed), diff)
		}
	}
}

// mustCorpusProject is the multi-file corpus project named name.
func mustCorpusProject(name string) incrementalProject {
	projects, err := corpusProjects()
	if err != nil {
		panic(err)
	}
	for _, p := range projects {
		if p.name == name {
			return p
		}
	}
	panic("no corpus project " + name)
}

// TestUndefinedTypeOwnerReadsNoOtherFile holds a file that names a type it
// does not declare or import (`Box.inspect`, with `Box` declared only in a
// file it does not import) to the same check from source and against
// interfaces: the access stays untyped, and no other file's type function
// answers it. FuzzCheckFromInterfacesMatchesFull found the program's check
// typing it through another file's impl by name.
func TestUndefinedTypeOwnerReadsNoOtherFile(t *testing.T) {
	p := mustCorpusProject("12-derives-and-standard-interfaces/cross_file_debug")
	src := p.texts[p.entry]
	texts := map[string]string{p.entry: src}
	steps := fuzzSteps("shapes", src, int64(hashText(src)))
	texts["shapes"], _ = steps[1].change(src, src)
	want, got := interfaceDumps(p, texts)
	if want != got {
		t.Fatalf("checked against interfaces differs from a full check:\n%s", dumpDiff(want, got))
	}
	if !strings.Contains(want, "undefined type or variant 'Box'") {
		t.Fatalf("the reproducer no longer names an undefined Box:\n%s", want)
	}
}

// TestAGenericReceiversImplIsSeenWithoutItsFile holds an impl for a generic
// receiver written without a `<T>` clause (`impl Named for Box<T>`) to the
// same check from source and against interfaces in a file that imports
// the interface and the type but not the impl's file, and so sees the
// impl. Its header named its own file too, through `T`, so a check against
// interfaces read the impl only in a file that imported its file, and
// `[Box{v: 1}]` as `List<Named>` was a type mismatch there.
func TestAGenericReceiversImplIsSeenWithoutItsFile(t *testing.T) {
	p := incrementalProject{name: "generic receiver impl", entry: "main", texts: map[string]string{
		"box":   "pub struct Box<T> {\n    v: T\n}\n",
		"named": "pub interface Named {\n    fn name(x: self) -> String\n}\n",
		"impls": "import {\n    box.{Box}\n    named.{Named}\n}\n\nimpl Named for Box<T> {\n    fn name(_b: Box<T>) -> String {\n        \"box\"\n    }\n}\n",
		"use":   "import {\n    box.{Box}\n    named.{Named}\n}\n\npub fn boxes() -> List<Named> {\n    [Box{v: 1}]\n}\n",
		"main":  "import {\n    std::io\n    named.{Named}\n    use\n    impls\n}\n\nfn main() {\n    io.inspect(use.boxes() |> Iter.map(fn x -> Named.name(x)) |> Iter.to_list())\n}\n",
	}}
	want, got := interfaceDumps(p, p.texts)
	if want != got {
		t.Fatalf("checked against interfaces differs from a full check:\n%s", dumpDiff(want, got))
	}
	dm := newManager()
	full := dm.CheckProgram(p.root(), p.entry, p.texts)
	fromInterfaces, _ := dm.ImplHeadersForTest(full)
	for _, h := range fromInterfaces["impls"] {
		if slices.Contains(h.Files, "impls") {
			t.Errorf("the impl's header names its own file: %v", h.Files)
		}
	}
}

// TestAnImplLeftInABodyLeavesTheInterfaceMethodUntyped holds an impl block
// a parse error leaves inside a function body (an unclosed `{` before it)
// to the same check from source and against interfaces. The impl's
// function has no symbol of its own there, and typing nested functions
// looked its name up in module scope, wrote the impl function's signature
// onto the interface's method of that name, and so typed a call
// `Has.count_of(h, 2)` in another file only in the full check.
// FuzzCheckFromInterfacesMatchesFull found it in
// 11-interfaces-and-impls/boxed_existentials.
func TestAnImplLeftInABodyLeavesTheInterfaceMethodUntyped(t *testing.T) {
	p := incrementalProject{name: "impl left in a body", entry: "main", texts: map[string]string{
		"has":  "pub interface Has<T> {\n    fn count_of(value: self, x: T) -> Int\n}\n\nfn broken(xs: List<Int>) -> Int {\n    xs |> Iter.map(&T.showe T: Show {\n}\n\nimpl Has<Int> for List<Int> {\n    fn count_of(xs: List<Int>, x: Int) -> Int {\n        xs |> Iter.filter(&(&1 == x)) |> Iter.count()\n    }\n}\n",
		"main": "import {\n    std::io\n    has.{Has}\n}\n\nfn main() {\n    h: Has<Int> = [1, 2, 2]\n    io.inspect(Has.count_of(h, 2))\n}\n",
	}}
	want, got := interfaceDumps(p, p.texts)
	if want != got {
		t.Fatalf("checked against interfaces differs from a full check:\n%s", dumpDiff(want, got))
	}
	if !strings.Contains(want, "def 2:8 count_of") {
		t.Fatalf("the reproducer no longer declares the interface method at 2:8:\n%s", want)
	}
	for _, line := range strings.Split(want, "\n") {
		if strings.HasPrefix(line, "def 2:8 count_of") && !strings.Contains(line, "= <nil>") {
			t.Errorf("the interface method took a function's type: %s", line)
		}
	}
}

// TestImplHeadersFromInterfacesMatchTheProgram holds the impl headers a
// check against interfaces works out for each file, from the file built
// against its closure's interfaces alone, to the program's check from
// source, over the harness's projects and every multi-file corpus project.
func TestImplHeadersFromInterfacesMatchTheProgram(t *testing.T) {
	projects := []incrementalProject{layeredProject(), signalProject(), dispatchProject(), rateProject(), sameNamesProject(), orderIncremental()}
	corpus, err := corpusProjects()
	if err != nil {
		t.Fatal(err)
	}
	projects = append(projects, corpus...)
	impls := 0
	for _, p := range projects {
		dm := newManager()
		full := dm.CheckProgram(p.root(), p.entry, p.texts)
		got, want := dm.ImplHeadersForTest(full)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: impl headers from interfaces %v, from the program %v", p.name, got, want)
		}
		for _, h := range want {
			impls += len(h)
		}
	}
	if impls == 0 {
		t.Fatal("no project has an impl, so nothing was compared")
	}
}

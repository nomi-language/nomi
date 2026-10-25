package analysis_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/virtualproject"
	"github.com/nomi-language/nomi/std"
)

// stdBaseSeed is one program the base tests check: a corpus file checked in
// place, or an in-memory tour block or spec program.
type stdBaseSeed struct {
	name string
	// path is the corpus file's path; empty for an in-memory program.
	path string
	src  string
}

// stdBaseSeeds are every corpus file under tests/, every fixture under
// internal/irbuild/testdata (the deliberately failing ones among them),
// every runnable tour block, and every complete program in docs/spec.md.
func stdBaseSeeds(t *testing.T) []stdBaseSeed {
	t.Helper()
	var seeds []stdBaseSeed
	for _, dir := range []string{"../../tests", "../irbuild/testdata"} {
		root, err := filepath.Abs(dir)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".nomi") {
				paths = append(paths, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(paths)
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(root, path)
			seeds = append(seeds, stdBaseSeed{name: filepath.Base(dir) + "/" + filepath.ToSlash(rel), path: path, src: string(data)})
		}
	}
	chapters, err := filepath.Glob("../../tour/src/content/docs/*.md")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(chapters)
	for _, chapter := range chapters {
		data, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			if b.Runs() {
				seeds = append(seeds, stdBaseSeed{name: fmt.Sprintf("tour/%s:L%d", filepath.Base(chapter), b.Line), src: b.Code})
			}
		}
	}
	spec, err := os.ReadFile("../../docs/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	specMain := regexp.MustCompile(`(?m)^fn main\(`)
	for _, b := range doctest.ExtractBlocks(string(spec), "nomi") {
		if specMain.MatchString(b.Code) && !strings.Contains(b.Code, "...") {
			seeds = append(seeds, stdBaseSeed{name: fmt.Sprintf("spec:L%d", b.Line), src: b.Code})
		}
	}
	return seeds
}

// checkSeed runs the front end over seed, attached tests kept, and answers
// its entry's analysis (nil when the source does not parse) and every
// diagnostic it reports.
func checkSeed(t *testing.T, seed stdBaseSeed) (*analysis.FileAnalysis, string) {
	t.Helper()
	var c *frontend.Checker
	src := seed.src
	if seed.path != "" {
		c = frontend.New(frontend.Config{})
		c.PrepareFile(seed.path, seed.path)
	} else {
		vp, err := virtualproject.FromMarkedSource(seed.src)
		if err != nil {
			return nil, "project: " + err.Error()
		}
		c = frontend.New(frontend.Config{VirtualFiles: vp.VirtualFiles, VirtualManifest: vp.Manifest})
		src = vp.EntrySource
	}
	nodes, err := c.Prepare(src, frontend.Mode{Tests: true})
	if err != nil {
		return nil, "prepare: " + err.Error()
	}
	fa, _, err := c.Analyze(nodes)
	if err != nil {
		return fa, err.Error()
	}
	return fa, ""
}

// dumpSeed is what checking seed decides: its diagnostics, every program
// file's analysis (analysis.DumpFileForTest), the program's impl index
// (analysis.DumpProgramIndexForTest) and its missing-impl diagnostics.
func dumpSeed(t *testing.T, seed stdBaseSeed) (string, bool) {
	t.Helper()
	fa, diags := checkSeed(t, seed)
	root := ""
	if seed.path != "" {
		root = filepath.Dir(seed.path)
	}
	var b strings.Builder
	b.WriteString("diagnostics: " + diags + "\n")
	if fa == nil {
		return b.String(), false
	}
	p := fa.Program()
	for _, file := range p.Files() {
		if !analysis.IsStdlibKey(file.Origin) {
			b.WriteString(analysis.DumpFileForTest(file, nil, root))
		}
	}
	b.WriteString(analysis.DumpProgramIndexForTest(p, root))
	b.WriteString(analysis.DumpFileForTest(&analysis.FileAnalysis{}, analysis.FinalizeCoherence(fa), root))
	return b.String(), analysis.UsesStdBaseForTest(fa)
}

// A program built over the stdlib's base (analysis.StdBase) is analyzed
// exactly as one whose build folds every stdlib file itself: for every
// corpus file, irbuild fixture, tour block and spec program, every file's
// analysis, the program's impl index and every diagnostic dump alike both
// ways. No build writes the base.
func TestStdBase_ABuildOverTheBaseMatchesOneThatFoldsEveryFile(t *testing.T) {
	base := std.Shared().Base
	if base == nil {
		t.Fatal("the stdlib forms no base, so every build folds every stdlib file and this test compares nothing")
	}
	before := analysis.DumpStdBaseForTest(base)
	defer func() {
		if after := analysis.DumpStdBaseForTest(base); after != before {
			t.Errorf("building programs wrote the stdlib's base:\n%s", firstDifference(before, after))
		}
	}()
	seeds := stdBaseSeeds(t)
	if len(seeds) < 300 {
		t.Fatalf("found %d programs; the corpus, tour and spec should give several hundred", len(seeds))
	}
	overBase := 0
	for _, seed := range seeds {
		with, used := dumpSeed(t, seed)
		without := dumpWithoutBase(t, seed)
		if used {
			overBase++
		}
		// A few programs are analyzed differently from one check to the
		// next whichever way they are built (block_local_types.nomi resolves
		// a block-local struct's field reference to one of two symbols at
		// random), so a pair that differs is checked again before it counts.
		for retry := 0; with != without && retry < 20; retry++ {
			with, _ = dumpSeed(t, seed)
			without = dumpWithoutBase(t, seed)
		}
		if with != without {
			t.Errorf("%s: the build over the base (got) differs from the one that folds every file (want):\n%s", seed.name, firstDifference(without, with))
		}
	}
	// Almost every program builds over the base; a change that stopped them
	// would leave this test comparing one path with itself.
	if overBase < len(seeds)*9/10 {
		t.Errorf("%d of %d programs built over the base", overBase, len(seeds))
	}
}

// A program that declares a primitive under a name a stdlib file binds
// (here an alias of Int named like std/calendar's `Weeks`) could change the
// stdlib's identity answers, so its build folds every file after all, and is
// analyzed as a build without the base is. Under a name the stdlib does not
// bind, the build keeps the base.
func TestStdBase_AProgramDeclaringAPrimitiveUnderAStdlibNameFoldsEveryFile(t *testing.T) {
	program := func(name string) stdBaseSeed {
		seed := stdBaseSeed{name: name, src: "import std/io\n\ntypealias " + name + " Int\n\nfn main() {\n    value: " + name + " = 2\n    io.print(value)\n}\n"}
		if _, diags := checkSeed(t, seed); diags != "" {
			t.Fatalf("the program with %s does not check: %s", name, diags)
		}
		return seed
	}
	seed := program("Weeks")
	with, used := dumpSeed(t, seed)
	if used {
		t.Fatal("the build took the stdlib's share from the base, though the program declares a primitive named Weeks")
	}
	if without := dumpWithoutBase(t, seed); with != without {
		t.Errorf("the build that fell back differs from one without the base:\n%s", firstDifference(without, with))
	}
	if _, used := dumpSeed(t, program("Meters")); !used {
		t.Error("a program declaring a primitive named Meters, which the stdlib does not bind, did not build over the base")
	}
}

// dumpWithoutBase is dumpSeed for a build that folds every stdlib file
// itself.
func dumpWithoutBase(t *testing.T, seed stdBaseSeed) string {
	prev := analysis.SetStdBaseEnabled(false)
	defer analysis.SetStdBaseEnabled(prev)
	dump, _ := dumpSeed(t, seed)
	return dump
}

// A program's build does no work proportional to the stdlib: with the base,
// the program-wide passes visit no stdlib file and no impl a stdlib file
// declares, however many modules the stdlib has. The same build without the
// base visits thousands, which is what the counter is shown to see.
func TestStdBase_AProgramsBuildVisitsNoStdlibFile(t *testing.T) {
	root := appScopeProject(t, map[string]string{
		"main.nomi": `import {
    std/io
    shapes.{Shape, Circle}
}

fn main() {
    io.print(Shape.area(#Circle{radius: 2}))
    io.print(#Circle{radius: 2} == #Circle{radius: 3})
}
`,
		"shapes.nomi": `pub interface Shape {
    fn area(value: self) -> Int
}

pub struct Circle {
    radius: Int
}

derive Equatable for Circle

impl Shape for Circle {
    fn area(value: Circle) -> Int {
        value.radius * value.radius
    }
}
`,
	})
	seed := stdBaseSeed{name: "main", path: filepath.Join(root, "main.nomi")}
	data, err := os.ReadFile(seed.path)
	if err != nil {
		t.Fatal(err)
	}
	seed.src = string(data)
	std.Shared()
	check := func() {
		fa, diags := checkSeed(t, seed)
		if diags != "" {
			t.Fatalf("the program does not check: %s", diags)
		}
		if !analysis.UsesStdBaseForTest(fa) {
			t.Fatal("the program was not built over the base")
		}
	}
	if visits := analysis.CountStdlibWorkForTest(check); visits != 0 {
		t.Errorf("building the program over the base visited stdlib files or their impls %d times, want 0", visits)
	}
	prev := analysis.SetStdBaseEnabled(false)
	defer analysis.SetStdBaseEnabled(prev)
	without := analysis.CountStdlibWorkForTest(func() {
		if _, diags := checkSeed(t, seed); diags != "" {
			t.Fatalf("the program does not check without the base: %s", diags)
		}
	})
	if without < 1000 {
		t.Errorf("without the base the build visited the stdlib %d times; the counter should see the thousands of visits that build makes", without)
	}
}

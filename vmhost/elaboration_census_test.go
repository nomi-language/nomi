package vmhost

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	nast "github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/std"
)

// TestElaborationCensus runs the elaboration verifier
// (analysis.VerifyElaboration) over every program this repository holds
// and prints what it finds: counts by category and by construct, and a few
// examples of each category, per group (the corpus under tests/ with its
// embedded programs, the stdlib's modules with their tests, the tour's
// runnable blocks, the spec's complete programs, and the programs
// internal/irbuild's tests and testdata hold). It reports and never fails;
// TestElaborationGate fails on the categories that are closed.
//
// It runs only when asked:
//
//	NOMI_ELAB_CENSUS=1 go test ./vmhost -run TestElaborationCensus -count=1 -v
//
// NOMI_ELAB_DIR=<dir> runs it over one project directory instead, read in
// place and never written: every .nomi file there with a `fn main` or
// tests is an entry, and each file under the directory that an entry
// reaches is verified once. NOMI_ELAB_EXAMPLES sets how many examples each
// category prints (3), and NOMI_ELAB_OUT=<file> writes every violation,
// one per line.
func TestElaborationCensus(t *testing.T) {
	dir := os.Getenv("NOMI_ELAB_DIR")
	if os.Getenv("NOMI_ELAB_CENSUS") == "" && dir == "" {
		t.Skip("set NOMI_ELAB_CENSUS=1, or NOMI_ELAB_DIR=<project>, to run the elaboration census")
	}
	examples := 3
	if n, err := strconv.Atoi(os.Getenv("NOMI_ELAB_EXAMPLES")); err == nil {
		examples = n
	}
	var groups []*elabGroup
	if dir != "" {
		groups = append(groups, elabProjectGroup(t, dir))
	} else {
		groups = elabRepositoryGroups(t)
	}
	var b strings.Builder
	all := &elabGroup{name: "all"}
	for _, g := range groups {
		g.write(&b, examples)
		all.programs += g.programs
		all.rejected += g.rejected
		all.files += g.files
		all.found = append(all.found, g.found...)
		all.add(g.counts)
	}
	if len(groups) > 1 {
		all.write(&b, 0)
	}
	t.Log("\n" + b.String())
	if out := os.Getenv("NOMI_ELAB_OUT"); out != "" {
		var lines []string
		for _, g := range groups {
			for _, f := range g.found {
				lines = append(lines, g.name+"\t"+f.String())
			}
		}
		if err := os.WriteFile(out, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Errorf("write %s: %v", out, err)
		}
	}
}

// TestElaborationGate holds the checker to handing irbuild a closed record
// for every call and every value expression, in derived and expanded bodies
// as in written ones: it runs the verifier over the census's groups (the
// corpus, the stdlib's modules with their tests, the tour, the spec and
// irbuild's fixtures) and fails on any violation whose construct is "call"
// or "expr", with or without "derived", or whose implicit category is one
// the checker now records (gatedImplicit). A variable inference left open
// counts as closed once the checker has closed it (analysis.TypeVar.Closed).
// The other "implicit" categories are reported by TestElaborationCensus and
// gated nowhere yet.
func TestElaborationGate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	var bad []string
	for _, g := range elabRepositoryGroups(t) {
		for _, f := range g.found {
			switch strings.TrimPrefix(f.v.Construct, "derived ") {
			case "call", "expr":
				bad = append(bad, g.name+"\t"+f.String())
			case "implicit":
				if gatedImplicit[f.v.Category] {
					bad = append(bad, g.name+"\t"+f.String())
				}
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d calls or expressions have no closed record "+
			"(NOMI_ELAB_CENSUS=1 go test ./vmhost -run TestElaborationCensus -v lists them by category):\n%s",
			len(bad), strings.Join(bad, "\n"))
	}
}

// gatedImplicit are the implicit operations the checker records, which
// TestElaborationGate holds to zero: a call leaving defaulted parameters
// out (CallDefaults), an interpolation hole (HoleDisplays), an operator
// (Operators), a value entering an interface (Coercions) and a source
// entering an `Iter<T>` (IterViews).
var gatedImplicit = map[string]bool{
	analysis.ElabDefaultsUnrecorded: true,
	analysis.ElabDisplayUnrecorded:  true,
	analysis.ElabOperatorNominal:    true,
	analysis.ElabOperatorStructural: true,
	analysis.ElabOperatorTypeParam:  true,
	analysis.ElabBoxingUnrecorded:   true,
	analysis.ElabIterViewUnrecorded: true,
}

// elabRepositoryGroups is every group of programs this repository holds,
// each verified.
func elabRepositoryGroups(t *testing.T) []*elabGroup {
	return []*elabGroup{
		elabSeedGroup(t, "corpus", corpusSeeds(t)),
		elabStdGroup(t),
		elabSeedGroup(t, "tour", tourSeeds(t)),
		elabSeedGroup(t, "spec", specSeeds(t)),
		elabSeedGroup(t, "irbuild fixtures", irbuildFixtureSeeds(t)),
	}
}

// elabFound is one violation with where it was found.
type elabFound struct {
	file string
	v    analysis.ElabViolation
	// source is the violation's source line, trimmed, or "" for a
	// synthesized one.
	source string
}

func (f elabFound) String() string {
	return fmt.Sprintf("%s:%d:%d\t%s\t%s\t%s\t%s", f.file, f.v.Line, f.v.Col, f.v.Construct, f.v.Category, f.v.Text, f.source)
}

type elabGroup struct {
	name string
	// programs are the programs the front end checked, rejected those it
	// refused (not verified), and files the distinct files verified.
	programs, rejected, files int
	found                     []elabFound
	counts                    analysis.ElabCounts
}

func (g *elabGroup) add(c analysis.ElabCounts) {
	g.counts.Exprs += c.Exprs
	g.counts.Calls += c.Calls
	g.counts.GenericCalls += c.GenericCalls
	g.counts.DerivedExprs += c.DerivedExprs
	g.counts.DerivedCalls += c.DerivedCalls
	g.counts.DerivedGenericCalls += c.DerivedGenericCalls
}

func (g *elabGroup) write(b *strings.Builder, examples int) {
	fmt.Fprintf(b, "== %s: %d programs checked (%d rejected, not verified), %d files verified, %d violations\n",
		g.name, g.programs, g.rejected, g.files, len(g.found))
	c := g.counts
	fmt.Fprintf(b, "  checked: %d value expressions, %d calls (%d generic); in derived bodies %d, %d (%d)\n",
		c.Exprs, c.Calls, c.GenericCalls, c.DerivedExprs, c.DerivedCalls, c.DerivedGenericCalls)
	byConstruct := map[string]int{}
	byCategory := map[string][]elabFound{}
	for _, f := range g.found {
		byConstruct[f.v.Construct]++
		byCategory[f.v.Category] = append(byCategory[f.v.Category], f)
	}
	b.WriteString("  by construct:\n")
	for _, k := range sortedByCount(byConstruct) {
		fmt.Fprintf(b, "    %7d  %s\n", byConstruct[k], k)
	}
	counts := map[string]int{}
	for k, v := range byCategory {
		counts[k] = len(v)
	}
	b.WriteString("  by category:\n")
	for _, k := range sortedByCount(counts) {
		derived := 0
		for _, f := range byCategory[k] {
			if strings.HasPrefix(f.v.Construct, "derived ") {
				derived++
			}
		}
		fmt.Fprintf(b, "    %7d  %s", counts[k], k)
		if derived > 0 {
			fmt.Fprintf(b, " (%d in derived bodies)", derived)
		}
		b.WriteString("\n")
		shown := 0
		for _, sourced := range []bool{true, false} {
			for _, f := range byCategory[k] {
				if shown >= examples {
					break
				}
				if (f.source != "") != sourced {
					continue
				}
				fmt.Fprintf(b, "             %s:%d:%d %s  `%s`\n", f.file, f.v.Line, f.v.Col, f.v.Text, f.source)
				shown++
			}
		}
	}
	bodies := map[string]bool{}
	for _, f := range g.found {
		if strings.HasPrefix(f.v.Construct, "derived ") {
			label, _, _ := strings.Cut(f.v.Text, ": ")
			bodies[f.file+" "+label] = true
		}
	}
	fmt.Fprintf(b, "  derived declarations with a violation: %d\n", len(bodies))
}

func sortedByCount(m map[string]int) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

// verifyModule runs the verifier over one checked file and reads its
// source lines for the report.
func verifyModule(name, path string, fa *analysis.FileAnalysis, nodes []nast.Node) ([]elabFound, analysis.ElabCounts) {
	vs, counts := analysis.VerifyElaborationCounting(fa, nodes)
	if len(vs) == 0 {
		return nil, counts
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(string(data), "\n")
	out := make([]elabFound, 0, len(vs))
	for _, v := range vs {
		src := ""
		if !analysis.IsSynthesizedLine(v.Line) && v.Line >= 1 && v.Line <= len(lines) {
			src = strings.TrimSpace(lines[v.Line-1])
			if len(src) > 120 {
				src = src[:120] + "..."
			}
		}
		out = append(out, elabFound{file: name, v: v, source: src})
	}
	return out, counts
}

// elabSeedGroup checks each seed as checkLowers does, in a directory of
// its own, and verifies each of its files. A file several seeds hold with
// the same text (a project's sibling) is verified once.
func elabSeedGroup(t *testing.T, name string, seeds []loweringSeed) *elabGroup {
	g := &elabGroup{name: name}
	root := t.TempDir()
	fc := elabFrontendConfig(t)
	type result struct {
		accepted bool
		files    map[string][]elabFound
		counts   map[string]analysis.ElabCounts
	}
	results := make([]result, len(seeds))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				dir := filepath.Join(root, strconv.Itoa(i))
				results[i] = func() (r result) {
					defer func() {
						if recover() != nil {
							r = result{}
						}
					}()
					entry, err := writeInput(dir, seeds[i].src)
					if err != nil {
						return result{}
					}
					prog, err := irbuild.AnalyzeFile(entry, fc)
					if err != nil || prog == nil {
						return result{}
					}
					r = result{accepted: true, files: map[string][]elabFound{}, counts: map[string]analysis.ElabCounts{}}
					for _, m := range prog.Modules {
						if m.FA == nil || !strings.HasPrefix(m.Path, dir+string(filepath.Separator)) {
							continue
						}
						rel, _ := filepath.Rel(dir, m.Path)
						data, _ := os.ReadFile(m.Path)
						key := fmt.Sprintf("%s %x", filepath.ToSlash(rel), sha256.Sum256(data))
						r.files[key], r.counts[key] = verifyModule(seeds[i].name+"/"+filepath.ToSlash(rel), m.Path, m.FA, m.Nodes)
					}
					return r
				}()
			}
		}()
	}
	for i := range seeds {
		next <- i
	}
	close(next)
	wg.Wait()
	seen := map[string]bool{}
	for _, r := range results {
		if !r.accepted {
			g.rejected++
			continue
		}
		g.programs++
		var keys []string
		for k := range r.files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if seen[k] {
				continue
			}
			seen[k] = true
			g.files++
			g.found = append(g.found, r.files[k]...)
			g.add(r.counts[k])
		}
	}
	return g
}

func elabFrontendConfig(t *testing.T) frontend.Config {
	cfg := newConfig(nil)
	fc, err := cfg.frontendConfig()
	if err != nil {
		t.Fatal(err)
	}
	fc.SourceBoundProvided = true
	return fc
}

// elabStdGroup verifies every module of the stdlib (std.All), shared and
// program-lowered (irbuild's programStdModules) alike, with its bodies,
// tests and `//!` prompts checked.
func elabStdGroup(t *testing.T) *elabGroup {
	g := &elabGroup{name: "std"}
	lib := std.All()
	var mods []string
	for m := range lib.Files {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		if err := lib.Bodies(m); err != nil {
			t.Logf("std/%s: %v", m, err)
			g.rejected++
			continue
		}
		g.programs++
		g.files++
		// Bodies grafts the module's bodies into a new state: the
		// stdlib answered before it holds the module's interface alone.
		cur := std.All()
		path := filepath.Join("..", "std", filepath.FromSlash(m)+".nomi")
		found, counts := verifyModule("std/"+m+".nomi", path, cur.Files[m], cur.Nodes[m])
		g.found = append(g.found, found...)
		g.add(counts)
	}
	return g
}

// elabProjectGroup checks each entry of the project at dir in place: every
// .nomi file with a `fn main` or tests. Each file under dir that an entry
// reaches is verified once, with the first entry that reaches it.
func elabProjectGroup(t *testing.T, dir string) *elabGroup {
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := &elabGroup{name: filepath.Base(abs)}
	fc := elabFrontendConfig(t)
	var entries []string
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) && p != abs {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".nomi") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if topLevelMain.Match(data) || topLevelTest.Match(data) {
			entries = append(entries, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		prog, err := irbuild.AnalyzeFile(entry, fc)
		if err != nil || prog == nil {
			t.Logf("%s: the front end rejects it: %v", entry, err)
			g.rejected++
			continue
		}
		g.programs++
		for _, m := range prog.Modules {
			if m.FA == nil || seen[m.Path] || !strings.HasPrefix(m.Path, abs+string(filepath.Separator)) {
				continue
			}
			seen[m.Path] = true
			g.files++
			rel, _ := filepath.Rel(abs, m.Path)
			found, counts := verifyModule(filepath.ToSlash(rel), m.Path, m.FA, m.Nodes)
			g.found = append(g.found, found...)
			g.add(counts)
		}
	}
	return g
}

// irbuildFixtureSeeds are the programs internal/irbuild's tests hold: each
// .nomi file under its testdata (a directory's entries with the
// directory's files), and each string literal in its test files that
// holds a whole program (a top-level `fn main` or test), single-file or in
// `// FILE:` sections. A program a test builds from parts is missed.
func irbuildFixtureSeeds(t *testing.T) []loweringSeed {
	const root = "../internal/irbuild"
	var seeds []loweringSeed
	err := filepath.WalkDir(filepath.Join(root, "testdata"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".nomi") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !topLevelMain.Match(data) && !topLevelTest.Match(data) {
			return nil
		}
		src := string(data)
		if dir := filepath.Dir(p); filepath.Base(dir) != "testdata" {
			src = elabBundleDir(t, dir, p)
		}
		rel, _ := filepath.Rel(root, p)
		seeds = append(seeds, loweringSeed{name: "irbuild/" + filepath.ToSlash(rel), src: src})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || (!topLevelMain.MatchString(s) && !topLevelTest.MatchString(s)) {
				return true
			}
			if strings.HasPrefix(strings.TrimSpace(s), "// FILE:") {
				if _, _, ok := splitInput(s); !ok {
					return true
				}
			}
			seeds = append(seeds, loweringSeed{
				name: fmt.Sprintf("irbuild/%s:%d", filepath.Base(f), fset.Position(lit.Pos()).Line),
				src:  s,
			})
			return true
		})
	}
	return seeds
}

// elabBundleDir is a testdata directory as one input, entry first.
func elabBundleDir(t *testing.T, dir, entry string) string {
	var b strings.Builder
	write := func(p string) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, p)
		b.WriteString("// FILE: " + filepath.ToSlash(rel) + "\n" + string(data))
		if !strings.HasSuffix(string(data), "\n") {
			b.WriteString("\n")
		}
	}
	write(entry)
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == entry {
			return nil
		}
		if strings.HasSuffix(p, ".nomi") || filepath.Base(p) == "nomi.toml" {
			write(p)
		}
		return nil
	})
	return b.String()
}

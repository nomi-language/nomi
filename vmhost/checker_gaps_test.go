package vmhost

import (
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// The rule these tests hold the checker to: after checking, every value
// expression of a program the front end accepts has a fully resolved type
// in its FileAnalysis (analysis.UnresolvedExprs). The IR builder reads
// those types; where the checker skipped an expression, the builder derives
// a type of its own or declines with a reason that names the builder, not
// the checker. checkLowers (lowering_fuzz_test.go) reports a skipped
// expression as a failure of its own, so the fuzz target and
// TestFrontEndAcceptsSoItLowers name the checker's skip at its source.
//
// An expression breaks the rule when it has no type, a nil one, or an
// unsolved variable as its own type; one nested in a type argument, as in
// `[]`'s `List<?1>`, is allowed. TestSeedExprsAreTyped holds the corpus,
// the tour, the spec and the embedded programs to it, TestStdlibExprsAreTyped
// the stdlib, and TestKnownCheckerGaps the knownCheckerGaps list below.

// checkerGap is an expression shape the checker accepts and leaves without
// a resolved type: a known break in the rule. Each is pinned by a
// reproducer that TestKnownCheckerGaps checks: the front end accepts it and
// UnresolvedExprs reports it. When a fix makes the reproducer typed, or
// makes the front end reject it, the test fails; delete the entry in the
// same change.
type checkerGap struct {
	name string
	// fix says what the checker should do.
	fix string
	// match matches each report the gap causes, as describeUnresolved
	// writes it ("file:line:col: Kind in Parent has what: `source line`").
	match *regexp.Regexp
	// src is the reproducer, an input as checkLowers takes it. A gap only
	// the stdlib shows has none, and names the std file instead.
	src string
	// std is the file under std/ that shows a stdlib-only gap.
	std string
}

var knownCheckerGaps = []checkerGap{}

// knownCheckerGapFor is the known gap that matches report, or nil.
func knownCheckerGapFor(report string) *checkerGap {
	for i := range knownCheckerGaps {
		if knownCheckerGaps[i].match.MatchString(report) {
			return &knownCheckerGaps[i]
		}
	}
	return nil
}

// stdUnresolved is every value expression of the stdlib's modules
// (std.All: shared and program-lowered alike), with their bodies, attached
// tests and test declarations checked,
// that has no resolved type, as describeUnresolved writes it with the file
// under std/, and how many attached tests it walked.
func stdUnresolved() ([]string, int) {
	lib := std.All()
	var mods []string
	for m := range lib.Files {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	var out []string
	attached := 0
	for _, m := range mods {
		if err := lib.Bodies(m); err != nil {
			panic(err)
		}
		// Bodies grafts the module's bodies into a new state: the stdlib
		// read before it holds the module's interface alone. Its test
		// declarations are not among its nodes (TestDecls).
		cur := std.All()
		tests, err := cur.TestDecls(m)
		if err != nil {
			panic(err)
		}
		nodes := append(append([]ast.Node(nil), cur.Nodes[m]...), tests...)
		for _, n := range nodes {
			attached += len(ast.AttachedTestsOf(n))
		}
		us := analysis.UnresolvedExprs(cur.Files[m], nodes)
		out = append(out, describeUnresolved(m+".nomi", filepath.Join("..", "std", filepath.FromSlash(m)+".nomi"), us)...)
	}
	return out, attached
}

// TestKnownCheckerGaps holds each known gap's reproducer to its gap: the
// front end accepts it and the checker leaves an expression it matches
// without a type.
func TestKnownCheckerGaps(t *testing.T) {
	stdReports, _ := stdUnresolved()
	for _, gap := range knownCheckerGaps {
		t.Run(gap.name, func(t *testing.T) {
			var reports []string
			if gap.src == "" {
				for _, r := range stdReports {
					if strings.HasPrefix(r, gap.std+":") {
						reports = append(reports, r)
					}
				}
			} else {
				o := checkLowers(t.TempDir(), gap.src)
				switch {
				case !o.accepted:
					t.Fatalf("the front end rejects this reproducer now; if that is the fix (%s), remove %s:\n%s\n%s",
						gap.fix, gap.name, gap.src, o.rejection)
				case o.internal != "":
					t.Fatalf("the compiler panics:\n%s", o.internal)
				}
				reports = o.unresolved
			}
			matched := 0
			for _, r := range reports {
				if gap.match.MatchString(r) {
					matched++
				} else if gap.src != "" {
					t.Errorf("the reproducer leaves another expression untyped: %s", r)
				}
			}
			if matched == 0 {
				where := gap.src
				if where == "" {
					where = "std/" + gap.std
				}
				t.Fatalf("the checker types every expression of this reproducer now; remove %s from knownCheckerGaps:\n%s",
					gap.name, where)
			}
		})
	}
}

// TestStdlibExprsAreTyped holds the stdlib's modules, as the shared
// analysis checks them, to the rule. A report a known gap matches is
// excused. It fails when it walked no attached test, which says it read the
// modules' interfaces rather than their bodies.
func TestStdlibExprsAreTyped(t *testing.T) {
	reports, attached := stdUnresolved()
	if attached == 0 {
		t.Fatal("no std module's attached tests were walked: the harness read interfaces, not bodies")
	}
	t.Logf("%d attached tests walked", attached)
	for _, r := range reports {
		if knownCheckerGapFor(r) == nil {
			t.Errorf("std/%s", r)
		}
	}
}

// TestSeedExprsAreTyped holds every lowering seed (corpus files with their
// projects, embedded programs, runnable tour blocks, the spec's complete
// programs) the front end accepts to the rule. Only the front end runs, on
// every core; TestFrontEndAcceptsSoItLowers covers the generated programs.
// A report a known gap matches is excused.
func TestSeedExprsAreTyped(t *testing.T) {
	seeds := loweringSeeds(t)
	reports := make([][]string, len(seeds))
	dir := t.TempDir()
	var wg sync.WaitGroup
	next := make(chan int)
	cfg := newConfig(nil)
	fc, err := cfg.frontendConfig()
	if err != nil {
		t.Fatal(err)
	}
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				reports[i] = seedUnresolved(filepath.Join(dir, strconv.Itoa(i)), seeds[i].src, fc)
			}
		}()
	}
	for i := range seeds {
		next <- i
	}
	close(next)
	wg.Wait()
	excused := 0
	for i, s := range seeds {
		for _, r := range reports[i] {
			if knownCheckerGapFor(r) != nil {
				excused++
				continue
			}
			t.Errorf("%s: %s", s.name, r)
		}
	}
	t.Logf("%d seeds; %d expressions excused by a known checker gap", len(seeds), excused)
}

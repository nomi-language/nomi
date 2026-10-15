package analysis_test

import (
	"flag"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/rotation"
)

// fuzzProjects are the projects the fuzzer edits: orderProject and the
// multi-file corpus directories, in a fixed order, so an input's project
// index names the same project on every machine.
func fuzzProjects(tb testing.TB) []incrementalProject {
	tb.Helper()
	corpus, err := corpusProjects()
	if err != nil {
		tb.Fatal(err)
	}
	return append(append([]incrementalProject{orderIncremental()}, corpus...), layeredProject())
}

// projectFile answers the project and the file an input's indexes name.
func projectFile(projects []incrementalProject, project, file uint) (incrementalProject, string) {
	p := projects[project%uint(len(projects))]
	keys := make([]string, 0, len(p.texts))
	for key := range p.texts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return p, keys[file%uint(len(keys))]
}

// fuzzRecheck is the one manager every input's rechecks go through, as one
// language server sees edits to many projects: whatever an incremental
// check keeps between edits, it keeps across inputs too.
var fuzzRecheck = sync.OnceValue(func() recheckFunc { return newManager().RecheckWithOutcome })

// fuzzPaths counts the ways the fuzzer's rechecks went, by path, in this
// process. NOMI_RECHECK_LOG names a file each input appends its counts to,
// so a fuzzing run's workers can be added up afterwards.
var fuzzPaths sync.Map

// notePaths counts outcomes in fuzzPaths and appends them to the file
// NOMI_RECHECK_LOG names, if any.
func notePaths(outcomes []analysis.RecheckOutcome) {
	counts := map[analysis.RecheckPath]int{}
	for _, o := range outcomes {
		counts[o.Path]++
		n, _ := fuzzPaths.LoadOrStore(o.Path, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
	}
	path := os.Getenv("NOMI_RECHECK_LOG")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "bodies %d dependents %d full %d unchanged %d\n", counts[analysis.RecheckBodies], counts[analysis.RecheckDependents], counts[analysis.RecheckFull], counts[analysis.RecheckUnchanged])
}

// fuzzEdits is one input's edit sequence over the file key of p: its text
// becomes src, a stretch of src is deleted and a character typed there, a
// block of src's lines is pasted at a line start, and the original text
// comes back. seed places the edits. It answers the harness's report of the
// first difference, or "".
func fuzzEdits(tb testing.TB, p incrementalProject, key, src string, seed int64) string {
	tb.Helper()
	diff, outcomes := runEdits(tb, p, fuzzSteps(key, src, seed), fuzzRecheck(), false)
	notePaths(outcomes)
	return diff
}

// fuzzSteps are fuzzEdits' edits.
func fuzzSteps(key, src string, seed int64) []editStep {
	rng := rand.New(rand.NewSource(seed))
	at := func() int {
		off := rng.Intn(len(src) + 1)
		for off > 0 && off < len(src) && !utf8.RuneStart(src[off]) {
			off--
		}
		return off
	}
	steps := []editStep{setText("text replaced", key, src)}
	if len(src) > 0 {
		from, to := at(), at()
		if from > to {
			from, to = to, from
		}
		lines := strings.SplitAfter(src, "\n")
		first := rng.Intn(len(lines))
		block := strings.Join(lines[first:min(first+1+rng.Intn(8), len(lines))], "")
		p := at()
		for p > 0 && src[p-1] != '\n' {
			p--
		}
		steps = append(steps,
			setText("stretch deleted", key, src[:from]+src[to:]),
			setText("character typed", key, src[:from]+"."+src[to:]),
			setText("block pasted", key, src[:p]+block+src[p:]),
		)
	}
	return append(steps, restore("original back", key))
}

// fuzzHang is how long one input's edits may take.
const fuzzHang = 30 * time.Second

// fuzzSeedText bounds the files the fuzzer starts from: a large file makes
// every input slow.
const fuzzSeedText = 12 << 10

// FuzzIncrementalMatchesFull edits one file of a multi-file project
// (fuzzProjects) through the recheck seam, DocumentManager.Recheck, and
// after each edit compares the program it answers with a full check of the
// same texts, dump against dump (runEdits). The fuzzer mutates the file's
// new text and picks the project and the file; the edits that follow
// (fuzzEdits) delete, type and paste into it and then restore it. With
// Recheck a full check it always passes; it is there for an incremental
// one. Run it with
//
//	go test ./internal/analysis -run '^$' -fuzz '^FuzzIncrementalMatchesFull$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4
//
// An input runs about a dozen checks, so the default minute spent
// minimizing every new interesting input stalls the run; hence the short
// -fuzzminimizetime. An input that takes over fuzzHang panics with every
// goroutine's stack. A failing input is written under
// internal/analysis/testdata/fuzz/, which plain `go test` replays.
//
// Under plain `go test` it runs one seed, and
// TestIncrementalMatchesFullSample and TestIncrementalMatchesFullRotating
// run a fixed and a daily set of edits.
func FuzzIncrementalMatchesFull(f *testing.F) {
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
		// A check that never ends would stall the fuzzer with nothing to
		// show; a panic reports it with every goroutine's stack.
		hang := time.AfterFunc(fuzzHang, func() {
			buf := make([]byte, 1<<20)
			panic(fmt.Sprintf("editing %s in %s took over %v:\n%s", key, p.name, fuzzHang, buf[:runtime.Stack(buf, true)]))
		})
		defer hang.Stop()
		if diff := fuzzEdits(t, p, key, src, int64(hashText(src))); diff != "" {
			t.Fatal(diff)
		}
	})
}

func hashText(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// TestIncrementalMatchesFullSample runs fuzzEdits over a fixed sample: every
// file of orderProject and the entry of every other corpus project, each
// edited from its own text.
func TestIncrementalMatchesFullSample(t *testing.T) {
	projects := fuzzProjects(t)
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
			if diff := fuzzEdits(t, p, key, p.texts[key], int64(hashText(p.name+"/"+key))); diff != "" {
				t.Fatal(diff)
			}
		}
	}
	// The sample's edits must reach the incremental paths, or the
	// comparisons above say nothing about them.
	if n, ok := fuzzPaths.Load(analysis.RecheckBodies); !ok || n.(*atomic.Int64).Load() == 0 {
		t.Errorf("no edit of the sample rebuilt only bodies")
	}
	if n, ok := fuzzPaths.Load(analysis.RecheckDependents); !ok || n.(*atomic.Int64).Load() == 0 {
		t.Errorf("no edit of the sample checked only the edited file and its importers")
	}
}

// rotatingIncrementalCount is how many edit sequences
// TestIncrementalMatchesFullRotating runs.
const rotatingIncrementalCount = 6

// TestIncrementalMatchesFullRotating runs fuzzEdits beside the fixed
// sample, a different handful every UTC day (internal/rotation): seed s
// picks a project and a file, and places the edits, with a rand source
// seeded with s.
func TestIncrementalMatchesFullRotating(t *testing.T) {
	projects := fuzzProjects(t)
	set := rotation.For(t, "./internal/analysis", rotatingIncrementalCount)
	for _, seed := range set.Seeds() {
		rng := rand.New(rand.NewSource(seed))
		p, key := projectFile(projects, uint(rng.Intn(len(projects))), uint(rng.Intn(1<<16)))
		t.Logf("seed %d edits %s in %s", seed, key, p.name)
		if diff := fuzzEdits(t, p, key, p.texts[key], seed); diff != "" {
			t.Fatalf("%s%s", set.Failure(seed), diff)
		}
	}
}

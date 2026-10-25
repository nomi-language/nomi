package expectation_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/vm"
)

// TestExpectation_ADebugMachineRunsEveryProgramAsAPlainOneDoes runs the
// corpus, the tour's blocks and the deliberately-red fixtures on debug
// machines (internal/vm's debug.go) with no breakpoints and no exception
// filters, and requires each transcript to be byte-identical to its
// committed record. A debug machine compiles statement hooks, saves pcs,
// links activations across goroutines and consults its debugger at every
// fault; none of it may change what a program prints, returns or reports.
//
// The corpus and the tour run a fixed sample of every fourth program (about
// 12 seconds); NOMI_DEBUG_IDENTICAL=full runs all of them (about 31). The
// red fixtures always run in full: they are the population that reaches the
// fault hook. vmhost's TestSpecProgramsOnADebugMachine is the same check
// over the spec's programs. A program run through a generated FFI wrapper
// runs in its own process and is not on a debug machine.
func TestExpectation_ADebugMachineRunsEveryProgramAsAPlainOneDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs a sample of every population on debug machines; -short")
	}
	pinRecorderEnv(t)
	defer vm.DebugEveryMachine()()
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	every := 4
	if os.Getenv("NOMI_DEBUG_IDENTICAL") == "full" {
		every = 1
	}
	compare := func(population string, id string, o observed) {
		t.Helper()
		set := committedSet(t, population)
		rec, ok := set.Lookup(id)
		if !ok {
			t.Errorf("%s: %s has no committed record", population, id)
			return
		}
		got := expectation.Normalize(o.transcript(), wtRoot)
		if got != rec.Transcript || o.exit != rec.Exit {
			t.Errorf("%s: %s on a debug machine differs from its record (exit %d, want %d):\n%s",
				population, id, o.exit, rec.Exit, expectation.LineDiff(rec.Transcript, got))
		}
	}
	ran := 0
	root := filepath.Join(repoRoot(t), "tests")
	for i, rel := range nomiFilesUnder(t, root) {
		if i%every != 0 {
			continue
		}
		o, _ := runCommand(t, filepath.Join(root, rel))
		compare("corpus", rel, o)
		ran++
	}
	tourRoot, chapters := tourChapters(t)
	block := 0
	for _, chapter := range chapters {
		data, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(tourRoot, chapter)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			if !b.Runs() {
				continue
			}
			block++
			if block%every != 0 {
				continue
			}
			o, _ := runTourBlock(b)
			compare("tour", fmt.Sprintf("%s:L%d", filepath.ToSlash(rel), b.Line), o)
			ran++
		}
	}
	fixtures := filepath.Join(repoRoot(t), "internal", "irbuild", "testdata")
	for _, rel := range failureFixtures {
		o, _ := runCommand(t, filepath.Join(fixtures, rel))
		compare("failure", rel, o)
		ran++
	}
	if ran < 100 {
		t.Fatalf("ran %d programs on debug machines; the enumeration is wrong", ran)
	}
	// A debugger records every crossing out of the machine for step back
	// (internal/vm's debugtravel.go) unless the program starts a task; the
	// recording must not change a transcript either.
	if vm.RecordedCrossings() == 0 {
		t.Fatal("no debug machine recorded a crossing; the runs did not exercise time travel's recording")
	}
	t.Logf("%d programs ran on debug machines as their records say, recording %d crossings", ran, vm.RecordedCrossings())
}

// committedSet is a population's committed golden file, parsed once.
func committedSet(t *testing.T, population string) *expectation.Set {
	t.Helper()
	if s, ok := committedSets[population]; ok {
		return s
	}
	path, err := expectation.Path(population)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := expectation.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	committedSets[population] = s
	return s
}

var committedSets = map[string]*expectation.Set{}

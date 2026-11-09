package expectation_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/tourgo"
)

// TestObjectsLinkToTheSameProgram runs the corpus, the tour's blocks and the
// deliberately-red fixtures with every lowering linked through objects
// (internal/irbuild's objects.go): each module of the program, the std
// modules it links and its std instances are encoded apart with
// ir.EncodeObject, decoded with one ir.Linker and linked by linkage name, so
// no pointer of the lowering survives into the run. Each transcript must be
// byte-identical to its committed record. Before encoding, ir.CheckLinkage
// requires every symbol two modules name to carry a linkage name, no two
// symbols to share one unless both are the same code, and a failure there
// fails the lowering, so a missing or colliding name shows here as a
// program that did not run.
//
// The corpus and the tour run a sample of every eighth program (about 20
// seconds); NOMI_OBJECTS_LINK=full runs all of them, as `make check-full`'s
// objects-link step does. Its objects-irbuild step runs
// TestIRGolden_RecordsMatchTheVM under NOMI_LINK_OBJECTS=1, so every
// program irbuild.expect records is linked through objects and held to its
// record. A program run through a generated FFI wrapper lowers in the
// wrapper's process, which sees only NOMI_LINK_OBJECTS. TestLink_* in
// internal/ir hold the linker's rules, and TestLinkedStd_* in
// internal/irbuild hold that two programs reaching listed std modules link
// one kept lowering of them.
func TestObjectsLinkToTheSameProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs a sample of every population linked through objects; -short")
	}
	pinRecorderEnv(t)
	defer irbuild.LinkThroughObjects()()
	runLinkedPopulations(t, "linked through objects")
}

// TestFilesLowerOnTheirOwnAsInTheProgram runs the same programs with every
// lowering kept and each of its files lowered again on its own, against
// its imports' interfaces and the kept objects of the other files
// (internal/irbuild's perfile.go, RelowerEachFile; NOMI_LINK_OBJECTS=perfile
// does it for any test). Each file's module must be the whole lowering's
// part by part; the program it links must define every name the whole one
// does and name no function by a symbol no module defines while one
// defines its name; and the run of the program that the first file other
// than the entry links (the entry's, for a one-file program) must equal
// the record byte for byte. A file may miss (a decline the whole lowering
// also has, counted differently); NOMI_PERFILE_REPORT=1 writes each miss on
// stderr.
//
// The checks rely on irbuild's dropLinkedDuplicates running against kept
// objects (a std instance asked for again must not be defined twice under
// one symbol, which the VM reads as ambiguous) and on writeDeclKey
// spelling a program's types by declaration, never by node.
// NOMI_OBJECTS_LINK=full (about 110 seconds) runs every program, as `make
// check-full`'s objects-perfile step does.
func TestFilesLowerOnTheirOwnAsInTheProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs a sample of every population lowered one file at a time; -short")
	}
	pinRecorderEnv(t)
	defer irbuild.RelowerEachFile()()
	runLinkedPopulations(t, "lowered one file at a time")
}

// runLinkedPopulations runs a sample of the corpus and the tour, and every
// failure fixture, and requires each transcript to equal its record.
func runLinkedPopulations(t *testing.T, how string) {
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	every := 8
	if os.Getenv("NOMI_OBJECTS_LINK") == "full" {
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
			t.Errorf("%s: %s %s differs from its record (exit %d, want %d):\n%s",
				population, id, how, o.exit, rec.Exit, expectation.LineDiff(rec.Transcript, got))
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
			// A block that imports a module only the browser links has
			// no record (TestExpectation_Tour).
			if !b.Runs() || len(tourgo.Unlinked(b.Code)) > 0 {
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
		t.Fatalf("ran %d programs %s; the enumeration is wrong", ran, how)
	}
	t.Logf("%d programs %s ran as their records say", ran, how)
}

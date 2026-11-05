package expectation_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/progcache"
)

// TestCachedRunsMatchTheRecords runs the corpus and the deliberately-red
// fixtures twice each with the artifact cache in a directory of the test's
// own (internal/progcache, vmhost.Cache): the first run checks, lowers and
// keeps the program, and the second runs what was kept, with no check or
// lowering. Both transcripts must be byte-identical to the committed
// record, and the second run of every program the first kept must have read
// it, so a program the cache keeps runs and fails exactly as a fresh load
// does: its output, its failures and their positions, its test report.
//
// By default it runs every sixteenth corpus program and every fixture
// (about 17 seconds); NOMI_CACHED_RUNS=full runs all of them (about 200).
func TestCachedRunsMatchTheRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs a sample of the corpus twice through the artifact cache; -short")
	}
	pinRecorderEnv(t)
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	if artifactcache.ProgramRoot() == "" {
		t.Fatal("the artifact cache is off with a directory set; nothing would be kept")
	}
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	every := 16
	if os.Getenv("NOMI_CACHED_RUNS") == "full" {
		every = 1
	}
	kept, read := 0, 0
	check := func(population, id, path string) {
		t.Helper()
		set := committedSet(t, population)
		rec, ok := set.Lookup(id)
		if !ok {
			t.Errorf("%s: %s has no committed record", population, id)
			return
		}
		for pass, name := range []string{"first", "second"} {
			hits, stores := progcache.Counts()
			o, _ := runCommand(t, path)
			got := expectation.Normalize(o.transcript(), wtRoot)
			if got != rec.Transcript || o.exit != rec.Exit {
				t.Errorf("%s: %s's %s run through the artifact cache differs from its record (exit %d, want %d):\n%s",
					population, id, name, o.exit, rec.Exit, expectation.LineDiff(rec.Transcript, got))
				return
			}
			hits2, stores2 := progcache.Counts()
			switch {
			case pass == 0 && stores2 > stores:
				kept++
			case pass == 0:
				// Not kept: it does not check or lower cleanly, or has Go
				// bindings. The second run loads it afresh too.
			case hits2 > hits:
				read++
			}
		}
	}
	root := filepath.Join(repoRoot(t), "tests")
	for i, rel := range nomiFilesUnder(t, root) {
		if i%every == 0 {
			check("corpus", rel, filepath.Join(root, rel))
		}
	}
	fixtures := filepath.Join(repoRoot(t), "internal", "irbuild", "testdata")
	for _, rel := range failureFixtures {
		check("failure", rel, filepath.Join(fixtures, rel))
	}
	if kept < 50 {
		t.Fatalf("the cache kept %d programs; the enumeration or the cache is wrong", kept)
	}
	if read != kept {
		t.Errorf("the cache kept %d programs and %d second runs read theirs", kept, read)
	}
	t.Logf("%d programs kept and run again from the cache as their records say", kept)
}

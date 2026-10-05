package expectation

// THE FORMAT'S OWN TESTS, and the round trip is the load-bearing one.
//
// Every transcript is stored VERBATIM, so Render/Parse must be
// lossless for text that a naive line-oriented format mangles: a trailing
// newline, an interior blank line, leading indentation, and a line that begins
// with this format's own prefixes. If any of those round-trips wrong, the
// failure-text records are silently corrupted and every comparison against them
// is a comparison against the corruption.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// awkwardTranscript is every shape that breaks a careless writer, in one
// string. It is a real assertion report's shape — indented rows under a `FAIL`
// header — plus the traps.
const awkwardTranscript = "FAIL m :: a case\n" +
	"  line 3: expected true\n" +
	"  assert x == 1\n" +
	"\n" + // an interior blank line
	"  values:\n" +
	"    x\n" +
	"      = \"  leading and trailing spaces  \"\n" +
	"T not a prefix\n" + // a line that starts with this format's own prefix
	"C neither is this\n" +
	"# nor is this a comment\n" +
	"test result: FAILED. 0 passed, 1 failed\n" // a trailing newline

func TestRoundTripIsLosslessForFailureText(t *testing.T) {
	want := &Set{
		Population: "probe",
		What:       "a two-line\ndefinition, because What is multi-line in every real artifact",
		Cases: []Case{
			NewCase("plain.nomi", 0, 3, "ok m :: one\n"),
			NewCase("red.nomi", 1, 2, awkwardTranscript),
		},
	}
	got, err := Parse(want.Render())
	if err != nil {
		t.Fatalf("Render wrote something Parse rejects: %v", err)
	}
	if got.Population != want.Population {
		t.Errorf("population = %q, want %q", got.Population, want.Population)
	}
	if got.What != want.What {
		t.Errorf("what = %q, want %q", got.What, want.What)
	}
	if len(got.Cases) != len(want.Cases) {
		t.Fatalf("%d records, want %d", len(got.Cases), len(want.Cases))
	}
	for i := range want.Cases {
		if got.Cases[i] != want.Cases[i] {
			t.Errorf("record %d round-tripped wrong:\n got %#v\nwant %#v",
				i, got.Cases[i], want.Cases[i])
		}
	}
	if got.Cases[1].Transcript != awkwardTranscript {
		t.Errorf("the failure text is not byte-identical after a round trip:\n got %q\nwant %q",
			got.Cases[1].Transcript, awkwardTranscript)
	}
}

// TestRoundTripIsLosslessForFailureText above swallows a Parse error, which
// would make it vacuous. This is the other half: Parse must not error on what
// Render writes.
func TestRenderIsParseable(t *testing.T) {
	s := &Set{Population: "probe", What: "one line"}
	s.Add(NewCase("red.nomi", 1, 1, awkwardTranscript))
	if _, err := Parse(s.Render()); err != nil {
		t.Fatalf("Render wrote something Parse rejects: %v", err)
	}
}

// TestSourceRoundTrips pins the `S` lines an inline record carries, including
// the same trailing-newline and prefix traps the transcript has.
func TestSourceRoundTrips(t *testing.T) {
	c := NewCase("control_test.nomi@0123456789abcdef", 0, 1, "ok <dir>/control_test.nomi :: a\n")
	c.Source = "test \"a\" {\n  assert 1 == 1\n}\n\nS not a prefix\n"
	s := &Set{Population: "probe", What: "w", Cases: []Case{c}}
	got, err := Parse(s.Render())
	if err != nil {
		t.Fatalf("Render wrote something Parse rejects: %v", err)
	}
	if got.Cases[0] != c {
		t.Errorf("record round-tripped wrong:\n got %#v\nwant %#v", got.Cases[0], c)
	}
}

func TestParseRejectsRatherThanSkips(t *testing.T) {
	emptyDigest := Digest("")
	for _, tc := range []struct{ name, body string }{
		{"unknown prefix", "population p\nX something\n"},
		{"field before a record", "population p\nE 1\n"},
		{"non-numeric exit", "population p\nC a\nE nope\n"},
		{"non-numeric case count", "population p\nC a\nN nope\n"},
		// A record with no transcript is a truncated golden file, not an empty
		// answer; an empty answer is one bare `T`.
		{"record without a transcript", "population p\nC a\nE 0\nN 0\nH " + emptyDigest + "\n"},
		// A transcript edited by hand without its digest.
		{"digest does not match the text", "population p\nC a\nE 0\nN 0\nH " + emptyDigest + "\nT edited\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.body)); err == nil {
				t.Fatal("Parse accepted it; a lenient reader lets a corrupted " +
					"artifact read as a smaller population and pass")
			}
		})
	}
}

// TestCompareNamesEveryKindOfDifference is the planted-positive check for the
// comparison itself. An instrument that reports nothing is indistinguishable
// from an instrument that finds nothing.
func TestCompareNamesEveryKindOfDifference(t *testing.T) {
	recorded := &Set{Population: "p", What: "w"}
	recorded.Add(NewCase("same.nomi", 0, 1, "ok\n"))
	recorded.Add(NewCase("moved.nomi", 0, 2, "two\n"))
	recorded.Add(NewCase("exited.nomi", 0, 1, "fine\n"))
	recorded.Add(NewCase("shrank.nomi", 0, 5, "five\n"))
	recorded.Add(NewCase("red.nomi", 1, 1, "FAIL m :: a\n  line 1: nope\n"))
	recorded.Add(NewCase("gone.nomi", 0, 1, "here\n"))

	got := &Set{Population: "p", What: "w"}
	got.Add(NewCase("same.nomi", 0, 1, "ok\n"))
	got.Add(NewCase("moved.nomi", 0, 2, "THREE\n"))
	got.Add(NewCase("exited.nomi", 1, 1, "fine\n"))
	got.Add(NewCase("shrank.nomi", 0, 4, "five\n"))
	got.Add(NewCase("red.nomi", 1, 1, "FAIL m :: a\n  line 1: DIFFERENT\n"))
	got.Add(NewCase("new.nomi", 0, 1, "novel\n"))

	diffs := strings.Join(recorded.Compare(got), "\n")
	for _, want := range []string{
		"moved.nomi: output changed",
		"exited.nomi: exit status 1, recorded 0",
		"shrank.nomi: declares 4 case(s), recorded 5 — the suite changed size",
		"red.nomi: FAILURE TEXT CHANGED",
		"new.nomi: NEW case",
		"gone.nomi: RECORDED case is GONE",
	} {
		if !strings.Contains(diffs, want) {
			t.Errorf("Compare did not report %q:\n%s", want, diffs)
		}
	}
	if strings.Contains(diffs, "same.nomi") {
		t.Errorf("Compare reported an unchanged record:\n%s", diffs)
	}
	// The failure-text branch must show the TEXT, which is the whole reason a
	// red record stores it. A diff that printed two hashes would be useless
	// for exactly the population this instrument exists to cover.
	if !strings.Contains(diffs, "line 1: nope") || !strings.Contains(diffs, "line 1: DIFFERENT") {
		t.Errorf("the failure-text diff does not show both texts:\n%s", diffs)
	}
	// A passing record's diff shows the moved line too, now that the golden
	// file holds the text rather than a hash.
	if !strings.Contains(diffs, `"two"`) || !strings.Contains(diffs, `"THREE"`) {
		t.Errorf("the output-changed diff does not show the moved line:\n%s", diffs)
	}
}

func TestTranscriptSplitsBackIntoItsStreams(t *testing.T) {
	for _, tc := range []struct{ stdout, stderr string }{
		{"out\n", ""},
		{"out\n", "err\n"},
		{"", "only stderr\n"},
		{"no newline", "err"},
	} {
		stdout, stderr := SplitTranscript(Transcript(tc.stdout, tc.stderr))
		if stdout != tc.stdout || stderr != tc.stderr {
			t.Errorf("Transcript(%q, %q) split back into %q, %q", tc.stdout, tc.stderr, stdout, stderr)
		}
	}
}

func TestNormalizeDirTakesOutATempDirectory(t *testing.T) {
	dir := t.TempDir()
	got := NormalizeDir("ok "+dir+"/control_test.nomi :: a\nin "+dir+"\n", dir)
	want := "ok <dir>/control_test.nomi :: a\nin <dir>\n"
	if got != want {
		t.Errorf("NormalizeDir = %q, want %q", got, want)
	}
}

// TestCompareSubsetIsWhatAPartialEngineNeeds is the check for a partial run. A
// VM run that blocks on a tenth of the corpus produces a strict subset, and the
// full Compare would drown the one real disagreement in absences.
func TestCompareSubsetIsWhatAPartialEngineNeeds(t *testing.T) {
	recorded := &Set{Population: "p", What: "the recording's own definition"}
	recorded.Add(NewCase("ran-and-agrees.nomi", 0, 1, "ok\n"))
	recorded.Add(NewCase("ran-and-differs.nomi", 0, 1, "recorded\n"))
	recorded.Add(NewCase("refused-a.nomi", 0, 1, "unreached\n"))
	recorded.Add(NewCase("refused-b.nomi", 0, 1, "unreached\n"))

	// A run's Set describes its own run: a different `What`, a different
	// population label, and only the ids it reached.
	engine := &Set{Population: "p-via-the-vm", What: "what the VM ran"}
	engine.Add(NewCase("ran-and-agrees.nomi", 0, 1, "ok\n"))
	engine.Add(NewCase("ran-and-differs.nomi", 0, 1, "ENGINE\n"))

	diffs := recorded.CompareSubset(engine)
	if len(diffs) != 1 {
		t.Fatalf("CompareSubset reported %d difference(s), want exactly the one real "+
			"disagreement:\n%s", len(diffs), strings.Join(diffs, "\n"))
	}
	if !strings.Contains(diffs[0], "ran-and-differs.nomi") {
		t.Errorf("the one reported difference is not the disagreeing case: %q", diffs[0])
	}

	// The planted positive for the claim that this is NEEDED: the full Compare
	// on the same pair must drown it. If Compare reported the same one thing,
	// CompareSubset would be dead weight.
	full := recorded.Compare(engine)
	if len(full) <= len(diffs) {
		t.Fatalf("Compare reported %d and CompareSubset %d; CompareSubset exists "+
			"because the full check is unusable for a subset, and this pair does not "+
			"show that:\n%s", len(full), len(diffs), strings.Join(full, "\n"))
	}
	joined := strings.Join(full, "\n")
	for _, want := range []string{"refused-a.nomi: RECORDED case is GONE", "what:", "population:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Compare did not report %q, so it is not the strict check:\n%s", want, joined)
		}
	}
}

// TestCompareSubsetStillSeesANewCase pins the one absence-shaped thing a subset
// check must NOT forgive: a run producing a case the recording does not
// have is a disagreement in the other direction.
func TestCompareSubsetStillSeesANewCase(t *testing.T) {
	recorded := &Set{Population: "p", What: "w"}
	recorded.Add(NewCase("known.nomi", 0, 1, "ok\n"))
	engine := &Set{Population: "p", What: "w"}
	engine.Add(NewCase("invented.nomi", 0, 1, "ok\n"))
	diffs := strings.Join(recorded.CompareSubset(engine), "\n")
	if !strings.Contains(diffs, "invented.nomi: NEW case") {
		t.Errorf("CompareSubset forgave an invented case:\n%s", diffs)
	}
}

// TestCompareSeesAWholePopulationMoveWithoutPrintingItAll checks the bound. A
// producer bug moves every record, and an unbounded report would print
// megabytes into a test log.
func TestCompareSeesAWholePopulationMoveWithoutPrintingItAll(t *testing.T) {
	recorded := &Set{Population: "p", What: "w"}
	got := &Set{Population: "p", What: "w"}
	for i := range 500 {
		id := string(rune('a'+i%26)) + string(rune('a'+i/26))
		recorded.Add(NewCase(id, 0, 1, "before\n"))
		got.Add(NewCase(id, 0, 1, "after\n"))
	}
	diffs := recorded.Compare(got)
	if len(diffs) == 0 {
		t.Fatal("a 500-record move reported nothing")
	}
	if len(diffs) > 41 {
		t.Fatalf("%d diffs reported; the bound is not holding", len(diffs))
	}
	if !strings.Contains(diffs[len(diffs)-1], "and more") {
		t.Errorf("a truncated report does not say it was truncated: %q", diffs[len(diffs)-1])
	}
}

func TestNormalizeTakesOutTheRootAndTheJitter(t *testing.T) {
	root := "/somewhere/nomi"
	got := Normalize("at "+root+"/x.nomi retrying in 137ms\n", root)
	want := "at <root>/x.nomi retrying in <jitter>\n"
	if got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

// TestIsFailingIsNotJustANonZeroExit pins the rule the FailureMarkers comment
// states. A front-end error exits 1 and has no failure REPORT to compare.
func TestIsFailingIsNotJustANonZeroExit(t *testing.T) {
	if IsFailing("analysis: type 'X' has no member 'y'\n") {
		t.Error("an analyzer diagnostic counted as failure text")
	}
	if !IsFailing("FAIL m :: a case\n") {
		t.Error("a per-case FAIL marker did not count")
	}
	if !IsFailing("test result: FAILED. 0 passed, 1 failed\n") {
		t.Error("the FAILED summary did not count")
	}
}

// TestEveryPopulationHasACommittedArtifact is the check that the instrument
// exists at all. It is separate from the recorders in populations_test.go on purpose:
// those take minutes, and "is the artifact in the repo and readable" is a
// question that should be answerable in milliseconds.
func TestEveryPopulationHasACommittedArtifact(t *testing.T) {
	// THE FAILURE POPULATION IS LISTED LAST AND IS THE ONLY ONE REQUIRED TO
	// HOLD FAILURE TEXT. The others hold none — measured, not assumed; see
	// the recorders' header — and asserting `Failing() > 0` for them would be
	// asserting a fact that is false about this repo.
	for _, tc := range []struct {
		population  string
		minRecords  int
		mustAllFail bool
	}{
		{"corpus", 200, false},
		{"stdlib", 25, false},
		{"tour", 100, false},
		{"reference", 25, false},
		{"failure", 40, true},
	} {
		t.Run(tc.population, func(t *testing.T) {
			s, err := Load(tc.population)
			if err != nil {
				t.Fatalf("loading the committed artifact: %v", err)
			}
			if s.Population != tc.population {
				t.Errorf("artifact names population %q", s.Population)
			}
			if len(s.Cases) < tc.minRecords {
				t.Errorf("%d records, want at least %d — an artifact that shrank to "+
					"nothing would pass every comparison", len(s.Cases), tc.minRecords)
			}
			if s.What == "" {
				t.Error("the artifact does not say what one record covers")
			}
			for _, c := range s.Cases {
				if c.Digest == "" {
					t.Errorf("%s has no digest", c.ID)
				}
				if filepath.IsAbs(c.ID) || strings.Contains(c.ID, "..") {
					t.Errorf("%s is not a population-relative id; two worktrees "+
						"would disagree", c.ID)
				}
				if c.Failing && c.Transcript == "" {
					t.Errorf("%s is marked failing and stores no text", c.ID)
				}
			}
			if tc.mustAllFail && s.Failing() != len(s.Cases) {
				t.Errorf("%d of %d records carry failure text; this is the population "+
					"whose whole purpose is failure text", s.Failing(), len(s.Cases))
			}
		})
	}
}

// TestArtifactsAreByteIdenticalAfterARoundTrip is the guard against a silent
// rewrite. A committed artifact must be exactly what Render produces from what
// Parse read out of it — otherwise the next regeneration reformats the whole
// file and buries the one record that actually moved.
func TestArtifactsAreByteIdenticalAfterARoundTrip(t *testing.T) {
	for _, population := range []string{"corpus", "stdlib", "tour", "failure", "irbuild", "reference"} {
		t.Run(population, func(t *testing.T) {
			path, err := Path(population)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Parse(data)
			if err != nil {
				t.Fatalf("parsing the committed artifact: %v", err)
			}
			s.Sort()
			if got := s.Render(); string(got) != string(data) {
				t.Errorf("%s is not what Render produces from it; a regeneration would "+
					"rewrite the whole file. First difference at byte %d.",
					population, firstDifference(string(data), string(got)))
			}
		})
	}
}

func firstDifference(a, b string) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

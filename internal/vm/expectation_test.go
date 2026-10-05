package vm_test

// Complete programs are compared with committed transcript artifacts. Empty
// transcripts and test reports are excluded here; testreport_test.go owns reports.
// Retention counts alone do not establish whole-program behavior. Which
// records must compare is decided below (vmRequiredComparable), and the
// output-mutation controls classify their records from the committed artifact
// or the block source, never from a hand list. Tour programs
// also cover the required scalar/control-flow graph shapes. The separate no-match
// fixture verifies reachable trap output, which wildcard-ended cases cannot cover.

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// emptyTranscriptDigest is sha256 of the empty string, which is what a library
// file with no entry point records.
//
// SPELLED OUT RATHER THAN COMPUTED, so a reader can see that the exclusion
// below is keyed on a constant and not on "whatever the VM produced".
const emptyTranscriptDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// WHICH RECORDS MUST COMPARE, by population and unit.
//
// `CompareSubset` reports nothing about a subset that got SMALLER, and
// expectation.go makes asserting the denominator the caller's obligation: "A
// subset check with no denominator is satisfiable by running nothing." The
// denominator here is a SET, not a count, so a record that drops out is named:
//
//   - A population with no list below must compare EVERY candidate. The tour
//     and the failure fixtures are recorded by the same VM this harness runs,
//     so a candidate the harness cannot compare is a harness or VM
//     regression, and its refusal is printed.
//   - A population with a list compares exactly the records the committed
//     list names. The corpus holds files this bare harness cannot run (Go
//     bindings it does not have, test files whose producer declines a case,
//     helper files with no entry), so "every candidate" is not the bar.
//     A listed record that stops comparing is the regression the list
//     exists for; a record that starts comparing, or a new corpus file that
//     compares, is the producer widening. Both fail until the list is
//     regenerated, and the regenerated list's diff names the moved records.
//
// The record classification (report-shaped, empty, candidate) is read from
// the committed artifact on every run and pinned nowhere: the artifact is
// owned by internal/expectation, whose own Compare already fails when a
// record appears or vanishes.
var vmComparableLists = map[string]string{
	"programs/corpus": "vm-corpus-programs.txt",
	"reports/corpus":  "vm-corpus-reports.txt",
}

// vmComparableRegenerate is the command that rewrites the lists.
const vmComparableRegenerate = "NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/vm " +
	"-run 'TestVMExpectation_TheVMIsComparedAgainstTheCommittedArtifacts|" +
	"TestVMReport_TheVMIsComparedAgainstTheReportShapedRecords' -count=1"

// vmRequiredComparable is the set of records `unit` must compare in
// `population`, given its candidates: the committed list when there is one,
// every candidate otherwise.
func vmRequiredComparable(t *testing.T, unit, population string, candidates []string) []string {
	t.Helper()
	name, listed := vmComparableLists[unit+"/"+population]
	if !listed {
		return candidates
	}
	path := vmComparableListPath(t, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v; create it with\n  %s", path, err, vmComparableRegenerate)
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	return ids
}

func vmComparableListPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := expectation.Dir()
	if err != nil {
		t.Fatalf("resolving the expectations directory: %v", err)
	}
	return filepath.Join(dir, name)
}

// vmRequireComparable checks that `got` compared exactly the records
// vmRequiredComparable names. With `regenerate` set and
// NOMI_REGENERATE_EXPECTATIONS=1, a listed population's list is rewritten
// from `got` instead.
func vmRequireComparable(t *testing.T, unit, population string, candidates []string,
	got *expectation.Set, refused []string, regenerate bool) {
	t.Helper()
	compared := recordIDs(got)
	if name, listed := vmComparableLists[unit+"/"+population]; listed && regenerate &&
		expectation.RegenerateRequested() {
		var b strings.Builder
		fmt.Fprintf(&b, "# The %s records internal/vm's %s harness compares against %s.expect.\n",
			population, unit, population)
		b.WriteString("# A record that leaves this list stopped comparing; name the cause.\n")
		fmt.Fprintf(&b, "# Regenerate with:\n#   %s\n", vmComparableRegenerate)
		for _, id := range compared {
			b.WriteString(id + "\n")
		}
		path := vmComparableListPath(t, name)
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		t.Logf("%s: REWROTE %s with %d record(s) because NOMI_REGENERATE_EXPECTATIONS=1",
			population, name, len(compared))
		return
	}
	want := vmRequiredComparable(t, unit, population, candidates)
	have := map[string]bool{}
	for _, id := range compared {
		have[id] = true
	}
	why := map[string]string{}
	for _, r := range refused {
		if id, reason, ok := strings.Cut(r, ": "); ok {
			why[id] = reason
		}
	}
	wanted := map[string]bool{}
	var dropped, added []string
	for _, id := range want {
		wanted[id] = true
		if !have[id] {
			reason := why[id]
			if reason == "" {
				reason = "not a candidate in the committed artifact"
			}
			dropped = append(dropped, fmt.Sprintf("    %s: %s", id, reason))
		}
	}
	for _, id := range compared {
		if !wanted[id] {
			added = append(added, "    "+id)
		}
	}
	if len(dropped) > 0 {
		t.Errorf("%s/%s: %d record(s) that must compare did not. CompareSubset forgives "+
			"absence, so this is the only place a regression here shows:\n%s",
			unit, population, len(dropped), strings.Join(dropped, "\n"))
	}
	if len(added) > 0 {
		t.Errorf("%s/%s: %d record(s) compared that the committed list does not name. "+
			"If the producer widened or a corpus file was added, regenerate with\n  %s\n%s",
			unit, population, len(added), vmComparableRegenerate, strings.Join(added, "\n"))
	}
}

// sortedCopy is ids sorted, leaving ids alone.
func sortedCopy(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// tourBlock is the record id of the one runnable block in `chapter` whose
// code contains `marker`.
//
// A TEST NAMES A TOUR BLOCK BY ITS CONTENT AND NOT BY ITS LINE. A record's id
// is `chapter:L<line>`, which moves whenever anything is inserted above the
// block; the marker moves only when the block itself is edited, and then the
// failure lists the chapter's blocks that do match.
func tourBlock(t *testing.T, chapter, marker string) string {
	t.Helper()
	var hits []string
	for id, code := range tourBlocksByID(t) {
		if strings.HasPrefix(id, chapter+":L") && strings.Contains(code, marker) {
			hits = append(hits, id)
		}
	}
	sort.Strings(hits)
	if len(hits) != 1 {
		t.Fatalf("%d runnable block(s) in %s contain %q, and a test needs exactly one: %v",
			len(hits), chapter, marker, hits)
	}
	return hits[0]
}

// vmLinkedRecords are the tour records that became comparable only because a
// machine can now resolve a callee in a sibling module.
//
// NAMED RATHER THAN DERIVED, because the control that uses them has to run the
// UNLINKED case and require it to fail: a list computed by "which records need
// a link" would be computed by the very mechanism under test.
var vmLinkedRecords = []struct{ chapter, marker string }{
	{"modules-and-imports.md", `name = "myproject"`},        // `io.print(math.double(7))`
	{"modules-and-imports.md", `name = "vis_demo"`},         // the same program with a private sibling beside it
	{"modules-and-imports.md", `name = "nested_file_demo"`}, // a sibling named `header`
}

// usesDbg reports whether a block's source spells the `dbg` keyword, read
// from its tokens so a comment or a string that mentions dbg does not count.
//
// THE DBG PLANT'S CLASSIFICATION IS A PROPERTY OF THE INPUT. A list derived
// from "which transcripts contain dbg output" would be derived from the
// output under test; the source is not, and it classifies every comparable
// record, so a new block needs no list entry.
func usesDbg(code string) bool {
	for _, tok := range lexer.Lex(code) {
		if tok.Type == token.DBG {
			return true
		}
	}
	return false
}

// vmWholeProgramRoots are the populations whose record ids are paths, with the
// directory they are relative to. stdlib and failure are absent because they
// have no candidates at all; tour is absent because its ids are not paths.
var vmWholeProgramRoots = map[string]string{
	"corpus": filepath.Join("..", "..", "tests"),
}

// candidateIDs is the records the VM could in principle produce a whole
// transcript for, with the two exclusions counted.
//
// THE FILTERS ARE PROPERTIES OF THE RECORD AND NOT OF ANY RUN, which is what
// keeps this from being a comparison that selects the cases it passes. `N` and
// the digest are read out of the committed artifact before the VM is opened.
func candidateIDs(s *expectation.Set) (ids []string, reportShaped, empty int) {
	for _, c := range s.Cases {
		switch {
		case c.Cases > 0:
			reportShaped++
		case c.Digest == emptyTranscriptDigest:
			empty++
		default:
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids, reportShaped, empty
}

// vmRun is one whole-program attempt: lower path, find the entry, run it.
//
// It answers a normalized transcript, or the reason this record is not
// comparable, which is reported per record so a refusal is actionable.
//
// A `Run` ERROR IS NEVER A COMPARISON FAILURE, and that is deliberate. The VM
// reports a callee this module did not retain, and an instruction it has no
// arm for, as errors; both mean it ran PART of a program, and a partial
// transcript compared against a whole one would report a producer's retention
// boundary as a wrong answer.
func vmRun(path string) (transcript string, reason string) {
	prog, err := irbuild.Analyze(path)
	if err != nil {
		return "", "the front end refuses this program, so the record is a diagnostic"
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		return "", "Generate refuses this program"
	}
	var (
		entry *ir.Func
		mod   *ir.Module
		found int
	)
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" && len(f.Params()) == 0 {
				entry, mod, found = f, m, found+1
			}
		}
	}
	if found != 1 || entry == nil {
		retained := 0
		for _, m := range res.IR {
			retained += len(m.Funcs())
		}
		return "", fmt.Sprintf("the entry is not retained (%d function(s) retained for this program)",
			retained)
	}
	// THE WHOLE PROGRAM'S MODULE SET, not just the entry's module. A Nomi
	// program with sibling files lowers to one `ir.Module` per unit, and a
	// callee may sit in the module next door (vmLinkedRecords' three
	// modules-and-imports.md blocks). Running the entry's module alone would report them
	// as a retention boundary when nothing is missing but the link.
	var out bytes.Buffer
	mod, mods, err := vmLink(mod, res.IRModules())
	if err != nil {
		return "", "the link hook refused it: " + err.Error()
	}
	machine := vm.NewProgram(mod, mods, &out)
	booted, err := machine.Boot()
	if err != nil {
		return "", "the VM could not boot it: " + err.Error()
	}
	if _, err := booted.Run("main"); err != nil {
		return "", "the VM ran part of it: " + err.Error()
	}
	return out.String(), ""
}

// vmLink is the module set a harness run hands the machine, given the entry
// module and the lowering's linked set. It is the identity; irimage_test.go
// replaces it with an encode and decode round trip, so the same harness shows
// a decoded image runs to the committed records.
var vmLink = func(entry *ir.Module, mods []*ir.Module) (*ir.Module, []*ir.Module, error) {
	return entry, mods, nil
}

// vmSubsetOf runs every candidate and returns the engine's Set plus a
// per-record refusal report.
//
// THE `expectation.Set` IS THE ENGINE'S OWN, which `CompareSubset`'s contract
// requires: "an engine's Set describes its own run, not the recording's", and
// it is why Population and What are deliberately not compared.
func vmSubsetOf(t *testing.T, population string, ids []string,
	pathFor func(string) (string, bool)) (*expectation.Set, []string) {
	t.Helper()
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	got := &expectation.Set{Population: population,
		What: "one record per program whose entry is a retained zero-parameter ir.Func, " +
			"run on internal/vm and compared as a SUBSET of the committed population."}
	var refused []string
	for _, id := range ids {
		path, ok := pathFor(id)
		if !ok {
			refused = append(refused, fmt.Sprintf("%s: could not be staged", id))
			continue
		}
		transcript, reason := vmRun(path)
		if reason != "" {
			refused = append(refused, fmt.Sprintf("%s: %s", id, reason))
			continue
		}
		got.Add(expectation.NewCase(id, 0, 0, expectation.Normalize(transcript, root)))
	}
	got.Sort()
	return got, refused
}

// TestVMExpectation_TheVMIsComparedAgainstTheCommittedArtifacts is the
// wiring's acceptance: two populations enumerated, every candidate run, the
// result compared with `CompareSubset`, and every count pinned.
func TestVMExpectation_TheVMIsComparedAgainstTheCommittedArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers every candidate program in two populations; -short")
	}
	// A FRESH CACHE ROOT PER RUN. internal/ffirun/cache.go's guard is load
	// bearing and two runs sharing a root is how a stale artifact gets read.
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	totalComparable, records, tourCandidates := 0, 0, 0
	for _, population := range []string{"corpus", "tour", "stdlib", "failure"} {
		set, err := expectation.Load(population)
		if err != nil {
			t.Fatalf("loading the committed expectation for %s: %v", population, err)
		}
		records += len(set.Cases)
	}
	for _, population := range []string{"corpus", "tour"} {
		t.Run(population, func(t *testing.T) {
			recorded, err := expectation.Load(population)
			if err != nil {
				t.Fatalf("loading the committed expectation for %s: %v", population, err)
			}
			ids, reportShaped, empty := candidateIDs(recorded)
			if population == "tour" {
				tourCandidates = len(ids)
			}

			pathFor := vmPathResolver(t, population)
			got, refused := vmSubsetOf(t, population, ids, pathFor)

			// THE COMPARISON. Every difference is an error; an unrun
			// candidate is not a difference, which is the whole reason this
			// is CompareSubset and not Compare.
			if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
				t.Errorf("%s: the VM disagrees with the committed expectation in %d place(s):\n%s",
					population, len(diffs), strings.Join(diffs, "\n"))
			}
			// THE DENOMINATOR, which CompareSubset cannot supply.
			vmRequireComparable(t, "programs", population, ids, got, refused, true)
			totalComparable += len(got.Cases)
			for _, id := range recordIDs(got) {
				t.Logf("  COMPARED %s", id)
			}
			for _, r := range refused {
				t.Logf("  not comparable %s", r)
			}
			t.Logf("%s: %d records, %d report-shaped, %d empty, %d candidates, %d COMPARED",
				population, len(recorded.Cases), reportShaped, empty, len(ids), len(got.Cases))
		})
	}
	if totalComparable == 0 {
		t.Fatal("the VM was compared against ZERO records, so every CompareSubset call above " +
			"was vacuous. See TestVMExpectation_AnEmptySubsetPassesSoTheDenominatorIsTheGate " +
			"for why that would pass silently.")
	}
	// BOTH DENOMINATORS: the share of tour candidates flatters, and the share
	// of all committed records is what the engine is actually compared on.
	// The gap between the two is the exclusions this file's header itemizes.
	t.Logf("the VM is compared against %d record(s) of %d committed (%.1f%%); the "+
		"tour holds %d candidates", totalComparable, records,
		100*float64(totalComparable)/float64(records), tourCandidates)
}

// recordIDs is a Set's ids, for logging.
func recordIDs(s *expectation.Set) []string {
	out := make([]string, 0, len(s.Cases))
	for _, c := range s.Cases {
		out = append(out, c.ID)
	}
	return out
}

// TestVMExpectation_TheReportShapedPopulationsAreOutByConstruction pins the
// two populations with no candidates at all, and it costs nothing: it reads
// the artifacts and runs no program.
//
// THE CLAIM IS STRUCTURAL AND WORTH CHECKING RATHER THAN ASSERTING IN PROSE.
// Every record of `stdlib` and `failure` declares cases, so every transcript
// is `rt.TestReporter`'s report. If a record with N=0 ever appeared in either,
// the VM would have a candidate there and this file would be silently
// ignoring it.
func TestVMExpectation_TheReportShapedPopulationsAreOutByConstruction(t *testing.T) {
	for _, population := range []string{"stdlib", "failure"} {
		recorded, err := expectation.Load(population)
		if err != nil {
			t.Fatalf("loading %s: %v", population, err)
		}
		if len(recorded.Cases) == 0 {
			t.Errorf("%s holds no records, so this check has no subject", population)
		}
		ids, reportShaped, empty := candidateIDs(recorded)
		if len(ids) != 0 {
			t.Errorf("%s now has %d candidate(s) — %v — and this file excludes the whole "+
				"population. The exclusion is no longer true.", population, len(ids), ids)
		}
		if reportShaped != len(recorded.Cases) || empty != 0 {
			t.Errorf("%s: %d of %d records are report-shaped and %d are empty; the exclusion "+
				"rests on every record being a test report", population, reportShaped,
				len(recorded.Cases), empty)
		}
	}
}

// TestVMExpectation_APlantedDivergenceInVMOutputIsCaught is the positive
// control, and without it the comparison above is unproven.
//
// A ZERO IS VALIDATED BY PLANTING A POSITIVE. The specific way the wiring
// could be vacuous is that `CompareSubset` forgives absence, so a subset check
// over a set the VM failed to produce reports nothing. This runs EVERY
// comparable record through the same `vmRun` -> `Normalize` -> `NewCase` ->
// `CompareSubset` path with the machine's output MUTATED, and requires the
// difference to be reported for every one of them by id.
//
// THE MUTATION IS IN THE VM'S OUTPUT AND NOT IN THE EXPECTATION, which is the
// direction that matters: mutating the recorded side would be measuring the
// artifact, and regenerating the artifact to turn a red test green is this
// instrument's own failure mode.
func TestVMExpectation_APlantedDivergenceInVMOutputIsCaught(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; stages and lowers one tour block; -short")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatalf("loading the tour expectation: %v", err)
	}
	ids, _, _ := candidateIDs(recorded)
	pathFor := vmPathResolver(t, "tour")

	// The clean run first, so the control has its own control: a mutant that
	// is caught while the original is ALSO caught would prove nothing.
	var comparable []string
	clean := &expectation.Set{Population: "tour", What: "the control"}
	for _, id := range ids {
		path, ok := pathFor(id)
		if !ok {
			continue
		}
		transcript, reason := vmRun(path)
		if reason != "" {
			continue
		}
		comparable = append(comparable, id)
		clean.Add(expectation.NewCase(id, 0, 0, expectation.Normalize(transcript, root)))
	}
	if len(comparable) == 0 {
		t.Fatal("no tour record is comparable, so this control has no subject and the " +
			"comparison it validates has nothing to validate")
	}
	if diffs := recorded.CompareSubset(clean); len(diffs) != 0 {
		t.Fatalf("the unmutated run already disagrees, so the mutant below measures nothing:\n%s",
			strings.Join(diffs, "\n"))
	}

	// The plant: one character of the VM's stdout, for every comparable
	// record. Every one must be reported.
	mutant := &expectation.Set{Population: "tour", What: "the plant"}
	for _, c := range clean.Cases {
		text := c.Transcript
		bent := strings.Replace(text, "Hello", "Goodbye", 1)
		if bent == text {
			bent = text + "one line the VM did not print\n"
		}
		mutant.Add(expectation.NewCase(c.ID, 0, 0, bent))
	}
	// Compare each plant separately: CompareSubset caps a combined report at
	// 40 entries, while this control must check every comparable program.
	var diffs []string
	for _, c := range mutant.Cases {
		one := &expectation.Set{Population: "tour", What: "one plant"}
		one.Add(c)
		diffs = append(diffs, recorded.CompareSubset(one)...)
	}
	if len(diffs) != len(mutant.Cases) {
		t.Fatalf("mutated %d record(s) of VM output and CompareSubset reported %d "+
			"difference(s); the wiring does not see a wrong answer:\n%s",
			len(mutant.Cases), len(diffs), strings.Join(diffs, "\n"))
	}
	for _, id := range comparable {
		if !strings.Contains(strings.Join(diffs, "\n"), id) {
			t.Fatalf("the mutation of %s was not reported:\n%s", id, strings.Join(diffs, "\n"))
		}
	}
	t.Logf("planted a divergence in %d comparable record(s) of VM output and the wiring "+
		"reported every one:\n%s", len(mutant.Cases), strings.Join(diffs, "\n"))
}

// TestVMExpectation_APlantedDivergenceInDbgOutputIsCaught is the control for
// the `dbg` code path, which the one above does not reach.
//
// THE DIFFERENCE IS WHERE THE MUTATION LIVES. The test above bends the
// TRANSCRIPT after the run, which shows `CompareSubset` and the digesting
// reach the run's output. It says nothing about the `dbg` code path: the same
// mutation fires for a record whose transcript is three `io.print`s.
//
// THIS ONE MUTATES INSIDE `rt.DbgText` ITSELF, through `rt.Highlight` — the
// hook `internal/termcolor`'s `init` installs in the `nomi` binary, and the one `DbgText` passes both the operand's source
// text and the rendered value through. So the plant travels the real route:
// `dbgHost` -> `rt.DbgText` -> `highlight` -> the bytes `Run` produced ->
// `Normalize` -> `NewCase` -> `CompareSubset`.
//
// AND IT DISCRIMINATES, which is the property that makes it worth a second
// test. Exactly the `dbg` records must be reported and the ones that print
// through `io.print` must not — so a mutation that happened to change
// everything, or a comparison that reported everything, fails here.
//
// THE HOOK IS A REAL SEAM AND NOT A TEST BACK DOOR, which is worth saying
// because it decides whether this control is measuring the shipped path.
// `rt.Highlight` is nil in an embedding that does not link `internal/termcolor`
// and non-nil under `nomi run`,
// so both states are real; this installs a third function into the same slot.
func TestVMExpectation_APlantedDivergenceInDbgOutputIsCaught(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; stages and lowers the tour's dbg blocks; -short")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatalf("loading the tour expectation: %v", err)
	}
	pathFor := vmPathResolver(t, "tour")

	// EVERY TOUR CANDIDATE, classified by its source: a block that spells
	// `dbg` must be reported and one that does not must not. Every tour
	// candidate is required to compare (vmRequiredComparable), so the run
	// below covers the whole population and a new block needs no entry.
	blocks := tourBlocksByID(t)
	ids, _, _ := candidateIDs(recorded)
	var dbgIDs, nonDbgIDs []string
	for _, id := range ids {
		if usesDbg(blocks[id]) {
			dbgIDs = append(dbgIDs, id)
		} else {
			nonDbgIDs = append(nonDbgIDs, id)
		}
	}
	if len(dbgIDs) == 0 || len(nonDbgIDs) == 0 {
		t.Fatalf("%d tour candidate(s) use dbg and %d do not; the plant needs both to "+
			"discriminate", len(dbgIDs), len(nonDbgIDs))
	}

	run := func(what string, ids ...string) *expectation.Set {
		s := &expectation.Set{Population: "tour", What: what}
		for _, id := range ids {
			path, ok := pathFor(id)
			if !ok {
				t.Fatalf("%s could not be staged", id)
			}
			transcript, reason := vmRun(path)
			if reason != "" {
				t.Fatalf("%s is a tour candidate, every one must compare, and the VM "+
					"answered %q", id, reason)
			}
			s.Add(expectation.NewCase(id, 0, 0, expectation.Normalize(transcript, root)))
		}
		return s
	}

	// The clean run first, so the plant has its own control.
	if diffs := recorded.CompareSubset(run("the control", ids...)); len(diffs) != 0 {
		t.Fatalf("the unmutated run already disagrees, so the plant below measures "+
			"nothing:\n%s", strings.Join(diffs, "\n"))
	}

	saved := rt.Highlight
	defer func() { rt.Highlight = saved }()
	rt.Highlight = func(_ io.Writer, s string) string { return s + "<PLANTED>" }
	// PER RECORD, because a report is bounded at the first forty differences
	// and the dbg records outnumber that; one record per comparison keeps
	// every record's answer visible.
	var diffs []string
	for _, id := range ids {
		diffs = append(diffs, recorded.CompareSubset(run("the plant", id))...)
	}
	rt.Highlight = saved

	reported := strings.Join(diffs, "\n")
	for _, id := range dbgIDs {
		if !strings.Contains(reported, id+":") {
			t.Errorf("a divergence planted inside rt.DbgText was NOT reported for %s, "+
				"whose source uses dbg, so its dbg transcript is not actually being "+
				"compared", id)
		}
	}
	for _, id := range nonDbgIDs {
		if strings.Contains(reported, id+":") {
			t.Errorf("the plant was reported for %s, whose source spells no `dbg`, so "+
				"the mutation is not specific to the path it names", id)
		}
	}
	if len(diffs) != len(dbgIDs) {
		t.Errorf("planted inside rt.DbgText and got %d difference(s) for %d dbg "+
			"record(s)", len(diffs), len(dbgIDs))
	}
	if t.Failed() {
		t.Fatalf("reported:\n%s", reported)
	}
	t.Logf("planted a divergence inside rt.DbgText and the artifact comparison "+
		"reported exactly the %d dbg record(s) and none of the %d others",
		len(dbgIDs), len(nonDbgIDs))
}

// TestVMExpectation_SiblingCallRecordsRunOnlyWhenLinked is the positive
// control for linking a program's modules: the records in vmLinkedRecords
// compare only because a machine resolves a callee in a sibling module.
//
// THE CLAIM COULD BE VACUOUS IN TWO WAYS AND THIS CLOSES BOTH. The three
// records might have started passing for some unrelated reason — in which case
// running them UNLINKED would also pass — or `NewProgram` might be resolving
// by something other than the declaration's identity, in which case linking a
// module set that does not contain the callee would also pass. So each of the
// three is run three ways over ONE lowering: unlinked, linked to the program,
// and linked to every module EXCEPT the one holding the callee.
//
// A REVERSED RESULT IS THE INTERESTING FAILURE. If the unlinked run passes,
// the link is not what made the record comparable and this file's reading is
// wrong. If the excluding run passes, the resolution is finding the callee
// somewhere it was not given and the identity claim is wrong.
func TestVMExpectation_SiblingCallRecordsRunOnlyWhenLinked(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers three tour blocks three ways; -short")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	pathFor := vmPathResolver(t, "tour")
	for _, linked := range vmLinkedRecords {
		id := tourBlock(t, linked.chapter, linked.marker)
		t.Run(id, func(t *testing.T) {
			path, ok := pathFor(id)
			if !ok {
				t.Fatalf("%s could not be staged", id)
			}
			prog, err := irbuild.Analyze(path)
			if err != nil {
				t.Fatalf("the front end refuses %s: %v", id, err)
			}
			res, _, err := irbuild.GenerateIR(prog)
			if err != nil {
				t.Fatalf("Generate refuses %s: %v", id, err)
			}
			var entry *ir.Module
			for _, m := range res.IR {
				for _, f := range m.Funcs() {
					if f.Name() == "main" && len(f.Params()) == 0 {
						entry = m
					}
				}
			}
			if entry == nil {
				t.Fatalf("%s does not retain its entry, so this control has no subject", id)
			}
			if len(res.IR) < 2 {
				t.Fatalf("%s lowered to %d module(s); a link needs at least two and this "+
					"control would otherwise be comparing a machine with itself",
					id, len(res.IR))
			}

			// UNLINKED: the entry module alone, which must fail.
			var bare bytes.Buffer
			_, bareErr := vm.New(entry, &bare).Run("main")
			if bareErr == nil {
				t.Fatalf("%s ran on the entry module ALONE, so the link is not what made "+
					"it comparable and this file's reading is wrong", id)
			}
			if !strings.Contains(bareErr.Error(), "did not retain") {
				t.Fatalf("%s failed unlinked for a reason other than the callee: %v",
					id, bareErr)
			}

			// LINKED, MINUS THE CALLEE'S MODULE: the identity must not be
			// found in a set that does not hold it.
			var partial bytes.Buffer
			_, partialErr := vm.NewProgram(entry, []*ir.Module{entry}, &partial).Run("main")
			if partialErr == nil {
				t.Fatalf("%s ran with only its own module in the link set, so resolution "+
					"is reaching a declaration it was not given", id)
			}

			// LINKED: the whole program, which must run and print something.
			var whole bytes.Buffer
			if _, err := vm.NewProgram(entry, res.IRModules(), &whole).Run("main"); err != nil {
				t.Fatalf("%s does not run linked: %v", id, err)
			}
			if whole.Len() == 0 {
				t.Fatalf("%s ran linked and printed nothing; the expectation it is "+
					"compared against is non-empty by construction", id)
			}
			t.Logf("unlinked: %v\nlinked: %q", bareErr, whole.String())
		})
	}
}

// TestVMExpectation_AnEmptySubsetPassesSoTheDenominatorIsTheGate states the
// contract's own warning as an executable fact rather than quoting it.
//
// expectation.go: "What CompareSubset cannot do is notice that the subset
// shrank, so a caller MUST assert its own coverage count separately. A subset
// check with no denominator is satisfiable by running nothing."
//
// This is the reading behind every pin in this file: an engine that produced
// NOTHING passes `CompareSubset` against the full corpus with zero
// differences. So the comparison is not the gate — the count is.
func TestVMExpectation_AnEmptySubsetPassesSoTheDenominatorIsTheGate(t *testing.T) {
	recorded, err := expectation.Load("corpus")
	if err != nil {
		t.Fatalf("loading the corpus expectation: %v", err)
	}
	nothing := &expectation.Set{Population: "corpus", What: "an engine that ran nothing"}
	if diffs := recorded.CompareSubset(nothing); len(diffs) != 0 {
		t.Fatalf("CompareSubset reported %d difference(s) for an engine that ran nothing; "+
			"this file's pins are justified by it reporting NONE:\n%s",
			len(diffs), strings.Join(diffs, "\n"))
	}
	// And the full check does not forgive it, which is the split CompareSubset
	// exists for and the reason a partial engine cannot simply use Compare.
	if full := recorded.Compare(nothing); len(full) == 0 {
		t.Fatal("Compare also forgave an engine that ran nothing, so the two entry points " +
			"do not differ and CompareSubset has no reason to exist")
	}
}

// TestVMExpectation_ComparableProgramsCoverScalarControlFlow pins graph
// coverage in programs whose complete output is independently recorded.
func TestVMExpectation_ComparableProgramsCoverScalarControlFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers every tour candidate")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	required := []string{"*ir.Arith", "*ir.Bind", "*ir.Branch", "*ir.Call", "*ir.Concat", "*ir.Const", "*ir.Copy", "*ir.Jump", "*ir.Make", "*ir.Match", "*ir.NoMatch", "*ir.Proj", "*ir.Ref", "*ir.Render", "*ir.Return", "*ir.Slot"}
	recorded, err := expectation.Load("tour")
	if err != nil {
		t.Fatalf("loading the tour expectation: %v", err)
	}
	ids, _, _ := candidateIDs(recorded)
	pathFor := vmPathResolver(t, "tour")
	subsetShapes := map[string]int{}
	subsetFuncs, subsetRecords := 0, 0
	for _, id := range ids {
		path, ok := pathFor(id)
		if !ok {
			continue
		}
		if _, reason := vmRun(path); reason != "" {
			continue
		}
		prog, err := irbuild.Analyze(path)
		if err != nil {
			t.Fatalf("%s lowered a moment ago and no longer analyzes: %v", id, err)
		}
		res, _, err := irbuild.GenerateIR(prog)
		if err != nil {
			t.Fatalf("%s lowered a moment ago and no longer generates: %v", id, err)
		}
		subsetRecords++
		for _, mod := range res.IR {
			shapes, funcs := graphShapes(mod)
			subsetFuncs += funcs
			for s, n := range shapes {
				subsetShapes[s] += n
			}
		}
	}
	if subsetRecords == 0 {
		t.Fatal("no comparable record, so this reading has no subject")
	}

	for _, shape := range required {
		if subsetShapes[shape] == 0 {
			t.Errorf("comparable Tour programs do not cover %s", shape)
		}
	}
	t.Logf("%d comparable records, %d retained functions, %d graph shapes", subsetRecords, subsetFuncs, len(subsetShapes))
}

// graphShapes counts every instruction and terminator in a module by its Go
// type, and the functions they came from.
func graphShapes(mod *ir.Module) (map[string]int, int) {
	shapes := map[string]int{}
	funcs := 0
	for _, f := range mod.Funcs() {
		funcs++
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				shapes[fmt.Sprintf("%T", in)]++
			}
			if term := b.Term(); term != nil {
				shapes[fmt.Sprintf("%T", term)]++
			}
		}
	}
	return shapes, funcs
}

// vmPathResolver answers, for a record id, the entry `.nomi` file to lower.
//
// corpus is a join against the population root, because a record
// id IS the population-relative path. The tour is not: a block's id is
// `chapter:L<line>` and the code lives in the markdown, so the blocks are
// extracted with `internal/doctest`'s own extractor — the same call the
// recorder and `internal/irbuild`'s tour probes make — and staged to a temp dir.
//
// A THIRD COPY OF THE STAGING EXISTS AND THE DRIFT RISK IS REAL, for
// `runReference`'s reason and with `runReference`'s bound: both other copies
// live in `_test.go` files and are unimportable, and all three are checked
// against the SAME committed artifact, so a copy that staged a block
// differently would produce a different transcript and fail. The evidence that
// this copy is right is that `irbuild.Analyze` accepts every staged block.
func vmPathResolver(t *testing.T, population string) func(string) (string, bool) {
	t.Helper()
	if root, ok := vmWholeProgramRoots[population]; ok {
		abs, err := filepath.Abs(root)
		if err != nil {
			t.Fatalf("resolving the %s root: %v", population, err)
		}
		return func(id string) (string, bool) {
			path := filepath.Join(abs, filepath.FromSlash(id))
			if _, err := os.Stat(path); err != nil {
				return "", false
			}
			return path, true
		}
	}
	if population != "tour" {
		t.Fatalf("no path resolver for population %q", population)
	}
	blocks := tourBlocksByID(t)
	base := t.TempDir()
	staged := map[string]string{}
	return func(id string) (string, bool) {
		if path, ok := staged[id]; ok {
			return path, true
		}
		code, ok := blocks[id]
		if !ok {
			return "", false
		}
		path := stageTourBlock(t, filepath.Join(base, fmt.Sprintf("b%03d", len(staged))), code)
		staged[id] = path
		return path, true
	}
}

// tourBlocksByID extracts every runnable tour block, keyed the way the
// recorder keys it.
func tourBlocksByID(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", "..", "tour", "src", "content", "docs"))
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdx")) {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, b := range doctest.ExtractBlocks(string(body), "nomi-run") {
			if b.HasInfo("ignore") {
				continue
			}
			out[fmt.Sprintf("%s:L%d", filepath.ToSlash(rel), b.Line)] = b.Code
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tour content: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no runnable tour blocks found; the extraction is wrong and every tour " +
			"candidate below would report as unstageable")
	}
	return out
}

// stageTourBlock writes one block's files to dir and answers its entry path.
func stageTourBlock(t *testing.T, dir, code string) string {
	t.Helper()
	entrySrc, entryName, virtual, manifest, splitErr := vmhost.SplitMultiFile(code)
	if splitErr != nil {
		t.Fatalf("splitting a tour block: %v", splitErr)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("staging dir: %v", err)
	}
	for name, src := range virtual {
		p := filepath.Join(dir, tourFileName(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("staging %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("staging %s: %v", name, err)
		}
	}
	// The manifest is re-staged from the block's RAW text rather than from the
	// parsed *analysis.Manifest, which is `tourResidueStage`'s reasoning: a
	// round trip through a TOML writer would be a second encoding of one fact.
	if manifest != nil {
		if raw := tourRawManifest(code); raw != "" {
			if err := os.WriteFile(filepath.Join(dir, "nomi.toml"), []byte(raw), 0o644); err != nil {
				t.Fatalf("staging the manifest: %v", err)
			}
		}
	}
	if entryName == "" {
		entryName = "main"
	}
	entry := filepath.Join(dir, tourFileName(filepath.Base(entryName)))
	if err := os.WriteFile(entry, []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("staging the entry: %v", err)
	}
	return entry
}

// tourFileName is an import-path key as a file name, idempotent over a name
// that already carries the suffix.
func tourFileName(name string) string {
	if strings.HasSuffix(name, ".nomi") {
		return name
	}
	return name + ".nomi"
}

// tourRawManifest is the raw text of a block's `// FILE: nomi.toml` section.
func tourRawManifest(code string) string {
	const marker = "// FILE:"
	var out []string
	in := false
	for _, line := range strings.Split(code, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, marker) {
			in = strings.TrimSpace(strings.TrimPrefix(trimmed, marker)) == "nomi.toml"
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n")
}

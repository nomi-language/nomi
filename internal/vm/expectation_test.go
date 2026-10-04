package vm_test

// Complete programs are compared with committed transcript artifacts. Empty
// transcripts and test reports are excluded here; testreport_test.go owns reports.
// Retention counts alone do not establish whole-program behavior. The measured
// subset and its named output-mutation controls are pinned below. Tour programs
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

// popPins is one population's measured shape. Every field is pinned because
// `CompareSubset` reports nothing about a subset that got SMALLER, and
// expectation.go makes asserting the denominator the caller's obligation: "A
// subset check with no denominator is satisfiable by running nothing."
//
// FOUR NUMBERS, because they fail differently. `reportShaped` moving means a
// file gained or lost tests. `empty` moving means a library file gained an
// entry point. `candidates` moving is the sum of those. `comparable` moving UP
// is the producer widening; moving DOWN is a regression no emit digest can
// see.
//
// The record count is DERIVED rather than pinned: it equals `reportShaped +
// empty + candidates` in every population, since every record falls in
// exactly one of the three. `records()` is compared against the artifact's
// own count, so a record that vanishes still breaks the sum.
type popPins struct {
	reportShaped int
	empty        int
	candidates   int
	comparable   int
}

// records is the population's record count, derived from the classification.
func (p popPins) records() int { return p.reportShaped + p.empty + p.candidates }

// vmPopulations is the measured state of all four artifacts.
//
// corpus is enumerated FROM THE ARTIFACT — a record's id is the
// population-relative path, so the committed file is the population's own
// enumeration and this consumer does not walk the tree a third time. The tour
// cannot be: a block's id is `chapter:L<line>` and only the markdown holds the
// code, so the blocks are extracted with the doctest runner's own extractor.
var vmPopulations = map[string]popPins{
	// A record is report-shaped when its `N` is above zero, empty when its `H`
	// is sha256 of the empty string, and a candidate otherwise; a row can be
	// derived from `<population>.expect` that way, and a wrong row's failure
	// prints the derived split.
	//
	// The corpus's candidates are its `fn main` files and its helper files,
	// whose record is `nomi run`'s refusal of a file with no `fn main` or a
	// diagnostic (not comparable: no entry). Nine compare:
	// `15-app-and-defer/app_field_permission/main.nomi` boots a Settings app
	// and prints the root through three sibling files' reads,
	// `15-app-and-defer/effects/main.nomi` dispatches a sibling file's
	// interface through its declaring unit's symbol,
	// `14-modules-and-packaging/file_import_display/main.nomi` prints values
	// whose types a sibling file declares,
	// `12-derives-and-standard-interfaces/generic_debug/main.nomi` renders
	// user generic types through Debug and Display, and the five entry files
	// whose boot a test group's `boot` line names
	// (`15-app-and-defer/{deadline_floor/supervisor_app, import_vs_app/import_app,
	// with_overrides/app}.nomi`, `16-concurrency/{message_loop/counter_app,
	// supervisors/app}.nomi`). The others include the
	// `18-ffi-and-dynamic/*_app/main.nomi` programs, whose Go bindings this
	// harness does not have.
	"corpus": {reportShaped: 164, empty: 0, candidates: 98, comparable: 9},
	// Every tour candidate is compared.
	"tour": {reportShaped: 16, empty: 0, candidates: 113, comparable: 113},
	// Every record is a `nomi test std/<module>` report.
	"stdlib": {reportShaped: 29, empty: 0, candidates: 0, comparable: 0},
	// Every record is a deliberately-red test report, which is why this is
	// the only population with power over a wrong failure message;
	// testreport_test.go compares it.
	"failure": {reportShaped: 49, empty: 0, candidates: 0, comparable: 0},
}

// vmLinkedRecords are the tour records that became comparable only because a
// machine can now resolve a callee in a sibling module.
//
// NAMED RATHER THAN DERIVED, because the control that uses them has to run the
// UNLINKED case and require it to fail: a list computed by "which records need
// a link" would be computed by the very mechanism under test.
var vmLinkedRecords = []string{
	"modules-and-imports.md:L79",  // `io.print(math.double(7))`
	"modules-and-imports.md:L117", // the same program with a private sibling beside it
	"modules-and-imports.md:L162", // a NESTED sibling, `http/header`
}

// vmDbgRecords are the comparable tour records whose transcript is `dbg`
// output, and vmNonDbgRecords the rest.
//
// BOTH LISTS ARE NAMED AND BOTH ARE USED, for `vmLinkedRecords`'s reason
// doubled: the plant below has to report every member of the first and NO
// member of the second, and a list derived from "which records contain the
// string `dbg`" would be derived from the output under test.
//
// THE SECOND LIST IS WHAT MAKES THE PLANT A PLANT rather than a global
// mutation: a mutation that changed every record would prove the comparison
// sees SOMETHING and not that it sees `dbg`.
//
// KEEPING THE FIRST LIST COMPLETE IS LOAD-BEARING. The plant runs over these
// two lists rather than over "everything comparable", so a `dbg` record left
// out would leave the plant passing over it while reporting that it covered
// the population. The count assertion below is what forces the addition.
var vmDbgRecords = []string{
	"scalars-and-strings.md:L229",     // byte buffers and octet iteration
	"scalars-and-strings.md:L158",     // grapheme and codepoint iteration
	"scalars-and-strings.md:L189",     // codepoint literal case arms over String.to_codepoints
	"generics.md:L68",                 // generic sort/take pipeline
	"interfaces-and-dispatch.md:L334", // bounded generic sort/take pipeline
	"functions-and-lambdas.md:L17",    // named-function conditional early return

	"functions-and-lambdas.md:L101", // lambda default supplier and explicit override

	"bindings-and-expressions.md:L337", // a scoped block supplies a binding value

	"bindings-and-expressions.md:L361", // conditional binding, literal case and condition chain

	"bindings-and-expressions.md:L15",  // `dbg greeting` over a String binding
	"bindings-and-expressions.md:L93",  // `dbg double(4)` — an impure operand, forced
	"bindings-and-expressions.md:L123", // `dbg area(6, 7)`
	"bindings-and-expressions.md:L187", // TWO dbgs, the first non-final: the drop delivery
	"bindings-and-expressions.md:L208", // `_ignored_count = 3` then `dbg "done"`
	"bindings-and-expressions.md:L224", // `_ = compute()`, a non-final dbg, a tail `Unit`
	"pipes.md:L16",                     // `5 |> double() |> dbg`, the piped stage
	"pipes.md:L40",                     // lazy map and list materialization
	"pipes.md:L62",                     // filtered sequence counts in both call and pipe forms
	"collections.md:L31",               // a lazy pipeline over an unbounded range stops at take
	"collections.md:L68",               // filter, map and a seeded reduction
	"collections.md:L189",              // bounded, inclusive and unbounded Int ranges
	"collections.md:L216",              // a codepoint literal range mapped through Codepoint.to_string
	"collections.md:L253",              // a Decimal range stepped by Range.step_by
	"typed-literals.md:L72",            // a struct built by its Literal handler
	"typed-literals.md:L113",           // Regex literals through std's handler and extern hosts
	"dates-and-times.md:L27",           // Date and DateTime literals and field reads
	"dates-and-times.md:L61",           // calendar arithmetic with Days, Weeks, Months and Years
	"dates-and-times.md:L142",          // DateTime equality across zones
	"dates-and-times.md:L172",          // civil and physical DateTime arithmetic across DST
	"dates-and-times.md:L226",          // a zone conversion compared with ==
	"dates-and-times.md:L253",          // DST disambiguation through DateTime.in_zone
	"interfaces-and-dispatch.md:L620",  // derived Comparable ordering over enum variants
	"structs-enums-distinct.md:L29",    // struct field defaults, punning and derived struct Debug
	"interfaces-and-dispatch.md:L264",  // struct, enum and distinct Debug through their impls
	"structs-enums-distinct.md:L312",   // bare, positional and struct-shaped variant Debug
	"pattern-matching.md:L46",          // dot-leading struct-shaped variant construction and patterns
	"interfaces-and-dispatch.md:L468",  // derived ToJson/FromJson and Json.encode
	"structs-enums-distinct.md:L64",    // target-typed brace literals at an annotation and a field
	"structs-enums-distinct.md:L113",   // struct and record spreads, punning and copies
	"structs-enums-distinct.md:L493",   // a Map of Lists built through Map.get and Map.put
	"interfaces-and-dispatch.md:L594",  // derived Comparable sort over a list of structs
	"interfaces-and-dispatch.md:L655",  // Iter.sort_by over a projected String key
	"interfaces-and-dispatch.md:L546",  // struct == and a Map keyed by a struct
	"structs-enums-distinct.md:L149",   // a nested spread and a bare patch at a struct field
	"structs-enums-distinct.md:L447",   // embedded struct and marker values in a List<Event>
	"iteration-and-loops.md:L113",      // callback-local return keeps mapping
	"iteration-and-loops.md:L75",       // break in a reduce and continue in a map
	"scalars-and-strings.md:L43",       // `dbg` over the scalar chapter's bindings
	"iteration-and-loops.md:L41",       // inline Iter.loop over Int and tuple state
	"concurrency.md:L31",               // three spawned tasks awaited in a concurrent block
	"concurrency.md:L79",               // producers and a consumer over a buffered channel
	"concurrency.md:L159",              // a try in a concurrent block cancels the sleeping sibling
	"concurrency.md:L243",              // Task.spawn_all over a filtered source, then await_all
	"concurrency.md:L652",              // a cancelled task's Outcome
	"concurrency.md:L801",              // a `with` context deadline ends a task's sleep
	"concurrency.md:L453",              // supervised audit tasks flushed before reading their channel
}

var vmNonDbgRecords = []string{
	"bindings-and-expressions.md:L32", // Display print and Debug inspect

	"scalars-and-strings.md:L90",       // three `io.print`s
	"scalars-and-strings.md:L114",      // a literal `${`, `$` and `#{` printed as text
	"modules-and-imports.md:L21",       // a parsed Date inspected through its std Debug impl
	"modules-and-imports.md:L216",      // an opaque type's impl functions called from a sibling file
	"modules-and-imports.md:L79",       // the three linked sibling-call records
	"modules-and-imports.md:L117",      //
	"modules-and-imports.md:L162",      //
	"interfaces-and-dispatch.md:L303",  // io.print through a user Display impl
	"typed-literals.md:L26",            // a distinct built by its Literal handler, then destructured
	"capabilities-and-context.md:L143", // deferred closes printed at a scoped block's exit
	"capabilities-and-context.md:L13",  // a booted `App.port` read printed through io.print
	"capabilities-and-context.md:L81",  // a `with` replacement of `App.logger` dispatched through Logger.log
	"capabilities-and-context.md:L181", // a closure made after a `with` line reads the field where it is called
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
	// callee may sit in the module next door (`modules-and-imports.md:L79`,
	// `:L117` and `:L162`). Running the entry's module alone would report them
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

	totalComparable := 0
	for _, population := range []string{"corpus", "tour"} {
		t.Run(population, func(t *testing.T) {
			pins := vmPopulations[population]
			recorded, err := expectation.Load(population)
			if err != nil {
				t.Fatalf("loading the committed expectation for %s: %v", population, err)
			}
			// The split is derived BEFORE the total is checked, and the
			// total's failure prints it, so a reader whose total is wrong
			// gets the number to copy rather than re-deriving it from
			// `<population>.expect` by hand.
			ids, reportShaped, empty := candidateIDs(recorded)
			if len(recorded.Cases) != pins.records() {
				t.Fatalf("%s holds %d records and the pin says %d; the population changed. "+
					"The derived split is {reportShaped: %d, empty: %d, candidates: %d}. "+
					"Copy that rather than adjusting the total, and attribute the move to a "+
					"named cause — this table pins a total AND a split, and only the total is a guard",
					population, len(recorded.Cases), pins.records(),
					reportShaped, empty, len(ids))
			}
			if reportShaped != pins.reportShaped {
				t.Errorf("%s: %d report-shaped records (N>0), pin %d",
					population, reportShaped, pins.reportShaped)
			}
			if empty != pins.empty {
				t.Errorf("%s: %d empty-transcript records, pin %d", population, empty, pins.empty)
			}
			if len(ids) != pins.candidates {
				t.Errorf("%s: %d candidates, pin %d", population, len(ids), pins.candidates)
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
			if len(got.Cases) != pins.comparable {
				t.Errorf("%s: the VM produced a whole transcript for %d record(s) and the pin "+
					"says %d.\nA FALL IS A REGRESSION the comparison above cannot see, because "+
					"CompareSubset forgives absence. A RISE is the producer widening and the "+
					"pin should be raised with the reading that justifies it.\nrefusals:\n%s",
					population, len(got.Cases), pins.comparable, strings.Join(refused, "\n"))
			}
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
	records := 0
	for _, pins := range vmPopulations {
		records += pins.records()
	}
	t.Logf("the VM is compared against %d record(s) of %d committed (%.1f%%); the "+
		"tour holds %d candidates and the compared count is %.1f%% of them",
		totalComparable, records, 100*float64(totalComparable)/float64(records),
		vmPopulations["tour"].candidates,
		100*float64(totalComparable)/float64(vmPopulations["tour"].candidates))
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
// `stdlib` and `failure` are 29 and 49 records and EVERY ONE declares cases,
// so every transcript is `rt.TestReporter`'s report. If a record with N=0 ever
// appeared in either, the VM would have a candidate there and this file would
// be silently ignoring it.
func TestVMExpectation_TheReportShapedPopulationsAreOutByConstruction(t *testing.T) {
	for _, population := range []string{"stdlib", "failure"} {
		pins := vmPopulations[population]
		recorded, err := expectation.Load(population)
		if err != nil {
			t.Fatalf("loading %s: %v", population, err)
		}
		if len(recorded.Cases) != pins.records() {
			t.Errorf("%s holds %d records, pin %d", population, len(recorded.Cases), pins.records())
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

	// THE NAMED RECORDS, so the reading is over the records the lists claim
	// rather than over whatever happens to be comparable.
	ids := append(append([]string(nil), vmDbgRecords...), vmNonDbgRecords...)

	run := func(what string, ids ...string) *expectation.Set {
		s := &expectation.Set{Population: "tour", What: what}
		for _, id := range ids {
			path, ok := pathFor(id)
			if !ok {
				t.Fatalf("%s could not be staged", id)
			}
			transcript, reason := vmRun(path)
			if reason != "" {
				t.Fatalf("%s is named as comparable and the VM answered %q", id, reason)
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
	for _, id := range vmDbgRecords {
		if !strings.Contains(reported, id) {
			t.Fatalf("a divergence planted inside rt.DbgText was NOT reported for the "+
				"dbg record %s, so the dbg transcripts are not actually being "+
				"compared:\n%s", id, reported)
		}
	}
	for _, id := range vmNonDbgRecords {
		if strings.Contains(reported, id) {
			t.Fatalf("the plant was reported for %s, which prints through io.print and "+
				"reaches no `dbg` — so the mutation is not specific to the path it "+
				"names:\n%s", id, reported)
		}
	}
	if len(diffs) != len(vmDbgRecords) {
		t.Fatalf("planted inside rt.DbgText and got %d difference(s) for %d dbg "+
			"record(s):\n%s", len(diffs), len(vmDbgRecords), reported)
	}
	t.Logf("planted a divergence inside rt.DbgText and the artifact comparison "+
		"reported exactly the %d dbg record(s) and none of the %d others:\n%s",
		len(vmDbgRecords), len(vmNonDbgRecords), reported)
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
	for _, id := range vmLinkedRecords {
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

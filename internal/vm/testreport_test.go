package vm_test

// THE VM AGAINST THE REPORT-SHAPED HALF OF THE COMMITTED EXPECTATIONS.
//
// `expectation_test.go` compares whole-PROGRAM transcripts and leaves the
// records whose transcript is a test report to this file. The unit here is a
// FILE'S TEST REPORT — what `nomi test <file>` prints — which is a different
// unit from that file's and therefore a different denominator, kept in a
// separate table for that reason rather than for convenience.
//
// A report needs both halves: the producer retains each test body as a test
// case (`ir.Module.Tests()`, apart from the declarations in `Funcs()`), and
// this package's runner drives them.
// `TestVMReport_TestCasesAreRetainedApartFromDeclarations` checks the first
// half.
//
// # WHAT A COMPARABLE RECORD REQUIRES, AND WHY EACH FILTER IS A PROPERTY
//
//	N > 0                the transcript is the report. That is the FILTER
//	                     IN here, and the exclusion in the other file.
//	every case retained  `len(mod.Tests())` must equal the recorded case
//	                     count. A file whose producer declined one case
//	                     would produce a transcript missing that case's
//	                     line, and comparing a partial report against a
//	                     whole one reports a RETENTION BOUNDARY as a wrong
//	                     answer.
//	every case runnable  a body that reaches an instruction this machine
//	                     has no arm for is NOT a failing case. `Machine.
//	                     RunTests` answers a reason instead of a report for
//	                     exactly that, because a machine that reported its
//	                     own limits as failure text would be comparing
//	                     itself.
//
// # WHICH RECORDS MUST COMPARE
//
// Decided as the whole-program unit's is (`vmRequiredComparable`, unit
// "reports"): every report-shaped tour and failure record, and the corpus
// records named in the committed `vm-corpus-reports.txt`, since a corpus test
// file whose producer declines one case cannot compare. The failure records
// carry their transcripts verbatim, so a wrong failure message shows as text.
// `stdlib` is not run here: a stdlib record's id is a module, not a path this
// unit lowers, and internal/expectation's TestExpectation_Stdlib compares
// every stdlib case.
//
// irimage_test.go drives this harness with each file's linked set
// round-tripped through the image format.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
)

// vmReportRoots are the populations whose record ids are paths, with the
// directory they are relative to.
//
// `stdlib` is absent because a stdlib record's id is a MODULE NAME and its
// cases are lowered by `irbuild.GenerateStdlibTestIR` rather than through
// `irbuild.Analyze`. internal/expectation's TestExpectation_Stdlib compares them.
var vmReportRoots = map[string]string{
	"corpus":  "tests",
	"failure": "internal/irbuild/testdata",
}

// reportShapedIDs is the records whose transcript IS a test report, with the
// count so a caller can pin it.
//
// THE FILTER IS A PROPERTY OF THE RECORD AND NOT OF ANY RUN, which is what
// keeps this from selecting the cases it passes: `N` is read out of the
// committed artifact before the machine is opened.
func reportShapedIDs(s *expectation.Set) (ids []string, cases map[string]expectation.Case) {
	cases = map[string]expectation.Case{}
	for _, c := range s.Cases {
		if c.Cases == 0 {
			continue
		}
		ids = append(ids, c.ID)
		cases[c.ID] = c
	}
	sort.Strings(ids)
	return ids, cases
}

// vmReportRun is one file's whole test report: lower it, find the module the
// entry file produced, and run every case it retained.
//
// It answers a normalized transcript plus the exit status and case count a
// record carries, or the reason this record is not comparable. THE REASONS ARE
// THE DELIVERABLE: "retained 2 of 6 cases" over a corpus file is a producer
// measurement, and reporting it per record is what makes a zero actionable
// instead of merely true.
// vmReportCaptureStderr runs fn with the process's stderr captured.
//
// rt writes a supervised task's failure report — `nomi: background task
// errored (attempt 1): … — retrying in …` — to os.Stderr, where the compiled
// program writes it too, and the recorder's transcript carries it under a
// `--- stderr ---` label. So the VM's report is compared with that stream
// included, or a program whose worker restarts could never match. The package
// runs no test in parallel, so the swap is observed by this run alone.
func vmReportCaptureStderr(fn func() (int, string)) (string, int, string) {
	r, w, err := os.Pipe()
	if err != nil {
		exit, reason := fn()
		return "", exit, reason
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	exit, reason := fn()
	os.Stderr = saved
	_ = w.Close()
	captured := <-done
	_ = r.Close()
	return captured, exit, reason
}

func vmReportRun(population, path string, wantCases int) (transcript string,
	exit, cases int, reason string) {
	prog, err := irbuild.Analyze(path)
	if err != nil {
		return "", 0, 0, "the front end refuses this program, so the record is a diagnostic"
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		return "", 0, 0, "Generate refuses this program"
	}
	var mod *ir.Module
	for _, m := range res.IR {
		// BY THE DECLARING PATH, which is what `ir.NewModule` is handed and
		// what `nomi test <file>` selects on: the command collects the NAMED
		// file's cases and no others, and `importedModuleRefusal` keeps the
		// builder's test table identical to it. A sibling module's tests are not
		// this record's.
		if m.Name() == path {
			mod = m
		}
	}
	if mod == nil {
		return "", 0, 0, "this file retained nothing at all, so it has no module"
	}
	if population == "tour" {
		// A TOUR RECORD'S N IS A FLAG, NOT A COUNT: `runTourBlock`'s set
		// records 1 for any block that declares tests. So a block of two
		// cases with one retained would pass a comparison against N and then
		// run half the block. The block's own case count is the runner's,
		// taken from the entry the front end checked, and the flag is what
		// the record is compared against.
		flag := wantCases
		defer func() {
			if reason == "" {
				cases = flag
			}
		}()
	}
	// A RECORD'S N COUNTS CASES, a `subtests` row each (rt.CountCases), so
	// it is not the number of tests to retain. The tests to retain are the
	// entry's declared ones, and the cases the run counted are read off its
	// own summary line (reportCases), which is the number the command records.
	declared := len(frontend.CollectTestCases(prog.Entry().Nodes))
	if got := len(mod.Tests()); got != declared {
		return "", 0, 0, fmt.Sprintf("retained %d of %d case(s)", got, declared)
	}
	var out bytes.Buffer
	// THE PROGRAM'S MODULES, as `vmRun` hands a whole program: a std host
	// outside rt (calendar parsing, regex) is answered through its generated
	// adapter, and a machine opened without the linked modules reports its
	// own limit instead of the case.
	mod, mods, err := vmLink(mod, res.IRModules())
	if err != nil {
		return "", 0, 0, "the link hook refused it: " + err.Error()
	}
	m := vm.NewProgram(mod, mods, &out).WithLocaleData(programLocales(prog))
	// A tour block's tests read its data files as its run does (tourFiles).
	if files := tourFiles(path); files != nil {
		m = m.WithFiles(files)
	}
	if population != "tour" {
		var stderr string
		stderr, exit, reason = vmReportCaptureStderr(func() (int, string) { return m.RunTests(path) })
		if reason != "" {
			return "", 0, 0, "the machine could not run a case: " + reason
		}
		// The recorder's transcript: stdout, then stderr under a label when
		// anything reached it (internal/expectation's `observed.transcript`).
		transcript = out.String()
		if stderr != "" {
			transcript += "--- stderr ---\n" + stderr
		}
		return transcript, exit, reportCases(out.String()), ""
	}
	// THE TOUR'S COMPOSITION IS THE PLAYGROUND'S AND IT IS NOT `nomi test`'s,
	// in two ways, both read off `runTourBlock` in
	// internal/expectation/populations_test.go rather than guessed:
	//
	//  1. THE PROGRAM RUNS FIRST, into the same buffer, and then the tests.
	//     `nomi test <file>` lowers `fn main` and never calls it
	//     (`renderTestBoot`), so the two commands produce different
	//     transcripts for one file and the record is the playground's.
	//  2. A CASE IS LABELLED BARE. The playground builds a
	//     `vmhost.NewTestReport` and calls `rep.Result(c.Name, …)` with no
	//     path, because a block has no file a reader could open.
	//
	// The second is why `rt.RunTestsLabelled` has a label parameter.
	for _, f := range mod.Funcs() {
		if f.Name() != "main" || len(f.Params()) != 0 {
			continue
		}
		// The program runs as the playground's run does: boot when the
		// block's entry declares one, `main`, then the app's shutdown.
		if err := m.Main(context.Background(), nil, false); err != nil {
			return "", 0, 0, "the machine ran part of the block's program: " + err.Error()
		}
		break
	}
	exit, reason = m.RunTestsLabelled(func(name string) string { return name })
	if reason != "" {
		return "", 0, 0, "the machine could not run a case: " + reason
	}
	return out.String(), exit, len(mod.Tests()), ""
}

// reportCases is the number of cases a report's `test result:` line counts,
// passed, failed and blocked summed, or 0 when it has none.
func reportCases(report string) int {
	m := reportSummary.FindStringSubmatch(reportColour.ReplaceAllString(report, ""))
	if m == nil {
		return 0
	}
	n := 0
	for _, s := range m[1:] {
		v, _ := strconv.Atoi(s)
		n += v
	}
	return n
}

var (
	reportSummary = regexp.MustCompile(`(?m)^test result: \S+\. (\d+) passed, (\d+) failed(?:, (\d+) blocked)?$`)
	reportColour  = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

// vmReportSubsetOf runs every report-shaped record and returns the engine's
// Set plus a per-record refusal report.
func vmReportSubsetOf(t *testing.T, population string, ids []string,
	recorded map[string]expectation.Case,
	pathFor func(string) (string, bool)) (*expectation.Set, []string) {
	t.Helper()
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	got := &expectation.Set{Population: population,
		What: "one record per file whose every `test` case is a retained ir.Func, run " +
			"on internal/vm through rt.RunTestsTo and compared as a SUBSET of the " +
			"committed population."}
	var refused []string
	for _, id := range ids {
		path, ok := pathFor(id)
		if !ok {
			refused = append(refused, fmt.Sprintf("%s: could not be staged", id))
			continue
		}
		transcript, exit, cases, reason := vmReportRun(population, path, recorded[id].Cases)
		if reason != "" {
			refused = append(refused, fmt.Sprintf("%s: %s", id, reason))
			continue
		}
		got.Add(expectation.NewCase(id, exit, cases, expectation.Normalize(transcript, root)))
	}
	got.Sort()
	return got, refused
}

// vmReportPathResolver answers, for a record id, the entry `.nomi` file to
// lower.
//
// The tour's staging is `vmPathResolver`'s, CALLED rather than copied: a
// block's id is `chapter:L<line>` and only the markdown holds the code, and a
// fourth copy of the staging is what `expectation_test.go` already names as a
// drift risk.
func vmReportPathResolver(t *testing.T, population string) func(string) (string, bool) {
	t.Helper()
	if population == "tour" {
		return vmPathResolver(t, "tour")
	}
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	rel, known := vmReportRoots[population]
	if !known {
		t.Fatalf("no root for population %q", population)
	}
	base := filepath.Join(root, rel)
	return func(id string) (string, bool) { return filepath.Join(base, id), true }
}

// TestVMReport_TestCasesAreRetainedApartFromDeclarations checks that a file's
// test cases are retained as test cases, apart from its declarations.
//
// It counts how many of the file's retained `ir.Func`s are DECLARATIONS
// (`Module.Funcs()`) and how many are test CASES (`Module.Tests()`). A runner
// built over `Funcs()` alone would have nothing to drive, and a test case
// must not be resolvable as a callee.
func TestVMReport_TestCasesAreRetainedApartFromDeclarations(t *testing.T) {
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "internal/irbuild/testdata/tests_fail.nomi")
	prog, err := irbuild.Analyze(path)
	if err != nil {
		t.Fatalf("analyzing %s: %v", path, err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatalf("generating %s: %v", path, err)
	}
	var decls, cases int
	for _, m := range res.IR {
		decls += len(m.Funcs())
		cases += len(m.Tests())
		for _, c := range m.Tests() {
			for _, f := range m.Funcs() {
				if f == c.Fn() {
					t.Errorf("%s is in BOTH Funcs() and Tests(); a test case is not a "+
						"declaration and must not be resolvable as a callee", c.Name())
				}
			}
		}
	}
	if cases == 0 {
		t.Fatal("no test case was retained for tests_fail.nomi, so the runner has " +
			"nothing to drive and the whole comparison above is vacuous")
	}
	if decls == 0 {
		t.Error("tests_fail.nomi declares `fn double`, so a declaration should be " +
			"retained too; a zero here means the fixture stopped exercising the call path")
	}
	t.Logf("tests_fail.nomi: %d retained declaration(s), %d retained test case(s)", decls, cases)
}

// TestVMReport_AttachedCasesAreRetained holds a file with both case kinds to
// retaining every case: `testdata/tests_attached.nomi` declares one `test`
// declaration and three `//!` prompt cases, and a producer that declined the
// prompts would retain one.
func TestVMReport_AttachedCasesAreRetained(t *testing.T) {
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	set, err := expectation.Load("failure")
	if err != nil {
		t.Fatal(err)
	}
	var declared int
	for _, c := range set.Cases {
		if c.ID == "tests_attached.nomi" {
			declared = c.Cases
		}
	}
	if declared == 0 {
		t.Fatal("tests_attached.nomi is not in the failure population any more, so this " +
			"control has no witness; find another file with both case kinds")
	}
	path := filepath.Join(root, "internal/irbuild/testdata/tests_attached.nomi")
	prog, err := irbuild.Analyze(path)
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	retained := 0
	for _, m := range res.IR {
		if m.Name() == path {
			retained = len(m.Tests())
		}
	}
	if retained != declared {
		t.Errorf("%d of %d cases of tests_attached.nomi retained; its `//!` prompts are "+
			"ordinary test bodies and every one should be", retained, declared)
	}
}

package expectation_test

// THE GOLDEN FILES' WRITER AND THEIR CHECK, on the VM.
//
// Four populations of runnable Nomi — the corpus, the stdlib's `//!` prompts,
// the tour's blocks and the deliberately-red fixtures — are run
// the way the command that owns each one runs it, and the rendered golden file
// must be byte-identical to the committed one. With
// NOMI_REGENERATE_EXPECTATIONS=1 the rendered file is written over the
// committed one instead, and a reviewer names the reason for every moved record
// in the commit message.
//
// The runs go through internal/vmcmd, which is what `nomi run` and `nomi test`
// call, so a record is the command's output by construction: a file that
// declares tests is run as `nomi test` runs it, anything else as `nomi run`,
// and an FFI project through its generated wrapper in both cases.
//
// IT CANNOT SAY THE VM IS CORRECT. It says the observable output is what the
// repo recorded. If a record was wrong when it was written, this defends it.

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/vmcmd"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// observed is one run's whole observable output.
type observed struct {
	stdout string
	stderr string
	exit   int
}

func (o observed) transcript() string {
	return expectation.Transcript(o.stdout, o.stderr)
}

// nomiLang is the repository root, this package's ../..
func nomiLang(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// requireFFIPreparation fails the test when the FFI wrapper for path cannot
// be prepared.
//
// vmcmd reports a preparation failure as the file's own `FAIL` line, which is
// right for `nomi test` and wrong for a recorder: written down, it reads as
// the program's output. The usual cause is NOMI_FFIRUN_CACHE_ROOT being unset
// or unusable, which internal/ffirun refuses under `go test` by design, so
// that a test never reads or evicts a developer's real build cache.
func requireFFIPreparation(t *testing.T, path string) {
	t.Helper()
	if _, err := ffirun.Prepare(path); err != nil {
		t.Fatalf("preparing the FFI wrapper for %s: %v\n"+
			"The recorder will not record a run whose wrapper could not be prepared: "+
			"its transcript would be the preparation error, read as the program's output.",
			path, err)
	}
}

// runCommand runs path the way the command that owns it would: `nomi test`
// when it declares tests, `nomi run` otherwise. It answers the cases the run
// accounted for, 0 for a program.
func runCommand(t *testing.T, path string) (observed, int) {
	t.Helper()
	requireFFIPreparation(t, path)
	if hasTests, err := vmhost.FileDeclaresTests(path); err == nil && hasTests {
		return runTests(t, path)
	}
	var out, errOut bytes.Buffer
	code := vmcmd.Run(path, &out, &errOut, bytes.NewReader(nil), nil, false)
	return observed{stdout: out.String(), stderr: errOut.String(), exit: code}, 0
}

// runTests is `nomi test <path>`, captured. The report and the cases' own
// output share one buffer because they share one stream in `nomi test`.
func runTests(t *testing.T, path string) (observed, int) {
	t.Helper()
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		return observed{stderr: err.Error() + "\n", exit: 1}, 0
	}
	defer restoreEnv()
	var out, errOut bytes.Buffer
	rep := vmhost.NewTestReport(&out)
	tester := &vmcmd.Tester{Out: &out, Stderr: &errOut, Stdin: bytes.NewReader(nil), Rep: rep}
	cases := tester.File(path, vmhost.TestOptions{})
	o := observed{stderr: errOut.String()}
	if rep.Summary() {
		o.exit = 1
	}
	o.stdout = out.String()
	return o, cases
}

// nomiFilesUnder lists every .nomi file under root, sorted, slash-separated
// relative paths.
func nomiFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var rels []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".nomi") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(rels)
	return rels
}

// pinRecorderEnv fixes every environment variable that shapes a record.
//
// NOMI_FFIRUN_CACHE_ROOT is required: ffirun refuses the default cache root
// under `go test`. NOMI_COLOR=never keeps escape sequences out of every
// transcript whatever the shell exports. NOMI_ENV is cleared, as a fresh shell
// has it, because `nomi test` defaults it to "test" and leaves a caller's value
// alone, and one corpus program prints it.
func pinRecorderEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	t.Setenv("NOMI_COLOR", "never")
	if prior, ok := os.LookupEnv("NOMI_ENV"); ok {
		if err := os.Unsetenv("NOMI_ENV"); err != nil {
			t.Fatalf("clearing NOMI_ENV: %v", err)
		}
		t.Cleanup(func() { _ = os.Setenv("NOMI_ENV", prior) })
	}
}

// TestExpectation_APreparationFailureIsNeverRecorded is the planted positive
// for requireFFIPreparation: with an unusable cache root, the FFI project's
// preparation fails, and the report vmcmd would print for it is a plausible
// failing `nomi test` transcript that differs from the committed record. That
// transcript is what a recorder without the check would write down.
func TestExpectation_APreparationFailureIsNeverRecorded(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(notADir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("staging the unusable cache root: %v", err)
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", filepath.Join(notADir, "builds"))
	t.Setenv("NOMI_COLOR", "never")

	const rel = "18-ffi-and-dynamic/callback_ffi_app/main_test.nomi"
	path := filepath.Join(nomiLang(t), "tests", filepath.FromSlash(rel))
	if _, err := ffirun.Prepare(path); err == nil {
		t.Fatalf("preparing %s succeeded with an unusable cache root, so this test "+
			"measures nothing", rel)
	}
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	o, _ := runTests(t, path)
	got := expectation.Normalize(o.transcript(), root)
	if !expectation.IsFailing(got) || o.exit != 1 {
		t.Errorf("the run with a failed preparation reported no failure (exit %d):\n%s", o.exit, got)
	}
	want, err := expectation.Load("corpus")
	if err != nil {
		t.Fatal(err)
	}
	recorded, ok := want.Lookup(rel)
	if !ok {
		t.Fatalf("corpus.expect has no record for %s; the population changed", rel)
	}
	if recorded.Transcript == got {
		t.Errorf("the failed-preparation transcript equals the committed record for %s, "+
			"so the wrapper makes no observable difference and this guard is vacuous", rel)
	}
	if recorded.Exit != 0 || recorded.Cases == 0 {
		t.Errorf("the committed record for %s is exit %d / %d case(s); it should be a passing "+
			"wrapper run with cases", rel, recorded.Exit, recorded.Cases)
	}
}

// checkOrRecord renders got and either writes it over the committed golden
// file (NOMI_REGENERATE_EXPECTATIONS=1) or requires the committed file to be
// byte-identical to it, so a changed header, population or record order fails
// as well as a moved record.
func checkOrRecord(t *testing.T, got *expectation.Set) {
	t.Helper()
	got.Sort()
	path, err := expectation.Path(got.Population)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the committed golden file for %s: %v", got.Population, err)
	}
	want, err := expectation.Parse(committed)
	if err != nil {
		t.Fatalf("the committed golden file for %s does not parse: %v", got.Population, err)
	}
	regenerate := expectation.RegenerateRequested()
	gaps := 0
	for _, c := range got.Cases {
		// A BLOCKED transcript is not an answer: checking or regenerating, a
		// program the VM cannot run fails rather than being recorded.
		if reason, gap := expectation.VMGap(c.Transcript); gap {
			gaps++
			t.Errorf("%s: the VM cannot run it (%s)", c.ID, reason)
			continue
		}
		rec, ok := want.Lookup(c.ID)
		if !ok || regenerate {
			continue
		}
		if verdict, detail := expectation.JudgeVM(rec, c.Transcript, c.Exit); verdict == expectation.Wrong {
			t.Errorf("%s: the VM runs it to a different answer than its record, which is a "+
				"VM bug to fix: %s", c.ID, detail)
		}
	}
	t.Logf("%s: %d records, %d declared cases, %d FAILING on purpose, fingerprint %s",
		got.Population, len(got.Cases), got.TotalCases(), got.Failing(), got.Fingerprint()[:16])
	if regenerate {
		if gaps > 0 {
			t.Fatalf("%s: not rewriting the golden file over %d program(s) the VM cannot run",
				got.Population, gaps)
		}
		if err := expectation.Store(got); err != nil {
			t.Fatalf("storing %s: %v", got.Population, err)
		}
		t.Logf("%s: REWROTE the committed golden file because "+
			"NOMI_REGENERATE_EXPECTATIONS=1. Review the diff and name the reason for "+
			"every moved record in the commit message.", got.Population)
		return
	}
	if bytes.Equal(committed, got.Render()) {
		return
	}
	diffs := want.Compare(got)
	if len(diffs) == 0 {
		diffs = []string{"no record moved, but the rendered file differs from the committed " +
			"one (header, ordering or format); regenerate it"}
	}
	t.Errorf("%s: the VM does not reproduce the committed golden file; "+
		"%d difference(s):\n%s", got.Population, len(diffs), strings.Join(diffs, "\n"))
}

// TestExpectation_Corpus: one record per tests/ file. The per-file N
// is the declared case count, which is how a dropped case is caught without
// splitting the transcript.
func TestExpectation_Corpus(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs every corpus program; -short")
	}
	pinRecorderEnv(t)
	root := filepath.Join(nomiLang(t), "tests")
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	set := &expectation.Set{
		Population: "corpus",
		What: "one record per .nomi file under tests/, run as the command that owns it " +
			"would: `nomi test` when the file declares tests, `nomi run` otherwise, through the " +
			"generated FFI wrapper when the project needs one. N is the declared case count " +
			"vmcmd.Tester.File answers.",
	}
	for _, rel := range nomiFilesUnder(t, root) {
		o, cases := runCommand(t, filepath.Join(root, rel))
		set.Add(expectation.NewCase(rel, o.exit, cases,
			expectation.Normalize(o.transcript(), wtRoot)))
	}
	checkOrRecord(t, set)
}

// TestExpectation_Stdlib: one record per stdlib module carrying `//!` prompt
// cases, run as `nomi test std/<module>` runs it.
func TestExpectation_Stdlib(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs every stdlib module's prompts; -short")
	}
	pinRecorderEnv(t)
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	set := &expectation.Set{
		Population: "stdlib",
		What: "one record per stdlib module carrying `//!` prompt cases, run as " +
			"`nomi test std/<module>` runs it. N is the module's prompt-case count.",
	}
	for _, module := range stdlibModulesWithPrompts(t) {
		path := strings.TrimPrefix(std.Load().FileURI(module), "file://")
		if !vmcmd.IsStdlibPath(path) {
			t.Fatalf("std/%s resolves to %s, which `nomi test` would not run as a stdlib module", module, path)
		}
		o, cases := runTests(t, path)
		set.Add(expectation.NewCase(module, o.exit, cases,
			expectation.Normalize(o.transcript(), wtRoot)))
	}
	checkOrRecord(t, set)
}

// stdlibModulesWithPrompts lists the stdlib modules that declare at least one
// prompt case.
func stdlibModulesWithPrompts(t *testing.T) []string {
	t.Helper()
	var modules []string
	for module := range std.Load().Modules {
		src, ok := std.ReadFile(module)
		if !ok {
			continue
		}
		has, err := frontend.SourceDeclaresTests(string(src))
		if err != nil || !has {
			continue
		}
		modules = append(modules, module)
	}
	sort.Strings(modules)
	if len(modules) == 0 {
		t.Fatal("no stdlib module declares prompt cases; the enumeration is wrong")
	}
	return modules
}

// tourChapters is every tour chapter, sorted.
func tourChapters(t *testing.T) (root string, chapters []string) {
	t.Helper()
	root = filepath.Join(nomiLang(t), "tour", "src", "content", "docs")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdx")) {
			chapters = append(chapters, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tour content: %v", err)
	}
	sort.Strings(chapters)
	return root, chapters
}

// TestExpectation_Tour: one record per runnable tour block. The hidden
// `<!-- expect -->` block (vmhost's TestTourDoctests) is the authority on a
// block's program output; this pins the set of blocks and records what a
// block's own tests report, which the expect block does not cover.
func TestExpectation_Tour(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs every tour block; -short")
	}
	pinRecorderEnv(t)
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	root, chapters := tourChapters(t)
	set := &expectation.Set{
		Population: "tour",
		What: "one record per runnable ```nomi-run block in tour, keyed " +
			"chapter:L<line>, run the way the tour playground runs it. N is 1 when the block " +
			"declares tests and 0 otherwise; a block's own tests run and their report is part " +
			"of the transcript, which the hidden <!-- expect --> block does not cover.",
	}
	for _, chapter := range chapters {
		data, err := os.ReadFile(chapter)
		if err != nil {
			t.Fatalf("reading %s: %v", chapter, err)
		}
		rel, err := filepath.Rel(root, chapter)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			if b.HasInfo("ignore") {
				continue
			}
			id := fmt.Sprintf("%s:L%d", filepath.ToSlash(rel), b.Line)
			o, hasTests := runTourBlock(b)
			n := 0
			if hasTests {
				n = 1
			}
			set.Add(expectation.NewCase(id, o.exit, n,
				expectation.Normalize(o.transcript(), wtRoot)))
		}
	}
	if len(set.Cases) == 0 {
		t.Fatal("no runnable ```nomi-run blocks found; the enumeration is wrong")
	}
	checkOrRecord(t, set)
}

// runTourBlock runs one block the way the tour playground does: one lowering
// runs the program, then its tests, into the same buffer.
func runTourBlock(b doctest.Block) (observed, bool) {
	var out bytes.Buffer
	entrySrc, entryName, virtualFiles, manifest, err := vmhost.SplitMultiFile(b.Code)
	if err != nil {
		return observed{stderr: err.Error() + "\n", exit: 1}, false
	}
	hasTests, err := vmhost.SourceContainsTests(entrySrc)
	if err != nil {
		return observed{stderr: err.Error() + "\n", exit: 1}, false
	}
	p, err := vmhost.LoadSource(entryName, entrySrc,
		vmhost.WithVirtualFiles(virtualFiles), vmhost.WithVirtualManifest(manifest))
	if err != nil {
		return observed{stdout: out.String(), stderr: err.Error() + "\n", exit: 1}, hasTests
	}
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		var errOut bytes.Buffer
		if blocked, ok := vmhost.IsBlocked(err); ok {
			blocked.Write(&errOut, entryName)
		} else {
			vmhost.WriteFailure(&errOut, err)
		}
		return observed{stdout: out.String(), stderr: errOut.String(), exit: 1}, hasTests
	}
	if !hasTests {
		return observed{stdout: out.String()}, false
	}
	rep := vmhost.NewTestReport(&out)
	for _, c := range p.Cases(&out, vmhost.TestOptions{}) {
		if c.Blocked != nil {
			rep.Blocked(c.Name, c.Blocked)
			continue
		}
		rep.Result(c.Name, c.Err)
	}
	o := observed{}
	if rep.Summary() {
		o.exit = 1
	}
	o.stdout = out.String()
	return o, true
}

// failureFixtures is every fixture under internal/irbuild/testdata whose run
// reports a failure, as measured by TestExpectation_FailureCensus. These are
// the population with power over a wrong failure message: the other three hold
// no case that fails on purpose.
var failureFixtures = []string{
	"anon_struct.nomi",
	"anon_vs_nominal.nomi",
	"assert_boundary.nomi",
	"assert_shape.nomi",
	"assertable_subject_report.nomi",
	"check_shape_report.nomi",
	"context_values_report.nomi",
	"dbg_surfaces.nomi",
	"debug_display_surfaces.nomi",
	"debug_embeds.nomi",
	"debug_erased.nomi",
	"decimal.nomi",
	"enum_field_read_trap.nomi",
	"erased_operand.nomi",
	"erased_operand_row.nomi",
	"func_operand_row.nomi",
	"generic_struct.nomi",
	"impl_call_operand_row.nomi",
	"inspect_through_list.nomi",
	"iter_operand_report.nomi",
	"ordering.nomi",
	"pattern_assert.nomi",
	"pipe_assert_report.nomi",
	"pipe_stage_keyword_report.nomi",
	"pipe_stages.nomi",
	"scalar_operator_traps.nomi",
	"sibimpl/report_test.nomi",
	"siblings/report_test.nomi",
	"std_enum_modes.nomi",
	"tests_attached.nomi",
	"tests_call_boundary.nomi",
	"tests_case_subject.nomi",
	"tests_defined_as.nomi",
	"tests_fail.nomi",
	"tests_group_failure.nomi",
	"tests_inspect_vs_display.nomi",
	"tests_lambda_boundary.nomi",
	"tests_mixed.nomi",
	"tests_operands.nomi",
	"tests_refute.nomi",
	"tests_short_circuit.nomi",
	"tests_trap.nomi",
	"try_attached.nomi",
	"try_check_in_test.nomi",
	"try_in_test.nomi",
	"unit_value.nomi",
	"untyped_equality_report.nomi",
	"untyped_list_report.nomi",
	"virtual_clock_trap.nomi",
}

// TestExpectation_Failure records the failure text of every deliberately-red
// fixture verbatim. Every record must report a failure: a fixture that quietly
// stopped failing would keep matching while measuring nothing.
func TestExpectation_Failure(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs the deliberately-red fixtures; -short")
	}
	pinRecorderEnv(t)
	root := filepath.Join(nomiLang(t), "internal", "irbuild", "testdata")
	wtRoot, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	set := &expectation.Set{
		Population: "failure",
		What: "one record per deliberately-red fixture under internal/irbuild/testdata, run as " +
			"the command that owns it would. Every record FAILS on purpose, so every record " +
			"carries its transcript verbatim: this is the only population in the repo with " +
			"power over a wrong failure message. Membership is measured by " +
			"TestExpectation_FailureCensus.",
	}
	for _, rel := range failureFixtures {
		path := filepath.Join(root, rel)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("failureFixtures names %s, which is not there: %v", rel, statErr)
			continue
		}
		o, cases := runCommand(t, path)
		set.Add(expectation.NewCase(rel, o.exit, cases, expectation.Normalize(o.transcript(), wtRoot)))
	}
	// A VM gap keeps its record, so this is asked of what is recorded.
	checkOrRecord(t, set)
	for _, c := range set.Cases {
		if !expectation.IsFailing(c.Transcript) {
			t.Errorf("%s reports NO failure, so it measures no failure text; "+
				"re-run the census and drop it from failureFixtures:\n%s", c.ID, c.Transcript)
		}
	}
}

// TestExpectation_FailureCensus re-derives failureFixtures by running every
// fixture under internal/irbuild/testdata. Gated: set NOMI_EXPECTATION_CENSUS=1
// when the red set may have changed.
func TestExpectation_FailureCensus(t *testing.T) {
	if os.Getenv("NOMI_EXPECTATION_CENSUS") != "1" {
		t.Skip("slow; set NOMI_EXPECTATION_CENSUS=1 to re-derive failureFixtures")
	}
	pinRecorderEnv(t)
	root := filepath.Join(nomiLang(t), "internal", "irbuild", "testdata")
	recorded := map[string]bool{}
	for _, rel := range failureFixtures {
		recorded[rel] = true
	}
	all := nomiFilesUnder(t, root)
	red := 0
	for _, rel := range all {
		o, _ := runCommand(t, filepath.Join(root, rel))
		if !expectation.IsFailing(o.transcript()) {
			continue
		}
		red++
		if !recorded[rel] {
			t.Errorf("%s reports a failure and is NOT in failureFixtures; add it", rel)
		}
		delete(recorded, rel)
	}
	for rel := range recorded {
		t.Errorf("failureFixtures names %s, which no longer reports a failure; remove it", rel)
	}
	t.Logf("%d of %d fixtures report a failure", red, len(all))
}

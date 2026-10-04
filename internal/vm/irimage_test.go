package vm_test

// THE IR IMAGE FORMAT AGAINST EVERY POPULATION THAT PRODUCES IR.
//
// `nomi build` appends an encoded
// ir.Image to a runner binary, and the runner hands the decoded modules to the
// VM in place of a lowering. So a decoded image must be the graph the lowering
// built: it must lint, it must encode back to the same bytes, and it must run
// to the same output.
//
// THE THREE CHECKS, AND WHAT EACH ONE COVERS:
//
//   - RE-ENCODE EQUALITY, over every module the corpus, the
//     failure fixtures, the tour, the cached stdlib and every stdlib module's
//     attached tests produce. The encoder writes every field of every node,
//     so equal bytes after a round trip mean an equal graph, pointer sharing
//     included.
//   - LINT (ir.LintModule), over the whole decoded stdlib once, and over each
//     program's own units and each stdlib module's test unit.
//   - RUNNING. The committed records: the harness of expectation_test.go and
//     testreport_test.go, run again with vmLink swapped for the round trip,
//     must compare as it does undecoded, with the same pinned counts. The
//     stdlib's attached tests: each module whose undecoded run is stable
//     across two runs must run identically decoded.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
)

// imageRoundTrip encodes mods with entry as the image's entry, decodes it,
// checks the decoded image encodes to the same bytes and that its first
// `lint` modules lint, and answers the decoded entry and set.
func imageRoundTrip(entry *ir.Module, mods []*ir.Module, lint int) (*ir.Module, []*ir.Module, error) {
	idx := -1
	for i, m := range mods {
		if m == entry {
			idx = i
		}
	}
	data, err := ir.EncodeImage(ir.Image{Modules: mods, Entry: idx})
	if err != nil {
		return nil, nil, err
	}
	im, err := ir.DecodeImage(data)
	if err != nil {
		return nil, nil, err
	}
	again, err := ir.EncodeImage(im)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(data, again) {
		return nil, nil, errorString("the decoded image encodes to different bytes")
	}
	for i := 0; i < lint && i < len(im.Modules); i++ {
		if err := ir.LintModule(im.Modules[i]); err != nil {
			return nil, nil, err
		}
	}
	return im.EntryModule(), im.Modules, nil
}

type errorString string

func (e errorString) Error() string { return string(e) }

// helloIR lowers hello world and answers its result.
func helloIR(t *testing.T) *irbuild.Result {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hello.nomi")
	if err := os.WriteFile(path, []byte("import std/io\n\nfn main() {\n  io.print(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, err := irbuild.Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestIRImage_TheStdlibRoundTrips holds the whole cached stdlib to the
// format, and reports the sizes and decode times the plan's stopping rule
// reads.
func TestIRImage_TheStdlibRoundTrips(t *testing.T) {
	res := helloIR(t)
	linked := res.IRModules()
	stdlib := linked[len(res.IR):]
	if len(stdlib) == 0 {
		t.Fatal("hello world links no stdlib module, so this checks nothing")
	}

	stdData, err := ir.EncodeModules(stdlib)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ir.DecodeModules(stdData)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range decoded {
		if err := ir.LintModule(m); err != nil {
			t.Fatalf("decoded %s does not lint: %v", m.Name(), err)
		}
	}
	again, err := ir.EncodeModules(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdData, again) {
		t.Fatal("the decoded stdlib encodes to different bytes")
	}
	// DETERMINISM, over the population with the most map-shaped state.
	for i := 0; i < 3; i++ {
		b, err := ir.EncodeModules(stdlib)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stdData, b) {
			t.Fatal("encoding the stdlib twice gave different bytes")
		}
	}

	idx := -1
	for i, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				idx = i
			}
		}
	}
	if idx < 0 {
		t.Fatal("hello world retained no main")
	}
	helloData, err := ir.EncodeImage(ir.Image{Modules: linked, Entry: idx})
	if err != nil {
		t.Fatal(err)
	}
	// THE MINIMUM OF SEVERAL DECODES, because the minimum is the reading
	// load does not inflate.
	best := time.Duration(1 << 62)
	for i := 0; i < 10; i++ {
		t0 := time.Now()
		if _, err := ir.DecodeImage(helloData); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(t0); d < best {
			best = d
		}
	}
	funcs := 0
	for _, m := range stdlib {
		funcs += len(m.Funcs())
	}
	t.Logf("stdlib: %d modules, %d funcs, %d bytes encoded", len(stdlib), funcs, len(stdData))
	t.Logf("hello world linked set: %d modules, %d bytes, decode %v (min of 10)",
		len(linked), len(helloData), best)

	// And the decoded hello world runs.
	im, err := ir.DecodeImage(helloData)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	booted, err := vm.NewProgram(im.EntryModule(), im.Modules, &out).Boot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := booted.Run("main"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello\n" {
		t.Fatalf("decoded hello world printed %q", out.String())
	}
}

// TestIRImage_StdlibTestIRRoundTrips holds every stdlib module's attached
// test lowering (irbuild.GenerateStdlibTestIR, `nomi test <std file>`) to the
// format, and runs each decoded module's cases against its undecoded run.
func TestIRImage_StdlibTestIRRoundTrips(t *testing.T) {
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "std", "*.nomi"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no stdlib sources found: %v", err)
	}
	sort.Strings(files)
	roundTripped, compared, unstable := 0, 0, []string{}
	for _, path := range files {
		module := irbuild.StdlibTestModule(path)
		if module == "" {
			continue
		}
		prog, res, err := irbuild.GenerateStdlibTestIR(module, nil, nil)
		if err != nil {
			continue
		}
		var entry *ir.Module
		for _, m := range res.IR {
			if m.Name() == prog.Entry().Path {
				entry = m
			}
		}
		if entry == nil {
			continue
		}
		dEntry, dMods, err := imageRoundTrip(entry, res.IRModules(), len(res.IR))
		if err != nil {
			t.Errorf("std/%s: %v", module, err)
			continue
		}
		roundTripped++
		if len(entry.Tests()) == 0 {
			continue
		}
		run := func(e *ir.Module, mods []*ir.Module) string {
			var out bytes.Buffer
			m := vm.NewProgram(e, mods, &out)
			stderr, exit, reason := vmReportCaptureStderr(func() (int, string) { return m.RunTests(path) })
			return out.String() + "\n--- stderr ---\n" + stderr + "\n--- exit ---\n" +
				strconv.Itoa(exit) + " " + reason
		}
		first := run(entry, res.IRModules())
		if second := run(entry, res.IRModules()); first != second {
			unstable = append(unstable, module)
			continue
		}
		if got := run(dEntry, dMods); got != first {
			t.Errorf("std/%s: the decoded tests report differently:\n%s",
				module, expectation.LineDiff(first, got))
			continue
		}
		compared++
	}
	t.Logf("%d stdlib test units round-tripped, %d run and compared, %d skipped as unstable run to run: %s",
		roundTripped, compared, len(unstable), strings.Join(unstable, " "))
	if roundTripped == 0 || compared == 0 {
		t.Fatal("no stdlib test unit was round-tripped and run, so this checked nothing")
	}
}

// TestIRImage_DecodedProgramsMatchTheCommittedRecords runs the whole-program
// and report harnesses again with every program's linked set round-tripped
// through the format, and holds them to the same records and the same pinned
// counts as the undecoded runs.
func TestIRImage_DecodedProgramsMatchTheCommittedRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers every candidate program in four populations; -short")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	saved := vmLink
	defer func() { vmLink = saved }()
	var linkErrs []string
	vmLink = func(entry *ir.Module, mods []*ir.Module) (*ir.Module, []*ir.Module, error) {
		// The program's own units come first in its linked set: lint them
		// through the entry. The stdlib is linted once, by
		// TestIRImage_TheStdlibRoundTrips.
		own := 0
		for i, m := range mods {
			if m == entry {
				own = i + 1
			}
		}
		e, ms, err := imageRoundTrip(entry, mods, own)
		if err != nil {
			linkErrs = append(linkErrs, entry.Name()+": "+err.Error())
		}
		return e, ms, err
	}

	for _, population := range []string{"corpus", "tour"} {
		recorded, err := expectation.Load(population)
		if err != nil {
			t.Fatal(err)
		}
		ids, _, _ := candidateIDs(recorded)
		got, _ := vmSubsetOf(t, population, ids, vmPathResolver(t, population))
		if diffs := recorded.CompareSubset(got); len(diffs) != 0 {
			t.Errorf("%s: decoded programs disagree with the records:\n%s", population, strings.Join(diffs, "\n"))
		}
		if want := vmPopulations[population].comparable; len(got.Cases) != want {
			t.Errorf("%s: %d decoded programs compared, the undecoded harness pins %d", population, len(got.Cases), want)
		}
	}
	for _, population := range []string{"corpus", "failure", "tour"} {
		set, err := expectation.Load(population)
		if err != nil {
			t.Fatal(err)
		}
		ids, recorded := reportShapedIDs(set)
		got, _ := vmReportSubsetOf(t, population, ids, recorded, vmReportPathResolver(t, population))
		if diffs := set.CompareSubset(got); len(diffs) != 0 {
			t.Errorf("%s: decoded test files disagree with the records:\n%s", population, strings.Join(diffs, "\n"))
		}
		if want := vmReportPopulations[population].comparable; len(got.Cases) != want {
			t.Errorf("%s: %d decoded test files compared, the undecoded harness pins %d", population, len(got.Cases), want)
		}
	}
	if len(linkErrs) > 0 {
		t.Errorf("%d round trips failed:\n%s", len(linkErrs), strings.Join(linkErrs, "\n"))
	}
}

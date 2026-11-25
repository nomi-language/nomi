package vm_test

// Complete programs, run bare (vm.NewProgram, Boot, Run), are compared with
// the committed transcripts. irimage_test.go drives this harness with every
// program's linked set round-tripped through the image format, or pruned as
// `nomi build` prunes it. Empty transcripts and test reports are excluded
// here; testreport_test.go owns reports. Which records must compare is
// decided below (vmRequiredComparable). internal/expectation compares every
// record whole, through the command path.

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/internal/tourpackages"
	"github.com/nomi-language/nomi/internal/virtualproject"
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
	"-run 'TestIRImage_DecodedProgramsMatchTheCommittedRecords' -count=1"

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

// vmWholeProgramRoots are the populations whose record ids are paths, with the
// directory they are relative to. stdlib and failure are absent because they
// have no candidates at all; tour is absent because its ids are not paths.
var vmWholeProgramRoots = map[string]string{
	"corpus": filepath.Join("..", "..", "tests"),
}

// candidateIDs is the records the VM could in principle produce a whole
// transcript for, with the three exclusions counted.
//
// THE FILTERS ARE PROPERTIES OF THE RECORD AND NOT OF ANY RUN, which is what
// keeps this from being a comparison that selects the cases it passes. `N`,
// the exit status and the digest are read out of the committed artifact
// before the VM is opened.
//
// A record with a nonzero exit is a program that stops on purpose (a tour
// block that reaches a `todo` or returns `Err` from main). vmRun runs `main`
// bare: it has no vmrunner to print an `Err` from main, and it reads a Run
// error as a partial run, so it cannot produce that record's transcript.
func candidateIDs(s *expectation.Set) (ids []string, reportShaped, empty, stops int) {
	for _, c := range s.Cases {
		switch {
		case c.Cases > 0:
			reportShaped++
		case c.Exit != 0:
			stops++
		case c.Digest == emptyTranscriptDigest:
			empty++
		default:
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids, reportShaped, empty, stops
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
	var out, errOut bytes.Buffer
	machine, _, reason := vmOpenProgram(path, &out, &errOut)
	if reason != "" {
		return "", reason
	}
	booted, err := machine.Boot()
	if err != nil {
		return "", "the VM could not boot it: " + err.Error()
	}
	result, err := booted.Run("main")
	if err != nil {
		return "", "the VM ran part of it: " + err.Error()
	}
	if r, isRecord := result.(*rt.Record); isRecord && r.Desc.Name == "results.Result" && r.Desc.Variants[r.Tag].Name == "Err" {
		// What main's `Err` prints (an Exit's message, an error's text) is
		// a runner's to print, and this runs main bare.
		return "", "main returned Err, which only a runner reports"
	}
	return expectation.Transcript(out.String(), errOut.String()), ""
}

// vmOpenProgram lowers path and opens the machine its main runs on, over the
// module set vmLink hands it, writing to out and errOut. It answers the
// machine and the linked entry module, or the reason the record is not
// comparable.
func vmOpenProgram(path string, out, errOut io.Writer) (*vm.Machine, *ir.Module, string) {
	prog, err := irbuild.AnalyzeFile(path, frontend.Config{})
	if err != nil {
		return nil, nil, "the front end refuses this program, so the record is a diagnostic"
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		return nil, nil, "Generate refuses this program"
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
		return nil, nil, fmt.Sprintf("the entry is not retained (%d function(s) retained for this program)",
			retained)
	}
	// THE WHOLE PROGRAM'S MODULE SET, not just the entry's module. A Nomi
	// program with sibling files lowers to one `ir.Module` per unit, and a
	// callee may sit in the module next door (two modules-and-imports.md
	// blocks). Running the entry's module alone would report them as a
	// retention boundary when nothing is missing but the link.
	mod, mods, err := vmLink(mod, res.IRModules())
	if err != nil {
		return nil, nil, "the link hook refused it: " + err.Error()
	}
	machine := vm.NewProgram(mod, mods, out).WithLocaleData(programLocales(prog)).WithErrorOutput(errOut)
	if files := tourFiles(path); files != nil {
		machine = machine.WithFiles(files)
	}
	return machine, mod, ""
}

// programLocales is the CLDR data prog runs with, as vmhost gives it: the
// locales its nomi.toml lists under [i18n] (`locales` and `partial_locales`), or every locale.
func programLocales(prog *irbuild.Program) *cldrdata.Data {
	var locales []string
	source := ""
	if prog.Root != "" {
		if m, err := analysis.LoadManifest(prog.Root); err == nil && m != nil {
			locales, source = m.CarriedLocales(), m.Source()
		}
	}
	return cldrfull.ForProgram(locales, source)
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

// recordIDs is a Set's ids, for logging.
func recordIDs(s *expectation.Set) []string {
	out := make([]string, 0, len(s.Cases))
	for _, c := range s.Cases {
		out = append(out, c.ID)
	}
	return out
}

// vmPathResolver answers, for a record id, the entry `.nomi` file to lower.
//
// corpus is a join against the population root, because a record
// id IS the population-relative path. The tour is not: a block's id is
// `chapter:L<line>` and the code lives in the markdown, so the blocks are
// extracted with `internal/doctest`'s extractor, the recorder's, and staged
// to a temp dir as the tour's runner writes them (stageTourBlock).
//
// The recorder loads a block through vmhost.LoadMarked. This harness runs
// the machine bare, so it stages the block itself, and the committed records
// hold the staging to the recorder's: a block staged differently runs to a
// different transcript and fails. The files whose cases a block's report
// holds after its entry's are tourTestFiles'.
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
			if !b.Runs() {
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
// The block's other files that declare tests are recorded by that path
// (tourTestFiles).
func stageTourBlock(t *testing.T, dir, code string) string {
	t.Helper()
	// A block that imports a module the tour links gets its files, as the
	// tour's runner attaches them (tourpackages.Source).
	code = tourpackages.Source(code)
	project, projectErr := virtualproject.FromMarkedSource(code)
	// A block that names modules is its files already, as the tour's
	// runner writes them (vmhost.LoadMarked).
	if projectErr == nil && project.Files != nil {
		root, err := project.Write(dir)
		if err != nil {
			t.Fatalf("staging a tour block: %v", err)
		}
		entry := filepath.Join(root, filepath.FromSlash(project.EntryName))
		storeTourBlock(t, entry, root, tourData{dir: project.Dir, files: project.DataFiles}, project.TestFiles)
		return entry
	}
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
	// parsed *analysis.Manifest: a round trip through a TOML writer would be a
	// second encoding of one fact.
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
	// The block's data files are the in-memory file system it runs
	// against, as the tour's runner gives it (vmRun).
	data, err := vmhost.DataFiles(code)
	if err != nil {
		t.Fatalf("a tour block's data files: %v", err)
	}
	// The front end reads the data files a block embeds from beside its
	// entry, as the tour's runner hands them to it (vmhost.WithVirtualData);
	// the run still reads the in-memory copies.
	for name, text := range data {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("staging %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatalf("staging %s: %v", name, err)
		}
	}
	var tests []string
	if projectErr == nil {
		tests = project.TestFiles
	}
	storeTourBlock(t, entry, dir, tourData{files: data}, tests)
	return entry
}

// storeTourBlock records a staged block, written under root with its entry
// at entry: its data files under the entry's path and under the path of each
// of its other files that declare tests (virtualproject.Project.TestFiles),
// whose paths it records under the entry's (tourTestFiles).
func storeTourBlock(t *testing.T, entry, root string, data tourData, testFiles []string) {
	t.Helper()
	for _, name := range testFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("staging a tour block's test file %s: %v", name, err)
		}
		data.tests = append(data.tests, path)
		tourDataFiles.Store(path, tourData{dir: data.dir, files: data.files})
	}
	tourDataFiles.Store(entry, data)
}

// tourDataFiles is each staged tour block's data files, by its entry path
// and the path of each of its other test files.
var tourDataFiles sync.Map // string -> tourData

// tourData is a staged tour block's data files, by their paths from the
// block's root, and its project's directory from there: "" unless the
// block holds sibling projects (virtualproject.Project.Dir). Under the
// entry's path, tests is the staged path of each other file of the block
// that declares tests, in block order.
type tourData struct {
	dir   string
	files map[string]string
	tests []string
}

// tourTestFiles is the staged path of each file of the tour block entered
// at entry, other than the entry, that declares tests, in block order: the
// files whose cases the tour runs after the entry's (vmhost.LoadMarked).
// It is nil for a path that is no staged block's entry.
func tourTestFiles(entry string) []string {
	data, ok := tourDataFiles.Load(entry)
	if !ok {
		return nil
	}
	return data.(tourData).tests
}

// tourFiles is the in-memory file system a staged tour block runs against,
// or nil for a program that is not one.
func tourFiles(path string) any {
	data, ok := tourDataFiles.Load(path)
	if !ok {
		return nil
	}
	// The run's relative paths start at the project, as LoadMarked's do
	// (vmhost.BlockFiles).
	block := data.(tourData)
	m := stdio.NewMemory(strings.TrimSuffix("/work/"+block.dir, "/"))
	for name, text := range block.files {
		m.Put("/work/"+name, []byte(text))
	}
	return m
}

// tourFileName is an import-path key as a file name, idempotent over a name
// that already carries the suffix; a phrase catalog's key is its path,
// `.toml` included.
func tourFileName(name string) string {
	if strings.HasSuffix(name, ".nomi") || strings.HasSuffix(name, ".toml") {
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

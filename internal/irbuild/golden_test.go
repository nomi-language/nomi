package irbuild

// THE GOLDEN SIDE OF THIS PACKAGE'S COMPARISONS.
//
// The corpus sweep and the FFI build gate compare a run
// against the committed golden files in testdata/expectations/
// (internal/expectation), not against a second live run.
//
// Every other comparison in this package — the three-way helpers and the
// direct oracle sites — reads the `irbuild` population below through
// goldenFor. vmReference in differential_test.go runs a program on the
// VM as its command would; it records that population and is the subject of
// tests that check a program's output against a literal.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
)

var (
	goldenMu   sync.Mutex
	goldenSets = map[string]*expectation.Set{}
)

// goldenSet loads one committed population, once per package run.
func goldenSet(t *testing.T, population string) *expectation.Set {
	t.Helper()
	goldenMu.Lock()
	defer goldenMu.Unlock()
	if s, ok := goldenSets[population]; ok {
		return s
	}
	s, err := expectation.Load(population)
	if err != nil {
		t.Fatalf("loading the golden file for %s: %v", population, err)
	}
	goldenSets[population] = s
	return s
}

// goldenRecord is one record of a committed population.
func goldenRecord(t *testing.T, population, id string) (expectation.Case, bool) {
	t.Helper()
	return goldenSet(t, population).Lookup(id)
}

func goldenMismatchText(want expectation.Case, text string, exit int) string {
	switch {
	case text != want.Transcript:
		return fmt.Sprintf("output differs from the golden record (exit %d, golden %d)\n%s",
			exit, want.Exit, expectation.LineDiff(want.Transcript, text))
	case exit != want.Exit:
		return fmt.Sprintf("exit status %d, golden %d", exit, want.Exit)
	}
	return ""
}

// THE IRBUILD POPULATION: golden outputs for every program this package's tests
// compare the VM against, whether it is a fixture in the
// repository or a program a test writes to a temp directory.
//
// A record is keyed by the PROGRAM, not by the test: the entry file plus every
// user module the front end loads for it, plus each module directory's
// nomi.toml, go.mod, go.sum and Go files. The key is
//
//	<entry>@<sha256(key)[:16]>
//
// where the archive is those files, relative to their common directory, with
// the worktree root and that directory masked, and the key is the archive with
// each .nomi file replaced by its tokens (goldenTokenKey). Reformatting a
// fixture keeps its key; a record whose output names a source line still
// moves when that line does. <entry> is the entry's path
// from the worktree root for a repository program, and its path within the
// program's directory for a temp one. A temp program's record stores the
// archive as its source, so it can be re-run; a repository program's record
// stores none, because the files are in the repository.
//
// A changed program is a new key with no record, and the test fails naming the
// regeneration command. Regenerating upserts the records the run touched, and
// drops a repository record whose files have since changed. Temp records no
// test asks for any more are harmless. Do not delete irbuild.expect to prune
// them: TestIRGolden_RecordsMatchTheVM re-records every existing record, and
// most repository fixtures have no other test that records them, so a run
// from an empty file keeps only the programs a test compares directly, a small
// fraction of the population. Remove a stale record by hand instead.
const irbuildGoldenPopulation = "irbuild"

// irbuildGoldenCommand is the recording run for the irbuild population.
const irbuildGoldenCommand = "NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -count=1 -timeout 0"

var (
	irbuildGoldenMu      sync.Mutex
	irbuildGoldenPending = map[string]expectation.Case{}
)

// goldenProgram is one program's identity in the irbuild population.
type goldenProgram struct {
	id      string
	archive string
	// dir is the program's directory: the common ancestor of its files.
	dir string
	// entry is the entry file, absolute.
	entry string
	// inRepo reports that the program lives in the repository, so its record
	// stores no source.
	inRepo bool
}

// goldenProgramFiles is the files that make up the program at entry: every
// user module the front end loads, and the build files beside each. A program
// the front end rejects is its entry file alone.
func goldenProgramFiles(entry string) []string {
	files := map[string]bool{entry: true}
	dirs := map[string]bool{filepath.Dir(entry): true}
	if prog, err := Analyze(entry); err == nil {
		for _, m := range prog.Modules {
			if m.Path != "" {
				files[m.Path] = true
				dirs[filepath.Dir(m.Path)] = true
			}
		}
	}
	for dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if e.IsDir() {
				continue
			}
			if name == "nomi.toml" || name == "go.mod" || name == "go.sum" ||
				(strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")) {
				files[filepath.Join(dir, name)] = true
			}
		}
	}
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// commonDir is the deepest directory containing every path.
func commonDir(paths []string) string {
	dir := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		for {
			rel, err := filepath.Rel(dir, p)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return dir
			}
			dir = parent
		}
	}
	return dir
}

// goldenProgramOf identifies the program at entry.
func goldenProgramOf(t *testing.T, entry string) goldenProgram {
	t.Helper()
	entry, err := filepath.Abs(entry)
	if err != nil {
		t.Fatal(err)
	}
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	files := goldenProgramFiles(entry)
	g := goldenProgram{dir: commonDir(files), entry: entry}
	var b, key strings.Builder
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		rel, _ := filepath.Rel(g.dir, f)
		content := g.normalize(string(data), root)
		fmt.Fprintf(&b, "-- %s %d --\n%s\n", filepath.ToSlash(rel), len(content), content)
		if strings.HasSuffix(f, ".nomi") {
			content = goldenTokenKey(content)
		}
		fmt.Fprintf(&key, "-- %s %d --\n%s\n", filepath.ToSlash(rel), len(content), content)
	}
	g.archive = b.String()
	// NOMI_COLOR is part of the key, so one program recorded under two
	// settings is two records rather than one the second setting overwrote.
	// TestMain pins it to never. NOMI_ENV is not: a VM test run sets it for
	// its length, so a parallel test reading it here would see a value that
	// depends on scheduling.
	env := "NOMI_COLOR=" + os.Getenv("NOMI_COLOR") + "\n"
	hash := expectation.Digest(env + key.String())[:16]
	if rel, err := filepath.Rel(root, entry); err == nil && !strings.HasPrefix(rel, "..") {
		g.inRepo = true
		g.id = filepath.ToSlash(rel) + "@" + hash
	} else {
		rel, _ := filepath.Rel(g.dir, entry)
		g.id = filepath.ToSlash(rel) + "@" + hash
	}
	return g
}

// goldenTokenKey is a .nomi file as the key sees it: its tokens' type names,
// lexemes and tags, one per line, without positions or blank lines. A layout
// change from the formatter leaves it alone; any change to what the file says
// does not. Type names rather than numbers, so adding or removing a token
// kind moves no key.
func goldenTokenKey(source string) string {
	var b strings.Builder
	for _, tok := range lexer.Lex(source) {
		if tok.Type == token.BLANK_LINE {
			continue
		}
		fmt.Fprintf(&b, "%s %q %q\n", tok.Type, tok.Lexeme, tok.Tag)
	}
	return b.String()
}

// displayDir is how rt.DisplayPath spells the program's directory from this
// process: relative when it is under the working directory, absolute
// otherwise.
func (g goldenProgram) displayDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return g.dir
	}
	rel, err := filepath.Rel(wd, g.dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return g.dir
	}
	return filepath.ToSlash(rel)
}

// normalize masks the program's directory, in both the absolute and the
// DisplayPath spelling, then the worktree root and the jitter.
func (g goldenProgram) normalize(text, root string) string {
	text = expectation.NormalizeDir(text, g.dir)
	if d := g.displayDir(); d != g.dir && d != "." {
		text = strings.ReplaceAll(text, d+"/", "<dir>/")
	}
	return expectation.Normalize(text, root)
}

// denormalize puts this run's paths back, spelling the directory as
// DisplayPath would. The jitter mask stays.
func (g goldenProgram) denormalize(text, root string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "<dir>", g.displayDir()), "<root>", root)
}

// goldenFor is the golden record for the program at path, and that record as
// an observation with this run's paths put back. Under
// NOMI_REGENERATE_EXPECTATIONS=1 it runs the VM and records the answer
// instead. A program with no record fails the test.
func goldenFor(t *testing.T, path string) (goldenProgram, expectation.Case, observation) {
	t.Helper()
	g := goldenProgramOf(t, path)
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	if expectation.RegenerateRequested() {
		obs := vmReference(path)
		c := expectation.NewCase(g.id, obs.exit, 0,
			g.normalize(expectation.Transcript(obs.stdout, obs.stderr), root))
		if !g.inRepo {
			c.Source = g.archive
		}
		if reason, gap := expectation.VMGap(c.Transcript); gap {
			// A BLOCKED transcript is not an answer, so it is never recorded.
			t.Fatalf("%s: the VM cannot run this program (%s), so there is no answer to "+
				"write down", g.id, reason)
		}
		irbuildGoldenMu.Lock()
		irbuildGoldenPending[g.id] = c
		irbuildGoldenMu.Unlock()
		return g, c, obs
	}
	want, ok := goldenRecord(t, irbuildGoldenPopulation, g.id)
	if !ok {
		t.Fatalf("no golden record %s in irbuild.expect, so there is no written-down answer "+
			"for this program. A new or changed program needs one: run\n"+
			"  NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -run '^%s$' -count=1\n"+
			"and name the reason in the commit message.", g.id, strings.Split(t.Name(), "/")[0])
	}
	if !g.inRepo && want.Source != g.archive {
		// The key ignores layout, so a re-laid-out temp program finds the
		// record of its older text. That record's transcript can name lines
		// the new text no longer has, and TestIRGolden_RecordsMatchTheVM
		// re-runs the stored text, so nothing else would notice.
		t.Fatalf("golden record %s stores a different text of this program (a layout "+
			"change keeps the key). Refresh it: run\n"+
			"  NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild -run '^%s$' -count=1",
			g.id, strings.Split(t.Name(), "/")[0])
	}
	stdout, stderr := expectation.SplitTranscript(g.denormalize(want.Transcript, root))
	return g, want, observation{stdout: stdout, stderr: stderr, exit: want.Exit}
}

// goldenReference is vmReference(path) answered from the golden file: the
// recorded observation, with this run's paths put back so a caller can compare
// it with a live run.
func goldenReference(t *testing.T, path string) observation {
	t.Helper()
	_, _, obs := goldenFor(t, path)
	return obs
}

// flushIRBuildGolden writes the records a regeneration run recorded, merged over
// the committed ones. Called from TestMain after m.Run, which is the only place
// the set is final. Answers a message on failure.
func flushIRBuildGolden() string {
	irbuildGoldenMu.Lock()
	defer irbuildGoldenMu.Unlock()
	if !expectation.RegenerateRequested() || len(irbuildGoldenPending) == 0 {
		return ""
	}
	set, err := expectation.Load(irbuildGoldenPopulation)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Sprintf("loading irbuild.expect to merge into it: %v", err)
		}
		set = &expectation.Set{}
	}
	set.Population = irbuildGoldenPopulation
	set.What = "one record per program an internal/irbuild test compares the VM " +
		"against, keyed <entry>@<sha256(its files, .nomi files as tokens)[:16]>, run as the command that " +
		"owns it would run it. <dir> is the program's directory. A temp program carries its " +
		"files as source; a repository program's entry is its path from the worktree root."
	root, _ := expectation.Root()
	merged := map[string]expectation.Case{}
	stalePrefix := map[string]bool{}
	for id := range irbuildGoldenPending {
		entry, _, _ := strings.Cut(id, "@")
		stalePrefix[entry] = true
	}
	for _, c := range set.Cases {
		entry, _, _ := strings.Cut(c.ID, "@")
		// A repository program re-recorded this run replaces its older
		// records: the files moved on, so the old key can never be asked for.
		if c.Source == "" && stalePrefix[entry] && root != "" {
			continue
		}
		merged[c.ID] = c
	}
	for id, c := range irbuildGoldenPending {
		merged[id] = c
	}
	set.Cases = set.Cases[:0]
	for _, c := range merged {
		set.Add(c)
	}
	set.Sort()
	if err := expectation.Store(set); err != nil {
		return fmt.Sprintf("writing irbuild.expect: %v", err)
	}
	fmt.Fprintf(os.Stderr, "irbuild: recorded %d golden record(s); irbuild.expect now holds %d\n",
		len(irbuildGoldenPending), len(set.Cases))
	return ""
}

// unpackGoldenArchive writes an archive's files under dir, putting `<dir>` and
// `<root>` back, and answers the paths it wrote by their archive name.
func unpackGoldenArchive(archive, dir, root string) (map[string]string, error) {
	out := map[string]string{}
	rest := archive
	for rest != "" {
		header, after, ok := strings.Cut(rest, "\n")
		if !ok || !strings.HasPrefix(header, "-- ") || !strings.HasSuffix(header, " --") {
			return nil, fmt.Errorf("malformed archive header %q", header)
		}
		fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(header, "-- "), " --"))
		if len(fields) != 2 {
			return nil, fmt.Errorf("malformed archive header %q", header)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil || n+1 > len(after) {
			return nil, fmt.Errorf("archive header %q: bad length", header)
		}
		content := after[:n]
		rest = after[n+1:]
		content = strings.ReplaceAll(strings.ReplaceAll(content, "<dir>", dir), "<root>", root)
		path := filepath.Join(dir, filepath.FromSlash(fields[0]))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, err
		}
		out[fields[0]] = path
	}
	return out, nil
}

// TestIRGolden_RecordsMatchTheVM runs every irbuild record's program on the VM
// as its command would (vmReference) and requires the result to be the
// record. A temp program is unpacked from its stored archive into a fresh
// directory; a repository program runs in place, after checking its files
// still hash to the record's key.
//
// Under NOMI_REGENERATE_EXPECTATIONS=1 it is the whole population's writer:
// each record is re-recorded from the VM (a repository program whose files
// changed, under its new key). A program the VM cannot run fails either way:
// a BLOCKED transcript is never recorded as an answer.
func TestIRGolden_RecordsMatchTheVM(t *testing.T) {
	regenerate := expectation.RegenerateRequested()
	set := goldenSet(t, irbuildGoldenPopulation)
	if len(set.Cases) == 0 {
		t.Fatal("irbuild.expect holds no records, so this package's comparisons have no answers")
	}
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	inRepo, temp := 0, 0
	for _, c := range set.Cases {
		entryName, _, _ := strings.Cut(c.ID, "@")
		var path string
		if c.Source == "" {
			inRepo++
			path = filepath.Join(root, filepath.FromSlash(entryName))
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s: its program is gone from the repository; regenerate with\n  %s",
					c.ID, irbuildGoldenCommand)
				continue
			}
		} else {
			temp++
			files, err := unpackGoldenArchive(c.Source, t.TempDir(), root)
			if err != nil {
				t.Errorf("%s: %v", c.ID, err)
				continue
			}
			path = files[entryName]
			if path == "" {
				t.Errorf("%s: the stored archive has no entry %s", c.ID, entryName)
				continue
			}
		}
		g := goldenProgramOf(t, path)
		if g.id != c.ID && regenerate && c.Source == "" {
			// A repository program whose files moved on is re-recorded
			// under its new key; the flush drops the old one.
			c.ID = g.id
		}
		if g.id != c.ID {
			t.Errorf("%s: its files now hash to %s, so the program changed without its golden "+
				"record; regenerate with\n  %s", c.ID, g.id, irbuildGoldenCommand)
			continue
		}
		obs := vmReference(path)
		text := g.normalize(expectation.Transcript(obs.stdout, obs.stderr), root)
		verdict, detail := expectation.JudgeVM(c, text, obs.exit)
		if verdict == expectation.Gap {
			// Checking or regenerating, a program the VM cannot run fails: a
			// BLOCKED transcript is never recorded as an answer.
			t.Errorf("%s: the VM cannot run it (%s)", c.ID, detail)
			continue
		}
		if regenerate {
			rec := c
			if verdict == expectation.Wrong {
				if _, gap := expectation.VMGap(text); gap {
					t.Errorf("%s: the VM runs part of this program to a different answer than "+
						"its record: %s", c.ID, detail)
					continue
				}
				rec = expectation.NewCase(c.ID, obs.exit, c.Cases, text)
				rec.Source = c.Source
				t.Logf("%s: re-recorded from the VM: %s", c.ID, detail)
			}
			irbuildGoldenMu.Lock()
			irbuildGoldenPending[c.ID] = rec
			irbuildGoldenMu.Unlock()
			continue
		}
		if verdict == expectation.Wrong {
			t.Errorf("%s: the VM runs it to a different answer than its golden record, which is "+
				"a VM bug to fix: %s", c.ID, detail)
		}
	}
	t.Logf("%d irbuild golden record(s) checked on the VM: %d repository programs, "+
		"%d temp programs", len(set.Cases), inRepo, temp)
}

// corpusGoldenRoot is the worktree root, for putting `<root>` back into a
// corpus record.
func corpusGoldenRoot(t *testing.T) string {
	t.Helper()
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// A golden key survives a layout change and moves with a change to the code.
func TestGoldenTokenKey_IgnoresLayoutOnly(t *testing.T) {
	base := "fn main() {\n    x = 1\n    case x {\n        1 -> io.print(\"one\")\n        _ -> io.print(\"other\")\n    }\n}\n"
	relaid := "fn main() {\n  x = 1\n\n  case x {\n    1 ->\n      io.print(\"one\")\n\n    _ ->\n      io.print(\"other\")\n  }\n}\n"
	changed := strings.Replace(base, "\"one\"", "\"uno\"", 1)
	if goldenTokenKey(base) != goldenTokenKey(relaid) {
		t.Errorf("a layout-only change moved the key")
	}
	if goldenTokenKey(base) == goldenTokenKey(changed) {
		t.Errorf("changing a string literal left the key alone")
	}
}

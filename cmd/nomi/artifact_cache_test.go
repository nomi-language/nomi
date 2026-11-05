package main

import (
	"bytes"
	"flag"
	"fmt"
	"hash/fnv"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/internal/rotation"
)

// The artifact cache's harness (internal/progcache, vmhost.Cache): a
// command that reads a kept program must print exactly what it prints when
// it checks and lowers the files afresh, after any edit to them.

// cacheProject is a project the harness edits on disk: a generated one,
// whose edit kinds it applies, or a multi-file corpus directory, copied
// whole.
type cacheProject struct {
	name string
	// files holds every file's text by its slash-separated path from the
	// project's root, nomi.toml and catalogs included.
	files map[string]string
	// entry is the path of the file the commands are given.
	entry string
	// gen is the generated project, or nil for a corpus directory.
	gen *projectgen.Project
}

// nomiFiles are p's .nomi files' paths, sorted.
func (p cacheProject) nomiFiles() []string {
	var out []string
	for rel := range p.files {
		if strings.HasSuffix(rel, ".nomi") {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// cacheProjects are the projects the harness edits, in a fixed order: a
// generated project of about 600 lines, then every multi-file corpus
// directory with no Go bindings and an entry (main.nomi, else its first
// test file), as the incremental fuzzer picks them.
var cacheProjects = sync.OnceValues(func() ([]cacheProject, error) {
	gen := projectgen.Generate(projectgen.Config{Seed: 7, Lines: 600})
	p := cacheProject{name: "generated", files: map[string]string{"nomi.toml": projectgen.Manifest}, entry: gen.Entry + ".nomi", gen: gen}
	for key, text := range gen.Files {
		p.files[key+".nomi"] = text
	}
	out := []cacheProject{p}
	dirs, err := filepath.Glob(filepath.Join("tests", "*", "*"))
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			continue
		}
		c := cacheProject{name: filepath.ToSlash(dir), files: map[string]string{}}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			c.files[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			return nil, err
		}
		var tests []string
		for _, rel := range c.nomiFiles() {
			if rel == "main.nomi" {
				c.entry = rel
			} else if strings.HasSuffix(rel, "_test.nomi") {
				tests = append(tests, rel)
			}
		}
		if c.entry == "" && len(tests) > 0 {
			c.entry = tests[0]
		}
		if len(c.nomiFiles()) > 1 && c.entry != "" {
			out = append(out, c)
		}
	}
	return out, nil
})

// cacheEdit is one step of an edit sequence: file rel's text becomes text.
type cacheEdit struct {
	what, rel, text string
	// others are more files the edit writes, by path; "" removes one.
	others map[string]string
}

// fuzzCacheEdits are the incremental fuzzer's edits of rel: its text
// becomes src, a stretch of src is deleted and a character typed there, a
// block of src's lines is pasted at a line start, and the original text
// comes back. seed places them.
func fuzzCacheEdits(p cacheProject, rel, src string, seed int64) []cacheEdit {
	rng := rand.New(rand.NewSource(seed))
	at := func() int {
		off := rng.Intn(len(src) + 1)
		for off > 0 && off < len(src) && !utf8.RuneStart(src[off]) {
			off--
		}
		return off
	}
	edits := []cacheEdit{{what: "text replaced", rel: rel, text: src}}
	if len(src) > 0 {
		from, to := at(), at()
		if from > to {
			from, to = to, from
		}
		lines := strings.SplitAfter(src, "\n")
		first := rng.Intn(len(lines))
		block := strings.Join(lines[first:min(first+1+rng.Intn(8), len(lines))], "")
		at := at()
		for at > 0 && src[at-1] != '\n' {
			at--
		}
		edits = append(edits,
			cacheEdit{what: "stretch deleted", rel: rel, text: src[:from] + src[to:]},
			cacheEdit{what: "character typed", rel: rel, text: src[:from] + "." + src[to:]},
			cacheEdit{what: "block pasted", rel: rel, text: src[:at] + block + src[at:]},
		)
	}
	return append(edits, cacheEdit{what: "original back", rel: rel, text: p.files[rel]})
}

// genCacheEdits are projectgen's edit kinds, each made once and then
// undone, over p's generated project, and a file added that the leaf
// imports, then taken away.
func genCacheEdits(p cacheProject) []cacheEdit {
	var edits []cacheEdit
	for _, kind := range projectgen.EditKinds {
		key, text := p.gen.Edit(kind, 0)
		rel := key + ".nomi"
		edits = append(edits, cacheEdit{what: string(kind), rel: rel, text: text}, cacheEdit{what: string(kind) + ", undone", rel: rel, text: p.files[rel]})
	}
	return append(edits, fileAddedEdits(p, 0)...)
}

// fileAdded names the edit fileAddedEdits makes.
const fileAdded = "file added"

// fileAddedEdits add a file the leaf imports and calls (projectgen's
// FileAdded) and then take it away again.
func fileAddedEdits(p cacheProject, n int) []cacheEdit {
	texts := p.gen.FileAdded(n)
	leaf := p.gen.Leaf()
	add, undo := cacheEdit{what: fileAdded, rel: leaf + ".nomi", text: texts[leaf], others: map[string]string{}},
		cacheEdit{what: fileAdded + ", undone", rel: leaf + ".nomi", text: p.files[leaf+".nomi"], others: map[string]string{}}
	for key, text := range texts {
		if key != leaf {
			add.others[key+".nomi"], undo.others[key+".nomi"] = text, ""
		}
	}
	return []cacheEdit{add, undo}
}

// cacheRun is one command's observable output, with the cache's own report
// (NOMI_DEBUG_ARTIFACT_CACHE) taken out of stderr.
type cacheRun struct {
	stdout, stderr string
	exit           int
	// report is the cache's report lines.
	report []string
}

func (r cacheRun) String() string {
	return fmt.Sprintf("exit %d\nstdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
}

// read reports whether the command read a kept program.
func (r cacheRun) read() bool {
	for _, line := range r.report {
		if strings.HasSuffix(line, ": hit") {
			return true
		}
	}
	return false
}

// relinked reports whether the command relinked a kept program from its
// file records after files changed (vmhost's filerecord.go).
func (r cacheRun) relinked() bool {
	for _, line := range r.report {
		if strings.Contains(line, ": relinked ") {
			return true
		}
	}
	return false
}

// kept reports whether the command read or wrote a kept program.
func (r cacheRun) kept() bool {
	for _, line := range r.report {
		if strings.HasSuffix(line, ": hit") || strings.HasSuffix(line, ": stored") {
			return true
		}
	}
	return false
}

// runNomiIn runs nomi with args in dir, its artifact cache at cache ("off"
// for none).
func runNomiIn(t *testing.T, dir, cache string, args ...string) cacheRun {
	t.Helper()
	cmd := exec.Command(nomiBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_ARTIFACT_CACHE="+cache, "NOMI_DEBUG_ARTIFACT_CACHE=1", "NOMI_COLOR=never")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	r := cacheRun{stdout: out.String()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		r.exit = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("nomi %s: %v", strings.Join(args, " "), err)
	}
	var rest []string
	for _, line := range strings.SplitAfter(errOut.String(), "\n") {
		if strings.HasPrefix(line, "artifact cache: ") {
			r.report = append(r.report, strings.TrimSpace(line))
			continue
		}
		rest = append(rest, line)
	}
	r.stderr = strings.Join(rest, "")
	return r
}

// sharedStd is the directory of std's artifacts every child of this test
// binary reads (noManagedGoEnv's cache), made by a first command if no
// child made it yet.
var sharedStd = sync.OnceValues(func() (string, error) {
	root := os.Getenv("NOMI_ARTIFACT_CACHE")
	dir, err := os.MkdirTemp("", "nomi-std-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	hello := filepath.Join(dir, "hello.nomi")
	if err := os.WriteFile(hello, []byte("fn main() {\n}\n"), 0o644); err != nil {
		return "", err
	}
	if out, err := exec.Command(nomiBin, "run", hello).CombinedOutput(); err != nil {
		return "", fmt.Errorf("nomi run hello.nomi: %v\n%s", err, out)
	}
	return filepath.Join(root, "std"), nil
})

// newCacheDir is an artifact cache of the test's own that holds std's
// artifacts and no program, so a command over it neither makes std again
// nor reads another test's programs.
func newCacheDir(t *testing.T) string {
	t.Helper()
	std, err := sharedStd()
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	entries, err := os.ReadDir(std)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cache, "std"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		from, to := filepath.Join(std, e.Name()), filepath.Join(cache, "std", e.Name())
		if os.Link(from, to) == nil {
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cache
}

// runFreshIn runs nomi as runNomiIn does with the programs and file records
// cache keeps set aside, so it reads none: the command as it runs with no
// program kept.
func runFreshIn(t *testing.T, dir, cache string, args ...string) cacheRun {
	t.Helper()
	var moved []string
	for _, kind := range []string{"programs", "files"} {
		kept := filepath.Join(cache, kind)
		if os.Rename(kept, kept+".aside") == nil {
			moved = append(moved, kept)
		}
	}
	r := runNomiIn(t, dir, cache, args...)
	for _, kind := range []string{"programs", "files"} {
		if err := os.RemoveAll(filepath.Join(cache, kind)); err != nil {
			t.Fatal(err)
		}
	}
	for _, kept := range moved {
		if err := os.Rename(kept+".aside", kept); err != nil {
			t.Fatal(err)
		}
	}
	if r.read() || r.relinked() {
		t.Fatalf("`nomi %s` read a kept program with none kept: %v", strings.Join(args, " "), r.report)
	}
	return r
}

// commandsFor are the commands the harness compares for an entry: check,
// and run or test as the entry is a program or a test file.
func commandsFor(entry string) [][]string {
	if strings.HasSuffix(entry, "_test.nomi") {
		return [][]string{{"check", entry}, {"test", entry}}
	}
	return [][]string{{"check", entry}, {"run", entry}}
}

// requireCachedMatchesFresh runs each command three times in dir: twice
// with cache, then with no program kept (runFreshIn). Both cached runs
// must print what the fresh one printed, and the second must read a kept program whenever the first
// read or kept one. It answers how many second runs read one, and how many
// first runs relinked one from its file records.
func requireCachedMatchesFresh(t *testing.T, dir, entry, cache, step string) (read, relinked int) {
	t.Helper()
	read, relinked, _ = requireCachedMatchesFreshWhy(t, dir, entry, cache, step)
	return read, relinked
}

// requireCachedMatchesFreshWhy is requireCachedMatchesFresh, and answers
// the cache's reasons for not relinking.
func requireCachedMatchesFreshWhy(t *testing.T, dir, entry, cache, step string) (read, relinked int, why []string) {
	t.Helper()
	for _, args := range commandsFor(entry) {
		first := runNomiIn(t, dir, cache, args...)
		second := runNomiIn(t, dir, cache, args...)
		fresh := runFreshIn(t, dir, cache, args...)
		for i, got := range []cacheRun{first, second} {
			if got.String() != fresh.String() {
				t.Fatalf("after %s, `nomi %s` with the artifact cache (run %d, %v) printed\n%s\nand with none\n%s",
					step, strings.Join(args, " "), i+1, got.report, got, fresh)
			}
		}
		if (first.kept() || first.relinked()) && !second.read() {
			t.Fatalf("after %s, the second `nomi %s` read no kept program though the first %v", step, strings.Join(args, " "), first.report)
		}
		if second.read() {
			read++
		}
		if first.relinked() {
			relinked++
		}
		for _, line := range first.report {
			if _, after, ok := strings.Cut(line, "not relinked: "); ok {
				why = append(why, after)
			}
		}
	}
	return read, relinked, why
}

// runCacheEdits writes p under a new directory, runs requireCachedMatchesFresh
// on it as it is and after each edit, and answers how many runs read a kept
// program and how many relinked one from its file records.
func runCacheEdits(t *testing.T, p cacheProject, edits []cacheEdit) (read, relinked int) {
	t.Helper()
	read, relinked, _ = runCacheEditsBy(t, p, edits)
	return read, relinked
}

// runCacheEditsBy is runCacheEdits, and answers how many commands relinked
// after each edit, by the edit's description.
func runCacheEditsBy(t *testing.T, p cacheProject, edits []cacheEdit) (read, relinked int, byEdit map[string]int) {
	t.Helper()
	byEdit = map[string]int{}
	cacheWhy = map[string][]string{}
	dir := t.TempDir()
	for rel, text := range p.files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cache := newCacheDir(t)
	read, relinked = requireCachedMatchesFresh(t, dir, p.entry, cache, "writing "+p.name)
	for _, e := range edits {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(e.rel)), []byte(e.text), 0o644); err != nil {
			t.Fatal(err)
		}
		for rel, text := range e.others {
			path := filepath.Join(dir, filepath.FromSlash(rel))
			if text == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		r, l, why := requireCachedMatchesFreshWhy(t, dir, p.entry, cache, fmt.Sprintf("%s (%s in %s)", e.what, e.rel, p.name))
		read, relinked = read+r, relinked+l
		byEdit[e.what] += l
		cacheWhy[e.what] = append(cacheWhy[e.what], why...)
	}
	return read, relinked, byEdit
}

// pickCacheFile answers the project and the .nomi file an input's indexes
// name.
func pickCacheFile(projects []cacheProject, project, file uint) (cacheProject, string) {
	p := projects[project%uint(len(projects))]
	files := p.nomiFiles()
	return p, files[file%uint(len(files))]
}

func cacheSeed(s string) int64 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int64(h.Sum32())
}

// cacheFuzzSeedText bounds the files the fuzzer starts from.
const cacheFuzzSeedText = 8 << 10

// FuzzCachedBuildMatchesFresh edits one file of a project on disk
// (cacheProjects) and after each edit requires `nomi check` and `nomi run`
// (or `nomi test` for a test entry) with the artifact cache to print
// exactly what they print with none (requireCachedMatchesFresh). The fuzzer
// mutates the file's new text and picks the project and the file; the edits
// that follow delete, type and paste into it and then restore it, as
// FuzzIncrementalMatchesFull's do. In the generated project, projectgen's
// edits of bodies, signatures and impls and an added file, each undone,
// come first. Run it with
//
//	go test ./cmd/nomi -run '^$' -fuzz '^FuzzCachedBuildMatchesFresh$' -fuzztime 5m -fuzzminimizetime 5s -parallel 4
//
// Under plain `go test` it runs one seed, and
// TestCachedBuildMatchesFreshSample and TestCachedBuildMatchesFreshRotating
// run a fixed and a daily set of edits, projectgen's edit kinds among them.
func FuzzCachedBuildMatchesFresh(f *testing.F) {
	if testing.Short() {
		f.Skip("runs nomi several times per edit; -short")
	}
	projects, err := cacheProjects()
	if err != nil {
		f.Fatal(err)
	}
	fuzz := flag.Lookup("test.fuzz")
	fuzzing := fuzz != nil && fuzz.Value.String() != ""
	for i, p := range projects {
		for j, rel := range p.nomiFiles() {
			if (fuzzing || i == 0 && j == 0) && len(p.files[rel]) <= cacheFuzzSeedText {
				f.Add(uint(i), uint(j), p.files[rel])
			}
		}
	}
	f.Fuzz(func(t *testing.T, project, file uint, src string) {
		if !utf8.ValidString(src) || len(src) > 2*cacheFuzzSeedText {
			t.Skip()
		}
		p, rel := pickCacheFile(projects, project, file)
		edits := fuzzCacheEdits(p, rel, src, cacheSeed(src))
		if p.gen != nil {
			// The generated project's edits of signatures, impls and files
			// first, each undone, then the fuzzer's.
			edits = append(genCacheEdits(p), edits...)
		}
		runCacheEdits(t, p, edits)
	})
}

// TestCachedBuildMatchesFreshSample runs a fixed sample: every projectgen
// edit kind, made and undone, over the generated project, and the
// fuzzer's edits of the entries of two corpus projects.
func TestCachedBuildMatchesFreshSample(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	projects, err := cacheProjects()
	if err != nil {
		t.Fatal(err)
	}
	read, relinked, byEdit := runCacheEditsBy(t, projects[0], genCacheEdits(projects[0]))
	// Each of these edits, and its undoing, relinks the program from its
	// file records rather than checking it whole.
	for _, kind := range relinkingKinds {
		for _, what := range []string{kind, kind + ", undone"} {
			if byEdit[what] == 0 {
				t.Errorf("no command relinked the program after the edit %q: %q", what, cacheWhy[what])
			}
		}
	}
	for _, i := range []int{1, len(projects) / 2} {
		p := projects[i]
		r, l := runCacheEdits(t, p, fuzzCacheEdits(p, p.entry, p.files[p.entry], cacheSeed(p.name)))
		read, relinked = read+r, relinked+l
	}
	if read == 0 {
		t.Fatal("no command read a kept program, so the comparisons say nothing about the cache")
	}
	// The generated project's body edits, of a leaf, a hub and the entry,
	// and their undoing relink the program from its file records.
	if relinked == 0 {
		t.Fatal("no command relinked a program from its file records, so the comparisons say nothing about them")
	}
}

// cacheWhy holds runCacheEditsBy's last run's reasons for not relinking, by
// edit.
var cacheWhy map[string][]string

// relinkingKinds are the projectgen edits a command after which must relink
// the program from its file records.
// The hub's signature edit reaches most of the program's files, which a
// relink does not check one at a time.
var relinkingKinds = []string{string(projectgen.BodyLeaf), string(projectgen.BodyHub), string(projectgen.BodyCallLeaf), string(projectgen.InstanceLeaf),
	string(projectgen.BodyEntry), string(projectgen.SignatureLeaf), string(projectgen.ImplLeaf), fileAdded}

// rotatingCacheCount is how many edit sequences
// TestCachedBuildMatchesFreshRotating runs.
const rotatingCacheCount = 2

// TestCachedBuildMatchesFreshRotating runs the fuzzer's edits beside the
// fixed sample, a different pair every UTC day (internal/rotation): seed s
// picks a project and a file and places the edits.
func TestCachedBuildMatchesFreshRotating(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi several times per edit; -short")
	}
	projects, err := cacheProjects()
	if err != nil {
		t.Fatal(err)
	}
	set := rotation.For(t, "./cmd/nomi", rotatingCacheCount)
	for _, seed := range set.Seeds() {
		rng := rand.New(rand.NewSource(seed))
		p, rel := pickCacheFile(projects, uint(rng.Intn(len(projects))), uint(rng.Intn(1<<16)))
		t.Logf("seed %d edits %s in %s", seed, rel, p.name)
		edits := fuzzCacheEdits(p, rel, p.files[rel], seed)
		if p.gen != nil {
			edits = append(genCacheEdits(p), edits...)
		}
		ok := t.Run(fmt.Sprint(seed), func(t *testing.T) { runCacheEdits(t, p, edits) })
		if !ok {
			t.Fatal(set.Failure(seed))
		}
	}
}

// keptRecords are the program records under cache.
func keptRecords(t *testing.T, cache string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(filepath.Join(cache, "programs"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// TestArtifactCacheRebuildsWhatItCannotRead damages a kept program's record
// every way a disk or another process can (truncated, emptied, a byte
// changed, another program's record in its place) and requires the next
// command to print what a fresh one prints, write the record again, and the
// one after to read it.
func TestArtifactCacheRebuildsWhatItCannotRead(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	projects, err := cacheProjects()
	if err != nil {
		t.Fatal(err)
	}
	p := projects[0]
	dir := t.TempDir()
	if err := p.gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	var testFile string
	for _, rel := range p.nomiFiles() {
		if strings.HasSuffix(rel, "_test.nomi") {
			testFile = rel
			break
		}
	}
	cache := newCacheDir(t)
	freshRun := runFreshIn(t, dir, cache, "run", p.entry)
	freshTest := runFreshIn(t, dir, cache, "test", testFile)
	runNomiIn(t, dir, cache, "run", p.entry)
	runNomiIn(t, dir, cache, "test", testFile)
	records := keptRecords(t, cache)
	if len(records) != 2 {
		t.Fatalf("the cache holds %d records after running a program and a test file; want 2", len(records))
	}
	damages := []struct {
		name   string
		damage func(data, other []byte) []byte
	}{
		{"truncated", func(data, _ []byte) []byte { return data[:len(data)/2] }},
		{"emptied", func(_, _ []byte) []byte { return nil }},
		{"a byte changed", func(data, _ []byte) []byte {
			out := bytes.Clone(data)
			out[len(out)*2/3] ^= 0x5a
			return out
		}},
		{"swapped", func(_, other []byte) []byte { return other }},
	}
	for _, d := range damages {
		for i, record := range records {
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			other, err := os.ReadFile(records[1-i])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(record, d.damage(data, other), 0o644); err != nil {
				t.Fatal(err)
			}
			args, fresh := []string{"run", p.entry}, freshRun
			if i == 1 {
				args, fresh = []string{"test", testFile}, freshTest
			}
			// Which record is the program's and which the test file's
			// is the keys' order, so try both commands' records each way.
			for _, try := range [][]string{{"run", p.entry}, {"test", testFile}} {
				want := freshRun
				if try[0] == "test" {
					want = freshTest
				}
				got := runNomiIn(t, dir, cache, try...)
				if got.String() != want.String() {
					t.Fatalf("with its record %s, `nomi %s` printed\n%s\nand with none\n%s", d.name, strings.Join(try, " "), got, want)
				}
			}
			again := runNomiIn(t, dir, cache, args...)
			if !again.read() || again.String() != fresh.String() {
				t.Fatalf("after its record was %s and rebuilt, `nomi %s` read %v and printed\n%s", d.name, strings.Join(args, " "), again.report, again)
			}
		}
	}
}

// fileRecords are the file records under cache (vmhost's filerecord.go).
func fileRecords(t *testing.T, cache string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(filepath.Join(cache, "files"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// TestFileRecordsRebuildWhatTheyCannotRead edits a leaf's body and damages
// the program's file record every way TestArtifactCacheRebuildsWhatItCannotRead
// damages a program's record, and requires the next command to print what
// a fresh one prints without relinking from the damaged record, and the
// edit after it to relink from the record that command wrote again.
func TestFileRecordsRebuildWhatTheyCannotRead(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	projects, err := cacheProjects()
	if err != nil {
		t.Fatal(err)
	}
	p := projects[0]
	dir := t.TempDir()
	if err := p.gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	var testFile string
	for _, rel := range p.nomiFiles() {
		if strings.HasSuffix(rel, "_test.nomi") {
			testFile = rel
			break
		}
	}
	cache := newCacheDir(t)
	runNomiIn(t, dir, cache, "run", p.entry)
	records := fileRecords(t, cache)
	if len(records) != 1 {
		t.Fatalf("the cache holds %d file records after running a program; want 1", len(records))
	}
	record := records[0]
	runNomiIn(t, dir, cache, "test", testFile)
	var otherRecord string
	for _, r := range fileRecords(t, cache) {
		if r != record {
			otherRecord = r
		}
	}
	if otherRecord == "" {
		t.Fatal("running a test file kept no file record of its own")
	}
	leaf, _ := p.gen.Edit(projectgen.BodyLeaf, 0)
	leafPath := filepath.Join(dir, filepath.FromSlash(leaf)+".nomi")
	edit := 0
	editLeaf := func() {
		edit++
		_, text := p.gen.Edit(projectgen.BodyLeaf, edit)
		if err := os.WriteFile(leafPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The program's own file record is the one the run rewrites.
	editLeaf()
	if r := runNomiIn(t, dir, cache, "run", p.entry); !r.relinked() {
		t.Fatalf("a leaf body edit did not relink the program: %v", r.report)
	}
	damages := []struct {
		name   string
		damage func(data, other []byte) []byte
	}{
		{"truncated", func(data, _ []byte) []byte { return data[:len(data)/2] }},
		{"emptied", func(_, _ []byte) []byte { return nil }},
		{"a byte changed", func(data, _ []byte) []byte {
			out := bytes.Clone(data)
			out[len(out)*2/3] ^= 0x5a
			return out
		}},
		{"swapped", func(_, other []byte) []byte { return other }},
	}
	for _, d := range damages {
		editLeaf()
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		other, err := os.ReadFile(otherRecord)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record, d.damage(data, other), 0o644); err != nil {
			t.Fatal(err)
		}
		fresh := runFreshIn(t, dir, cache, "run", p.entry)
		got := runNomiIn(t, dir, cache, "run", p.entry)
		if got.relinked() || got.String() != fresh.String() {
			t.Fatalf("with its file record %s, `nomi run` relinked %v and printed\n%s\nand with none\n%s", d.name, got.relinked(), got, fresh)
		}
		editLeaf()
		fresh = runFreshIn(t, dir, cache, "run", p.entry)
		again := runNomiIn(t, dir, cache, "run", p.entry)
		if !again.relinked() || again.String() != fresh.String() {
			t.Fatalf("after its file record was %s and written again, a leaf edit relinked %v and printed\n%s", d.name, again.relinked(), again)
		}
	}
}

// TestArtifactCacheTwoProcessesRace starts two commands over one program
// and an empty cache at once, several times: both must print what a fresh
// command prints, and a third must read the program either kept.
func TestArtifactCacheTwoProcessesRace(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	projects, err := cacheProjects()
	if err != nil {
		t.Fatal(err)
	}
	p := projects[0]
	dir := t.TempDir()
	if err := p.gen.Write(dir); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ {
		cache := newCacheDir(t)
		fresh := runFreshIn(t, dir, cache, "run", p.entry)
		var wg sync.WaitGroup
		runs := make([]cacheRun, 2)
		for i := range runs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runs[i] = runNomiIn(t, dir, cache, "run", p.entry)
			}()
		}
		wg.Wait()
		for i, r := range runs {
			if r.String() != fresh.String() {
				t.Fatalf("round %d: racing run %d printed\n%s\nand a fresh one\n%s", round, i+1, r, fresh)
			}
		}
		third := runNomiIn(t, dir, cache, "run", p.entry)
		if !third.read() || third.String() != fresh.String() {
			t.Fatalf("round %d: after two racing runs, a third read %v and printed\n%s", round, third.report, third)
		}
		if n := len(keptRecords(t, cache)); n != 1 {
			t.Fatalf("round %d: the cache holds %d records for one program", round, n)
		}
	}
}

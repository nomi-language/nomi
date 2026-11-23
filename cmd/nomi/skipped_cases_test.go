package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/projectgen"
	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/nomi-language/nomi/vmhost"
)

// TestSkippedCasesWouldPass holds `nomi test`'s stored passes (vmhost's
// testpass.go) to what running every case answers. A case a stored pass
// answers as unchanged must be one that would pass if it ran, with the
// cases it counts; a wrong skip is the one failure the feature cannot
// afford. That holds both for a case decided in its loaded file and for a
// file answered from its passes record with no program loaded
// (vmhost.Cache.TestUnchanged), which in a test binary also loads the
// file and panics when a case it answered is decided otherwise.
//
// Inputs: a sample of the corpus's projects (each directory under tests/,
// each exercise under tests/exercism, and each project with a nomi.toml
// below them; every one under
// NOMI_CORPUS_WALKS=full), a generated project (internal/projectgen) and a
// project of two test files and a dependency (fileLevelSkipProject). Each
// is copied to a directory of its own and tested with `nomi test --format
// json` in this process, with the artifact cache in a directory of the
// test's own: once to fill it, once more, which decides each case on the
// kept program, and a third time, which must answer from its passes record
// every file whose cases were all unchanged in the second. Then each edit
// of a seeded set (skipEdits: a constant, an operator or a comparison in a
// function, lines added and taken away above declarations, a local renamed,
// two fields of a struct swapped, a derive taken away, a `once`'s value, a
// default argument, an impl of Display added, a constant in a test,
// nomi.toml, a file no program loads, which its undo removes, a comment in a
// file, --timeout, another compiler (artifactcache.CompilerID), and the
// generated project's own edits, projectgen.EditKinds; for
// fileLevelSkipProject, a dependency's file, a file one test file does not
// load and nomi.toml's locales) is applied, the project tested twice through
// the cache, and tested with every case run and nothing kept
// (NOMI_ARTIFACT_CACHE=off, --all). In the first run through the cache each
// file the edit does not reach is answered from its passes record; in the
// second each file it reaches is decided on its kept program. Each case's
// status, case count, message and output must be the same in each run, and
// so after the edit is undone, against the first run. A run under another
// --timeout or compiler must answer no file from its passes record. An edit
// that only moves lines must leave every case with no effects unchanged or
// `moved` (an assertion whose failure is a value moved), never `changed`.
//
// A difference a run of every case through the cache shares is the kept
// program's (vmhost's file records), and fails as that.
//
// Known gaps: knownSkipGaps pins, by project, a kept program that runs
// otherwise than its files, which the harness requires still to show. And a
// typed literal's cell is named by where it is
// (irbuild's irliteralcell.go, `literal Regex"…" at 58:23`), so a case
// that reaches a literal its edit moved runs again; a line edit with a
// tagged literal below it in its file may leave cases `changed`, which the
// test counts and logs. The cases each edit ran are logged, not asserted.
//
// The rotating set (rotation.For) adds, for each seed, a generated project
// of that seed and a corpus project the seed picks, with edits the seed
// picks; a failure names the seed and the command that checks exactly it.
//
// Run it with `go test ./cmd/nomi -run '^TestSkippedCasesWouldPass$'
// -count=1`; NOMI_CORPUS_WALKS=full walks every corpus project, and
// NOMI_GEN_SEED and NOMI_GEN_COUNT pin and size the rotating set.
func TestSkippedCasesWouldPass(t *testing.T) {
	if testing.Short() {
		t.Skip("tests projects several times per edit; -short")
	}
	walk := rotation.CorpusWalk(t, 30)
	projects := skipCorpusProjects(t)
	var taken []skipProject
	for i, p := range projects {
		if walk.Takes(i) {
			taken = append(taken, p)
		}
	}
	gen := projectgen.Generate(projectgen.Config{Seed: 1, Lines: 1200})
	taken = append(taken, generatedSkipProject(gen, 1), fileLevelSkipProject())
	for _, p := range taken {
		h := fnv.New64a()
		h.Write([]byte(p.name))
		t.Run(p.name, func(t *testing.T) { checkSkips(t, p, int64(h.Sum64()>>1), "") })
	}
	set := rotation.For(t, "./cmd/nomi", 1)
	for _, seed := range set.Seeds() {
		r := rand.New(rand.NewSource(seed))
		picked := []skipProject{
			generatedSkipProject(projectgen.Generate(projectgen.Config{Seed: seed, Lines: 800}), seed),
			projects[r.Intn(len(projects))],
		}
		for _, p := range picked {
			t.Run(fmt.Sprintf("seed %d/%s", seed, p.name), func(t *testing.T) { checkSkips(t, p, seed, set.Failure(seed)) })
		}
	}
}

// knownSkipGaps are the projects, by name, after whose edits the program
// the artifact cache keeps runs otherwise than the files make it, every case
// run: a kept-program fault, not a stored pass's (requireSame).
var knownSkipGaps = map[string]string{}

// skipProject is a project the harness copies and edits: its files by path
// from its root.
type skipProject struct {
	name  string
	files map[string]string
	// gen is the generated project it was written from, or nil.
	gen *projectgen.Project
	// settle says its files must be older than the artifact cache's racy
	// window (two seconds) before its first run: a program that loads a
	// dependency's file is kept only then (progcache's Store).
	settle bool
}

// fileLevelSkipProject is a project for the edits that change what a file's
// kept program is read from beside its own files: an app of two test files,
// one that calls a dependency (a module of its own, which nomi.toml names
// by its directory) and one that loads a file of the app, with
// locales in nomi.toml. Each test asserts a value its edit changes, so a
// file wrongly answered from its passes record fails.
func fileLevelSkipProject() skipProject {
	return skipProject{name: "file level", settle: true, files: map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\n\n[i18n]\nsource_locale = \"en\"\nlocales = [\"en\", \"de\"]\n\n[dependencies]\npricing = { dir = \"../pricing\" }\n",
		"app/totals_test.nomi": "import pricing::rules\n\ntest \"the rate doubles\"\n    assert rules.rate() * 2 == 6\nend\n\n" +
			"test \"the rate is small\"\n    assert rules.rate() < 4\nend\n",
		"app/shapes.nomi":      "pub fn sides() -> Int\n    4\nend\n",
		"app/shapes_test.nomi": "import shapes\n\ntest \"a square has four sides\"\n    assert shapes.sides() == 4\nend\n",
		"pricing/nomi.toml":    "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi":   "pub fn rate() -> Int\n    3\nend\n",
	}}
}

// fileLevelEdits are fileLevelSkipProject's own edits: a dependency's
// file, a file one test file loads and the other does not, and the locales.
func fileLevelEdits(p skipProject) []skipEdit {
	return []skipEdit{
		{name: "a dependency's file", files: map[string]string{"pricing/rules.nomi": strings.Replace(p.files["pricing/rules.nomi"], "3", "4", 1)}},
		{name: "a file one test file does not load", files: map[string]string{"app/shapes.nomi": strings.Replace(p.files["app/shapes.nomi"], "4", "5", 1)}},
		{name: "nomi.toml's locales", files: map[string]string{"app/nomi.toml": strings.Replace(p.files["app/nomi.toml"], `"en", "de"`, `"en"`, 1)}},
	}
}

// skipCorpusProjects are the corpus's projects, in path order.
func skipCorpusProjects(t *testing.T) []skipProject {
	t.Helper()
	tests := filepath.Join(repoRoot(t), "tests")
	var dirs []string
	entries, err := os.ReadDir(tests)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(tests, e.Name()))
		sub, _ := os.ReadDir(filepath.Join(tests, e.Name()))
		for _, s := range sub {
			dir := filepath.Join(tests, e.Name(), s.Name())
			if !s.IsDir() {
				continue
			}
			if e.Name() == "exercism" || fileExists(filepath.Join(dir, "nomi.toml")) {
				dirs = append(dirs, dir)
			}
		}
	}
	sort.Strings(dirs)
	var out []skipProject
	for _, dir := range dirs {
		p := skipProject{name: strings.TrimPrefix(filepath.ToSlash(dir), filepath.ToSlash(tests)+"/"), files: map[string]string{}}
		nested := dir != filepath.Join(tests, "exercism") && filepath.Dir(dir) == tests
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// A category directory's subdirectories are projects of their
				// own, or files its own tests read.
				if path != dir && nested && fileExists(filepath.Join(path, "nomi.toml")) {
					return filepath.SkipDir
				}
				return nil
			}
			data, err := os.ReadFile(path)
			if err == nil {
				rel, _ := filepath.Rel(dir, path)
				p.files[filepath.ToSlash(rel)] = string(data)
			}
			return nil
		})
		if dir == filepath.Join(tests, "exercism") || !holdsTests(p.files) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// holdsTests reports whether a project has a file of tests.
func holdsTests(files map[string]string) bool {
	for rel, text := range files {
		if strings.HasSuffix(rel, ".nomi") && regexp.MustCompile(`(?m)^(test |group )`).MatchString(text) {
			return true
		}
	}
	return false
}

// generatedSkipProject is gen as a skipProject.
func generatedSkipProject(gen *projectgen.Project, seed int64) skipProject {
	p := skipProject{name: fmt.Sprintf("generated %d", seed), files: map[string]string{"nomi.toml": projectgen.Manifest}, gen: gen}
	for key, text := range gen.Files {
		p.files[key+".nomi"] = text
	}
	return p
}

// skipRecord is one record of `nomi test --format json`, as the harness
// compares it.
type skipRecord struct {
	Type        string `json:"type"`
	File        string `json:"file"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Cases       int    `json:"cases"`
	FailedCases int    `json:"failed_cases"`
	Message     string `json:"message"`
	Output      string `json:"output"`
	Decided     string `json:"decided"`
}

// skipRun is one `nomi test` of a project: its records by file and name.
type skipRun map[string]skipRecord

// testProject runs `nomi test --format json` over dir in this process, with
// the artifact cache cache ("off" for none) and runs, under timeout, or
// the default for zero.
func testProject(t *testing.T, dir, cache string, runs vmhost.RunPolicy, timeout time.Duration) skipRun {
	t.Helper()
	t.Setenv(artifactcache.RootEnv, cache)
	var out, errOut bytes.Buffer
	if timeout == 0 {
		timeout = vmhost.DefaultTestTimeout
	}
	parsed := testArgs{root: dir, format: vmhost.TestFormatJSON, timeout: timeout, runs: runs}
	_ = testVM(context.Background(), parsed, &out, &errOut, bytes.NewReader(nil))
	run := skipRun{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r skipRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("a record of `nomi test --format json %s` does not read: %v\n%s", dir, err, line)
		}
		if r.Type == "summary" {
			continue
		}
		rel, _ := filepath.Rel(dir, r.File)
		key := filepath.ToSlash(rel) + " :: " + r.Name
		if _, twice := run[key]; twice {
			key += fmt.Sprintf(" #%d", len(run))
		}
		run[key] = r
	}
	return run
}

// differences are the cases whose outcome in got differs from want's, each
// with how, a case got answered as unchanged first.
func differences(got, want skipRun) []string {
	var out []string
	for key, w := range want {
		g, ok := got[key]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: missing, want %s", key, w.Status))
		case g.Status != w.Status || g.Cases != w.Cases || g.FailedCases != w.FailedCases || g.Message != w.Message || g.Output != w.Output:
			how := fmt.Sprintf("%s: %s with %d cases (decided %q), want %s with %d", key, g.Status, g.Cases, g.Decided, w.Status, w.Cases)
			if g.Message != w.Message {
				how += fmt.Sprintf("\n  message %q\n  want    %q", g.Message, w.Message)
			}
			if g.Output != w.Output {
				how += fmt.Sprintf("\n  output %q\n  want   %q", g.Output, w.Output)
			}
			if g.Decided == "unchanged" {
				how = "WRONG SKIP " + how
			}
			out = append(out, how)
		}
	}
	for key, g := range got {
		if _, ok := want[key]; !ok {
			out = append(out, fmt.Sprintf("%s: %s, and no such case when every case runs", key, g.Status))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.HasPrefix(out[i], "WRONG") && !strings.HasPrefix(out[j], "WRONG") })
	return out
}

// answeredFromRecords is how many files run answered from their passes
// records (vmhost.FilesAnsweredFromPassesForTest).
func answeredFromRecords(run func()) int64 {
	before := vmhost.FilesAnsweredFromPassesForTest()
	run()
	return vmhost.FilesAnsweredFromPassesForTest() - before
}

// unchangedFiles counts the files of run whose every case was decided
// unchanged.
func unchangedFiles(run skipRun) int {
	all := map[string]bool{}
	for _, r := range run {
		if r.Type != "test" {
			continue
		}
		if _, seen := all[r.File]; !seen {
			all[r.File] = true
		}
		all[r.File] = all[r.File] && r.Decided == "unchanged"
	}
	n := 0
	for _, unchanged := range all {
		if unchanged {
			n++
		}
	}
	return n
}

// withCompiler runs run as a build of nomi whose identity is id, or as
// this one for "".
func withCompiler(id string, run func()) {
	if id == "" {
		run()
		return
	}
	was := artifactcache.CompilerID
	artifactcache.CompilerID = func() string { return id }
	defer func() { artifactcache.CompilerID = was }()
	run()
}

// ran counts the cases of run that ran, and those decided as each word.
func ran(run skipRun) (n int, by map[string]int) {
	by = map[string]int{}
	for _, r := range run {
		if r.Type != "test" {
			continue
		}
		by[r.Decided]++
		if r.Decided != "unchanged" {
			n++
		}
	}
	return n, by
}

// writeFiles writes files under dir, and removes each of gone.
func writeFiles(t *testing.T, dir string, files map[string]string, gone []string) {
	t.Helper()
	for rel, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range gone {
		_ = os.Remove(filepath.Join(dir, filepath.FromSlash(rel)))
	}
}

// checkSkips tests p as TestSkippedCasesWouldPass says, with the edits seed
// picks. failure starts a failure's text, for a project of the rotating set.
func checkSkips(t *testing.T, p skipProject, seed int64, failure string) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	writeFiles(t, dir, p.files, nil)
	if p.settle {
		time.Sleep(2100 * time.Millisecond)
	}
	first := testProject(t, dir, cache, vmhost.RunChangedOrEffects, 0)
	if len(first) == 0 {
		t.Skipf("%s reports no case", p.name)
	}
	gap, gapSeen := knownSkipGaps[p.name], false
	// requireSame fails when got, a run through the cache after step,
	// differs from want, every case run with nothing kept. A difference
	// that a run of every case through the cache shares is the kept
	// program's, not a stored pass's: one knownSkipGaps lists is logged,
	// and the cache is started again.
	requireSame := func(step string, got, want skipRun) {
		d := differences(got, want)
		if len(d) == 0 {
			return
		}
		all := testProject(t, dir, cache, vmhost.RunAll, 0)
		if len(differences(all, want)) == 0 {
			t.Fatalf("%safter %s, `nomi test` through the cache answers a case from a stored pass otherwise than running it:\n%s",
				failure, step, strings.Join(d, "\n"))
		}
		if gap == "" {
			t.Fatalf("%safter %s, the program the artifact cache keeps runs otherwise than the files make it, with every case run:\n%s",
				failure, step, strings.Join(differences(all, want), "\n"))
		}
		gapSeen = true
		t.Logf("%s: after %s, the known gap (%s):\n%s", p.name, step, gap, strings.Join(d, "\n"))
		cache = t.TempDir()
		testProject(t, dir, cache, vmhost.RunChangedOrEffects, 0)
		testProject(t, dir, cache, vmhost.RunChangedOrEffects, 0)
	}
	again := testProject(t, dir, cache, vmhost.RunChangedOrEffects, 0)
	requireSame("a second run with nothing edited", again, first)
	n, by := ran(again)
	t.Logf("%s: %d cases, %d ran again with nothing edited %v", p.name, len(first), n, by)
	var third skipRun
	answered := answeredFromRecords(func() { third = testProject(t, dir, cache, vmhost.RunChangedOrEffects, 0) })
	requireSame("a third run with nothing edited", third, first)
	if want := unchangedFiles(again); answered != int64(want) {
		t.Errorf("%sa third run with nothing edited answered %d files from their passes records; want the %d whose every case the second decided unchanged",
			failure, answered, want)
	}
	r := rand.New(rand.NewSource(seed))
	edits := skipEdits(p, r)
	if p.name == "file level" {
		edits = append(edits, fileLevelEdits(p)...)
	}
	for _, e := range edits {
		writeFiles(t, dir, e.files, e.gone)
		var got, got2 skipRun
		answered := answeredFromRecords(func() {
			withCompiler(e.compiler, func() { got = testProject(t, dir, cache, vmhost.RunChangedOrEffects, e.timeout) })
		})
		if (e.timeout != 0 || e.compiler != "") && answered > 0 {
			t.Errorf("%safter %s, %d files were answered from passes records decided under other settings", failure, e.name, answered)
		}
		want := testProject(t, dir, "off", vmhost.RunAll, e.timeout)
		requireSame(e.name, got, want)
		answered2 := answeredFromRecords(func() {
			withCompiler(e.compiler, func() { got2 = testProject(t, dir, cache, vmhost.RunChangedOrEffects, e.timeout) })
		})
		requireSame(e.name+", run again", got2, want)
		t.Logf("%s: after %s, %d then %d files answered from their passes records", p.name, e.name, answered, answered2)
		n, by := ran(got)
		if e.moves {
			if changed := by["changed"]; changed > 0 && !e.movesLiteral {
				var names []string
				for key, rec := range got {
					if rec.Decided == "changed" {
						names = append(names, key)
					}
				}
				sort.Strings(names)
				t.Errorf("%safter %s, which only moves lines, %d cases with no effects ran again as changed:\n%s",
					failure, e.name, changed, strings.Join(names, "\n"))
			}
		}
		t.Logf("%s: after %s, %d of %d cases ran %v", p.name, e.name, n, len(got), by)
		// Undo the edit, which the cache sees as one more.
		undo := map[string]string{}
		var gone []string
		for rel := range e.files {
			if text, ok := p.files[rel]; ok {
				undo[rel] = text
			} else {
				gone = append(gone, rel)
			}
		}
		for _, rel := range e.gone {
			undo[rel] = p.files[rel]
		}
		writeFiles(t, dir, undo, gone)
		requireSame(e.name+", undone", testProject(t, dir, cache, vmhost.RunChangedOrEffects, 0), first)
	}
	if gap != "" && !gapSeen {
		t.Errorf("knownSkipGaps lists %s (%s), and its runs no longer differ: remove the entry", p.name, gap)
	}
}

// skipEdit is one edit of a project: the files it writes and those it
// removes.
type skipEdit struct {
	name  string
	files map[string]string
	gone  []string
	// moves says the edit only moves lines; movesLiteral that a tagged
	// literal is below where it moves them in its file (the known gap).
	moves, movesLiteral bool
	// timeout and compiler, when set, are the edit itself: the project is
	// tested under that --timeout, or as a build of nomi of that identity.
	timeout  time.Duration
	compiler string
}

var (
	declLine     = regexp.MustCompile(`^(pub )?(fn|test|group|struct|enum|impl|interface|type|once|derive|refine) `)
	fnLine       = regexp.MustCompile(`^( {4})?(pub )?fn \w+`)
	testLine     = regexp.MustCompile(`^ *test "`)
	intToken     = regexp.MustCompile(`(^|[^\w.$])(\d+)([^\w.]|$)`)
	localBinding = regexp.MustCompile(`^ +([a-z][a-z0-9_]*) = `)
	fieldLine    = regexp.MustCompile(`^    [a-z_]\w*: \S`)
	structLine   = regexp.MustCompile(`^(pub )?(struct|enum) ([A-Z]\w*)$`)
	taggedLit    = regexp.MustCompile("[A-Za-z_][A-Za-z0-9_]*[\"`]")
	defaultArg   = regexp.MustCompile(`^(\s*(pub )?fn \w+\(.*= )(\d+)`)
)

// block is a function's or a test's lines in a file: [start, end] with
// end its `end` line.
type block struct{ start, end int }

// blocks are the functions (test == false) or tests of lines, at the top
// level or one level into an impl or a group.
func blocks(lines []string, test bool) []block {
	var out []block
	for i, line := range lines {
		isFn := fnLine.MatchString(line) && !strings.HasSuffix(line, " end")
		isTest := testLine.MatchString(line)
		if (test && !isTest) || (!test && !isFn) {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == indent+"end" {
				out = append(out, block{i, j})
				break
			}
		}
	}
	return out
}

// nomiFiles are p's .nomi files, sorted.
func nomiFiles(p skipProject) []string {
	var out []string
	for rel := range p.files {
		if strings.HasSuffix(rel, ".nomi") {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// lineEdit makes one edit of one line of a file: for each file in an order r
// picks, each line edit can change in it, the first change answers.
func lineEdit(p skipProject, r *rand.Rand, name string, change func(lines []string) ([]string, bool)) (skipEdit, bool) {
	files := nomiFiles(p)
	r.Shuffle(len(files), func(i, j int) { files[i], files[j] = files[j], files[i] })
	for _, rel := range files {
		lines := strings.Split(p.files[rel], "\n")
		if edited, ok := change(slicesClone(lines)); ok {
			return skipEdit{name: name + " in " + rel, files: map[string]string{rel: strings.Join(edited, "\n")}}, true
		}
	}
	return skipEdit{}, false
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

// inBlocks picks a line of a block of lines that holds no string and
// matches re, in an order r picks, and answers it with its block.
func inBlocks(r *rand.Rand, lines []string, bs []block, re *regexp.Regexp) (int, block, bool) {
	var at []int
	var of []block
	for _, b := range bs {
		for i := b.start + 1; i < b.end; i++ {
			if !strings.ContainsAny(lines[i], "\"`") && re.MatchString(lines[i]) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
				at, of = append(at, i), append(of, b)
			}
		}
	}
	if len(at) == 0 {
		return 0, block{}, false
	}
	k := r.Intn(len(at))
	return at[k], of[k], true
}

// bumpInt adds one to the first integer token of line.
func bumpInt(line string) string {
	m := intToken.FindStringSubmatchIndex(line)
	n, _ := strconv.Atoi(line[m[4]:m[5]])
	return line[:m[4]] + strconv.Itoa(n+1) + line[m[5]:]
}

// skipEdits are the edits checkSkips makes of p, each class at a site r
// picks, the classes that find no site left out.
func skipEdits(p skipProject, r *rand.Rand) []skipEdit {
	var out []skipEdit
	add := func(e skipEdit, ok bool) {
		if ok {
			out = append(out, e)
		}
	}
	bodyLine := func(name string, test bool, re *regexp.Regexp, change func(line string) string) {
		add(lineEdit(p, r, name, func(lines []string) ([]string, bool) {
			i, _, ok := inBlocks(r, lines, blocks(lines, test), re)
			if !ok {
				return nil, false
			}
			lines[i] = change(lines[i])
			return lines, true
		}))
	}
	bodyLine("a constant in a function", false, intToken, bumpInt)
	bodyLine("an operator in a function", false, regexp.MustCompile(` \+ `), func(l string) string { return strings.Replace(l, " + ", " - ", 1) })
	bodyLine("a comparison in a function", false, regexp.MustCompile(` == `), func(l string) string { return strings.Replace(l, " == ", " != ", 1) })
	bodyLine("a constant in a test", true, intToken, bumpInt)
	for _, add2 := range []bool{true, false} {
		name := "lines added above a declaration"
		if !add2 {
			name = "a line taken away above a declaration"
		}
		var movesLiteral bool
		e, ok := lineEdit(p, r, name, func(lines []string) ([]string, bool) {
			var sites []int
			for i, line := range lines {
				if !declLine.MatchString(line) {
					continue
				}
				// Above the declaration's comments, which a blank line
				// would part from it.
				for i > 0 && strings.HasPrefix(lines[i-1], "//") {
					i--
				}
				if add2 || i > 0 && lines[i-1] == "" {
					sites = append(sites, i)
				}
			}
			if len(sites) == 0 {
				return nil, false
			}
			i := sites[r.Intn(len(sites))]
			movesLiteral = taggedLit.MatchString(strings.Join(lines[i:], "\n"))
			if add2 {
				return append(lines[:i], append([]string{"", "// moved", ""}, lines[i:]...)...), true
			}
			return append(lines[:i-1], lines[i:]...), true
		})
		e.moves, e.movesLiteral = true, movesLiteral
		add(e, ok)
	}
	add(lineEdit(p, r, "a local renamed", func(lines []string) ([]string, bool) {
		bs := append(blocks(lines, false), blocks(lines, true)...)
		i, b, ok := inBlocks(r, lines, bs, localBinding)
		if !ok {
			return nil, false
		}
		name := localBinding.FindStringSubmatch(lines[i])[1]
		body := strings.Join(lines[b.start:b.end+1], "\n")
		if strings.Contains(body, name+":") || strings.Contains(body, "."+name) {
			return nil, false
		}
		word := regexp.MustCompile(`\b` + name + `\b`)
		for k := b.start + 1; k < b.end; k++ {
			lines[k] = word.ReplaceAllString(lines[k], name+"_renamed")
		}
		return lines, true
	}))
	add(lineEdit(p, r, "two fields of a struct swapped", func(lines []string) ([]string, bool) {
		for i := 0; i+2 < len(lines); i++ {
			if strings.HasPrefix(lines[i], "struct ") || strings.HasPrefix(lines[i], "pub struct ") {
				if fieldLine.MatchString(lines[i+1]) && fieldLine.MatchString(lines[i+2]) {
					lines[i+1], lines[i+2] = lines[i+2], lines[i+1]
					return lines, true
				}
			}
		}
		return nil, false
	}))
	add(lineEdit(p, r, "a derive taken away", func(lines []string) ([]string, bool) {
		var sites []int
		for i, line := range lines {
			if strings.HasPrefix(line, "derive ") {
				sites = append(sites, i)
			}
		}
		if len(sites) == 0 {
			return nil, false
		}
		i := sites[r.Intn(len(sites))]
		return append(lines[:i], lines[i+1:]...), true
	}))
	add(lineEdit(p, r, "a once's value", func(lines []string) ([]string, bool) {
		for i, line := range lines {
			if strings.HasPrefix(line, "once ") && !strings.ContainsAny(line, "\"`") && intToken.MatchString(line[strings.Index(line, "="):]) {
				eq := strings.Index(line, "=")
				lines[i] = line[:eq] + bumpInt(line[eq:])
				return lines, true
			}
		}
		return nil, false
	}))
	add(lineEdit(p, r, "a default argument", func(lines []string) ([]string, bool) {
		for i, line := range lines {
			if m := defaultArg.FindStringSubmatchIndex(line); m != nil && !strings.ContainsAny(line, "\"`") {
				n, _ := strconv.Atoi(line[m[6]:m[7]])
				lines[i] = line[:m[6]] + strconv.Itoa(n+1) + line[m[7]:]
				return lines, true
			}
		}
		return nil, false
	}))
	add(lineEdit(p, r, "an impl of Display added", func(lines []string) ([]string, bool) {
		text := strings.Join(lines, "\n")
		for _, line := range lines {
			m := structLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name := m[3]
			if strings.Contains(text, "Display for "+name) || allText(p, "Display for "+name) {
				continue
			}
			impl := fmt.Sprintf("impl Display for %s\n    fn to_string(%s) -> String\n        \"edited\"\n    end\nend\n", name, name)
			return append(lines, strings.Split(impl, "\n")...), true
		}
		return nil, false
	}))
	if text, ok := p.files["nomi.toml"]; ok {
		out = append(out, skipEdit{name: "nomi.toml", files: map[string]string{"nomi.toml": text + "\n# edited\n"}})
	}
	out = append(out, skipEdit{name: "a file no program loads", files: map[string]string{"zz_unloaded.nomi": "pub fn zz_unloaded() -> Int\n    1\nend\n"}})
	add(lineEdit(p, r, "a comment in a file", func(lines []string) ([]string, bool) {
		return append(lines, "// edited"), true
	}))
	out = append(out, skipEdit{name: "--timeout", timeout: 7 * time.Second}, skipEdit{name: "another compiler", compiler: "another build of nomi"})
	if p.gen != nil {
		for _, kind := range projectgen.EditKinds {
			key, text := p.gen.Edit(kind, 0)
			out = append(out, skipEdit{name: string(kind), files: map[string]string{key + ".nomi": text}})
		}
	}
	return out
}

// TestTestCommand_UnchangedTestsDoNotRun runs `nomi test` over one file
// with the artifact cache: the second run skips the test with no effects
// and runs the one that prints, `--all` runs both, `--changed` skips both
// and loads nothing, answering the file from its passes record, and an edit
// of the function the pure test calls runs it again.
func TestTestCommand_UnchangedTestsDoNotRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	dir := t.TempDir()
	cache := newCacheDir(t)
	src := "import std::io\n\nfn double(n: Int) -> Int\n    n * 2\nend\n\n" +
		"test \"pure\"\n    assert double(2) == 4\nend\n\n" +
		"test \"prints\"\n    io.print(\"hi\")\n    assert double(3) == 6\nend\n"
	mustWrite(t, filepath.Join(dir, "t_test.nomi"), src)
	for _, step := range []struct {
		name string
		args []string
		want []string
		// answered says the file is answered from its passes record.
		answered bool
	}{
		{"the first run", nil, []string{"ok t_test.nomi :: pure\n", "ok t_test.nomi :: prints\n  hi\n", "test result: ok. 2 passed, 0 failed\n"}, false},
		{"the second run", nil, []string{"ok t_test.nomi :: pure (unchanged)\n", "ok t_test.nomi :: prints\n  hi\n", "test result: ok. 2 passed, 0 failed; 1 ran, 1 unchanged\n"}, false},
		{"--all", []string{"--all"}, []string{"ok t_test.nomi :: pure\n", "ok t_test.nomi :: prints\n  hi\n", "test result: ok. 2 passed, 0 failed\n"}, false},
		{"--changed", []string{"--changed"}, []string{"ok t_test.nomi :: pure (unchanged)\n", "ok t_test.nomi :: prints (unchanged)\n", "test result: ok. 2 passed, 0 failed; 0 ran, 2 unchanged\n"}, true},
		{"an edit of double", nil, []string{"ok t_test.nomi :: pure\n", "ok t_test.nomi :: prints\n  hi\n", "test result: ok. 2 passed, 0 failed\n"}, false},
	} {
		if step.name == "an edit of double" {
			mustWrite(t, filepath.Join(dir, "t_test.nomi"), strings.Replace(src, "n * 2", "n + n", 1))
		}
		got := runNomiIn(t, dir, cache, append(append([]string{"test"}, step.args...), "t_test.nomi")...)
		for _, want := range step.want {
			if got.exit != 0 || !strings.Contains(got.stdout, want) {
				t.Fatalf("after %s, `nomi test` printed\n%s\nwant it to hold %q", step.name, got, want)
			}
		}
		answered := slices.ContainsFunc(got.report, func(line string) bool {
			return strings.HasSuffix(line, "(passes): answered from the record: 2 cases unchanged")
		})
		if answered != step.answered {
			t.Fatalf("after %s, the file was answered from its passes record: %v, want %v\n%s", step.name, answered, step.answered, strings.Join(got.report, "\n"))
		}
	}
	got := runNomiIn(t, dir, cache, "test", "--all", "--changed", "t_test.nomi")
	if got.exit == 0 || !strings.Contains(got.stderr, "--all and --changed cannot be used together") {
		t.Fatalf("`nomi test --all --changed` printed\n%s\nwant it refused", got)
	}
}

// TestTestCommand_ReachingRunsTheTestsThatReachAFunction runs `nomi test
// --reaching`: a line of a function, its declaration's or its body's,
// runs the tests whose code can call it and no other, the JSON records say
// which has effects, and a line no function holds is an error that names
// it, as `--run` matching nothing is.
func TestTestCommand_ReachingRunsTheTestsThatReachAFunction(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi; -short")
	}
	dir := t.TempDir()
	cache := newCacheDir(t)
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"reach\"\n")
	mustWrite(t, filepath.Join(dir, "shapes.nomi"), "pub fn sides() -> Int\n    4\nend\n\npub fn corners() -> Int\n    sides()\nend\n\npub fn edges() -> Int\n    12\nend\n")
	mustWrite(t, filepath.Join(dir, "shapes_test.nomi"), "import\n    std::io\n    shapes\nend\n\n"+
		"test \"sides\"\n    assert shapes.sides() == 4\nend\n\n"+
		"test \"corners\"\n    assert shapes.corners() == 4\nend\n\n"+
		"test \"edges\"\n    assert shapes.edges() == 12\nend\n\n"+
		"test \"prints\"\n    io.print(\"${shapes.corners()}\")\n    assert True\nend\n")
	for _, at := range []string{"shapes.nomi:1", "shapes.nomi:2"} {
		got := runNomiIn(t, dir, cache, "test", "--reaching", at, "--format", "json")
		run := map[string]skipRecord{}
		var effects []string
		for _, line := range strings.Split(strings.TrimSpace(got.stdout), "\n") {
			var r struct {
				skipRecord
				Effects bool `json:"effects"`
			}
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				t.Fatalf("a record of `nomi test --reaching %s` does not read: %v\n%s", at, err, line)
			}
			if r.Type == "test" {
				_, name, _ := strings.Cut(r.Name, " :: ")
				run[name] = r.skipRecord
				if r.Effects {
					effects = append(effects, name)
				}
			}
		}
		names := slices.Sorted(maps.Keys(run))
		if got.exit != 0 || !slices.Equal(names, []string{"corners", "prints", "sides"}) || !slices.Equal(effects, []string{"prints"}) {
			t.Fatalf("`nomi test --reaching %s` ran %v, with effects %v (exit %d), want corners, prints and sides, prints with effects\n%s", at, names, effects, got.exit, got)
		}
	}
	got := runNomiIn(t, dir, cache, "test", "--reaching", "nomi.toml:1")
	if got.exit == 0 || !strings.Contains(got.stderr, "no test reaches nomi.toml:1") {
		t.Fatalf("`nomi test --reaching nomi.toml:1` printed\n%s\nwant it refused, naming the line", got)
	}
}

// allText reports whether any file of p holds s.
func allText(p skipProject, s string) bool {
	for _, text := range p.files {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

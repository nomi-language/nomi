package vmhost

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
)

// The rule these tests hold the compiler to: a program the front end accepts
// lowers, and the checker has given every one of its value expressions a
// resolved type (checker_gaps_test.go). Program.Unsupported is what `nomi
// check` and the language server report for a body the IR builder declines,
// and what `nomi run` stops with before its first effect; a program the type
// checker accepts should never reach it.
//
// An input is one source text. With `// FILE: <path>` lines it is several
// files, the first of them the entry; without, it is one file, main.nomi.
// The files are written to a fresh directory and checked there as
// `nomi check <entry>` checks a file (Check), so sibling imports, nested
// directories, nomi.toml and `go` bindings resolve as they do on disk.
// Nothing runs.
//
// FuzzFrontEndAcceptsSoItLowers mutates the seeds byte by byte.
// TestFrontEndAcceptsSoItLowers checks every seed and every generated program
// (lowering_gen_test.go) once, under plain `go test`.

// lowerOutcome is what checking one input found.
type lowerOutcome struct {
	// accepted reports that the front end accepted the program.
	accepted bool
	// rejection is the front end's error when it did not.
	rejection string
	// declines are the diagnostics Program.Unsupported reported, each with
	// its hints.
	declines []string
	// reasons are the builder's reasons, one per decline ("[name] reason").
	reasons []string
	// unplaced are the declines that lost their cause: a diagnostic with no
	// source line, or one whose builder reason is missing or says no site
	// named one. Each is a compiler bug whatever the decline itself is, so
	// no known gap excuses it.
	unplaced []string
	// internal is a compiler panic, converted to an InternalError or not.
	internal string
	// unresolved are the value expressions of an accepted program the
	// checker left without a resolved type (analysis.UnresolvedExprs), each
	// as "file:line:col: what".
	unresolved []string
}

func (o lowerOutcome) failed() bool {
	return o.internal != "" || len(o.declines) > 0 || len(o.unresolved) > 0 || len(o.unplaced) > 0
}

// describe is the failure text for src: the source, then what went wrong.
func (o lowerOutcome) describe(src string) string {
	var b strings.Builder
	b.WriteString("the front end accepts this program:\n\n")
	b.WriteString(numbered(src))
	if o.internal != "" {
		b.WriteString("\nand the compiler panicked:\n" + o.internal)
		return b.String()
	}
	if len(o.unresolved) > 0 {
		b.WriteString("\nand the checker leaves these expressions without a resolved type:\n")
		for _, u := range o.unresolved {
			b.WriteString("  " + u + "\n")
		}
	}
	if len(o.unplaced) > 0 {
		b.WriteString("\nand these declines name no cause or no position:\n")
		for _, u := range o.unplaced {
			b.WriteString("  " + strings.ReplaceAll(u, "\n", "\n  ") + "\n")
		}
	}
	if len(o.declines) > 0 {
		b.WriteString("\nand the IR builder declines it:\n")
		for _, d := range o.declines {
			b.WriteString("  " + strings.ReplaceAll(d, "\n", "\n  ") + "\n")
		}
	}
	return b.String()
}

func numbered(src string) string {
	var b strings.Builder
	for i, line := range strings.Split(src, "\n") {
		fmt.Fprintf(&b, "%4d | %s\n", i+1, line)
	}
	return b.String()
}

var fuzzFileMarker = regexp.MustCompile(`^//\s*FILE:\s*([A-Za-z0-9_.\-/]+)\s*$`)

// splitInput is src's files, entry first. ok is false for an input that
// names a file outside its directory, or too many or too large files for a
// check to be worth its time.
func splitInput(src string) (names []string, files map[string]string, ok bool) {
	if len(src) > 1<<16 {
		return nil, nil, false
	}
	files = map[string]string{}
	current := ""
	var body strings.Builder
	flush := func() {
		if current != "" {
			files[current] += body.String()
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(src, "\n") {
		if m := fuzzFileMarker.FindStringSubmatch(strings.TrimRight(line, "\n")); m != nil {
			flush()
			current = m[1]
			if _, dup := files[current]; !dup {
				names = append(names, current)
				files[current] = ""
			}
			continue
		}
		body.WriteString(line)
	}
	if current == "" {
		return []string{"main.nomi"}, map[string]string{"main.nomi": src}, true
	}
	flush()
	if len(names) > 16 || !strings.HasSuffix(names[0], ".nomi") {
		return nil, nil, false
	}
	for _, name := range names {
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.Contains(name, "//") {
			return nil, nil, false
		}
		if !strings.HasSuffix(name, ".nomi") && name != "nomi.toml" && !strings.HasSuffix(name, "/nomi.toml") &&
			!strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "go.mod") {
			return nil, nil, false
		}
	}
	return names, files, true
}

const lowerReasonHint = "the lowering's reason: "

// checkLowers writes src's files under dir and checks the entry as `nomi
// check` does (Check): the front end, then the lowering, then
// Program.Unsupported. NOMI_DEBUG_LOWERING must be set for the builder's
// reasons to be recorded.
func checkLowers(dir, src string) (out lowerOutcome) {
	entry, err := writeInput(dir, src)
	if err != nil {
		return lowerOutcome{rejection: err.Error()}
	}
	defer func() {
		if r := recover(); r != nil {
			out = lowerOutcome{accepted: true, internal: fmt.Sprintf("%v\n%s", r, debug.Stack())}
		}
	}()
	cfg := newConfig(nil)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return lowerOutcome{rejection: err.Error()}
	}
	fc.SourceBoundProvided = true
	var analyzed *irbuild.Program
	p, err := lower(cfg, func() (*irbuild.Program, error) {
		prog, err := irbuild.AnalyzeFile(entry, fc)
		analyzed = prog
		return prog, err
	})
	if err != nil {
		var ie *InternalError
		if errors.As(err, &ie) {
			return lowerOutcome{accepted: true, internal: fmt.Sprintf("%v\n%s", ie.Panic, ie.Stack)}
		}
		return lowerOutcome{rejection: err.Error()}
	}
	out.accepted = true
	out.unresolved = unresolvedIn(dir, analyzed)
	u := p.Unsupported()
	if u == nil {
		return out
	}
	var ds Diagnostics
	if !errors.As(u, &ds) {
		out.declines, out.reasons = []string{u.Error()}, []string{u.Error()}
		return out
	}
	for _, d := range ds {
		text := strings.ReplaceAll(d.String(), dir+string(filepath.Separator), "")
		out.declines = append(out.declines, text)
		reason := "(no reason recorded)"
		for _, h := range d.Hints {
			if r, ok := strings.CutPrefix(h, lowerReasonHint); ok {
				reason = r
			}
		}
		out.reasons = append(out.reasons, reason)
		if d.Line <= 0 || unnamedDecline(reason) {
			out.unplaced = append(out.unplaced, text)
		}
	}
	return out
}

// unnamedDecline reports whether a decline's reason hint says the builder
// recorded no cause for it.
func unnamedDecline(reason string) bool {
	for _, lost := range []string{
		"(no reason recorded)",
		"no decline reason recorded",
		"the builder declined without naming a reason",
	} {
		if strings.Contains(reason, lost) {
			return true
		}
	}
	return false
}

// writeInput writes src's files under dir and returns the entry's path.
func writeInput(dir, src string) (string, error) {
	names, files, ok := splitInput(src)
	if !ok {
		return "", errors.New("not an input this harness checks")
	}
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, filepath.FromSlash(names[0])), nil
}

// seedUnresolved writes src under dir and runs the front end alone over
// it, as checkLowers does before it lowers, and returns unresolvedIn's
// reports. A rejected input, or one the front end panics on, has none:
// checkLowers reports a panic.
func seedUnresolved(dir, src string, fc frontend.Config) (reports []string) {
	entry, err := writeInput(dir, src)
	if err != nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			reports = nil
		}
	}()
	fc.SourceBoundProvided = true
	prog, err := irbuild.AnalyzeFile(entry, fc)
	if err != nil {
		return nil
	}
	return unresolvedIn(dir, prog)
}

// unresolvedIn is every value expression of prog's files that the checker
// left without a resolved type, as "file:line:col: what" with file relative
// to dir.
func unresolvedIn(dir string, prog *irbuild.Program) []string {
	if prog == nil {
		return nil
	}
	var out []string
	for _, m := range prog.Modules {
		if m.FA == nil {
			continue
		}
		name, err := filepath.Rel(dir, m.Path)
		if err != nil {
			name = m.Path
		}
		out = append(out, describeUnresolved(filepath.ToSlash(name), m.Path, analysis.UnresolvedExprs(m.FA, m.Nodes))...)
	}
	return out
}

// describeUnresolved is each of us, found in the file at path, as
// "name:line:col: what: `source line`".
func describeUnresolved(name, path string, us []analysis.UnresolvedExpr) []string {
	if len(us) == 0 {
		return nil
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(string(data), "\n")
	var out []string
	for _, u := range us {
		text := ""
		if u.Line >= 1 && u.Line <= len(lines) {
			text = strings.TrimSpace(lines[u.Line-1])
		}
		out = append(out, fmt.Sprintf("%s:%s: `%s`", name, u, text))
	}
	return out
}

// loweringSeed is one input: a name that says where it came from, and its
// source.
type loweringSeed struct {
	name string
	src  string
}

// loweringSeeds are every corpus file under tests/ (with the files of its
// project it names, as `// FILE:` sections), every program a corpus file
// embeds in a triple-quoted string for std/compiler to run, every runnable
// tour block, and every complete program in docs/spec.md.
func loweringSeeds(tb testing.TB) []loweringSeed {
	tb.Helper()
	seeds := corpusSeeds(tb)
	seeds = append(seeds, tourSeeds(tb)...)
	seeds = append(seeds, specSeeds(tb)...)
	return seeds
}

func corpusSeeds(tb testing.TB) []loweringSeed {
	const root = "../tests"
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".nomi") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		tb.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(paths)
	var seeds []loweringSeed
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		src, err := bundleCorpusFile(path)
		if err != nil {
			tb.Fatalf("bundle %s: %v", path, err)
		}
		seeds = append(seeds, loweringSeed{name: "tests/" + rel, src: src})
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		for i, prog := range embeddedPrograms(string(data)) {
			seeds = append(seeds, loweringSeed{name: fmt.Sprintf("tests/%s#%d", rel, i+1), src: prog})
		}
	}
	return seeds
}

var (
	topLevelMain = regexp.MustCompile(`(?m)^fn\s+main\(`)
	topLevelTest = regexp.MustCompile(`(?m)^(test "|tests ")`)
	wordRe       = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// bundleCorpusFile is the corpus file at path as one input: the file first,
// then each file of its project (the nearest directory below tests/ holding
// a nomi.toml, else the file's own directory; a file directly in a category
// directory has only its siblings there) that an included file names by a
// path segment, and the project's nomi.toml and Go files.
func bundleCorpusFile(path string) (string, error) {
	dir := filepath.Dir(path)
	root := dir
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "nomi.toml")); err == nil {
			root = d
			break
		}
		if filepath.Base(filepath.Dir(d)) == "tests" || filepath.Base(d) == "tests" {
			break
		}
	}
	files := map[string]string{}
	var extra []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// A category directory's subdirectories are projects of their own.
			if p != root && filepath.Base(filepath.Dir(root)) == "tests" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case strings.HasSuffix(p, ".nomi"):
		case rel == "nomi.toml" || rel == "go.mod" || (strings.HasSuffix(p, ".go") && !strings.Contains(rel, "/")):
			extra = append(extra, rel)
		default:
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	entryRel, _ := filepath.Rel(root, path)
	entryRel = filepath.ToSlash(entryRel)
	words := map[string]bool{}
	addWords := func(src string) {
		for _, w := range wordRe.FindAllString(src, -1) {
			words[w] = true
		}
	}
	addWords(files[entryRel])
	included := map[string]bool{entryRel: true}
	for changed := true; changed; {
		changed = false
		for rel, src := range files {
			if included[rel] || !strings.HasSuffix(rel, ".nomi") {
				continue
			}
			for _, seg := range strings.Split(strings.TrimSuffix(rel, ".nomi"), "/") {
				if words[seg] {
					included[rel] = true
					addWords(src)
					changed = true
					break
				}
			}
		}
	}
	var siblings []string
	for rel := range included {
		if rel != entryRel {
			siblings = append(siblings, rel)
		}
	}
	if len(siblings) == 0 && len(extra) == 0 && entryRel == filepath.Base(path) {
		return files[entryRel], nil
	}
	sort.Strings(siblings)
	sort.Strings(extra)
	var b strings.Builder
	section := func(name string) {
		body := files[name]
		b.WriteString("// FILE: " + name + "\n")
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	section(entryRel)
	for _, rel := range siblings {
		section(rel)
	}
	for _, rel := range extra {
		section(rel)
	}
	return b.String(), nil
}

var tripleQuoted = regexp.MustCompile(`(?s)"""\n(.*?)\n[ \t]*"""`)

// embeddedPrograms are the triple-quoted strings in src that hold a whole
// program (a `fn main` or a test), dedented as the string's value is. One
// that interpolates is a template, not a program, and is left out.
func embeddedPrograms(src string) []string {
	var out []string
	for _, m := range tripleQuoted.FindAllStringSubmatch(src, -1) {
		body := dedent(m[1])
		if strings.Contains(strings.ReplaceAll(body, `\${`, ""), "${") {
			continue
		}
		body = strings.ReplaceAll(body, `\${`, "${")
		if !topLevelMain.MatchString(body) && !topLevelTest.MatchString(body) {
			continue
		}
		out = append(out, body)
	}
	return out
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	min := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if min < 0 || n < min {
			min = n
		}
	}
	for i, l := range lines {
		if min > 0 && len(l) >= min {
			lines[i] = l[min:]
		} else {
			lines[i] = strings.TrimLeft(l, " \t")
		}
	}
	return strings.Join(lines, "\n")
}

// tourSeeds are the tour's runnable blocks. A multi-file block is reordered
// so that the file the playground runs comes first.
func tourSeeds(tb testing.TB) []loweringSeed {
	root := filepath.Clean("../tour/src/content/docs")
	var chapters []string
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
		tb.Fatalf("walk tour content: %v", err)
	}
	sort.Strings(chapters)
	var seeds []loweringSeed
	for _, chapter := range chapters {
		data, err := os.ReadFile(chapter)
		if err != nil {
			tb.Fatalf("read %s: %v", chapter, err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi-run") {
			if b.HasInfo("ignore") {
				continue
			}
			src := b.Code
			if strings.Contains(src, "// FILE:") {
				entrySrc, entryName, files, manifest, err := splitMultiFile(src)
				if err != nil {
					tb.Fatalf("%s:%d: %v", chapter, b.Line, err)
				}
				var sb strings.Builder
				sb.WriteString("// FILE: " + entryName + ".nomi\n" + entrySrc + "\n")
				var names []string
				for name := range files {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					sb.WriteString("// FILE: " + name + ".nomi\n" + files[name] + "\n")
				}
				if manifest != nil {
					if toml := tomlSection(b.Code); toml != "" {
						sb.WriteString("// FILE: nomi.toml\n" + toml + "\n")
					}
				}
				src = sb.String()
			}
			seeds = append(seeds, loweringSeed{name: fmt.Sprintf("tour/%s:L%d", filepath.Base(chapter), b.Line), src: src})
		}
	}
	return seeds
}

// tomlSection is the body of a block's `// FILE: nomi.toml` section.
func tomlSection(src string) string {
	_, files, ok := splitInput(src)
	if !ok {
		return ""
	}
	return files["nomi.toml"]
}

var specMain = regexp.MustCompile(`(?m)^fn main\(`)

// specSeeds are the spec's complete programs, as TestSpecPrograms selects
// them.
func specSeeds(tb testing.TB) []loweringSeed {
	const specPath = "../docs/spec.md"
	data, err := os.ReadFile(specPath)
	if err != nil {
		tb.Fatalf("read %s: %v", specPath, err)
	}
	var seeds []loweringSeed
	for _, b := range doctest.ExtractBlocks(string(data), "nomi") {
		if !specMain.MatchString(b.Code) || strings.Contains(b.Code, "...") {
			continue
		}
		seeds = append(seeds, loweringSeed{name: fmt.Sprintf("spec:L%d", b.Line), src: b.Code})
	}
	return seeds
}

// fuzzing reports whether this process runs with -fuzz: the seeds are then
// the fuzzer's starting corpus. Under plain `go test` the corpus, tour and
// spec seeds are held to the rule by the tests that run them, and
// TestFrontEndAcceptsSoItLowers checks the generated and embedded programs.
func fuzzing() bool {
	f := flag.Lookup("test.fuzz")
	return f != nil && f.Value.String() != ""
}

// FuzzFrontEndAcceptsSoItLowers fails for an input the front end accepts
// and the IR builder declines, one whose expressions the checker leaves
// without a resolved type (checker_gaps_test.go), or one that panics the
// compiler. An input whose every decline, untyped expression or panic
// matches a known gap (knownLoweringGaps, knownCheckerGaps) is skipped, so a
// run reports new causes. Run it with
//
//	go test ./vmhost -run '^$' -fuzz '^FuzzFrontEndAcceptsSoItLowers$' -fuzztime 5m
//
// A failing input is written under vmhost/testdata/fuzz/; plain `go test`
// replays everything there, so triage an input before committing it.
func FuzzFrontEndAcceptsSoItLowers(f *testing.F) {
	f.Setenv("NOMI_DEBUG_LOWERING", "1")
	if fuzzing() {
		for _, s := range loweringSeeds(f) {
			f.Add(s.src)
		}
		for _, g := range generatedPrograms() {
			f.Add(g.src)
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		o := checkLowers(t.TempDir(), src)
		if !o.failed() {
			return
		}
		if o.internal != "" {
			if gap := knownPanicFor(o.internal); gap != nil {
				t.Skipf("a known panic: %s", gap.name)
			}
			t.Fatal(o.describe(src))
		}
		if len(o.unplaced) > 0 {
			t.Fatal(o.describe(src))
		}
		var known []string
		for _, u := range o.unresolved {
			gap := knownCheckerGapFor(u)
			if gap == nil {
				t.Fatal(o.describe(src))
			}
			known = append(known, gap.name)
		}
		if len(o.declines) > 0 {
			gap := knownGapFor(o.reasons)
			if gap == nil {
				t.Fatal(o.describe(src))
			}
			known = append(known, gap.name)
		}
		t.Skipf("known causes: %s", strings.Join(known, ", "))
	})
}

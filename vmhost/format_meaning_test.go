package vmhost

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/doctest"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/rotation"
)

// The rule these tests hold `nomi fmt` to: formatting never changes what a
// program means. For every .nomi file of an input that parses:
//
//   - formatting does not panic, and the formatted text parses;
//   - its syntax tree equals the source's, apart from positions, comment
//     placement and the reorderings the formatter makes, and it holds the
//     same comments (format.SameMeaning);
//   - formatting the formatted text changes nothing;
//   - when formatting changed a file, the front end accepts the formatted
//     program exactly when it accepts the source. The fuzz target checks this for every input;
//     TestFormatKeepsMeaning, to stay fast, for one variant of a third of
//     the generated programs. It catches what the syntax tree hides: the checker
//     reads parentheses that the tree comparison treats as transparent.
//
// Inputs are this package's lowering seeds (lowering_fuzz_test.go: corpus
// files with their projects, embedded programs, runnable tour blocks and the
// spec's complete programs), every other tracked .nomi file, every plain
// `nomi` block of the tour and the spec, and the lowering generator's
// programs (lowering_gen_test.go). Those are almost all formatted already,
// so each is also rewritten into layout variants (layoutVariant): respaced,
// broken across lines inside expressions, with comments between tokens,
// with grouping parentheses added or removed, and with names long enough to
// force wraps. A variant need not mean what its seed means; it is held to
// the rule as an input of its own.
//
// FuzzFormatKeepsMeaning mutates the seeds and variants byte by byte.
// TestFormatKeepsMeaning checks the seeds, the generated programs and a
// fixed set of variants once, under plain `go test`, and
// TestFormatKeepsMeaningRotating a set of variants that changes every day.

// formatFailure is one break of the rule.
type formatFailure struct {
	// kind is "panic", "error", "meaning", "idempotence" or "front end".
	kind string
	// file is the input's file the failure is in, "" for the front end.
	file string
	msg  string
	// formatted is the formatter's output for file, when it gave one.
	formatted string
}

// formatOutcome is what checking one input found.
type formatOutcome struct {
	// parsed counts the input's .nomi files that parse.
	parsed int
	// changed reports that formatting changed some file.
	changed  bool
	failures []formatFailure
}

func (o formatOutcome) describe(src string) string {
	var b strings.Builder
	b.WriteString("formatting this input breaks the rule that it keeps meaning:\n\n")
	b.WriteString(numbered(src))
	for _, f := range o.failures {
		fmt.Fprintf(&b, "\n%s", f.kind)
		if f.file != "" {
			fmt.Fprintf(&b, " in %s", f.file)
		}
		b.WriteString(": " + f.msg + "\n")
		if f.formatted != "" {
			b.WriteString("formatted:\n" + numbered(f.formatted))
		}
	}
	return b.String()
}

// checkFormat holds src's .nomi files to the rule. With frontEnd set, an
// input whose formatting changed a file is checked by the front end before
// and after, in a fresh directory under dir. An input with Go files is not:
// checking its bindings runs the Go toolchain.
func checkFormat(dir, src string, frontEnd bool) (out formatOutcome) {
	names, files, ok := splitInput(src)
	if !ok {
		return out
	}
	formatted := map[string]string{}
	for _, name := range names {
		if !strings.HasSuffix(name, ".nomi") {
			continue
		}
		text := files[name]
		if _, _, err := parser.ParseFile(lexer.Lex(text)); err != nil {
			continue
		}
		out.parsed++
		once, f := formatOne(text)
		if f != nil {
			f.file = name
			out.failures = append(out.failures, *f)
			continue
		}
		if once != text {
			out.changed = true
		}
		formatted[name] = once
		if err := format.SameMeaning(text, once); err != nil {
			out.failures = append(out.failures, formatFailure{kind: "meaning", file: name, msg: err.Error(), formatted: once})
			continue
		}
		twice, f := formatOne(once)
		if f != nil {
			f.file, f.msg = name, "formatting the formatted text: "+f.msg
			f.formatted = once
			out.failures = append(out.failures, *f)
			continue
		}
		if twice != once {
			out.failures = append(out.failures, formatFailure{kind: "idempotence", file: name,
				msg:       "formatting the formatted text changes it again, first at " + firstChange(once, twice) + "\nformatted twice:\n" + numbered(twice),
				formatted: once})
		}
	}
	if !frontEnd || !out.changed || len(out.failures) > 0 || !strings.HasSuffix(names[0], ".nomi") {
		return out
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".go") {
			return out
		}
	}
	after := map[string]string{}
	for name, text := range files {
		after[name] = text
		if f, ok := formatted[name]; ok {
			after[name] = f
		}
	}
	before := frontEndDiagnostics(filepath.Join(dir, "source"), names, files)
	got := frontEndDiagnostics(filepath.Join(dir, "formatted"), names, after)
	// Acceptance must agree. When both are rejected the counts may differ:
	// recovery after a first error depends on the source's shape.
	if (before.count == 0) != (got.count == 0) || before.internal != got.internal {
		out.failures = append(out.failures, formatFailure{kind: "front end",
			msg: fmt.Sprintf("the source has %d diagnostics and the formatted program %d\nsource:\n%s\nformatted:\n%s",
				before.count, got.count, before.text, got.text)})
	}
	return out
}

// firstChange is the first line where b differs from a, as
// "line N: `a's line` became `b's line`" with the lines trimmed.
func firstChange(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = strings.TrimSpace(al[i])
		}
		if i < len(bl) {
			y = strings.TrimSpace(bl[i])
		}
		if i >= len(al) || i >= len(bl) || al[i] != bl[i] {
			return fmt.Sprintf("line %d: `%s` became `%s`", i+1, x, y)
		}
	}
	return "no line"
}

// formatOne formats one file's text, as a failure when Format panics or
// refuses text that parses.
func formatOne(text string) (out string, failure *formatFailure) {
	defer func() {
		if r := recover(); r != nil {
			failure = &formatFailure{kind: "panic", msg: fmt.Sprintf("%v\n%s", r, debug.Stack())}
		}
	}()
	out, err := format.Format(text)
	if err != nil {
		return "", &formatFailure{kind: "error", msg: "Format refuses text that parses: " + err.Error()}
	}
	return out, nil
}

type frontEndResult struct {
	count    int
	text     string
	internal string
}

// frontEndDiagnostics writes files under dir and runs the front end over
// the entry, names[0], as `nomi check` does before it lowers.
func frontEndDiagnostics(dir string, names []string, files map[string]string) (res frontEndResult) {
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return frontEndResult{count: -1, text: err.Error()}
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			return frontEndResult{count: -1, text: err.Error()}
		}
	}
	entry := filepath.Join(dir, filepath.FromSlash(names[0]))
	defer func() {
		if r := recover(); r != nil {
			res = frontEndResult{internal: "the front end panicked", text: fmt.Sprintf("%v\n%s", r, debug.Stack())}
		}
	}()
	cfg := newConfig(nil)
	fc, err := cfg.frontendConfig()
	if err != nil {
		return frontEndResult{count: -1, text: err.Error()}
	}
	fc.SourceBoundProvided = true
	hasTests, _ := frontend.FileDeclaresTests(entry)
	_, err = frontend.New(fc).CheckFile(entry, frontend.Mode{Tests: hasTests})
	if err == nil {
		return res
	}
	text := strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), "")
	var ds frontend.Diagnostics
	if errors.As(err, &ds) {
		return frontEndResult{count: len(ds), text: text}
	}
	return frontEndResult{count: 1, text: text}
}

// formatSeeds are the lowering seeds, every other tracked .nomi file, and
// every plain `nomi` block in the tour and the spec. The tracked files
// under tests/ are among the lowering seeds already, with their projects.
func formatSeeds(tb testing.TB) []loweringSeed {
	tb.Helper()
	seeds := loweringSeeds(tb)
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		tb.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	root := strings.TrimSpace(string(top))
	list, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.nomi").Output()
	if err != nil {
		tb.Fatalf("git ls-files: %v", err)
	}
	for _, rel := range strings.Split(string(list), "\x00") {
		if rel == "" || strings.HasPrefix(rel, "tests/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			tb.Fatal(err)
		}
		seeds = append(seeds, loweringSeed{name: rel, src: string(data)})
	}
	docs := []string{"../docs/spec.md"}
	chapters, err := filepath.Glob("../tour/src/content/docs/*.md*")
	if err != nil {
		tb.Fatal(err)
	}
	sort.Strings(chapters)
	docs = append(docs, chapters...)
	for _, path := range docs {
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		for _, b := range doctest.ExtractBlocks(string(data), "nomi") {
			seeds = append(seeds, loweringSeed{name: fmt.Sprintf("%s:L%d", filepath.Base(path), b.Line), src: b.Code + "\n"})
		}
	}
	return seeds
}

// seedHash is a stable number for name, so a variant's name always means
// the same text.
func seedHash(name string) int64 {
	h := fnv.New64a()
	h.Write([]byte(name))
	return int64(h.Sum64() >> 1)
}

// formatVariants is how many layout variants TestFormatKeepsMeaning checks
// per seed and per generated program.
const formatVariants = 2

// FuzzFormatKeepsMeaning fails for an input that formatting changes the
// meaning of, that it formats differently a second time, or that panics
// the formatter. A failure matching a known gap (knownFormatGaps) is
// skipped, so a run reports new causes. Run it with
//
//	go test ./vmhost -run '^$' -fuzz '^FuzzFormatKeepsMeaning$' -fuzztime 5m -parallel 4
//
// A failing input is written under vmhost/testdata/fuzz/; plain `go test`
// replays everything there, so triage an input before committing it.
func FuzzFormatKeepsMeaning(f *testing.F) {
	if fuzzing() {
		for _, s := range formatSeeds(f) {
			f.Add(s.src)
			f.Add(layoutVariant(s.src, seedHash(s.name)))
		}
		for _, g := range generatedPrograms() {
			f.Add(layoutVariant(g.src, seedHash(g.name)))
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		o := checkFormat(t.TempDir(), src, true)
		if len(o.failures) == 0 {
			return
		}
		if gap := knownFormatGapFor(o.failures); gap != nil {
			t.Skipf("a known formatter gap: %s", gap.name)
		}
		t.Fatal(o.describe(src))
	})
}

// checkFormatAll formats and checks n inputs on every core; input(i) is
// the i-th input's source and whether the front end checks it.
func checkFormatAll(t *testing.T, n int, input func(i int) (string, bool)) []formatOutcome {
	outcomes := make([]formatOutcome, n)
	dir := t.TempDir()
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				src, frontEnd := input(i)
				outcomes[i] = checkFormat(filepath.Join(dir, strconv.Itoa(i)), src, frontEnd)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
	return outcomes
}

// TestFormatKeepsMeaningRotating checks formatVariants more layout variants
// of every seed and generated program than TestFormatKeepsMeaning does, a
// different pair every UTC day (internal/rotation). Rotating seed r lays out
// the input named n as layoutVariant(src, seedHash(n)+r), and the front end
// checks the variant of a third of the generated programs, as in the fixed
// set. NOMI_GEN_SEED pins the start; a failure names its seed and input and
// the command that checks exactly that variant. A failure matching a known
// gap is excused.
func TestFormatKeepsMeaningRotating(t *testing.T) {
	set := rotation.For(t, "./vmhost", formatVariants)
	type input struct {
		name, src string
		seed      int64
		frontEnd  bool
	}
	var inputs []input
	sources := formatSeeds(t)
	gen := len(sources)
	sources = append(sources, generatedPrograms()...)
	for _, r := range set.Seeds() {
		for i, s := range sources {
			if !rotation.Wants(s.name) {
				continue
			}
			frontEnd := i >= gen && (seedHash(s.name)+r)%3 == 0
			inputs = append(inputs, input{s.name, layoutVariant(s.src, seedHash(s.name)+r), r, frontEnd})
		}
	}
	outcomes := checkFormatAll(t, len(inputs), func(i int) (string, bool) { return inputs[i].src, inputs[i].frontEnd })
	parsed, excused := 0, 0
	for i, in := range inputs {
		o := outcomes[i]
		if o.parsed > 0 {
			parsed++
		}
		if len(o.failures) == 0 {
			continue
		}
		if knownFormatGapFor(o.failures) != nil {
			excused++
			continue
		}
		t.Errorf("%s: %s%s", in.name, set.FailureOf(in.seed, in.name), o.describe(in.src))
	}
	t.Logf("%d variants, %d of them parse; %d excused by a known gap", len(inputs), parsed, excused)
}

// TestFormatKeepsMeaning checks every seed, every generated program and
// formatVariants layout variants of each under plain `go test`. A failure
// matching a known gap is excused; the gap's reproducer is held to it by
// TestKnownFormatGaps.
func TestFormatKeepsMeaning(t *testing.T) {
	type input struct {
		loweringSeed
		frontEnd bool
		// seed is the index of a variant's seed, -1 for a seed.
		seed int
	}
	var inputs []input
	add := func(s loweringSeed) {
		seed := len(inputs)
		inputs = append(inputs, input{s, false, -1})
		for v := 0; v < formatVariants; v++ {
			name := fmt.Sprintf("%s~layout%d", s.name, v)
			// The front end runs on the first variant of a third of the
			// generated programs: they mostly check, and they are small.
			frontEnd := v == 0 && strings.HasPrefix(s.name, "gen/") && seedHash(s.name)%3 == 0
			inputs = append(inputs, input{loweringSeed{name, layoutVariant(s.src, seedHash(name))}, frontEnd, seed})
		}
	}
	for _, s := range formatSeeds(t) {
		add(s)
	}
	for _, g := range generatedPrograms() {
		add(g)
	}
	outcomes := checkFormatAll(t, len(inputs), func(i int) (string, bool) { return inputs[i].src, inputs[i].frontEnd })
	var seeds, parsedSeeds, variants, parsed, changed, excused int
	for i, in := range inputs {
		o := outcomes[i]
		switch {
		case in.seed < 0:
			seeds++
			if o.parsed > 0 {
				parsedSeeds++
			}
		case outcomes[in.seed].parsed > 0:
			variants++
			if o.parsed > 0 {
				parsed++
			}
			if o.changed {
				changed++
			}
		}
		if len(o.failures) == 0 {
			continue
		}
		if knownFormatGapFor(o.failures) != nil {
			excused++
			continue
		}
		t.Errorf("%s: %s", in.name, o.describe(in.src))
	}
	t.Logf("%d seeds, %d of them parse; %d variants of those, %d of them parse and formatting changes %d; %d inputs excused by a known gap",
		seeds, parsedSeeds, variants, parsed, changed, excused)
	// Most variants of a seed that parses parse too, and formatting
	// changes most of them; a variant generator that stopped doing either
	// would leave this test checking nothing.
	if parsed < variants*3/5 || changed < parsed*3/5 {
		t.Errorf("of %d variants, %d parse and formatting changes %d; most should parse, and formatting should change most of those", variants, parsed, changed)
	}
}

package vmhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// The rule these tests hold the VM to: a program that lowers runs without
// crashing. lowering_fuzz_test.go holds the compiler to the rule before it,
// that a program the front end accepts lowers; this one runs what lowered.
//
// An input is a source text as the lowering harness reads it (`// FILE:`
// sections or one file). It is loaded as `nomi run` loads a file, with no
// input, its output in a buffer, and every run bounded by a step budget
// (vm.Machine.WithLimits): the calls, backward branches and iteration steps
// the VM takes. Its `main` runs, or, with no `main`, its test cases.
//
// A run ends in one of these classes:
//
//   - ok: main returned, or every case passed.
//   - trap: the program's own failure, which a correct VM reports for a
//     program that asks for it: a Nomi fault (rt.Error: overflow, a zero
//     divisor, an index out of range, `todo`, a missed `case`), a failed
//     `assert`, an `Err` from main, a failed test case.
//   - too long: the run used its step budget, or waited past its Context's
//     deadline or runWallLimit. A program that may loop (mayLoop) or wait
//     (it reaches the clock or concurrency) is skipped; any other is a
//     crash, since a loop-free program's steps are bounded by its size.
//   - crash: a Go panic in the VM, rt or a host function; a panic in the
//     VM's bytecode compiler (vm.CompilePanic); a machine limit reached
//     mid-run (an instruction or callee the VM cannot run, which Unretained
//     did not find before the run); a deterministic program that used its
//     budget with no loop, or outlived runWallLimit; or two runs of a
//     deterministic program that differ.
//
// An input is skipped, not run, when the front end rejects it, it does not
// lower (the lowering harness's rule), it has nothing to run, or it reaches
// an effect outside the machine (runEffects). A program that reaches the
// clock, randomness or concurrency runs once; any other runs twice, and the
// two runs' output and outcome must match.

// runSteps is each run's step budget. The generated programs take a few
// thousand steps; the corpus's programs that take more are skipped as too
// long rather than run for seconds each.
const runSteps = 2_000_000

// runDeadline is the wall-clock backstop the VM reads every 1024 steps: a
// crossing into Go is not interrupted, so a step can be slow
// (`String.repeat` with a large count). Exceeding it is "too long" too.
const runDeadline = 10 * time.Second

// runContextLimit is the deadline of the Context main runs under, which ends
// a sleep or a wait on the clock: such a run is "too long".
const runContextLimit = 2 * time.Second

// runWallLimit bounds a run that takes no steps at all. A program with no
// clock, randomness or concurrency cannot wait, so for it exceeding the
// limit is a crash, a hang where the step budget cannot see it; for any
// other (a receive no task will send to, a test case's sleep) it is "too
// long".
const runWallLimit = 15 * time.Second

type runClass int

const (
	runSkipped runClass = iota
	runOK
	runTrap
	runTooLong
	runCrash
)

func (c runClass) String() string {
	return [...]string{"skipped", "ok", "trap", "too long", "crash"}[c]
}

// runOutcome is what running one input found.
type runOutcome struct {
	class runClass
	// skip says why an input was skipped, or why a too-long run is not a
	// crash.
	skip string
	// crash is what went wrong, for a crash: its kind on the first line
	// (runCrashKinds), then the detail and any stack.
	crash string
	// once reports that the program ran once, being nondeterministic by its
	// effects.
	once bool
	// transcript is the first run's output and outcome text.
	transcript string
}

// runCrashKinds are the first words of a crash's text, which a known gap's
// kind names.
const (
	crashPanic    = "go panic"
	crashCompile  = "bytecode compiler panic"
	crashLimit    = "machine limit mid-run"
	crashHang     = "hang"
	crashLoopFree = "loop-free program used its step budget"
	crashDiffers  = "nondeterministic"
)

func (o runOutcome) failed() bool { return o.class == runCrash }

// describe is the failure text for src.
func (o runOutcome) describe(src string) string {
	return "this program lowers:\n\n" + numbered(src) + "\nand running it crashes the VM:\n" + o.crash
}

// runEffects sorts each crossing into Go that vm.Machine.Effects reports by
// callee. A callee in deterministicCrossings is answered inside the machine
// the same way every time (the output writes go to the run's buffer, and
// `io.read_line` reads the empty input). One in nondeterministicCrossings
// stays inside the machine but may answer differently each run: the clock,
// randomness, tasks, channels, supervisors, a Context's deadlines. Any other
// callee (files, `std/compiler`, a Go binding, a callee added later that
// nobody has sorted) reaches outside the machine, and the program is not
// run. A read or write of the running app is inside the machine.
var deterministicCrossings = map[string]bool{
	"io.print": true, "io.write": true, "io.inspect": true, "io.read_line": true, "dbg": true,
	// `io.capture` runs its function inside the machine and reads and writes
	// only its own strings.
	"io.run_captured": true, "io.run_replayed": true,
}

var nondeterministicCrossingPrefixes = []string{
	"instant.", "timer.", "random.", "context.", "concurrent", "supervisors.",
	"Task.", "Channel.", "Sender.", "Receiver.", "Supervisor.",
}

// effectClass is how a program's effects let it run: outside names the
// first effect that reaches outside the machine ("" when none does), and
// nondeterministic reports that it reaches one that may answer differently
// from run to run.
func effectClass(effects []string) (outside string, nondeterministic bool) {
	for _, e := range effects {
		_, callee, crosses := strings.Cut(e, " calls ")
		if !crosses {
			if strings.Contains(e, "the running app") {
				continue
			}
			return e, false
		}
		if deterministicCrossings[callee] {
			continue
		}
		matched := false
		for _, p := range nondeterministicCrossingPrefixes {
			if strings.HasPrefix(callee, p) {
				matched = true
			}
		}
		if !matched {
			return e, false
		}
		nondeterministic = true
	}
	return "", nondeterministic
}

// loopMarkers are the constructs that repeat work without a bound in the
// program's text: the unbounded iteration sources, `Iter.loop`, and a range
// (`..`, which also matches a rest pattern or a struct spread, erring
// towards "may loop").
var loopMarkers = regexp.MustCompile(`Iter\.(loop|repeat|from|iterate|cycle|unfold)\b|\.\.|concurrent|Task\.|spawn`)

var fnDecl = regexp.MustCompile(`\bfn\s+([a-z_][A-Za-z0-9_]*\??)`)

// mayLoop reports whether src may repeat work without a bound its text
// shows: it holds a loop marker, or a function reaches itself through the
// names its body mentions (a function's body runs from its `fn` to the next
// one at the same or a shallower indent, which is coarse and errs towards
// finding a cycle). A loop-free program cannot use the step budget, so one
// that does is a crash.
func mayLoop(src string) bool {
	if loopMarkers.MatchString(src) {
		return true
	}
	lines := strings.Split(src, "\n")
	type decl struct {
		name string
		body strings.Builder
	}
	var decls []*decl
	indentOf := func(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }
	var open []*decl
	var indents []int
	for _, l := range lines {
		ind := indentOf(l)
		if strings.TrimSpace(l) != "" {
			for len(open) > 0 && ind <= indents[len(indents)-1] && !strings.HasPrefix(strings.TrimSpace(l), "}") {
				open, indents = open[:len(open)-1], indents[:len(indents)-1]
			}
		}
		if m := fnDecl.FindStringSubmatch(l); m != nil {
			d := &decl{name: m[1]}
			decls = append(decls, d)
			for _, o := range open {
				o.body.WriteString(l + "\n")
			}
			open, indents = append(open, d), append(indents, ind)
			continue
		}
		for _, o := range open {
			o.body.WriteString(l + "\n")
		}
	}
	names := map[string]bool{}
	for _, d := range decls {
		names[d.name] = true
	}
	edges := map[string]map[string]bool{}
	for _, d := range decls {
		if edges[d.name] == nil {
			edges[d.name] = map[string]bool{}
		}
		for _, w := range identRe.FindAllString(d.body.String(), -1) {
			if names[w] {
				edges[d.name][w] = true
			}
		}
	}
	// A cycle in edges: depth-first search with colors.
	color := map[string]int{}
	var visit func(string) bool
	visit = func(n string) bool {
		color[n] = 1
		for next := range edges[n] {
			if color[next] == 1 || (color[next] == 0 && visit(next)) {
				return true
			}
		}
		color[n] = 2
		return false
	}
	for n := range edges {
		if color[n] == 0 && visit(n) {
			return true
		}
	}
	return false
}

// runInput loads src's files under dir and runs them, as the class comment
// above says.
func runInput(dir, src string) (out runOutcome) {
	entry, err := writeInput(dir, src)
	if err != nil {
		return runOutcome{skip: "not an input this harness runs"}
	}
	var p *Program
	func() {
		defer func() {
			if r := recover(); r != nil {
				out = runOutcome{class: runSkipped, skip: fmt.Sprintf("the compiler panics while loading it: %v", r)}
			}
		}()
		p, err = Load(entry, WithOutput(io.Discard), WithErrorOutput(io.Discard))
	}()
	if p == nil {
		if out.skip == "" {
			out.skip = "the front end rejects it"
			var ie *InternalError
			if errors.As(err, &ie) {
				out.skip = "the compiler panics while loading it"
			}
		}
		return out
	}
	if p.Unsupported() != nil {
		return runOutcome{skip: "it does not lower"}
	}
	plan := runPlanFor(p)
	if plan.skip != "" {
		return runOutcome{skip: plan.skip}
	}
	first := runOnce(p, plan, dir)
	if first.class == runCrash || first.class == runSkipped {
		return first
	}
	if first.class == runTooLong {
		if !plan.nondeterministic && !mayLoop(src) {
			first.class = runCrash
			first.crash = crashLoopFree + fmt.Sprintf(" (%d steps): no loop marker and no recursive function in its text", runSteps)
			return first
		}
		if first.skip == "" {
			first.skip = "it may loop or wait, and used its step budget or its time"
		}
		return first
	}
	if plan.nondeterministic {
		first.once = true
		return first
	}
	second := runOnce(p, plan, dir)
	if second.class == runCrash {
		return second
	}
	if second.transcript != first.transcript || second.class != first.class {
		first.class = runCrash
		first.crash = crashDiffers + ": two runs of a program with no clock, randomness or concurrency differ\n" +
			"first run (" + first.class.String() + "):\n" + first.transcript + "\nsecond run (" + second.class.String() + "):\n" + second.transcript
	}
	return first
}

// runPlan is what an input runs: main, or its test cases.
type runPlan struct {
	main             *ir.Func
	cases            []ir.TestCase
	nondeterministic bool
	skip             string
}

func runPlanFor(p *Program) runPlan {
	if p.entry == nil {
		return runPlan{skip: "nothing to run"}
	}
	m := p.machine(io.Discard)
	var roots []*ir.Func
	var plan runPlan
	bootFunc := func(sym *ir.Symbol) bool {
		if sym == nil {
			return true
		}
		for _, mod := range p.res.IRModules() {
			if f := mod.FuncFor(sym); f != nil {
				roots = append(roots, f)
				return true
			}
		}
		return false
	}
	switch {
	case p.HasMain():
		plan.main = p.entryFunc("main")
		if plan.main == nil {
			return runPlan{skip: "main is not retained"}
		}
		roots = append(roots, vm.MainRoots(p.entry, plan.main)...)
		if !bootFunc(p.entry.Boot()) {
			return runPlan{skip: "its boot is not retained"}
		}
	case p.prog.HasTests:
		plan.cases = p.entry.Tests()
		if len(plan.cases) == 0 {
			return runPlan{skip: "no test case is retained"}
		}
		names := map[string]bool{}
		for _, c := range plan.cases {
			if names[c.Name()] {
				return runPlan{skip: "duplicate test names"}
			}
			names[c.Name()] = true
			roots = append(roots, c.Fn())
			if !bootFunc(c.Group().Boot) {
				return runPlan{skip: "a group boot is not retained"}
			}
		}
	default:
		return runPlan{skip: "nothing to run"}
	}
	var effects []string
	for _, f := range roots {
		effects = append(effects, m.Effects(f)...)
	}
	outside, nondet := effectClass(effects)
	if outside != "" {
		return runPlan{skip: "it reaches outside the machine: " + outside}
	}
	plan.nondeterministic = nondet
	return plan
}

// runOnce runs plan on a fresh machine under the step budget, in its own
// goroutine so that a run that hangs where no step is taken is reported
// rather than hanging the test.
func runOnce(p *Program, plan runPlan, dir string) runOutcome {
	done := make(chan runOutcome, 1)
	go func() { done <- runBounded(p, plan, dir) }()
	select {
	case o := <-done:
		return o
	case <-time.After(runWallLimit):
		if plan.nondeterministic {
			return runOutcome{class: runTooLong, skip: fmt.Sprintf("it waits, and did not finish in %s", runWallLimit)}
		}
		return runOutcome{class: runCrash, crash: fmt.Sprintf("%s: the run took no step for its last part and did not finish in %s", crashHang, runWallLimit)}
	}
}

func runBounded(p *Program, plan runPlan, dir string) (out runOutcome) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			out = runOutcome{class: runCrash, crash: fmt.Sprintf("%s: %v\n%s", crashPanic, r, debug.Stack())}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), runContextLimit)
	defer cancel()
	base := p.machine(&buf).WithErrorOutput(&buf).WithInput(rt.NewInput(strings.NewReader("")))
	m := base.WithLimits(vm.Limits{Steps: runSteps, Deadline: time.Now().Add(runDeadline)})
	clean := func(s string) string { return strings.ReplaceAll(s, dir+string(filepath.Separator), "") }
	if plan.main != nil {
		var boots []*ir.Symbol
		if boot := p.entry.Boot(); boot != nil {
			boots = append(boots, boot)
		}
		if found := m.Unretained(vm.MainRoots(p.entry, plan.main), boots); len(found) > 0 {
			return runOutcome{skip: "blocked before it runs: " + p.blocked(found).Error()}
		}
		err := m.Main(ctx, nil, false)
		out = classifyRunError(m, err)
		if ctx.Err() != nil && out.class != runCrash {
			out.class = runTooLong
		}
		out.transcript = clean(buf.String() + "\n--- " + out.class.String() + ": " + errText(err))
		return out
	}
	var boots []*ir.Symbol
	var roots []*ir.Func
	for _, c := range plan.cases {
		roots = append(roots, c.Fn())
		if b := c.Group().Boot; b != nil {
			boots = append(boots, b, c.Group().Startup)
		}
	}
	if found := m.Unretained(roots, boots); len(found) > 0 {
		return runOutcome{skip: "blocked before it runs: " + p.blocked(found).Error()}
	}
	failures, limits := m.RunCases(plan.cases)
	out.class = runOK
	var b strings.Builder
	b.WriteString(buf.String())
	for i, c := range plan.cases {
		// RunCases sorts a case's outcome (vm.Machine.testFn): a limit is
		// the machine's, a compile panic is a compiler bug, and any other
		// failure is the case's own.
		var o runOutcome
		_, compilePanic := vm.AsCompilePanic(failures[i])
		switch {
		case m.Exhausted():
			o.class = runTooLong
		case limits[i] != "":
			o = runOutcome{class: runCrash, crash: crashLimit + ": " + limits[i]}
		case compilePanic:
			o = classifyRunError(m, failures[i])
		case failures[i] != nil:
			o.class = runTrap
		default:
			o.class = runOK
		}
		fmt.Fprintf(&b, "\n--- %s: %s: %s", c.Name(), o.class, errText(failures[i]))
		if o.class > out.class {
			out.class, out.crash = o.class, o.crash
		}
	}
	out.transcript = clean(b.String())
	return out
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// classifyRunError sorts the error a run on m answered.
func classifyRunError(m *vm.Machine, err error) runOutcome {
	if m.Exhausted() || errors.Is(err, vm.ErrLimit) {
		return runOutcome{class: runTooLong}
	}
	if err == nil {
		return runOutcome{class: runOK}
	}
	if cp, internal := vm.AsCompilePanic(err); internal {
		return runOutcome{class: runCrash, crash: fmt.Sprintf("%s: %s\n%s", crashCompile, cp.What(), cp.Stack)}
	}
	if _, limit := vm.ProgramFailure(err); limit {
		return runOutcome{class: runCrash, crash: crashLimit + ": " + err.Error()}
	}
	return runOutcome{class: runTrap}
}

// runSeeds are the seeds this rule runs under plain `go test`: every
// generated program, and every corpus, tour and spec seed with a top-level
// `fn main` (a corpus file's test cases run under TestTestCommand_VMCorpus).
func runSeeds(tb testing.TB) []loweringSeed {
	seeds := generatedPrograms()
	for _, s := range loweringSeeds(tb) {
		names, files, ok := splitInput(s.src)
		if ok && topLevelMain.MatchString(files[names[0]]) {
			seeds = append(seeds, s)
		}
	}
	return seeds
}

// runTally counts outcomes by class, and the skip reasons by their first
// words, for a test's log.
type runTally struct {
	byClass map[runClass]int
	skips   map[string]int
	once    int
}

func (t *runTally) add(o runOutcome) {
	if t.byClass == nil {
		t.byClass, t.skips = map[runClass]int{}, map[string]int{}
	}
	t.byClass[o.class]++
	if o.once {
		t.once++
	}
	if o.skip != "" {
		reason := o.skip
		if i := strings.IndexAny(reason, ":\n"); i >= 0 {
			reason = reason[:i]
		}
		t.skips[reason]++
	}
}

func (t *runTally) String() string {
	var b strings.Builder
	for c := runSkipped; c <= runCrash; c++ {
		fmt.Fprintf(&b, "%s %d, ", c, t.byClass[c])
	}
	fmt.Fprintf(&b, "run once (nondeterministic) %d", t.once)
	var reasons []string
	for r := range t.skips {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(&b, "\n  %d skipped or not compared: %s", t.skips[r], r)
	}
	return b.String()
}

// checkRun runs in and reports a crash that no known gap names.
func checkRun(t *testing.T, name, src string, tally *runTally, failure string) {
	t.Helper()
	o := runInput(t.TempDir(), src)
	tally.add(o)
	if !o.failed() {
		return
	}
	if gap := knownRunGapFor(o.crash); gap != nil {
		return
	}
	t.Errorf("%s: %s%s", name, failure, o.describe(src))
}

// TestRunInputClassifies holds runInput's classes to small programs of each
// kind, so a harness change that sorted everything as ok, or nothing as a
// crash, fails here.
func TestRunInputClassifies(t *testing.T) {
	cases := []struct {
		name, src string
		want      runClass
		skip      string
	}{
		{"ok", "import std/io\n\nfn main() {\n    io.print(1 + 2)\n}\n", runOK, ""},
		{"a trap", "import std/io\n\nfn half(n: Int): Int {\n    10 / n\n}\n\nfn main() {\n    io.print(half(0))\n}\n", runTrap, ""},
		{"a failed test case", "test \"t\" {\n    assert 1 == 2\n}\n", runTrap, ""},
		{"recursion past the budget", "import std/io\n\nfn spin(n: Int): Int {\n    spin(n + 1)\n}\n\nfn main() {\n    io.print(spin(0))\n}\n", runTooLong, "it may loop"},
		{"a file read", "import std/io\n\nfn main() {\n    io.inspect(io.read_file(\"x\"))\n}\n", runSkipped, "it reaches outside the machine"},
		{"a rejection", "fn main() {\n    x = \n}\n", runSkipped, "the front end rejects it"},
		{"a planted compile panic", "import std/io\n\nfn " + plantedTarget + "(): Int {\n    1\n}\n\nfn main() {\n    io.print(" + plantedTarget + "())\n}\n", runCrash, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.want == runCrash {
				plantCompilePanic(t)
			}
			o := runInput(t.TempDir(), c.src)
			if o.class != c.want || !strings.HasPrefix(o.skip, c.skip) {
				t.Fatalf("class %s, skip %q, crash %q; want class %s, skip %q", o.class, o.skip, o.crash, c.want, c.skip)
			}
			if c.want == runCrash && !strings.HasPrefix(o.crash, crashCompile) {
				t.Fatalf("crash %q, want a %s", o.crash, crashCompile)
			}
		})
	}
	// The generator writes no loop and no recursion, so a generated program
	// that uses its budget is a crash, not a skip.
	for _, g := range generatedPrograms() {
		if mayLoop(g.src) {
			t.Fatalf("%s: mayLoop says it may loop; the generator writes neither loops nor recursion:\n%s", g.name, g.src)
		}
	}
}

// TestLoweredProgramsRun runs every generated program and every seed with a
// `fn main` (runSeeds), and fails on a crash no known gap (knownRunGaps)
// names.
func TestLoweredProgramsRun(t *testing.T) {
	var tally runTally
	gen := 0
	for _, in := range runSeeds(t) {
		before := tally.byClass[runOK] + tally.byClass[runTrap]
		checkRun(t, in.name, in.src, &tally, "")
		if strings.HasPrefix(in.name, "gen/") && tally.byClass[runOK]+tally.byClass[runTrap] > before {
			gen++
		}
	}
	t.Logf("outcomes: %s", &tally)
	// Most generated programs run to an end; a generator or harness change
	// that broke that would leave this test running nothing.
	if gen < generatedCount*3/4 {
		t.Errorf("%d of %d generated programs ran to an end; most should", gen, generatedCount)
	}
}

// TestLoweredProgramsRunRotating runs generatedCount generated programs
// beside the fixed ones, from a start seed that changes every UTC day
// (internal/rotation). NOMI_GEN_SEED pins the start; a failure names the
// program's seed and the command that runs exactly that program.
func TestLoweredProgramsRunRotating(t *testing.T) {
	set := rotation.For(t, "./vmhost", generatedCount)
	var tally runTally
	for _, seed := range set.Seeds() {
		in := generatedProgram(int(seed))
		checkRun(t, in.name, in.src, &tally, set.Failure(seed))
	}
	t.Logf("outcomes: %s", &tally)
}

// FuzzLoweredProgramsRun fails for an input that lowers and crashes the VM
// when it runs (runInput's classes). An input matching a known gap
// (knownRunGaps) is skipped, so a run reports new causes. Run it with
//
//	go test ./vmhost -run '^$' -fuzz '^FuzzLoweredProgramsRun$' -fuzztime 5m -parallel 4
//
// A failing input is written under vmhost/testdata/fuzz/; plain `go test`
// replays everything there, so triage an input before committing it.
func FuzzLoweredProgramsRun(f *testing.F) {
	if fuzzing() {
		for _, s := range runSeeds(f) {
			f.Add(s.src)
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		o := runInput(t.TempDir(), src)
		if !o.failed() {
			return
		}
		if gap := knownRunGapFor(o.crash); gap != nil {
			t.Skipf("a known crash: %s", gap.name)
		}
		t.Fatal(o.describe(src))
	})
}

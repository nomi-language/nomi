package vmhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/progcache"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmrunner"
)

// Program is one lowered program, ready to run, call or test on the VM.
type Program struct {
	prog     *irbuild.Program
	res      *irbuild.Result
	entry    *ir.Module
	declines map[string]string
	// declineDetails are the same declines with the file and position each
	// was taken at, which Unsupported reports them by.
	declineDetails []*irbuild.Decline
	// stdModule is the stdlib module ("timer") whose cases this program
	// holds (LoadStdlib), or "" for any other program. A blocker in one is
	// followed into the std body that declined (stdBlocker).
	stdModule string
	// stdDeclines are the declines of each stdlib module's bodies, by
	// module, lowered when a blocker first needs them (stdDecline).
	stdDeclines map[string][]*irbuild.Decline
	// appTypes are the program's application types as the checks of the
	// files it was relowered from recorded them, for a program whose entry
	// has no check at hand (one relinked from a record's kept lowering,
	// checklowering_stored.go); appFieldNames reads them.
	appTypes []*analysis.StructType
	// recordTrees and recordFiles are what the records of a program
	// relinked from them name (filerecord.go's relink), which its modules,
	// the entry alone, do not: inputs answers them.
	recordTrees []progcache.Tree
	recordFiles []string
	// sources are the texts of the program's in-memory files by module path,
	// which a diagnostic quotes its source line from (a file on disk is read).
	sources map[string]string
	// parsed is the syntax of each file whose module holds none, parsed
	// from its text when a report first needs it (syntaxOf).
	parsedMu sync.Mutex
	parsed   map[string][]ast.Node
	// indexed is where each file's syntax has what a report shows
	// (indexOf), under parsedMu.
	indexed map[string]*syntaxIndex
	// advice is Advice once computed.
	advice *Diagnostics
	// hosts are the host tables every machine binds beside its own stdlib
	// adapters: std/compiler's, then the tables WithHosts gave.
	hosts []HostTable
	// out is Call's output writer.
	out io.Writer
	// errOut is the runtime's diagnostic output, or nil for os.Stderr.
	errOut io.Writer
	// fileNames names a source file in a run-time failure's text
	// (vm.Machine.WithFileNames), or is nil for its path from the working
	// directory. LoadMarked sets it for a project it wrote under a
	// directory.
	fileNames func(string) string
	// blockTests are the programs of a marked block's other test files,
	// each entered at its file (LoadMarked); Cases runs theirs after the
	// entry's.
	blockTests []*Program
	// input is what `io.read_line` reads, shared by every machine this
	// program opens, or nil for no input.
	input *rt.Input
	// debugger is what every machine this program opens reports to, or nil.
	debugger *vm.Debugger
	// liveTests receives each test case's output as it is written
	// (StreamTestOutput).
	liveTests io.Writer
	// testTimeout and testTimeoutHint are each test case's limit and the
	// hint its failure ends with (SetTestTimeout).
	testTimeout     time.Duration
	testTimeoutHint string
	// passes are the stored passes Test reads and writes (UseStoredPasses),
	// or nil for none.
	passes *passStore
	// kept is the sum of the kept program's record (progcache.Entry.Sum)
	// this program was decoded from (Cache.Program), or zero.
	kept [32]byte
	// reaching, when set, narrows the cases Test and Cases answer to those
	// whose code reaches the function there (SelectReaching).
	reaching *TestLocation
	// cfg is the configuration the program was loaded with, which a debugger
	// expression (CompileExpr) is loaded with too.
	cfg config
	// locales is the program's nomi.toml `[i18n].locales`, nil when it lists
	// none: whose CLDR data its machines read and its image carries.
	locales []string
	// source is the program's `[i18n].source_locale` when it lists locales,
	// else "": the locale a locale it does not carry formats as.
	source string
	// webEmbedded are the locales a browser build of the program embeds
	// (analysis.Manifest.WebLocales), nil when it lists no locales.
	webEmbedded []string
	// effects is the program's nomi.toml [effects.self] and
	// [effects.deps] tables, nil when it has neither
	// (EffectCallsNotAllowed).
	effects *analysis.Effects
	// modules is each dependency whose files the program holds, by its
	// root directory, worked out once (moduleAt).
	modulesMu sync.Mutex
	modules   map[string]*requiredModule
	// project is the project's module path, worked out once
	// (projectModule).
	projectOnce sync.Once
	project     string
	// currencyModules is what each directory's module allows custom
	// currency literals to name, by directory (currencyModuleOf).
	currencyModules map[string]currencyModule
	// tables are the phrase and noun tables the program carries, worked
	// out once (phraseTables).
	tablesOnce sync.Once
	tables     [2][]string
}

// localeData is the CLDR data the program's machines read: the locales
// its nomi.toml lists, as an image built from it carries them.
func (p *Program) localeData() *cldrdata.Data { return cldrfull.ForProgram(p.locales, p.source) }

// HostTable is a table of host adapters: bound against a machine's Env, it
// answers each adapter by the key the program's `host fn` crosses under. A
// generated adapter file's Bind is one; an embedder writes one.
type HostTable = vm.HostTable

// MainError is the error Run answers when `main` returns `Err`. Its Text is
// the payload's Display rendering when the payload's type implements Display
// (a String is its own text), and its Debug rendering otherwise; Error() is
// `error: ` and that text, the line `nomi run` prints to stderr before
// exiting 1.
type MainError = vm.MainError

// Exit is the error Run answers when `main` (or the entry boot) returns
// `Err(Exit{code:, message:})` with std/process's Exit: the run should end
// with status Code, printing Message and a newline to stderr when it is not
// empty (WriteFailure does). Code 0 is a successful end.
type Exit = vm.Exit

// ExitCode is the status a run that answered err exits with, as `nomi run`
// exits: 0 for nil, an Exit's code, and 1 for any other failure.
func ExitCode(err error) int { return vm.ExitCode(err) }

// Blocked is the error a program answers when it reaches something the VM
// cannot run.
//
// A body the compiler accepted and could not lower is reported as `nomi
// check` reports it: a diagnostic at the code the lowering stopped at, worded
// for the program's author. Anything else that stops a run (a crossing into
// Go that nothing binds, a dispatched call with no implementation, a machine
// limit) keeps its reason.
type Blocked struct {
	// Reasons is every blocker in the compiler's own terms, one line of the
	// form `[<name>] <why>` each: the lowering's reason for a declined body,
	// which the author is never shown (NOMI_DEBUG_LOWERING adds it to the
	// diagnostic as a hint).
	Reasons []string
	// diags are the declined bodies, located and worded for the author.
	diags frontend.Diagnostics
	// rest are the Reasons diags does not cover. Nil means all of them, for a
	// Blocked built from Reasons alone.
	rest []string
}

// others are the blockers with no diagnostic.
func (b *Blocked) others() []string {
	if b.rest == nil && b.diags == nil {
		return b.Reasons
	}
	return b.rest
}

// Error is each declined body's diagnostic in its short form
// (`path:line:col: message`), then any other blocker's reason.
func (b *Blocked) Error() string {
	var parts []string
	if len(b.diags) > 0 {
		parts = append(parts, b.diags.Error())
	}
	if others := b.others(); len(others) > 0 {
		parts = append(parts, "the VM cannot run this program:\n  "+strings.Join(others, "\n  "))
	}
	return strings.Join(parts, "\n")
}

// Write prints what `nomi run` prints for a program it cannot run: each
// declined body's diagnostic with its source line, as `nomi check` prints
// it, then one `BLOCKED <label> <reason>` line per other blocker and a
// closing line (vmrunner's, so a built binary reports what `nomi run`
// reports).
func (b *Blocked) Write(w io.Writer, label string) {
	if len(b.diags) > 0 {
		b.diags.Render(w)
	}
	if others := b.others(); len(others) > 0 {
		vmrunner.WriteBlocked(w, label, others)
	}
}

// Disassemble is the VM bytecode of the entry's function named name.
func (p *Program) Disassemble(name string) (string, error) {
	return p.machine(io.Discard).Disassemble(name)
}

// HasMain reports whether the entry declares `fn main`.
func (p *Program) HasMain() bool { return p.res.HasMain }

// RequireMain is nil when the entry declares `fn main`, and otherwise the
// error `nomi run` and `nomi build` give for it: there is nothing to run or
// build, where verb is "run" or "build". A program with exported functions
// is pointed at `nomi build --web` and the wasip1 reactor, and an entry that declares tests at
// `nomi test`.
func (p *Program) RequireMain(verb string) error {
	if p.HasMain() {
		return nil
	}
	path := DisplayPath(p.prog.Entry().Path)
	msg := fmt.Sprintf("%s declares no `fn main`, so there is no program to %s", path, verb)
	if p.HasExports() {
		msg += "; its exported functions are called by a host, so build it for a page with `nomi build --web`, or as a WASI reactor with `nomi build --target wasip1/wasm`"
	}
	if p.prog.HasTests {
		msg += fmt.Sprintf("; to run its tests, use `nomi test %s`", path)
	}
	return errors.New(msg)
}

// SetErrorOutput replaces the program's standard error (WithErrorOutput)
// for the runs that start after it: a debugger gives each pass of a time
// travel its own, which drops what a replay writes again.
func (p *Program) SetErrorOutput(w io.Writer) { p.errOut = w }

func (p *Program) machine(out io.Writer) *vm.Machine {
	return p.machineOver(p.entry, out)
}

// machineOver is machine with entry as the module the machine resolves a
// callee in first. Only checkLiterals passes another module, for a program
// whose entry file retained nothing.
func (p *Program) machineOver(entry *ir.Module, out io.Writer) *vm.Machine {
	m := vm.NewProgram(entry, p.res.IRModules(), out).WithHosts(p.hosts...).WithLocaleLoader(p.localeData).WithPhrases(p.phrases())
	if p.errOut != nil {
		m = m.WithErrorOutput(p.errOut)
	}
	if p.fileNames != nil {
		m = m.WithFileNames(p.fileNames)
	}
	if p.input != nil {
		m = m.WithInput(p.input)
	}
	if p.cfg.files != nil {
		m = m.WithFiles(p.cfg.files)
	}
	if src := p.embeddedSource(); src != nil {
		m = m.WithEmbeddedSource(src)
	}
	if p.debugger != nil {
		m = m.WithDebugger(p.debugger)
	}
	if p.liveTests != nil {
		m = m.WithLiveTestOutput(p.liveTests)
	}
	if p.testTimeout > 0 && p.debugger == nil {
		m = m.WithCaseLimit(p.testTimeout)
	}
	if p.cfg.stop != nil && p.cfg.stop.Done() != nil {
		m = m.StopOn(p.cfg.stop)
	}
	return m
}

// StreamTestOutput makes each test case write its output to w as it writes
// it, as well as into its CaseResult: a debugger shows what a stopped case
// has printed so far. Test then leaves each case's output out of a text
// report, which would print it a second time; a JSON report's records still
// carry it. Output an `io.capture` or `io.replay` takes is not streamed.
func (p *Program) StreamTestOutput(w io.Writer) {
	p.liveTests = w
}

// SetDebugger makes every machine the program opens from now on a debug
// machine reporting to d (internal/vm's debug.go): Run, Call and the test
// entry points then stop where d says. The program's modules become places
// d's breakpoints can land.
func (p *Program) SetDebugger(d *vm.Debugger) {
	p.debugger = d
	if d != nil {
		d.AddModules(append([]*ir.Module{p.entry}, p.res.IRModules()...)...)
	}
}

// Run boots the program and calls `main`, writing its output to out. A
// program with no `fn main` runs nothing.
//
// The error is a *Blocked when the program reaches something the VM cannot
// run, and otherwise the program's own failure, which WriteFailure renders as
// `nomi run` does: a fault, a failed assertion, or a *MainError when `main`
// returns `Err`. A main that returns `Ok`, or any value that is not a Result,
// succeeds.
//
// The run ends when ctx does (vm.Machine.StopOn), and then a run that did
// not finish answers ctx.Err().
func (p *Program) Run(ctx context.Context, out io.Writer, args []string, handleSignals bool) error {
	return p.run(ctx, out, args, handleSignals, nil)
}

// run is Run with environment overrides for the Startup a boot receives,
// which `compiler.run_file` supplies.
func (p *Program) run(ctx context.Context, out io.Writer, args []string, handleSignals bool, hostEnv map[string]string) error {
	if !p.HasMain() {
		return nil
	}
	mainFn := p.entryFunc("main")
	if mainFn == nil {
		return p.blockedName("main")
	}
	// nomi.toml's [effects] lists, and the host's (WithEffects),
	// refuse the run before anything in it does anything.
	if err := p.effectError(Roots{Exports: p.HasExports()}); err != nil {
		return err
	}
	m := p.machine(out).WithHostEnv(hostEnv)
	if ctx.Done() != nil {
		m = m.StopOn(ctx)
	}
	var boots []*ir.Symbol
	if boot := p.entry.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	if found := m.Unretained(vm.MainRoots(p.entry, mainFn), boots); len(found) > 0 {
		return p.blocked(found)
	}
	// NOMI_PROFILE (`nomi run --profile`) samples the run (internal/vm's
	// profile.go) and reports when it ends.
	m, finishProfile := vm.StartProfile(m, os.Getenv("NOMI_PROFILE"), p.errOut)
	failure, limit := programFailure(m.Main(ctx, args, handleSignals))
	finishProfile()
	if err := ctx.Err(); err != nil && failure != nil {
		// Stopped: whatever the run answered is the stop's doing.
		return err
	}
	if limit {
		return &Blocked{Reasons: []string{machineLimit(failure)}}
	}
	return failure
}

// Test runs the entry's test cases that the VM can run and reports every
// selected case in source order: a case that ran through rep.Result, and a
// case the VM cannot run through rep.Blocked with its reasons. label names a
// case in the report. Every runnable case runs before anything is reported,
// and the report prints each case's own output under its line, unless it was
// streamed (StreamTestOutput) and the report is text. out receives
// only output written outside every case (see Cases).
//
// file is the absolute path a JSON report locates each case by, with the
// case's first and last lines.
func (p *Program) Test(out io.Writer, rep *TestReport, file string, opts TestOptions, label func(string) string) {
	// nomi.toml's [effects] tables, with their std_in_tests, and the
	// host's (WithEffects) refuse the file's cases before any of them
	// runs.
	if err := p.effectError(Roots{Tests: &opts}); err != nil {
		rep.FailFile(file, err)
		return
	}
	cases := p.heldCases(out, opts, p.passes)
	if p.liveTests != nil && rep.Format() != TestFormatJSON {
		// Streamed as each case wrote it (StreamTestOutput).
		for i := range cases {
			cases[i].Output = ""
		}
	}
	reportCases(rep, file, cases, label)
}

// reportCases reports cases, a file's in source order, to rep as Test
// does: Cache.TestUnchanged reports a file answered from its passes record
// through it too.
func reportCases(rep *TestReport, file string, cases []CaseResult, label func(string) string) {
	for _, c := range cases {
		loc := TestLocation{File: file, Line: c.Line, EndLine: c.EndLine}
		if c.Blocked != nil {
			if c.BlockedPath == file {
				loc.ErrorLine = c.BlockedLine
			}
			rep.BlockedAt(loc, label(c.Name), c.Blocked, c.Output)
			continue
		}
		rep.ResultAt(loc, label(c.Name), c.Err, c.Output, c.Count())
	}
}

// CaseResult is one case's outcome. Blocked is non-nil for a case the VM
// could not run, and then Err is meaningless.
type CaseResult struct {
	Name    string
	Line    int
	EndLine int
	Err     error
	// Cases is how many cases the test counts as in a report's summary, and
	// FailedCases how many of them failed (rt.CountCases): 1 and 0 or 1 for a
	// test that ran no `subtests` table, the rows it ran for one that did
	// (Subtests). A blocked test counts as one blocked test, and these are 0.
	Cases       int
	FailedCases int
	// Subtests reports whether the test ran a `subtests` table: its ok line
	// names its count of cases.
	Subtests bool
	// Decided is how Test decided whether to run the case, for a program
	// that uses stored passes (UseStoredPasses), and "" otherwise: one of
	// rt's Decided constants. A case rt.DecidedUnchanged did not run: it
	// passed, with the cases its stored pass counted, and wrote nothing.
	Decided string
	// Effects says the case's code can have effects, as its walk found
	// them, for a program that uses stored passes or selects by reach
	// (SelectReaching).
	Effects bool
	// Output is what the case wrote while it ran: `io.print`, `io.write`,
	// `io.inspect` and `dbg`, from its boot, setup, body, the tasks it
	// started and the `once` initializers it forced, with `dbg` coloured as
	// the report colours its lines. A case cut off by a machine limit keeps
	// what it wrote before the limit.
	Output string
	// Blocked is each blocker as `nomi test` reports it: for a body the
	// compiler could not lower, the diagnostic `nomi check` gives in its
	// short form (`path:line:col: message`, then a line per hint); for
	// anything else, its reason.
	Blocked []string
	// Reasons are the same blockers in the compiler's own terms,
	// `[<name>] <why>`, which the report never shows.
	Reasons []string
	// BlockedPath and BlockedLine locate the first blocker that has a
	// source position (a path as the front end names the file); 0 when none
	// has one.
	BlockedPath string
	BlockedLine int
}

// Count is the cases r counts as, as a report takes them.
func (r CaseResult) Count() CaseCount {
	return CaseCount{Subtests: r.Subtests, Cases: r.Cases, Failed: r.FailedCases, Decided: r.Decided, Effects: r.Effects}
}

// counted sets r's case counts from the rows of `subtests` tables it ran.
func (r *CaseResult) counted(rows int) {
	c := rt.CountCases(rows, r.Err)
	r.Cases, r.FailedCases, r.Subtests = c.Cases, c.Failed, c.Subtests
}

// blockedBy fills r's blocker fields from b.
func (r *CaseResult) blockedBy(b *Blocked) {
	r.Blocked = blockedMessages(b)
	r.Reasons = b.Reasons
	for _, d := range b.diags {
		if d.Line > 0 {
			r.BlockedPath, r.BlockedLine = d.Path, d.Line
			break
		}
	}
}

// Cases runs the entry's runnable test cases and answers every selected case
// in source order, each with the output it wrote (CaseResult.Output). out
// receives only output written outside every case: a task that outlives the
// case that started it. When the file declares
// duplicate case names it answers those as failures and runs nothing.
//
// A program LoadMarked loads from a block with tests in more than one file
// answers the entry's cases and then each other test file's, in block
// order, as `nomi test` runs every test file of a project.
//
// The cases are held as Test holds them: when nomi.toml's [effects]
// lists, with std_in_tests, or the host's (WithEffects) do not allow
// what the selected cases reach,
// Cases runs nothing and answers each selected case failed with the
// *EffectError.
func (p *Program) Cases(out io.Writer, opts TestOptions) []CaseResult {
	if err := p.effectError(Roots{Tests: &opts}); err != nil {
		plan, _ := frontend.SelectTests(p.prog.Entry().Nodes, opts)
		results := make([]CaseResult, len(plan))
		for i, tc := range plan {
			results[i] = CaseResult{Name: tc.FullName(), Line: tc.Line, EndLine: tc.LastLine(), Err: err}
			results[i].counted(0)
		}
		return results
	}
	return p.heldCases(out, opts, nil)
}

// heldCases is Cases for a program its lists allow to run the cases opts
// selects: the entry's, then each other test file's, which Cases holds
// on its own. With passes, an entry's case that a stored pass answers does
// not run (entryCases).
func (p *Program) heldCases(out io.Writer, opts TestOptions, passes *passStore) []CaseResult {
	results := p.entryCases(out, opts, passes)
	for _, t := range p.blockTests {
		results = append(results, t.Cases(out, opts)...)
	}
	return results
}

// entryCases is Cases for the entry's own cases. With passes, a case whose
// stored pass the walk from it matches does not run and answers that pass
// (testpass.go), and each case that runs updates its stored pass.
func (p *Program) entryCases(out io.Writer, opts TestOptions, passes *passStore) []CaseResult {
	if p.debugger != nil || p.entry == nil {
		// A debugged run stops where its breakpoints say, in every case.
		passes = nil
	}
	plan, duplicates := frontend.SelectTests(p.prog.Entry().Nodes, opts)
	if len(duplicates) > 0 {
		results := make([]CaseResult, len(duplicates))
		for i, d := range duplicates {
			results[i] = CaseResult{Name: d.Name, Err: d.Err, Line: d.Line, EndLine: d.EndLine}
			results[i].counted(0)
		}
		return results
	}
	retained := map[string]ir.TestCase{}
	if p.entry != nil {
		for _, c := range p.entry.Tests() {
			retained[c.Name()] = c
		}
	}
	var m *vm.Machine
	if p.entry != nil {
		m = p.machine(out)
	}
	type outcome struct {
		blocked *Blocked
		err     error
		run     int // index into the runnable slice, or -1
		// pass is the case's fingerprint and code, which a pass stores,
		// or the stored pass that answered a case decided rt.DecidedUnchanged.
		pass    storedPass
		decided string
		// skip is a case whose code does not reach the functions
		// SelectReaching names: it is not reported.
		skip bool
		// effects says the case's walk found an effect, which a result
		// says when the run keeps passes or selects by reach.
		effects bool
	}
	outcomes := make([]outcome, len(plan))
	var runnable []ir.TestCase
	var sums *vm.CodeSums
	if passes != nil {
		passes.open(p.testTimeout)
		sums = passes.sums
	}
	var reaching []*ir.Func
	if p.reaching != nil && p.entry != nil {
		reaching = vm.FuncsAt(p.res.IR, p.reaching.File, p.reaching.Line)
	}
	for i, tc := range plan {
		outcomes[i].run = -1
		if tc.Clock == frontend.InvalidClock {
			outcomes[i].err = frontend.ErrInvalidClock()
			continue
		}
		c, ok := retained[tc.FullName()]
		if !ok || p.entry == nil {
			outcomes[i].blocked = &Blocked{
				Reasons: []string{p.testNotRetained(tc.FullName())},
				diags:   p.declineDiagnostics([]string{"test body: " + tc.FullName()}),
				rest:    []string{},
			}
			continue
		}
		reach := m.ReachCase(c, sums)
		if p.reaching != nil && !reach.Reaches(reaching) {
			outcomes[i].skip = true
			continue
		}
		if len(reach.Unretained) > 0 {
			outcomes[i].blocked = p.blocked(reach.Unretained)
			continue
		}
		outcomes[i].effects = len(reach.Effects) > 0
		if passes != nil {
			sum := p.caseFingerprint(c, reach.Parts)
			pass, decided := passes.decide(c.Name(), sum, reach)
			outcomes[i].pass, outcomes[i].decided = pass, decided
			if decided == rt.DecidedUnchanged {
				continue
			}
		}
		outcomes[i].run = len(runnable)
		runnable = append(runnable, c)
	}
	var failures []error
	var outputs, limits []string
	var rows []int
	if len(runnable) > 0 {
		failures, outputs, limits, rows = m.RunCases(runnable)
	}
	results := make([]CaseResult, len(plan))
	for i, tc := range plan {
		o := outcomes[i]
		results[i].Name, results[i].Line, results[i].EndLine = tc.FullName(), tc.Line, tc.LastLine()
		if o.skip {
			continue
		}
		if passes != nil || p.reaching != nil {
			results[i].Effects = o.effects
		}
		if o.run >= 0 {
			results[i].Output = outputs[o.run]
		}
		results[i].Decided = o.decided
		passed := false
		switch {
		case o.decided == rt.DecidedUnchanged:
			results[i].Cases, results[i].Subtests = o.pass.count.Cases, o.pass.count.Subtests
			// Kept as decided now: verified on this program or not.
			passes.keep(tc.FullName(), o.pass)
			continue
		case o.blocked != nil:
			results[i].blockedBy(o.blocked)
		case o.run >= 0 && limits[o.run] != "":
			results[i].blockedBy(&Blocked{Reasons: []string{"[vm] machine limit: " + limits[o.run]}})
		case o.run >= 0:
			results[i].Err = internalError(failures[o.run])
			var timedOut *TimedOut
			if errors.As(results[i].Err, &timedOut) {
				timedOut.Hint = p.testTimeoutHint
			}
			results[i].counted(rows[o.run])
			passed = results[i].Err == nil
		default:
			results[i].Err = o.err
			results[i].counted(0)
		}
		if passes != nil {
			// Only a pass is stored: a case that failed, was blocked or
			// timed out runs again next time.
			if passed {
				o.pass.count = CaseCount{Subtests: results[i].Subtests, Cases: results[i].Cases}
				passes.keep(tc.FullName(), o.pass)
			} else {
				passes.forget(tc.FullName())
			}
		}
	}
	if passes != nil {
		names := make([]string, 0, len(p.entry.Tests()))
		for _, c := range p.entry.Tests() {
			names = append(names, c.Name())
		}
		passes.store(names)
	}
	if p.reaching != nil {
		kept := results[:0]
		for i, r := range results {
			if !outcomes[i].skip {
				kept = append(kept, r)
			}
		}
		results = kept
	}
	return results
}

func (p *Program) entryFunc(name string) *ir.Func {
	if p.entry == nil {
		return nil
	}
	for _, f := range p.entry.Funcs() {
		if f.Name() == name && len(f.Params()) == 0 {
			return f
		}
	}
	return nil
}

func (p *Program) reasons(found []vm.Unretained) []string {
	out := make([]string, 0, len(found))
	for _, u := range found {
		switch u.Kind {
		case vm.NotRetained:
			out = append(out, p.notRetained(u.Name, u.Impl))
		case vm.OnceNotRetained:
			out = append(out, fmt.Sprintf("[%s] once initializer not retained: %s", u.Name, p.decline(blockerName(u), ir.ImplFunc{})))
		case vm.NoBinding:
			out = append(out, fmt.Sprintf("[%s] crosses into Go and the VM has no binding for it", hostadapt.DisplayName(u.Name)))
		case vm.NoImplementation:
			out = append(out, fmt.Sprintf("[%s] dispatched, and no implementation was retained", u.Name))
		}
	}
	return out
}

func (p *Program) notRetained(name string, impl ir.ImplFunc) string {
	return fmt.Sprintf("[%s] not retained: %s", name, p.decline(name, impl))
}

func (p *Program) testNotRetained(name string) string {
	reason, ok := p.declines["test body: "+name]
	if !ok {
		reason = "no decline reason recorded"
	}
	return "[test body] not retained: " + reason
}

// decline is the first reason the producer's last attempt at name, a
// function of impl block impl when impl names one, declined for. The census
// names an attempt by its declaration's own name, so an IR name falls back
// to its attempt name (attemptName).
func (p *Program) decline(name string, impl ir.ImplFunc) string {
	if reason, ok := p.declines[name]; ok {
		return reason
	}
	short := attemptName(name, impl)
	if short == "" {
		short = name
	} else if reason, ok := p.declines[short]; ok {
		return reason
	}
	return p.undeclined(short)
}

// declares reports whether the entry file declares a top-level function or
// `host fn` named name.
func (p *Program) declares(name string) bool {
	for _, n := range p.prog.Entry().Nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			if d.Name == name {
				return true
			}
		case *ast.ExternFunc:
			if d.Name == name {
				return true
			}
		}
	}
	return false
}

// undeclined describes a declaration the builder made no recorded attempt at,
// from the declaration itself.
func (p *Program) undeclined(name string) string {
	for _, m := range p.prog.Modules {
		for _, n := range m.Nodes {
			switch d := n.(type) {
			case *ast.ExternFunc:
				if d.Name != name {
					continue
				}
				return "a `host fn` with no VM body"
			case *ast.FuncDef:
				if d.Name == name {
					return "no decline reason recorded"
				}
			}
		}
	}
	return "no decline reason recorded (not declared in the program's own files; " +
		"stdlib bodies are lowered once per process, outside the decline census)"
}

func machineLimit(err error) string {
	return "[vm] machine limit: " + err.Error()
}

// IsBlocked reports whether err is a *Blocked.
func IsBlocked(err error) (*Blocked, bool) {
	var b *Blocked
	if errors.As(err, &b) {
		return b, true
	}
	return nil, false
}

// HasExports reports whether the program declares an `export fn`.
func (p *Program) HasExports() bool { return len(p.res.Exports) > 0 }

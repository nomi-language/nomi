package rt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The one implementation of `nomi test`'s reporting.
//
// Every caller must produce this output byte for byte: `nomi test` on the VM,
// the FFI wrapper's test mode, and the golden files that record it. A second
// formatter would make every comparison between them a comparison of
// formatters.
//
// It lives in rt because rt links no front end, so anything that runs a
// program can reach it.
//
// Note which writer the colour decision reads: the ok/FAIL markers and the
// report body key their colouring off os.Stdout even when the report is being
// written into a buffer. A caller that captures into a buffer depends on it:
// the buffer's colour state must still be "whatever the terminal says".

// TestReporter prints test outcomes and tallies them.
//
// It is a tally as well as a formatter because the summary line and the exit
// status are the same fact, and a caller that counted separately could
// disagree with what it printed.
type TestReporter struct {
	w       io.Writer
	format  TestFormat
	passed  int
	failed  int
	blocked int
}

func NewTestReporter(w io.Writer) *TestReporter {
	return &TestReporter{w: w}
}

// Result reports one test that ran, named as the report shows it.
func (r *TestReporter) Result(name string, err error) {
	if r.format == TestFormatJSON {
		r.ResultAt(TestLocation{}, name, err)
		return
	}
	if err != nil {
		r.failed++
		r.failure(name, err)
		return
	}
	r.passed++
	fmt.Fprintf(r.w, "%s %s\n", Green("ok"), name)
}

// Fail reports a failure that prevented a file's tests from running at all —
// a load error, a preparation error. It is spelled differently from a failing
// test on purpose: nothing ran, so there is no assertion to explain.
func (r *TestReporter) Fail(name string, err error) {
	if r.format == TestFormatJSON {
		r.failed++
		r.writeFileFailure("", name, err)
		return
	}
	r.failed++
	WriteFailLine(r.w, name, err)
}

// Renderer is an error that writes itself for a reader: compiler
// diagnostics, shown with their source lines.
type Renderer interface {
	Render(w io.Writer)
}

// WriteFailLine writes `FAIL <name>: <err>`. An error that renders itself
// (Renderer) goes below the name as it renders. An error whose text is lines
// that each stand on their own, such as compiler diagnostics that each name
// their file, goes below the name instead, one line each, so a terminal or an
// editor can jump to every one.
func WriteFailLine(w io.Writer, name string, err error) {
	var rd Renderer
	if errors.As(err, &rd) {
		fmt.Fprintf(w, "FAIL %s\n", name)
		rd.Render(w)
		return
	}
	var own interface{ OwnLines() bool }
	if errors.As(err, &own) && own.OwnLines() {
		fmt.Fprintf(w, "FAIL %s\n%v\n", name, err)
		return
	}
	fmt.Fprintf(w, "FAIL %s: %v\n", name, err)
}

// Add folds in counts produced by a nested runner — the FFI path runs a test
// file in a subprocess and reports its own lines.
func (r *TestReporter) Add(passed, failed int) {
	r.passed += passed
	r.failed += failed
}

// Blocked reports one case the VM could not run, with one line per blocker:
//
//	BLOCKED <name> <blocker>
//
// A blocker is what stopped the case. For code the compiler could not lower
// it is the diagnostic `nomi check` reports, in its short form, such as
// "main_test.nomi:18:20: this call to `skip_odd` is not supported yet, so
// `fn evens` cannot run". A blocker of several lines (a diagnostic with a
// hint) prints its later lines under the first, indented by two spaces. One
// BLOCKED line per blocker, so the blocked population is a grep:
// `grep '^BLOCKED'` lists every blocker with its case. A blocked case is
// neither passed nor failed; Summary counts it separately.
func (r *TestReporter) Blocked(name string, reasons []string) {
	r.BlockedAt(TestLocation{}, name, reasons)
}

// BlockedAt is Blocked for a case whose position is known. The text report is
// Blocked's; the JSON record is a test record with status "blocked" whose
// message is the blockers, one after another, and whose `error_line` is
// loc.ErrorLine when it is not zero.
func (r *TestReporter) BlockedAt(loc TestLocation, name string, reasons []string) {
	r.blocked++
	if len(reasons) == 0 {
		reasons = []string{"(no reason recorded)"}
	}
	if r.format == TestFormatJSON {
		r.writeRecord(testRecord{
			Type:      "test",
			File:      loc.File,
			Line:      loc.Line,
			EndLine:   loc.EndLine,
			Name:      name,
			Status:    TestStatusBlocked,
			Message:   strings.Join(reasons, "\n"),
			ErrorLine: loc.ErrorLine,
		})
		return
	}
	for _, reason := range reasons {
		first, more, _ := strings.Cut(reason, "\n")
		fmt.Fprintf(r.w, "BLOCKED %s %s\n", name, first)
		if more != "" {
			fmt.Fprintf(r.w, "  %s\n", strings.ReplaceAll(more, "\n", "\n  "))
		}
	}
}

// AddBlocked folds in a blocked count a nested runner reported.
func (r *TestReporter) AddBlocked(n int) { r.blocked += n }

// BlockedCount is the number of blocked cases, the third number Summary prints
// when it is not zero.
func (r *TestReporter) BlockedCount() int { return r.blocked }

// Counts returns the tally: the two numbers Summary prints and the exit status
// is derived from.
//
// It exists because the FFI wrapper reports a file's cases through this
// reporter and then has to hand the same two numbers back to the parent
// process over its result marker. Counting them a second time in the wrapper
// is what this type's header rules out.
func (r *TestReporter) Counts() (passed, failed int) { return r.passed, r.failed }

// Summary prints the `test result:` line and reports whether anything failed
// or was blocked.
//
// The blocked count is appended only when it is not zero, so a run with no
// blocked case prints exactly the line it always has. The verdict word is
// FAILED when a case failed, BLOCKED when none failed and one was blocked, and
// ok otherwise.
func (r *TestReporter) Summary() bool {
	if r.format == TestFormatJSON {
		r.writeSummary()
		return r.failed > 0 || r.blocked > 0
	}
	blocked := ""
	if r.blocked > 0 {
		blocked = fmt.Sprintf(", %s blocked", Red(fmt.Sprintf("%d", r.blocked)))
	}
	if r.failed > 0 {
		fmt.Fprintf(r.w, "test result: %s. %s passed, %s failed%s\n",
			Red("FAILED"), Green(fmt.Sprintf("%d", r.passed)), Red(fmt.Sprintf("%d", r.failed)), blocked)
		return true
	}
	if r.blocked > 0 {
		fmt.Fprintf(r.w, "test result: %s. %s passed, %d failed%s\n",
			Red("BLOCKED"), Green(fmt.Sprintf("%d", r.passed)), r.failed, blocked)
		return true
	}
	fmt.Fprintf(r.w, "test result: %s. %s passed, %d failed\n",
		Green("ok"), Green(fmt.Sprintf("%d", r.passed)), r.failed)
	return false
}

func (r *TestReporter) failure(name string, err error) {
	fmt.Fprintf(r.w, "%s %s\n", Red("FAIL"), name)
	r.failureBody(err)
}

// failureBody is the block under a FAIL line. The JSON report's `message` is
// this block, so it is rendered here once.
func (r *TestReporter) failureBody(err error) {
	var assertionErr *AssertionFailure
	if errors.As(err, &assertionErr) {
		WriteAssertionFailure(r.w, assertionErr)
		return
	}
	var earlyErr *EarlyReturnFailure
	if errors.As(err, &earlyErr) {
		writeEarlyReturnFailure(r.w, earlyErr)
		return
	}
	fmt.Fprintf(r.w, "  %v\n", err)
}

func writeEarlyReturnFailure(w io.Writer, earlyErr *EarlyReturnFailure) {
	fmt.Fprintf(w, "  %s\n", Dim(earlyErr.Error()))
	if earlyErr.Expr != "" {
		for _, line := range strings.Split(earlyErr.Expr, "\n") {
			writeSourceLine(w, "    ", line, nomiSource)
		}
	}
	fmt.Fprintf(w, "    %s\n", Dim("returned:"))
	fmt.Fprintf(w, "      %s\n", nomiSource(earlyErr.Value))
}

// nomiSource renders a fragment of Nomi source the way the report shows it.
// See Highlight for why a compiled binary leaves it uncoloured.
func nomiSource(src string) string { return highlight(os.Stdout, src) }

// One traversal renders an assertion failure, and there are two callers with
// two presentations of it.
//
// WriteAssertionFailure is the block a test report nests under a FAIL line:
// indented one level further, its labels dimmed and its source fragments
// highlighted. FormatAssertionFailure is the same report as a plain string, and
// it is what `AssertionFailure.format(failure)` hands back to a Nomi program and
// what the wasm entry point reports a failed run with.
//
// # Why they are one function and not two
//
// Two renderers of one failure drift: one can print an Assertable value's
// `details:` rows while the other silently drops them, and nothing would catch
// it. The corpus never fails an assertion, so a comparison of corpus runs is a
// statement about the passing path only. Failure rendering has no coverage
// except the fixtures that name it.
//
// So there is one renderer, and `format` is bound to FormatNomiAssertionFailure
// here, so a test report and a Nomi program cannot render one failure
// differently.

// assertionReportStyle is everything the two presentations differ by, and it is
// only three things: how far the whole block is indented, how a label is
// decorated (`values:`, `=`, the `line N:` header), and how a source fragment or
// a rendered value is decorated.
//
// Both decorators are the identity for the plain form, which is what makes
// `Dim("=") + " " + value` and `"= " + value` the same bytes rather than two
// spellings that have to be kept in step.
type assertionReportStyle struct {
	indent string
	label  func(string) string
	source func(string) string
}

func undecorated(s string) string { return s }

// WriteAssertionFailure renders an assertion failure the way `nomi test` and
// `nomi run` report it: the `line N: <reason>` header the failure itself
// carries, the assertion as written, and whatever context was collected — the
// binding it was defined as, the operand values, the pipeline stages, the
// Assertable's own details.
//
// The absolute source line comes from the failure value, so the caller does
// not have to reproduce any of this.
func WriteAssertionFailure(w io.Writer, assertionErr *AssertionFailure) {
	writeAssertionReport(w, assertionErr, assertionReportStyle{
		indent: "  ", label: Dim, source: nomiSource,
	})
}

// FormatAssertionFailure is the same report as a string with no decoration and
// no leading indent, and with no trailing newline: it is a value a Nomi program
// can compare, embed or print, not a block being written into a report.
//
// This is std/assertions' `AssertionFailure.format`. Its output is pinned as
// absolute text in assertionformat_test.go.
func FormatAssertionFailure(failure *AssertionFailure) string {
	var b strings.Builder
	writeAssertionReport(&b, failure, assertionReportStyle{
		indent: "", label: undecorated, source: undecorated,
	})
	return strings.TrimRight(b.String(), "\n")
}

// FormatEarlyReturnFailure is an early-return failure as plain text, with no
// leading indent and no trailing newline: the tour prints it under a failed
// case.
func FormatEarlyReturnFailure(failure *EarlyReturnFailure) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", failure.Error())
	if failure.Expr != "" {
		for _, line := range strings.Split(failure.Expr, "\n") {
			writeSourceLine(&b, "  ", line, undecorated)
		}
	}
	fmt.Fprintln(&b, "  returned:")
	fmt.Fprintf(&b, "    %s", failure.Value)
	return b.String()
}

func writeAssertionReport(w io.Writer, failure *AssertionFailure, st assertionReportStyle) {
	p := st.indent
	fmt.Fprintf(w, "%s%s\n", p, st.label(failure.Error()))
	exprHeader := failure.Keyword + " " + failure.Expr
	if assertionExprAlreadyIncludesKeyword(failure.Expr, failure.Keyword) {
		exprHeader = failure.Expr
	}
	st.writeSource(w, p+"  ", exprHeader)
	if failure.Binding != nil {
		fmt.Fprintf(w, "%s  %s\n", p, st.label("defined as:"))
		st.writeSource(w, p+"    ", failure.Binding.Expr)
		if len(failure.Binding.Pipeline) > 0 {
			// A binding's stages are not compacted, where an observed value's
			// are. Deliberate and pinned both ways: this block already shows the
			// defining expression whole just above it, so a stage that repeats
			// its own prefix reads as the pipeline it is.
			fmt.Fprintf(w, "%s  %s\n", p, st.label("pipeline values:"))
			for _, stage := range failure.Binding.Pipeline {
				st.writeSource(w, p+"    ", stage.Expr)
				st.writeValue(w, p+"      ", stage.Value)
			}
		}
	}
	if len(failure.Values) > 0 {
		// Every direct row under `values:` first, then every pipeline row under
		// `pipeline values:`, whatever order the two kinds were recorded in — so
		// the trace is walked twice rather than partitioned into a slice nobody
		// needs afterwards.
		hasDirectValues := false
		hasPipelineValues := false
		for _, observed := range failure.Values {
			if len(observed.Pipeline) > 0 {
				hasPipelineValues = true
			} else {
				hasDirectValues = true
			}
		}
		if hasDirectValues {
			fmt.Fprintf(w, "%s  %s\n", p, st.label("values:"))
		}
		for _, observed := range failure.Values {
			if len(observed.Pipeline) > 0 {
				continue
			}
			st.writeSource(w, p+"    ", observed.Expr)
			st.writeValue(w, p+"      ", observed.Value)
		}
		if hasPipelineValues {
			fmt.Fprintf(w, "%s  %s\n", p, st.label("pipeline values:"))
		}
		for _, observed := range failure.Values {
			if len(observed.Pipeline) == 0 {
				continue
			}
			displayStages := compactAssertionPipelineStages(observed.Pipeline)
			for _, stage := range observed.Pipeline {
				expr := stage.Expr
				if compact, ok := displayStages[stage.Expr]; ok {
					expr = compact
				}
				st.writeSource(w, p+"    ", expr)
				st.writeValue(w, p+"      ", stage.Value)
			}
		}
	}
	if len(failure.Details) > 0 {
		// An Assertable value's own rows. Neither half goes through `source`: a
		// detail label and its value are prose the user authored in an `impl
		// Assertable`, not source the runtime rendered, so highlighting them
		// would colour words inside a sentence. Not split on "\n" either, for
		// the same reason `actual:` is not — a label is a label.
		fmt.Fprintf(w, "%s  %s\n", p, st.label("details:"))
		for _, detail := range failure.Details {
			fmt.Fprintf(w, "%s    %s\n", p, detail.Label)
			fmt.Fprintf(w, "%s      %s %s\n", p, st.label("="), detail.Value)
		}
	}
	if failure.Actual != "" {
		// An empty Actual is absence and not an empty row, the same rule
		// nomiMaybeString applies when it turns "" into `None`.
		fmt.Fprintf(w, "%s  %s %s\n", p, st.label("actual:"), st.source(failure.Actual))
	}
}

// writeSource writes a source fragment, one output line per "\n" in it, each at
// the same column. Every position that shows Nomi source does this.
func (st assertionReportStyle) writeSource(w io.Writer, indent, src string) {
	for _, line := range strings.Split(src, "\n") {
		writeSourceLine(w, indent, line, st.source)
	}
}

// writeSourceLine writes one line of a source fragment at indent, rendered by
// render. A blank line is written empty, without the indent: a case's arms
// are separated by one, and indenting it would end the line in spaces.
func writeSourceLine(w io.Writer, indent, line string, render func(string) string) {
	if strings.TrimSpace(line) == "" {
		fmt.Fprintln(w)
		return
	}
	fmt.Fprintf(w, "%s%s\n", indent, render(line))
}

// writeValue writes the `= <value>` row that follows a source fragment.
func (st assertionReportStyle) writeValue(w io.Writer, indent, value string) {
	fmt.Fprintf(w, "%s%s %s\n", indent, st.label("="), st.source(value))
}

func compactAssertionPipelineStages(stages []AssertionPipelineStage) map[string]string {
	display := make(map[string]string, len(stages))
	var previous string
	for _, stage := range stages {
		expr := stage.Expr
		if previous != "" && strings.HasPrefix(expr, previous+"\n|> ") {
			expr = strings.TrimPrefix(expr, previous+"\n")
		}
		display[stage.Expr] = expr
		previous = stage.Expr
	}
	return display
}

func assertionExprAlreadyIncludesKeyword(expr, kw string) bool {
	if kw == "" {
		return true
	}
	trimmed := strings.TrimSpace(expr)
	if strings.HasPrefix(trimmed, kw+" ") || trimmed == kw {
		return true
	}
	for _, line := range strings.Split(trimmed, "\n") {
		if strings.TrimSpace(line) == "|> "+kw {
			return true
		}
	}
	return false
}

// DisplayPath is how a path appears in a report: relative to the working
// directory when it is under it, absolute otherwise.
//
// A compiled test binary carries the absolute path of the file it was lowered
// from and calls this at run time, exactly as `nomi test` does, so both report
// the same label from the same directory.
func DisplayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return filepath.ToSlash(rel)
}

// TestName is the label a test result is reported under: the file, then the
// test's full name.
func TestName(path, test string) string {
	name := DisplayPath(path)
	if test != "" {
		name += " :: " + test
	}
	return name
}

// DefaultTestEnv defaults NOMI_ENV to "test" for the duration of a test run,
// returning the restore. An environment the caller set is left alone.
func DefaultTestEnv() (func(), error) {
	if _, ok := os.LookupEnv("NOMI_ENV"); ok {
		return func() {}, nil
	}
	if err := os.Setenv("NOMI_ENV", "test"); err != nil {
		return nil, fmt.Errorf("default NOMI_ENV: %w", err)
	}
	return func() {
		_ = os.Unsetenv("NOMI_ENV")
	}, nil
}

// Test is one lowered `test "name" { ... }` declaration.
//
// Fn returns the failure that ended the body, or nil. A TestFailure rather than
// an error or a concrete *AssertionFailure: a test body has two value-shaped
// exits, because `try` propagating an `Err`/`None` inside one ends the case as
// an EarlyReturnFailure and not as an assertion.
//
// A nil *AssertionFailure boxed into an error interface is non-nil, and
// TestFailure must not reintroduce that hazard. Its single method is declared
// with a nil-receiver answer, so a nil failure of either concrete type still
// reads as a pass.
type Test struct {
	Name string
	Fn   func(fr *Frame) TestFailure
	// Bubble wraps the case, and nil means the real clock.
	//
	// `clock Clock.Virtual` on the declaring `tests` group sets this to
	// nomi/rt/vclock.RunCase, which runs the body inside a testing/synctest
	// bubble. `clock Clock.System` sets nothing, because the real clock is
	// already what a case with no clause gets — so the System spelling costs
	// no emitted text and no linked package, and there is only one code path
	// for "not bubbled".
	//
	// A function field rather than a `Virtual bool` plus a registration hook,
	// and the reason is a dependency direction rather than taste: the bubble
	// imports `testing` and `testing/synctest`, which must not be linked into
	// every artifact (see the vclock package header), so it cannot live here.
	// rt therefore cannot name it, and the seam has to be a value the
	// generated table supplies. A bool plus a package-level setter would work
	// and would be global mutable state reachable from every test in the
	// binary; a field is per case, which is what the language rule is —
	// `clock` is per group.
	//
	// The second parameter is the in-bubble cleanup: runTest passes
	// ResetSupervisors, the per-case supervisor drain. It is a parameter
	// because the drain has to happen inside the bubble to spend virtual time.
	Bubble func(body func() error, cleanup func()) error
}

// RunTestsLabelled is the same run with the label a case is reported under
// supplied, because there are two labellings:
//
//	`nomi test <file>`   `<path> :: <case>`, which is TestName's
//	the tour playground  `<case>`, bare
//
// The second has no path at all, because a playground block has no file a
// reader could open. Both labellings are in `testdata/expectations/` (the
// tour's records carry the bare form), so a run compared against those
// records needs both, and `TestName` cannot produce the bare form for any
// argument (`DisplayPath("")` is not empty).
//
// Supplying the label here keeps one reporting loop. The alternative is a
// second loop inside `internal/vm`, which is the "comparison of formatters"
// this file's header refuses.
//
// A function rather than a `bare bool`, because the two labellings are not a
// switch: `TestName` composes a DisplayPath with a separator, and a third
// caller with a third labelling would otherwise add a third boolean. The
// caller that has the label is the caller that knows how its command reports.
func RunTestsLabelled(ctx context.Context, w io.Writer, label func(string) string,
	tests []Test) int {
	if label == nil {
		panic("rt.RunTestsLabelled: a run with no labelling reports every case as \"\"")
	}
	restoreEnv, err := DefaultTestEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer restoreEnv()

	failures := RunTestCases(ctx, tests)

	rep := NewTestReporter(w)
	for i, t := range tests {
		rep.Result(label(t.Name), failures[i])
	}
	if rep.Summary() {
		return 1
	}
	return 0
}

// RunTestCases is the run half of RunTestsLabelled: every case in order, each
// on its own frame and clock with rt's per-case supervisor drain and boot
// cleanup, answering each case's error (nil when it passed). It reports
// nothing and does not default NOMI_ENV, so a caller that feeds several files'
// results into one TestReporter — `nomi test <dir>` on the VM — runs a file's
// cases here and reports them itself, with the same run-then-report order.
func RunTestCases(ctx context.Context, tests []Test) []error {
	fr := NewFrame(ctx)
	failures := make([]error, len(tests))
	for i, t := range tests {
		failures[i] = runTest(fr, t)
	}
	return failures
}

// runTest runs one test body, on the real clock or inside a virtual-time bubble
// as the case's `clock` declaration decided.
//
// The Nomi-fault recover is inside the bubble, and that placement is the
// reason this split exists. A Nomi trap reaches a test as a panic carrying
// *Error, and runTestBody converts it to the case's error — the text
// `nomi test` prints. vclock's layer 2 recovers any panic and renders it as
// "test panicked: …". So wrapping `t.Fn` directly would let layer 2 see the
// *Error first and report a trap under different text inside a bubble than
// outside one: a differing output, not a refusal, on every bubbled case that
// traps. Converting first means a bubbled trap and an unbubbled trap produce
// the same error value by construction rather than by two agreeing formatters.
//
// One behaviour deliberately differs, and only for a bug in rt or its caller:
// outside a bubble a panic that is not a Nomi fault is re-panicked so it keeps
// its Go traceback, and inside one vclock converts it instead. Re-panicking
// there is not available — it would reach synctest's own handler, which
// dereferences the zero-value testing.T's nil internals and kills the process
// (layer 2's reason). A readable failure beats a segfault, and neither is a
// state a correct program reaches.
func runTest(fr *Frame, t Test) error {
	if t.Bubble == nil {
		// Outside a bubble the drain is a plain deferred call: the budgets are
		// spent in real time, which is what a case with no `clock` clause asked
		// for. Deferred rather than sequential so a case that traps still
		// drains — otherwise one failing case leaks live goroutines into every
		// case after it.
		defer ResetSupervisors()
		return runTestBody(fr, t)
	}
	// Inside the bubble, which is the reason Test.Bubble has a second
	// parameter. A drain budget spent out here would be real seconds, and a
	// supervisor still holding a goroutine when the bubble closes is a hard
	// error — synctest reports "main bubble goroutine has exited but blocked
	// goroutines remain".
	return t.Bubble(func() error { return runTestBody(fr, t) }, ResetSupervisors)
}

// runTestBody converts a Nomi fault into the test's error: the case fails, its text is reported, and the run continues
// with the next case.
func runTestBody(fr *Frame, t Test) (err error) {
	fr = NewFrame(fr.ctx)
	defer func() {
		ResetSupervisors()
		defer func() {
			if r := recover(); r != nil {
				if err == nil {
					err = fmt.Errorf("%v", r)
				} else {
					err = fmt.Errorf("%w; cleanup: %v", err, r)
				}
			}
		}()
		RunBootCleanup(fr)
	}()

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		e, ok := r.(*Error)
		if !ok {
			panic(r)
		}
		err = e
	}()
	// Failure(), not a bare `!= nil` on the interface: an interface holding a
	// typed nil pointer is non-nil, and reporting that as a failure would fail
	// every case with an empty report. The nil-receiver method answers nil.
	if failure := t.Fn(fr); failure != nil {
		return failure.Failure()
	}
	return nil
}

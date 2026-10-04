package vm

// THE TEST RUNNER: `nomi test <file>` on this machine.
//
// Many records in `testdata/expectations/` are a `rt.TestReporter` REPORT
// rather than a program's own output: every stdlib and failure record, and
// many corpus and tour records. The failure population is the one with power
// over a WRONG FAILURE MESSAGE: its records carry verbatim transcript lines
// because a hash cannot show what a wrong diagnostic looks like. The test
// bodies this runner drives are built by `internal/irbuild/irtestbody.go`.
//
// # `rt` IS CALLED, AND THE ONLY NEW FUNCTION IN IT IS A WRITER PARAMETER
//
// Everything below the case bodies is `rt`'s and is reached rather than
// reproduced: the run-then-report ORDER, the `ok`/`FAIL` markers, the case
// NAMING (`rt.TestName` over `rt.DisplayPath`), the assertion report's whole
// layout, the `test result:` summary, the NOMI_ENV default, and the exit
// status. `rt.RunTestsTo` is `rt.RunTests` with the report's destination
// supplied, and `rt.RunTests` keeps its signature and its bytes by calling it
// with `os.Stdout` — the same split `rt.Dbg`/`rt.DbgText` took, for the same
// reason, and `rt/testreport.go`'s own header names the alternative: "Three
// callers must produce this output byte for byte … A second formatter would
// make every comparison between them a comparison of formatters."
//
// # What this machine supplies, and each is a fact `rt` cannot hold
//
//   - THE BODY, as an `rt.Test.Fn` closure over a retained `ir.Func`. The
//     signature takes an `*rt.Frame`. A case with no group boot ignores it:
//     this machine's activation state is its own `frame`. A case under a
//     group boot starts that boot on it and runs the body in the app it
//     publishes, so rt's per-case supervisor drain and boot cleanup see the
//     case's own app.
//   - THE BUBBLE, for a case under `clock Clock.Virtual`: `rt/vclock.RunCase`,
//     handed to rt's runner in the `rt.Test.Bubble` field.
//   - THE CONVERSION OF AN EXIT. A case has four outcomes and three of them
//     are the case's own result: it passed, an assertion failed, or it took a
//     Nomi fault. The fourth — this machine has no arm for an instruction, or
//     cannot resolve a callee — is NOT a failing case, and conflating the two
//     is the one thing that would make this instrument lie. See `RunTests`.
//   - THE WRITER. One sink for both the report and any `io.print` a case
//     makes, which is what puts a printing case's output ABOVE the whole
//     report block — `testdata/tests_mixed.nomi`'s subject, and the reason
//     `rt.RunTestsTo` writes to a parameter rather than to `os.Stdout` while
//     `io.print` goes to the machine's writer.
//
// # A Nomi fault is delivered by PANICKING with `rt`'s own error
//
// `runTestBody` recovers a panic carrying an `*rt.Error` and makes it the
// case's error, which is how rt's runner reports a trap. This machine has an
// error in hand rather than a panic, so it panics with `&rt.Error{}` and lets
// `rt` do the conversion. That is deliberately NOT a second conversion of one
// fact: `runTest`'s header records that the recover has to sit INSIDE a
// virtual-time bubble or a bubbled trap reports under different text from an
// unbubbled one, and reproducing that placement here would be the second
// implementation `rt/grapheme.go` calls "a divergence waiting for the input it
// does not have".

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/rt/vclock"
)

// RunTests runs every case this machine's entry module declares and writes the
// report to the machine's writer, answering the process exit status the command
// would use and the reason it could not run, if it could not.
//
// path is the file the report labels each case with, exactly as
// `nomi test <path>` labels it: `rt.TestName` relativizes it at run time
// through `rt.DisplayPath`, so the same machine over the same module reports
// the same label the command does from the same directory.
//
// A NON-EMPTY REASON MEANS NOTHING WAS RUN AND THE REPORT IS ABSENT, and the
// caller must not compare a partial one. Two things produce it: a module that
// declares no case at all (so the producer retained none of them), and a case
// this machine could not run to an exit. The second is the distinction the
// whole comparison rests on — a machine that reported a missing instruction as
// a FAILING CASE would turn its own limits into failure text and compare them
// against a recording, which is the shape of a test that passes by measuring
// itself.
//
// EVERY CASE IS RUN BEFORE ANYTHING IS REPORTED, and that is `rt`'s doing
// rather than this loop's: `rt.RunTestsTo` runs the slice to completion and
// then reports. So a case that prints puts its output above the whole report
// block, which is what the recording holds.
func (m *Machine) RunTests(path string) (exit int, reason string) {
	return m.RunTestsLabelled(func(name string) string { return rt.TestName(path, name) })
}

// RunTestsLabelled is the same run with the LABEL a case is reported under
// supplied, which is what a TOUR block needs: the playground reports a case
// under its bare name because a playground block has no file a reader could
// open, while `nomi test <file>` reports `<path> :: <case>`.
//
// BOTH LABELLINGS ARE IN THE COMMITTED RECORDS — 225 of the 239
// report-shaped records carry `nomi test`'s and the tour's 14 carry the bare
// one — so a runner compared against them needs both. The parameter is
// `rt.RunTestsLabelled`'s and the argument for it is there; this is the
// pass-through, and the two spellings in this package exist so a caller says
// which COMMAND it is reproducing rather than assembling a label.
func (m *Machine) RunTestsLabelled(label func(string) string) (exit int, reason string) {
	cases := m.mod.Tests()
	if len(cases) == 0 {
		return 0, "this module retained no test case"
	}
	tests := make([]rt.Test, 0, len(cases))
	var unrunnable []string
	for _, c := range cases {
		fn := c.Fn()
		if len(fn.Params()) != 0 {
			// A test body takes no operands. `ir.Module.DeclareTest` does not
			// check it — a container rejects what one call can be wrong about
			// — so this is the fence, and it is here rather than in the
			// closure because a case that cannot be entered must stop the
			// whole run rather than fail.
			unrunnable = append(unrunnable,
				fmt.Sprintf("%s: the body declares %d parameter(s)", c.Name(), len(fn.Params())))
			continue
		}
		group := c.Group()
		if limit := m.groupLimit(group); limit != "" {
			unrunnable = append(unrunnable, c.Name()+": "+limit)
			continue
		}
		test := rt.Test{Name: c.Name(), Fn: m.testFn(fn, group, &unrunnable)}
		if group.VirtualClock {
			// rt's runner opens the bubble around the boot, the body and the
			// supervisor drain, so all three spend virtual time.
			test.Bubble = vclock.RunCase
		}
		tests = append(tests, test)
	}
	if len(unrunnable) > 0 {
		return 0, strings.Join(unrunnable, "; ")
	}
	exit = rt.RunTestsLabelled(m.background(), m.out, label, tests)
	if len(unrunnable) > 0 {
		// COLLECTED DURING THE RUN AND CHECKED AFTER IT, because a body that
		// reaches an instruction this machine has no arm for does so while
		// `rt` is driving the loop. The report `rt` produced is then a report
		// of a run that did not happen, and the reason is what the caller
		// needs instead of it.
		return 0, strings.Join(unrunnable, "; ")
	}
	return exit, ""
}

// RunCases runs the given retained cases in order through `rt.RunTestCases`,
// the per-case runner a compiled test binary uses, and answers each case's
// error (nil when it passed) and each case's machine limit ("" when the
// machine ran it to an exit). It reports nothing: `nomi test` feeds several
// files' results into one `rt.TestReporter`.
//
// A NON-EMPTY LIMIT MEANS THE CASE DID NOT RUN TO ITS OWN RESULT. Its error is
// then meaningless and the caller reports the case as blocked, which is the
// distinction RunTestsLabelled draws for the whole run, drawn here per case.
func (m *Machine) RunCases(cases []ir.TestCase) (failures []error, limits []string) {
	limits = make([]string, len(cases))
	tests := make([]rt.Test, len(cases))
	for i, c := range cases {
		fn := c.Fn()
		group := c.Group()
		var caseLimits []string
		test := rt.Test{Name: c.Name()}
		switch {
		case len(fn.Params()) != 0:
			limits[i] = fmt.Sprintf("the body declares %d parameter(s)", len(fn.Params()))
		case m.groupLimit(group) != "":
			limits[i] = m.groupLimit(group)
		}
		if limits[i] != "" {
			test.Fn = func(*rt.Frame) rt.TestFailure { return nil }
			tests[i] = test
			continue
		}
		i := i
		inner := m.testFn(fn, group, &caseLimits)
		test.Fn = func(fr *rt.Frame) rt.TestFailure {
			failure := inner(fr)
			if len(caseLimits) > 0 {
				limits[i] = strings.Join(caseLimits, "; ")
			}
			return failure
		}
		if group.VirtualClock {
			test.Bubble = vclock.RunCase
		}
		tests[i] = test
	}
	failures = rt.RunTestCases(m.background(), tests)
	return failures, limits
}

// testFn is one case's body as `rt.Test.Fn`.
//
// THE THREE OUTCOMES ARE SORTED HERE and nowhere else, which is the whole of
// this file's judgement:
//
//	no error                 the case passed
//	*rt.AssertionFailure     the case failed; the failure IS the report
//	*vm.Fault                a Nomi fault; `rt` converts it from a panic
//	anything else            THIS MACHINE'S LIMIT, not the case's result
//
// The last is recorded into the caller's list and the case is reported as
// passing, because the run as a whole is about to be discarded: returning a
// failure instead would put this machine's own message into a transcript, and
// a transcript is what the comparison reads.
//
// A case with a group boot starts that boot afresh on the frame rt hands the
// case, one boot per case, and runs the body in the app it
// published. A fault in the boot fails the case the way a fault in the body
// does.
// groupLimit names a boot in group this program does not record, or "".
func (m *Machine) groupLimit(group ir.TestGroup) string {
	if b := group.Boot; b != nil && !m.testBoots[b] {
		return fmt.Sprintf("its group boot %s is not one this program records", b.Name())
	}
	if group.Boot != nil && group.Startup == nil {
		if f, err := m.resolveFunc(group.Boot, "boot"); err == nil && len(f.Params()) != 0 {
			return fmt.Sprintf("its group boot %s takes a Startup and has no startup", group.Boot.Name())
		}
	}
	return ""
}

func (m *Machine) testFn(fn *ir.Func, group ir.TestGroup, unrunnable *[]string) func(*rt.Frame) rt.TestFailure {
	return func(fr *rt.Frame) rt.TestFailure {
		run := m
		var err error
		if group.Boot != nil {
			run, err = m.bootTestOn(group, fr)
		}
		if err == nil {
			runtime := run.hostFrame
			if runtime == nil {
				runtime = rt.NewFrame(m.background())
			}
			_, err = run.activate(fn, nil, nil, runtime, true, run.depth, nil)
		}
		if err == nil {
			return nil
		}
		// A `try` whose boundary is the test body: the case ended early, and
		// the report is an rt.EarlyReturnFailure — the try's line, its text
		// as the formatter renders it, and the propagated variant as
		// rt.RowText renders it.
		var early *tryReturn
		if errors.As(err, &early) && early.at != nil {
			// A `try` that carried an AssertionFailure (`try testing.check(e)`)
			// is that assertion's failure, so the report is the check's own,
			// and the `try` was only transport.
			if e, isRec := enumRecord(early.value); isRec && isEnum(e, "results.Result") && variantName(e) == "Err" {
				if payload, has := payloadOf(e); has {
					if f, isFailure := nomiFailureOf(payload); isFailure {
						return f.Report()
					}
				}
			}
			return &rt.EarlyReturnFailure{Line: early.at.Pos().Line(), Expr: early.at.Text(),
				Value: rt.RowText(early.value)}
		}
		if failure, isAssertion := asAssertFailure(err); isAssertion {
			return failure
		}
		if fault, isFault := asFault(err); isFault {
			// `rt`'s OWN CONVERSION, reached by panicking. See the file header on why the recover is not
			// reproduced here.
			panic(&rt.Error{Msg: fault.Error()})
		}
		*unrunnable = append(*unrunnable, fmt.Sprintf("%s: %s", fn.Name(), err))
		return nil
	}
}

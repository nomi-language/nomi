package ir

import (
	"fmt"
	"slices"
)

// A MODULE'S TEST TABLE: the cases `nomi test <file>` runs, in the order it
// runs them.
//
// WHICH ROUTING READS IT, which is module.go's own test for a field on this
// container and the reason this is not a seventh absent-and-argued entry in
// that header. `internal/vm`'s runner reads it: a record in
// `testdata/expectations/{corpus,stdlib,failure}.expect` whose `N` is non-zero
// is a `rt.TestReporter` REPORT rather than a program's output, and 232 of the
// 468 committed records are that shape. A machine can only produce one if it
// can enumerate a file's cases by name and run each body; `Funcs()` cannot
// answer either, because a test body is not a declaration.
//
// THAT IS ALSO WHY A CASE IS NOT AN `AddFunc` WITH A NAMING CONVENTION. A
// `Func` in `Funcs()` is something the module DECLARES and something a `Ref`
// or a `Call` can name; a test body is reachable only from the runner, has no
// `Symbol` to be called by, and must not be resolvable as a callee. Keeping
// the two lists apart is also what keeps every existing count still: the
// retention probes count through `internal/irbuild`'s own observation hook and
// `FuncFor` scans `Funcs()`, and neither sees a case.
//
// THE ORDER IS THE CONTRACT AND NOT A CONVENIENCE. `rt.RunTests` runs the
// slice it is handed in order and reports in that same order, and `nomi test`
// collects cases in DECLARATION order — so the sequence here is part of the
// output a record compares against, exactly as the `ok`/`FAIL` lines are. A
// container that sorted, deduplicated or reordered would change a transcript.
//
// THE NAME IS THE REPORTED ONE, joined with " / " for a case in a group by the
// producer that collected it. It is carried rather than derived because the
// joining rule is the report's and lives in `internal/irbuild`'s
// `collectTestCases` beside `runtime`'s copy of it; a second joining here
// would be a third statement of a byte-for-byte contract.

// TestCase is one lowered `test "name" { … }` declaration: the name the report
// labels it with, the body to run, and what its enclosing `tests` group sets
// up around it.
type TestCase struct {
	name  string
	fn    *Func
	group TestGroup
}

// TestGroup is what a case inherits from its enclosing `tests` group and a
// runner must arrange before the body runs.
//
// Boot is the entry boot the group's `boot` line calls, or nil. It names a
// boot the module records with AddTestBoot, and a runner starts it afresh for
// each case. Startup is the zero-parameter function that evaluates the
// Startup the boot receives, the `boot` line's argument or the boot
// parameter's default; a runner calls it before the boot, on the same frame.
// It is nil for a boot that takes no parameter. VirtualClock marks a group whose
// `clock` declaration names `Clock.Virtual`: the case, its boot and its
// cleanup run in a virtual-time bubble.
//
// The group's `setup` and a case's pattern are not here: the producer lowers
// them into the body, where the value they produce flows as ordinary
// temporaries.
type TestGroup struct {
	Boot         *Symbol
	Startup      *Symbol
	VirtualClock bool
}

// Name is the case's reported name.
func (c TestCase) Name() string { return c.name }

// Fn is the body. It takes no operands and answers nothing: a test body's
// value is discarded by the runner, and how a failure LEAVES it is the
// consumer's (an unwind in the VM). See the fault/delivery division in
// ir.go's package header and assert.go.
func (c TestCase) Fn() *Func { return c.fn }

// Group is what the case's enclosing `tests` group sets up around it.
func (c TestCase) Group() TestGroup { return c.group }

// DeclareTest records one case this module's file declares.
//
// It rejects an unnamed case and a nil body — the two things ONE CALL can be
// wrong about, which is `DeclareCell`'s line — and accepts a repeated name,
// because whether two cases share a reported name is a fact about the SET.
// `internal/irbuild`'s `refuseDuplicateTestNames` already refuses that file
// outright, on the ground that duplicates are reported instead of running
// anything, so a container that panicked here would turn a refusal
// into a crash.
func (m *Module) DeclareTest(name string, f *Func) {
	m.DeclareTestIn(name, f, TestGroup{})
}

// DeclareTestIn records one case that runs inside the group's setup g. A
// non-nil g.Boot must be a boot this module records with AddTestBoot, so a
// runner can refuse a case whose boot it cannot start.
func (m *Module) DeclareTestIn(name string, f *Func, g TestGroup) {
	m.DeclareTestAt(len(m.tests), name, f, g)
}

// DeclareTestAt records one case at position i of the declaration order, for
// a producer that builds a case's body after the cases declared later in the
// file. It checks what DeclareTestIn checks, and i must lie in [0, len].
func (m *Module) DeclareTestAt(i int, name string, f *Func, g TestGroup) {
	if name == "" {
		panic("ir: Module.DeclareTest: a case with no name cannot be reported")
	}
	if f == nil {
		panic("ir: Module.DeclareTest: a case with no body runs nothing")
	}
	if i < 0 || i > len(m.tests) {
		panic(fmt.Sprintf("ir: Module.DeclareTestAt: position %d outside [0, %d]", i, len(m.tests)))
	}
	m.tests = slices.Insert(m.tests, i, TestCase{name: name, fn: f, group: g})
}

// Tests are the cases this module's file declares, in declaration order.
func (m *Module) Tests() []TestCase { return m.tests }

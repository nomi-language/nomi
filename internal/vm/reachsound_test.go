package vm_test

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// TestReach_EveryEnteredFunctionIsReached holds the reach walk (reach.go)
// to what the machine runs: every function a run enters is in the set the
// walk reaches from the roots that run started from. A built image keeps
// only that set (vm.PruneImage), the pre-run check names a missing body
// only within it (Machine.Unretained), and the capability lists and target
// checks read only the calls in it, so a function the walk misses is one a
// built program cannot call and an outside call no list holds.
//
// The machine counts entries (Machine.WithEntryCounts), which sees every
// body it ran, a lambda's and a test case's included. The inputs, each on a
// fresh counting machine:
//
//   - every corpus and tour record that is not a test report, run as
//     vmRun runs it (boot, then main), against the walk from main, its
//     failure renderer and the boot (vm.MainRoots);
//   - every corpus, failure and tour record that is a test report, and
//     every std module's attached tests, each case run on its own
//     (Machine.RunCases), against the walk from that case and its group's
//     boot and startup: the walk `nomi test` checks a case with before it
//     runs it. A case whose walk reaches a body that was not retained is
//     skipped, as `nomi test` blocks it.
//
// A miss fails naming the program or case, the functions entered and the
// roots. Every input runs, in about 30 seconds, so there is no sample:
//
//	go test ./internal/vm -run '^TestReach_EveryEnteredFunctionIsReached$' -count=1
func TestReach_EveryEnteredFunctionIsReached(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers and runs every program and test case of five populations; -short")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	s := &soundness{t: t}
	for _, population := range []string{"corpus", "tour"} {
		set, err := expectation.Load(population)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, c := range set.Cases {
			if c.Cases == 0 {
				ids = append(ids, c.ID)
			}
		}
		sort.Strings(ids)
		resolve := vmPathResolver(t, population)
		for _, id := range ids {
			if path, ok := resolve(id); ok {
				s.program(population+" "+id, path)
			}
		}
	}
	for _, population := range []string{"corpus", "failure", "tour"} {
		set, err := expectation.Load(population)
		if err != nil {
			t.Fatal(err)
		}
		ids, _ := reportShapedIDs(set)
		resolve := vmReportPathResolver(t, population)
		for _, id := range ids {
			path, ok := resolve(id)
			if !ok {
				continue
			}
			prog, res, mod, reason := vmLowerTests(path)
			if reason != "" {
				s.refuse(reason)
				continue
			}
			s.files++
			m := vmTestMachine(prog, path, mod, res.IRModules(), io.Discard).WithEntryCounts()
			s.cases(population+" "+id, m, mod.Tests())
		}
	}
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	set, err := expectation.Load("stdlib")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range set.Cases {
		path := filepath.Join(root, "std", filepath.FromSlash(c.ID)+".nomi")
		_, entry, res := stdlibTestUnit(path)
		if entry == nil {
			s.refuse("the std module's tests do not lower")
			continue
		}
		s.files++
		m := vm.NewProgram(entry, res.IRModules(), io.Discard).WithEntryCounts()
		s.cases("std/"+c.ID, m, entry.Tests())
	}
	var refusals []string
	for reason, n := range s.refused {
		refusals = append(refusals, fmt.Sprintf("%d %s", n, reason))
	}
	sort.Strings(refusals)
	t.Logf("%d programs and %d test files (%d cases) run, %d functions entered, %d blocked; not run: %s",
		s.programs, s.files, s.caseRuns, s.entered, s.blocked, strings.Join(refusals, "; "))
	if s.programs == 0 || s.caseRuns == 0 {
		t.Fatal("no program or case ran, so this checked nothing")
	}
}

// soundness counts what TestReach_EveryEnteredFunctionIsReached ran.
type soundness struct {
	t                                           *testing.T
	programs, files, caseRuns, entered, blocked int
	// refused counts the inputs that did not lower or have no main, by
	// reason.
	refused map[string]int
}

func (s *soundness) refuse(reason string) {
	if s.refused == nil {
		s.refused = map[string]int{}
	}
	if i := strings.Index(reason, " ("); i >= 0 {
		reason = reason[:i]
	}
	s.refused[reason]++
}

// program runs path's main on a counting machine and checks what it
// entered against the walk from main's roots and the boot.
func (s *soundness) program(label, path string) {
	m, mod, reason := vmOpenProgram(path, io.Discard, io.Discard)
	if reason != "" {
		s.refuse(reason)
		return
	}
	m = m.WithEntryCounts()
	var main *ir.Func
	for _, f := range mod.Funcs() {
		if f.Name() == "main" && len(f.Params()) == 0 {
			main = f
		}
	}
	roots := vm.MainRoots(mod, main)
	var boots []*ir.Symbol
	if boot := mod.Boot(); boot != nil {
		boots = append(boots, boot)
	}
	reached, blocked := m.Reached(roots, boots)
	if len(blocked) > 0 {
		s.blocked++
		return
	}
	s.programs++
	if booted, err := m.Boot(); err == nil {
		_, _ = booted.Run("main")
	}
	s.check(label, m, nil, reached, roots, boots)
}

// cases runs each of cases on its own on m, a counting machine, and checks
// what each entered against the walk from that case and its group's boot.
func (s *soundness) cases(label string, m *vm.Machine, cases []ir.TestCase) {
	for _, c := range cases {
		roots := []*ir.Func{c.Fn()}
		var boots []*ir.Symbol
		if b := c.Group().Boot; b != nil {
			boots = append(boots, b, c.Group().Startup)
		}
		reached, blocked := m.Reached(roots, boots)
		if len(blocked) > 0 {
			s.blocked++
			continue
		}
		s.caseRuns++
		before := vm.EntryCounts(m)
		m.RunCases([]ir.TestCase{c})
		s.check(label+" :: "+c.Name(), m, before, reached, roots, boots)
	}
}

// check fails for each function m entered more often than before records
// that reached lacks.
func (s *soundness) check(label string, m *vm.Machine, before map[*ir.Func]uint64,
	reached map[*ir.Func]bool, roots []*ir.Func, boots []*ir.Symbol) {
	s.t.Helper()
	var missed []string
	for f, n := range vm.EntryCounts(m) {
		if n <= before[f] {
			continue
		}
		s.entered++
		if !reached[f] {
			missed = append(missed, f.Name())
		}
	}
	if len(missed) == 0 {
		return
	}
	sort.Strings(missed)
	var from []string
	for _, f := range roots {
		if f != nil {
			from = append(from, f.Name())
		}
	}
	for _, b := range boots {
		if b != nil {
			from = append(from, "boot "+b.Name())
		}
	}
	s.t.Errorf("%s: the machine entered %s, which the walk from %s does not reach",
		label, strings.Join(missed, ", "), strings.Join(from, ", "))
}

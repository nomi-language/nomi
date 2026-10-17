package vm_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// syncOut is a program's output, written by one pass at a time and read by
// the test.
type syncOut struct {
	mu sync.Mutex
	b  strings.Builder
}

func (o *syncOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *syncOut) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

// travelSession is a debugSession whose run starts every pass a step back
// plans, as nomi dap's does.
type travelSession struct {
	*debugSession
	shown *syncOut
}

func travelProgram(t *testing.T, src, input string) *travelSession {
	t.Helper()
	var opts []vmhost.Option
	if input != "" {
		opts = append(opts, vmhost.WithInput(strings.NewReader(input)))
	}
	p, err := vmhost.LoadSource("dbg", src, opts...)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s := &debugSession{t: t, d: vm.NewDebugger(), p: p, done: make(chan error, 1)}
	p.SetDebugger(s.d)
	s.d.SetExceptionFilters(vm.ExceptionFilters{Traps: true, Assertions: true, MainErrors: true})
	return &travelSession{debugSession: s, shown: &syncOut{}}
}

func (s *travelSession) runPasses() {
	go func() {
		for {
			pass := s.d.StartPass()
			err := s.p.Run(context.Background(), pass.Gate(s.shown), nil, false)
			if pass.Abandoned() && s.d.Planned() {
				continue
			}
			s.done <- err
			return
		}
	}()
}

// line checks a stop's reason and line.
func (s *travelSession) line(st vm.Stop, reason vm.StopReason, line int) {
	s.t.Helper()
	if st.Reason != reason || st.Pos.Line() != line {
		s.t.Fatalf("stopped for %q at line %d (%s), want %q at line %d", st.Reason, st.Pos.Line(), st.Text, reason, line)
	}
}

func (s *travelSession) topLocals(thread int) string {
	s.t.Helper()
	frames, _ := s.stack(thread)
	return s.locals(frames[0].ID)
}

const travelSrc = `import std/io
import std/instant.Instant

fn twice(n: Int) -> Int {
    m = n * 2
    m
}

fn main() {
    t = Instant.as_nanos(Instant.now())
    line = Result.with_default(io.read_line(), "none")
    x = twice(3)
    io.print("${line} ${x}")
    y = x + 1
    io.print("y ${y} ${t}")
}
`

// Step back replays from the start: the clock and the input answer as they
// did, nothing is printed again, a call is passed over, and going forward
// past the present runs on live.
func TestTravel_StepBackReplaysWithRecordedEffects(t *testing.T) {
	s := travelProgram(t, travelSrc, "alpha\n")
	s.breakAt(13, 14)
	s.runPasses()
	st := s.stop()
	s.line(st, vm.StopBreakpoint, 13)
	at13 := s.d.Stats().Steps
	want13 := s.topLocals(st.Thread)
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 14)
	recorded := s.d.Stats().Recorded
	if recorded != 3 {
		t.Fatalf("recorded %d crossings, want 3 (the clock, read_line, print): %+v", recorded, s.d.Recording())
	}
	at14 := s.topLocals(st.Thread)

	if err := s.d.StepBack(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopStep, 13)
	stats := s.d.Stats()
	if stats.Steps != at13 || stats.Answered != 2 || stats.Passes != 2 {
		t.Fatalf("the replay to line 13 ran %d statements and answered %d crossings over %d passes; want %d, 2, 2",
			stats.Steps, stats.Answered, stats.Passes, at13)
	}
	if got := s.topLocals(st.Thread); got != want13 {
		t.Fatalf("locals after stepping back:\n  got  %s\n  want %s", got, want13)
	}
	for _, want := range []int{12, 11, 10} {
		if err := s.d.StepBack(st.Thread); err != nil {
			t.Fatal(err)
		}
		st = s.stop()
		s.line(st, vm.StopStep, want)
	}
	if err := s.d.StepBack(st.Thread); !errors.Is(err, vm.ErrNoHistory) {
		t.Fatalf("step back at the first statement: %v, want ErrNoHistory", err)
	}
	if out := s.shown.String(); out != "alpha 6\n" {
		t.Fatalf("output after stepping back: %q, want only the first pass's line", out)
	}
	// Forward again: line 13 prints nothing (recorded history), line 14 is
	// the present, line 15 prints live.
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 13)
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 14)
	if got := s.topLocals(st.Thread); got != at14 {
		t.Fatalf("locals at the present again:\n  got  %s\n  want %s", got, at14)
	}
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	out := s.shown.String()
	if !strings.HasPrefix(out, "alpha 6\ny 7 ") || strings.Count(out, "\n") != 2 {
		t.Fatalf("output: %q, want each line once", out)
	}
	if !strings.Contains(at14, "t: Int = "+strings.TrimSuffix(strings.TrimPrefix(out, "alpha 6\ny 7 "), "\n")) {
		t.Fatalf("the clock printed %q, which the recording did not answer (%s)", out, at14)
	}
}

const addSrc = `import std/io

fn add(acc: Int, n: Int) -> Int {
    s = acc + n
    s
}

fn main() {
    a = add(0, 1)
    b = add(a, 2)
    c = add(b, 3)
    io.print("${c}")
}
`

// Reverse continue goes to the latest earlier pass a breakpoint stopped at,
// its condition as it evaluated; with none, to the first statement. After
// the breakpoints change it scans first.
func TestTravel_ReverseContinueToConditionalBreakpoints(t *testing.T) {
	s := travelProgram(t, addSrc, "")
	cond := func(sc *vm.Scope) vm.Hit {
		v, _ := sc.Var("n")
		return vm.Hit{Stop: v.Value.(int64) >= 2}
	}
	s.d.SetBreakpointSpecs("dbg.nomi", []vm.BreakpointSpec{{Line: 4, Hit: cond}, {Line: 12}})
	s.runPasses()
	st := s.stop()
	s.line(st, vm.StopBreakpoint, 4)
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 4)
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 12)
	passes := s.d.Stats().Passes
	for _, n := range []string{"3", "2"} {
		if err := s.d.ReverseContinue(st.Thread); err != nil {
			t.Fatal(err)
		}
		st = s.stop()
		s.line(st, vm.StopBreakpoint, 4)
		if got := s.topLocals(st.Thread); !strings.Contains(got, "n: Int = "+n) {
			t.Fatalf("reverse continue stopped with %s, want n = %s", got, n)
		}
	}
	if err := s.d.ReverseContinue(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopStart, 9)
	if err := s.d.ReverseContinue(st.Thread); !errors.Is(err, vm.ErrNoHistory) {
		t.Fatalf("reverse continue at the start: %v", err)
	}
	if got := s.d.Stats().Passes; got != passes+3 {
		t.Fatalf("three reverse continues ran %d passes, want 3", got-passes)
	}
	// Forward to the end of main, then a breakpoint the passes never had.
	s.d.SetBreakpointSpecs("dbg.nomi", []vm.BreakpointSpec{{Line: 12}})
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 12)
	s.d.SetBreakpointSpecs("dbg.nomi", []vm.BreakpointSpec{{Line: 4}})
	passes = s.d.Stats().Passes
	if err := s.d.ReverseContinue(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 4)
	if got := s.topLocals(st.Thread); !strings.Contains(got, "n: Int = 3") {
		t.Fatalf("after a scan, stopped with %s, want n = 3", got)
	}
	if got := s.d.Stats().Passes; got != passes+2 {
		t.Fatalf("a reverse continue after a breakpoint change ran %d passes, want 2 (the scan and the replay)", got-passes)
	}
	s.d.SetBreakpointSpecs("dbg.nomi", nil)
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if out := s.shown.String(); out != "6\n" {
		t.Fatalf("output %q", out)
	}
}

// A restart is history: a step back after one replays it, and restarting in
// recorded history cuts the history there.
func TestTravel_StepBackAfterARestart(t *testing.T) {
	s := travelProgram(t, addSrc, "")
	s.breakAt(5)
	s.runPasses()
	st := s.stop()
	s.line(st, vm.StopBreakpoint, 5)
	frames, _ := s.stack(st.Thread)
	if _, err := s.d.RestartFrame(frames[0].ID); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopRestart, 4)
	restarted := s.d.Stats().Steps
	s.d.StepOver(st.Thread)
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 5)
	if err := s.d.StepBack(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopStep, 4)
	if got := s.d.Stats().Steps; got != restarted {
		t.Fatalf("stepped back to statement %d, want %d, the restarted frame's first", got, restarted)
	}
	if got := s.topLocals(st.Thread); got != "acc: Int = 0, n: Int = 1" {
		t.Fatalf("locals %s", got)
	}
	// Back once more: before the restart, at the call in main... then a
	// restart there, in recorded history, cuts the history.
	if err := s.d.StepBack(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopStep, 5)
	frames, _ = s.stack(st.Thread)
	if _, err := s.d.RestartFrame(frames[0].ID); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopRestart, 4)
	if s.d.Stats().Recorded != 0 {
		t.Fatalf("the log kept %d crossings after the restart", s.d.Stats().Recorded)
	}
	s.breakAt()
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if out := s.shown.String(); out != "6\n" {
		t.Fatalf("output %q", out)
	}
}

// A program that starts a task turns step back off from the spawn on.
func TestTravel_ATaskTurnsStepBackOff(t *testing.T) {
	s := travelProgram(t, `import {
    std/io
    std/tasks.Task
}

fn work(n: Int) -> Int {
    r = n * 2
    r
}

fn main() {
    v = concurrent {
        t = Task.spawn(fn -> work(21))
        Task.await(t)
    }

    io.print("${v}")
}
`, "")
	s.breakAt(17)
	s.runPasses()
	st := s.stop()
	err := s.d.StepBack(st.Thread)
	if err == nil || !strings.Contains(err.Error(), "started a task (concurrent at dbg.nomi:12)") {
		t.Fatalf("step back in a task program: %v", err)
	}
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

// A replay that does not make the crossing it recorded stops, saying so,
// and the program goes on live.
func TestTravel_ADivergedReplayStops(t *testing.T) {
	s := travelProgram(t, travelSrc, "alpha\n")
	s.breakAt(14)
	s.runPasses()
	st := s.stop()
	s.d.RenameCrossingForTest(1, "io.read_file")
	if err := s.d.StepBack(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	if st.Reason != vm.StopDiverged || st.Pos.Line() != 11 ||
		!strings.Contains(st.Text, "(dbg.nomi:11), where the recording has io.read_file at statement") {
		t.Fatalf("stop %+v", st)
	}
	if off := s.d.TravelOff(); !strings.Contains(off, "diverged") {
		t.Fatalf("time travel is not off: %q", off)
	}
	s.breakAt()
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	// The read after the divergence is live, and the input is used up.
	if out := s.shown.String(); !strings.HasPrefix(out, "alpha 6\nnone 6\ny 7 ") {
		t.Fatalf("output %q", out)
	}
}

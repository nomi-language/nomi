package vm_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// A debug session over one program, driven the way a DAP server drives the
// Debugger: breakpoints, then a run on its own goroutine, then a stop at a
// time, each answered with a step or a continue.
type debugSession struct {
	t    *testing.T
	d    *vm.Debugger
	p    *vmhost.Program
	out  bytes.Buffer
	done chan error
}

// debugProgram loads src as dbg.nomi under a fresh debugger with every
// exception filter on.
func debugProgram(t *testing.T, src string) *debugSession {
	t.Helper()
	p, err := vmhost.LoadSource("dbg", src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s := &debugSession{t: t, d: vm.NewDebugger(), p: p, done: make(chan error, 1)}
	p.SetDebugger(s.d)
	s.d.SetExceptionFilters(vm.ExceptionFilters{Traps: true, Assertions: true, MainErrors: true})
	return s
}

func (s *debugSession) breakAt(lines ...int) []vm.Breakpoint {
	return s.d.SetBreakpoints("dbg.nomi", lines)
}

// run starts main.
func (s *debugSession) run() {
	go func() { s.done <- s.p.Run(context.Background(), &s.out, nil, false) }()
}

// runTests starts the program's test cases.
func (s *debugSession) runTests() {
	go func() {
		var failed []string
		for _, c := range s.p.Cases(&s.out, vmhost.TestOptions{}) {
			if c.Err != nil || c.Blocked != nil {
				failed = append(failed, c.Name)
			}
		}
		if len(failed) > 0 {
			s.done <- fmt.Errorf("failed: %s", strings.Join(failed, ", "))
			return
		}
		s.done <- nil
	}()
}

// stop waits for the next stop.
func (s *debugSession) stop() vm.Stop {
	s.t.Helper()
	select {
	case st := <-s.d.Events():
		return st
	case err := <-s.done:
		s.t.Fatalf("the program ended (%v) where a stop was expected; output:\n%s", err, s.out.String())
	case <-time.After(10 * time.Second):
		s.t.Fatalf("no stop within 10s")
	}
	return vm.Stop{}
}

// finish waits for the program to end.
func (s *debugSession) finish() error {
	s.t.Helper()
	select {
	case st := <-s.d.Events():
		s.t.Fatalf("an unexpected stop: %+v", st)
	case err := <-s.done:
		return err
	case <-time.After(10 * time.Second):
		s.t.Fatalf("the program did not end within 10s")
	}
	return nil
}

// stack renders a stopped thread's stack as `name:line` frames.
func (s *debugSession) stack(thread int) ([]vm.StackFrame, string) {
	s.t.Helper()
	frames, err := s.d.Stack(thread)
	if err != nil {
		s.t.Fatal(err)
	}
	parts := make([]string, len(frames))
	for i, f := range frames {
		parts[i] = fmt.Sprintf("%s:%d", f.Name, f.Pos.Line())
		if f.Tail {
			parts[i] += "(tail)"
		}
	}
	return frames, strings.Join(parts, " ")
}

// locals renders a frame's locals as `name: Type = text`.
func (s *debugSession) locals(frameID int) string {
	s.t.Helper()
	vars, err := s.d.Locals(frameID)
	if err != nil {
		s.t.Fatal(err)
	}
	parts := make([]string, len(vars))
	for i, v := range vars {
		parts[i] = v.Name + ": " + v.Type + " = " + v.Value.Text()
	}
	return strings.Join(parts, ", ")
}

// at checks a stop: its reason, and the stopped thread's stack and the top
// frame's locals.
func (s *debugSession) at(st vm.Stop, reason vm.StopReason, stack, locals string) []vm.StackFrame {
	s.t.Helper()
	if st.Reason != reason {
		s.t.Fatalf("stopped for %q at %s, want %q", st.Reason, st.Pos, reason)
	}
	frames, got := s.stack(st.Thread)
	if got != stack {
		s.t.Fatalf("stack at %s:\n  got  %s\n  want %s", st.Pos, got, stack)
	}
	if l := s.locals(frames[0].ID); l != locals {
		s.t.Fatalf("locals at %s:\n  got  %s\n  want %s", st.Pos, l, locals)
	}
	return frames
}

const stepSrc = `import std/io

fn double(n: Int) -> Int {
  m = n * 2
  m
}

fn main() {
  x = 1
  y = double(x)
  io.print("${y}")
}
`

func TestDebug_ABreakpointInMainAndStepsOverInAndOut(t *testing.T) {
	s := debugProgram(t, stepSrc)
	if bps := s.breakAt(9); !bps[0].Verified || bps[0].Resolved != 9 {
		t.Fatalf("breakpoint: %+v", bps)
	}
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "main:9", "")
	s.d.StepOver(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "main:10", "x: Int = 1")
	s.d.StepIn(st.Thread)
	st = s.stop()
	frames := s.at(st, vm.StopStep, "double:4 main:10", "n: Int = 1")
	if l := s.locals(frames[1].ID); l != "x: Int = 1" {
		t.Fatalf("main's locals while double runs: %s", l)
	}
	s.d.StepOver(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "double:5 main:10", "n: Int = 1, m: Int = 2")
	s.d.StepOut(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "main:11", "x: Int = 1, y: Int = 2")
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if s.out.String() != "2\n" {
		t.Fatalf("output %q", s.out.String())
	}
}

func TestDebug_ABreakpointLandsOnTheNextStatement(t *testing.T) {
	s := debugProgram(t, stepSrc)
	bps := s.breakAt(7, 99)
	if !bps[0].Verified || bps[0].Resolved != 9 {
		t.Fatalf("a breakpoint on the blank line before main: %+v", bps[0])
	}
	if bps[1].Verified {
		t.Fatalf("a breakpoint past the end of the file landed: %+v", bps[1])
	}
}

const lambdaSrc = `import std/io

fn main() {
  xs = [1, 2, 3]
  k = 10
  ys = xs
    |> Iter.map(fn v -> {
      w = v + k
      w
    })
    |> Iter.to_list()
  io.print("${ys}")
}
`

func TestDebug_ABreakpointInAnIterCallback(t *testing.T) {
	s := debugProgram(t, lambdaSrc)
	s.breakAt(8)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "<lambda>:8 Iter.map:11 main:11", "v: Int = 1, k: Int = 10")
	s.d.StepOver(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "<lambda>:9 Iter.map:11 main:11", "v: Int = 1, k: Int = 10, w: Int = 11")
	s.d.Continue()
	st = s.stop()
	s.at(st, vm.StopBreakpoint, "<lambda>:8 Iter.map:11 main:11", "v: Int = 2, k: Int = 10")
	s.breakAt()
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if s.out.String() != "[11, 12, 13]\n" {
		t.Fatalf("output %q", s.out.String())
	}
}

func TestDebug_StepOverPassesAPipelinesStages(t *testing.T) {
	s := debugProgram(t, lambdaSrc)
	s.breakAt(6)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "main:6", "xs: List<Int> = [1, 2, 3], k: Int = 10")
	s.d.StepOver(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "main:12", "xs: List<Int> = [1, 2, 3], k: Int = 10, ys: List<Int> = [11, 12, 13]")
	s.d.StepOver(st.Thread)
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDebug_StepInEntersTheStagesAndTheirCallbacks(t *testing.T) {
	s := debugProgram(t, lambdaSrc)
	s.breakAt(6)
	s.run()
	st := s.stop()
	var lines []string
	for range 6 {
		s.d.StepIn(st.Thread)
		st = s.stop()
		_, stack := s.stack(st.Thread)
		lines = append(lines, stack)
	}
	got := strings.Join(lines, " | ")
	want := "main:7 | main:11 | <lambda>:8 Iter.map:11 main:11 | <lambda>:9 Iter.map:11 main:11 | " +
		"<lambda>:8 Iter.map:11 main:11 | <lambda>:9 Iter.map:11 main:11"
	if got != want {
		t.Fatalf("step in from the pipeline:\n  got  %s\n  want %s", got, want)
	}
	s.breakAt()
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

const tailSrc = `import std/io

fn count(n: Int, acc: Int) -> Int {
  if n == 0 {
    acc
  } else {
    count(n - 1, acc + n)
  }
}

fn main() {
  total = count(3, 0)
  io.print("${total}")
}
`

func TestDebug_ATailCallReplacesItsCaller(t *testing.T) {
	s := debugProgram(t, tailSrc)
	s.breakAt(5)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "count:5(tail) main:12", "n: Int = 0, acc: Int = 6")
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

const shadowSrc = `import std/io

fn main() {
  x = 1
  x = x + 1
  if x > 1 {
    x = x * 10
    io.print("${x}")
  }
  io.print("${x}")
}
`

func TestDebug_AShadowedNameShowsTheBindingInForce(t *testing.T) {
	s := debugProgram(t, shadowSrc)
	s.breakAt(5, 8, 10)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "main:5", "x: Int = 1")
	s.d.Continue()
	st = s.stop()
	s.at(st, vm.StopBreakpoint, "main:8", "x: Int = 20")
	s.d.Continue()
	st = s.stop()
	s.at(st, vm.StopBreakpoint, "main:10", "x: Int = 2")
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

const trapSrc = `import std/io

fn ratio(a: Int, b: Int) -> Int {
  q = a / b
  q
}

fn main() {
  n = 0
  io.print("${ratio(10, n)}")
}
`

func TestDebug_ATrapStopsWhereItHappens(t *testing.T) {
	s := debugProgram(t, trapSrc)
	s.run()
	st := s.stop()
	s.at(st, vm.StopTrap, "ratio:4 main:10", "a: Int = 10, b: Int = 0")
	if !strings.Contains(st.Text, "division by zero") {
		t.Fatalf("the trap's text: %q", st.Text)
	}
	s.d.Continue()
	err := s.finish()
	if err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("the program's failure after the stop: %v", err)
	}
}

func TestDebug_TrapsAreNotStopsWithoutTheFilter(t *testing.T) {
	s := debugProgram(t, trapSrc)
	s.d.SetExceptionFilters(vm.ExceptionFilters{})
	s.run()
	if err := s.finish(); err == nil {
		t.Fatal("the program did not fail")
	}
}

const assertSrc = `fn half(n: Int) -> Int {
  n / 2
}

test "half of ten" {
  got = half(10)
  assert got == 6
}
`

func TestDebug_AFailedAssertionStops(t *testing.T) {
	s := debugProgram(t, assertSrc)
	s.runTests()
	st := s.stop()
	s.at(st, vm.StopAssertion, "half of ten:7", "got: Int = 5")
	if st.Assertion == nil || !strings.Contains(st.Text, "got == 6") {
		t.Fatalf("the assertion's report: %q", st.Text)
	}
	s.d.Continue()
	if err := s.finish(); err == nil {
		t.Fatal("the case passed")
	}
}

const mainErrSrc = `fn main() -> Result<Unit, String> {
  reason = "boom"
  Err(reason)
}
`

func TestDebug_AnErrFromMainStopsBeforeMainEnds(t *testing.T) {
	s := debugProgram(t, mainErrSrc)
	s.run()
	st := s.stop()
	s.at(st, vm.StopMainError, "main:3", `reason: String = "boom"`)
	if st.Text != `"boom"` {
		t.Fatalf("the error's text: %q", st.Text)
	}
	s.d.Continue()
	if err := s.finish(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("the program's failure: %v", err)
	}
}

const taskSrc = `import std/io
import std/tasks.Task

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
`

func TestDebug_ABreakpointInATask(t *testing.T) {
	s := debugProgram(t, taskSrc)
	s.breakAt(5)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "work:5 <lambda>:11", "n: Int = 21")
	threads := s.d.Threads()
	if len(threads) < 2 {
		t.Fatalf("threads while a task is stopped: %+v", threads)
	}
	main := threads[0]
	if main.ID == st.Thread || main.Name != "main" {
		t.Fatalf("the first thread is %+v and the task's is %d", main, st.Thread)
	}
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if s.out.String() != "42\n" {
		t.Fatalf("output %q", s.out.String())
	}
}

const loopSrc = `import std/io

fn main() {
  n = Iter.loop(fn i = 0 ->
    if i == 1000000 {
      break i
    } else {
      i + 1
    }
  )
  io.print("${n}")
}
`

func TestDebug_PauseStopsARunningProgram(t *testing.T) {
	s := debugProgram(t, loopSrc)
	s.run()
	s.d.Pause()
	st := s.stop()
	if st.Reason != vm.StopPause {
		t.Fatalf("stopped for %q", st.Reason)
	}
	if _, stack := s.stack(st.Thread); !strings.HasPrefix(stack, "main:") {
		t.Fatalf("paused at %s", stack)
	}
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

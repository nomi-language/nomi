package vm_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

const taskTrapSrc = `import std/io
import std/tasks.Task

fn work(n: Int) -> Int {
  r = 10 / n
  r
}

fn main() {
  v = concurrent {
    t = Task.spawn(fn -> work(0))
    Task.await(t)
  }
  io.print("${v}")
}
`

func TestDebug_ATaskFailureStopsInTheTaskAndAgainWhereItIsAwaited(t *testing.T) {
	s := debugProgram(t, taskTrapSrc)
	s.d.SetExceptionFilters(vm.ExceptionFilters{Traps: true, TaskFailures: true})
	s.run()
	st := s.stop()
	s.at(st, vm.StopTrap, "work:5 <lambda>:11", "n: Int = 0")
	// The main thread is blocked in Task.await, and shows where.
	var main vm.Thread
	for _, th := range s.d.Threads() {
		if th.ID != st.Thread {
			main = th
		}
	}
	if _, stack := s.stack(main.ID); stack != "Task.await:12 <concurrent>:12 concurrent:10 main:10" {
		t.Fatalf("the awaiting thread's stack: %s", stack)
	}
	s.d.Continue()
	st = s.stop()
	s.at(st, vm.StopTaskFailure, "<concurrent>:12 concurrent:10 main:10", "t: Task<Int> = <task>")
	if !strings.Contains(st.Text, "division by zero") {
		t.Fatalf("the failure's text: %q", st.Text)
	}
	s.d.Continue()
	if err := s.finish(); err == nil {
		t.Fatal("the program did not fail")
	}
}

func TestDebug_AnAwaitedTaskFailureIsNotATrapStop(t *testing.T) {
	s := debugProgram(t, taskTrapSrc)
	s.d.SetExceptionFilters(vm.ExceptionFilters{TaskFailures: true})
	s.run()
	st := s.stop()
	if st.Reason != vm.StopTaskFailure {
		t.Fatalf("stopped for %q at %s", st.Reason, st.Pos)
	}
	s.d.Continue()
	if err := s.finish(); err == nil {
		t.Fatal("the program did not fail")
	}
	s = debugProgram(t, taskTrapSrc)
	s.d.SetExceptionFilters(vm.ExceptionFilters{Traps: true})
	s.run()
	st = s.stop()
	s.at(st, vm.StopTrap, "work:5 <lambda>:11", "n: Int = 0")
	s.d.Continue()
	if err := s.finish(); err == nil {
		t.Fatal("the program did not fail")
	}
}

const twoTasksSrc = `import std/io
import std/tasks.Task

fn work(n: Int) -> Int {
  r = n * 2
  r
}

fn main() {
  v = concurrent {
    a = Task.spawn(fn -> work(1))
    b = Task.spawn(fn -> work(2))
    Task.await(a) + Task.await(b)
  }
  io.print("${v}")
}
`

// Two tasks reach the same breakpoint. Each is reported, one at a time: one
// that arrives while the other is stopped parks with its stop, and the next
// resume reports it.
func TestDebug_TwoThreadsAtABreakpointAreBothReported(t *testing.T) {
	s := debugProgram(t, twoTasksSrc)
	s.breakAt(5)
	s.run()
	first := s.stop()
	if first.Reason != vm.StopBreakpoint {
		t.Fatalf("stopped for %q", first.Reason)
	}
	s.d.Continue()
	second := s.stop()
	if second.Reason != vm.StopBreakpoint || second.Thread == first.Thread {
		t.Fatalf("the second stop: %+v after %+v", second, first)
	}
	if _, stack := s.stack(second.Thread); !strings.HasPrefix(stack, "work:5 <lambda>:1") {
		t.Fatalf("the second thread's stack: %s", stack)
	}
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if s.out.String() != "6\n" {
		t.Fatalf("output %q", s.out.String())
	}
}

const stdStepSrc = `import std/io

fn main() {
  m = Some(2)
  r = Maybe.map(m, fn v -> {
    w = v * 3
    w
  })
  io.print("${r}")
}
`

func TestDebug_StepInPassesThroughSkippedFiles(t *testing.T) {
	s := debugProgram(t, stdStepSrc)
	s.d.SetSkipFiles(std.IsSourcePath)
	s.breakAt(5)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "main:5", "m: Maybe<Int> = Some(2)")
	s.d.StepIn(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "<lambda>:6 maybe.Maybe.map<Int, Int>:54 main:5", "v: Int = 2")
	s.d.StepOver(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "<lambda>:7 maybe.Maybe.map<Int, Int>:54 main:5", "v: Int = 2, w: Int = 6")
	s.d.StepOver(st.Thread)
	st = s.stop()
	s.at(st, vm.StopStep, "main:9", "m: Maybe<Int> = Some(2), r: Maybe<Int> = Some(6)")
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestDebug_StepInEntersTheStandardLibraryWhenNothingIsSkipped(t *testing.T) {
	s := debugProgram(t, stdStepSrc)
	s.breakAt(5)
	s.run()
	st := s.stop()
	s.d.StepIn(st.Thread)
	st = s.stop()
	frames, stack := s.stack(st.Thread)
	if stack != "maybe.Maybe.map<Int, Int>:53 main:5" || !std.IsSourcePath(frames[0].Pos.File()) {
		t.Fatalf("step in from main: %s in %s", stack, frames[0].Pos.File())
	}
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}

const oneLineSrc = `import std/io

fn count(n: Int, acc: Int) -> Int {
  if n == 0 { acc } else { count(n - 1, acc + n) }
}

fn main() {
  io.print("${count(2, 0)}")
}
`

// A line breakpoint stops once each time an activation reaches the line, at
// its first statement, not again at a later statement on the same line.
func TestDebug_ALineBreakpointStopsOncePerPassOverTheLine(t *testing.T) {
	s := debugProgram(t, oneLineSrc)
	s.breakAt(4)
	s.run()
	var stacks []string
	for range 3 {
		st := s.stop()
		_, stack := s.stack(st.Thread)
		stacks = append(stacks, stack)
		s.d.Continue()
	}
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(stacks, " | "); got != "count:4 main:8 | count:4(tail) main:8 | count:4(tail) main:8" {
		t.Fatalf("stops: %s", got)
	}
}

// Kill parks a stopped program for good: a resume runs nothing.
func TestDebug_KillEndsAStoppedProgram(t *testing.T) {
	s := debugProgram(t, oneLineSrc)
	s.breakAt(4)
	s.run()
	s.stop()
	s.d.Kill()
	s.d.Continue()
	select {
	case st := <-s.d.Events():
		t.Fatalf("a stop after kill: %+v", st)
	case err := <-s.done:
		t.Fatalf("the program ran on after kill: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if s.out.String() != "" {
		t.Fatalf("output after kill: %q", s.out.String())
	}
}

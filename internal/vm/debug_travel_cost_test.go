package vm_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// Replaying forward through a recorded restart with no breakpoint to stop
// at restarts the frame again, so the recording stays in step.
func TestTravel_ReplayingForwardRestartsWhereTheRecordingDid(t *testing.T) {
	s := travelProgram(t, `import std/io

fn add(acc: Int, n: Int) -> Int {
    io.print("adding ${acc} ${n}")
    s = acc + n
    s
}

fn main() {
    a = add(0, 1)
    b = add(a, 2)
    io.print("${b}")
}
`, "")
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
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 5)
	for _, want := range []int{4, 5, 4} {
		if err := s.d.StepBack(st.Thread); err != nil {
			t.Fatal(err)
		}
		st = s.stop()
		s.line(st, vm.StopStep, want)
	}
	s.breakAt()
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if out := s.shown.String(); out != "adding 0 1\nadding 0 1\nadding 1 2\n3\n" {
		t.Fatalf("output %q", out)
	}
}

// What going back costs, counted: a replay executes every statement before
// its target and no more, and answers each recorded crossing from the log
// without crossing.
func TestTravel_GoingBackCostsTheStatementsBeforeItsTarget(t *testing.T) {
	s := travelProgram(t, `import std/io

fn count(n: Int, acc: Int) -> Int {
    case n {
        0 ->
            acc

        _ -> {
            io.write(".")
            count(n - 1, acc + 1)
        }
    }
}

fn main() {
    total = count(1000, 0)
    io.print(" ${total}")
}
`, "")
	s.breakAt(17)
	s.runPasses()
	st := s.stop()
	s.line(st, vm.StopBreakpoint, 17)
	live := s.d.Stats()
	if live.Recorded != 1000 {
		t.Fatalf("recorded %d crossings, want the 1000 writes", live.Recorded)
	}
	if err := s.d.StepBack(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopStep, 16)
	back := s.d.Stats()
	// Line 16 is the program's first statement: the replay ran one
	// statement and answered nothing.
	if back.Steps != 1 || back.Answered != 0 {
		t.Fatalf("the replay to the first statement ran %d statements and answered %d crossings", back.Steps, back.Answered)
	}
	s.d.Continue()
	st = s.stop()
	s.line(st, vm.StopBreakpoint, 17)
	again := s.d.Stats()
	if again.Steps != live.Steps || again.Answered != 1000 || again.Recorded != 1000 {
		t.Fatalf("forward to the present again: %+v, want %d statements and 1000 answered from 1000 recorded", again, live.Steps)
	}
	// A reverse continue from here with no breakpoint behind goes to the
	// first statement: one replay of one statement.
	if err := s.d.ReverseContinue(st.Thread); err != nil {
		t.Fatal(err)
	}
	st = s.stop()
	s.line(st, vm.StopStart, 16)
	if got := s.d.Stats(); got.Steps != 1 || got.Passes != again.Passes+1 {
		t.Fatalf("reverse continue: %+v", got)
	}
	t.Logf("the present was statement %d; replaying to it from the start runs %d statements and answers %d crossings",
		live.Steps, live.Steps, live.Recorded)
	s.breakAt()
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if out := s.shown.String(); out != strings.Repeat(".", 1000)+" 1000\n" {
		t.Fatalf("output %q", out)
	}
}

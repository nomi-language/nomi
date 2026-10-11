package vm_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

const funcBreakSrc = `import std/io

struct Card {
  n: Int
}

impl Display for Card {
  fn to_string(c: Card): String {
    "card ${c.n}"
  }
}

fn countdown(n: Int): Int {
  if n == 0 {
    0
  } else {
    countdown(n - 1)
  }
}

fn main() {
  io.print(Display.to_string(Card{n: 1}))
  io.print("${countdown(2)}")
  io.print("${Maybe.map(Some(1), |x| x + 1)}")
}
`

// Each spelling a function breakpoint takes, what it names, and that it
// stops at every entry: a tail-recursive function stops once per call.
func TestDebug_FunctionBreakpointsNameFunctionsAsTheStackDoes(t *testing.T) {
	s := debugProgram(t, funcBreakSrc)
	got := s.d.SetFunctionBreakpoints([]vm.FunctionBreakpointSpec{
		{Name: "Display.to_string"},
		{Name: "dbg.countdown"},
		{Name: "Maybe.map"},
		{Name: "countdwn"},
		{Name: "lambda"},
	})
	var report []string
	for _, fb := range got {
		line := fb.Name + ": "
		if fb.Verified {
			line += "stops in " + strings.Join(fb.Functions, ", ") + " at line " + strconv.Itoa(fb.Pos.Line())
		} else {
			line += "unverified, similar " + strings.Join(fb.Similar, ", ")
		}
		report = append(report, line)
	}
	want := []string{
		"Display.to_string: stops in Card.to_string at line 9",
		"dbg.countdown: stops in countdown at line 14",
		"Maybe.map: stops in maybe.Maybe.map<Int, Int> at line " + strconv.Itoa(got[2].Pos.Line()),
		"countdwn: unverified, similar countdown",
		"lambda: unverified, similar ",
	}
	if strings.Join(report, "\n") != strings.Join(want, "\n") {
		t.Fatalf("function breakpoints:\n%s\nwant:\n%s", strings.Join(report, "\n"), strings.Join(want, "\n"))
	}
	if !strings.HasSuffix(got[2].Pos.File(), "maybe.nomi") {
		t.Fatalf("Maybe.map's first statement is at %s, not in std's maybe.nomi", got[2].Pos)
	}
	s.run()
	s.at(s.stop(), vm.StopBreakpoint, "Card.to_string:9 main:22", "c: Card = Card{n: 1}")
	for _, n := range []string{"2", "1", "0"} {
		s.d.Continue()
		st := s.stop()
		if st.Reason != vm.StopBreakpoint || len(st.Functions) != 1 || st.Functions[0] != 1 || !st.NotLines {
			t.Fatalf("stop %+v, want function breakpoint 1 alone", st)
		}
		if l := s.locals(s.topFrame(st.Thread)); l != "n: Int = "+n {
			t.Fatalf("countdown's locals: %s, want n = %s", l, n)
		}
	}
	s.d.Continue()
	if st := s.stop(); st.Reason != vm.StopBreakpoint || !strings.HasSuffix(st.Pos.File(), "maybe.nomi") {
		t.Fatalf("stop %+v, want Maybe.map's first statement", st)
	}
	s.d.SetFunctionBreakpoints(nil)
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if out := s.out.String(); out != "card 1\n0\nSome(2)\n" {
		t.Fatalf("output %q", out)
	}
}

func (s *debugSession) topFrame(thread int) int {
	s.t.Helper()
	frames, _ := s.stack(thread)
	return frames[0].ID
}

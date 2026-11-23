package vm_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

const funcBreakSrc = `import std::io

struct Card
  n: Int
end

impl Display for Card
  fn to_string(c: Card) -> String
    "card ${c.n}"
  end
end

fn countdown(n: Int) -> Int
  if n == 0 then
    0
  else
    countdown(n - 1)
  end
end

fn main()
  io.print(Display.to_string(Card{n: 1}))
  io.print("${countdown(2)}")
  io.print("${Option.map(Some(1), fn x -> x + 1)}")
end
`

// Each spelling a function breakpoint takes, what it names, and that it
// stops at every entry: a tail-recursive function stops once per call. An
// impl block's function is named by its block on the stack, and by its
// interface's function, as code calls it and as the stack shows it here.
func TestDebug_FunctionBreakpointsNameFunctionsAsTheStackDoes(t *testing.T) {
	s := debugProgram(t, funcBreakSrc)
	got := s.d.SetFunctionBreakpoints([]vm.FunctionBreakpointSpec{
		{Name: "Display.to_string"},
		{Name: "dbg.countdown"},
		{Name: "Option.map"},
		{Name: "countdwn"},
		{Name: "lambda"},
		{Name: "Card.to_string"},
		{Name: "Display.to_string for Card"},
		{Name: "Option.map<Int>"},
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
		"Display.to_string: stops in Display.to_string for Card at line 9",
		"dbg.countdown: stops in countdown at line 14",
		"Option.map: stops in Option<Int>.map at line " + strconv.Itoa(got[2].Pos.Line()),
		"countdwn: unverified, similar countdown",
		"lambda: unverified, similar ",
		"Card.to_string: stops in Display.to_string for Card at line 9",
		"Display.to_string for Card: stops in Display.to_string for Card at line 9",
		"Option.map<Int>: stops in Option<Int>.map at line " + strconv.Itoa(got[2].Pos.Line()),
	}
	if strings.Join(report, "\n") != strings.Join(want, "\n") {
		t.Fatalf("function breakpoints:\n%s\nwant:\n%s", strings.Join(report, "\n"), strings.Join(want, "\n"))
	}
	if !strings.HasSuffix(got[2].Pos.File(), "option.nomi") {
		t.Fatalf("Option.map's first statement is at %s, not in std's option.nomi", got[2].Pos)
	}
	s.run()
	s.at(s.stop(), vm.StopBreakpoint, "Display.to_string for Card:9 main:22", "c: Card = Card{n: 1}")
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
	if st := s.stop(); st.Reason != vm.StopBreakpoint || !strings.HasSuffix(st.Pos.File(), "option.nomi") {
		t.Fatalf("stop %+v, want Option.map's first statement", st)
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

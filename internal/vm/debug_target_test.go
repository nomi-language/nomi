package vm_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

const targetSrc = `import std/io

fn main() {
  f = |k: Int| k + 1
  y = f(2) + Maybe.with_default(Maybe.map(Some(1), |x| x + 1), 0)
  io.print("${y}")
}
`

// The targets of a statement with a call through a variable, a std
// function given a callback, and a host call: the std function is a target
// only while steps may end in std; its callback always is; the host call
// never is.
func TestDebug_StepInTargetsOfAStatement(t *testing.T) {
	for _, justMyCode := range []bool{true, false} {
		s := debugProgram(t, targetSrc)
		if justMyCode {
			s.d.SetSkipFiles(std.IsSourcePath)
		}
		s.breakAt(5)
		s.run()
		st := s.stop()
		frames, _ := s.stack(st.Thread)
		targets, err := s.d.StepInTargets(frames[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		var labels []string
		for _, tg := range targets {
			labels = append(labels, tg.Label)
		}
		want := "f, Maybe.map(|x| ...) callback"
		if !justMyCode {
			want = "f, Maybe.map(|x| ...) callback, maybe.Maybe.map<Int, Int>"
		}
		if got := strings.Join(labels, ", "); got != want {
			t.Fatalf("justMyCode %v: targets %s, want %s", justMyCode, got, want)
		}
		// Into the callback: Maybe.map calls it. The breakpoint, which
		// would stop at the callback's statement on its line too, goes.
		s.breakAt()
		if err := s.d.StepInto(st.Thread, 2); err != nil {
			t.Fatal(err)
		}
		st = s.stop()
		if st.Reason != vm.StopStep || st.Pos.Line() != 5 || st.Text != "" {
			t.Fatalf("stop %+v, want the callback's statement on line 5", st)
		}
		if _, stack := s.stack(st.Thread); !strings.HasPrefix(stack, "<lambda>:5 ") {
			t.Fatalf("stack %s, want the lambda innermost", stack)
		}
		s.d.Continue()
		if err := s.finish(); err != nil {
			t.Fatal(err)
		}
	}
}

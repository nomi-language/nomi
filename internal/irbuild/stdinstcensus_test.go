package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/std"
)

// A std instance closure is built while the caller is in the middle of an
// expression (`Iter.to_list(probe_walk([1, 2]))`), and a member of the
// closure may decline without failing it: an impl block of a generic type
// the module declares is built after the body that made the type
// (flushGenericImpls), and its decline is the impl's alone. A nested build
// starts outside the expressions of the attempt it interrupts
// (declineCensus.suspend). When it kept them, that decline counted against
// the caller's open expressions, and `walk<Int>`'s first statement, lowered
// before any expression of its own was open, found one of them abandoned:
// it declined with no reason, and the case was BLOCKED with "the builder
// declined without naming a reason".
//
// The probe's `each_while` declines because it passes a `Walked<T>` to
// `walk`, and the instance gens of `probe_walk` and `walk` each build their
// own `Walked<Int>`. That is a gap of its own; this test holds only that
// `walk<Int>` lowers whatever `each_while` does.
func TestStdInstCensus_ADeclinedImplLeavesTheClosuresOtherMembersLowering(t *testing.T) {
	src, ok := std.ReadFile("tasks")
	if !ok {
		t.Fatal("std/tasks is not embedded")
	}
	probe := strings.TrimRight(string(src), "\n") + `

/// Probe.
//! xs = Iter.to_list(probe_walk([1, 2]))
//! assert xs == [1, 2]
//
pub fn probe_walk<T>(xs: List<T>) -> Iter<T>
    Walked{xs:}
end

opaque struct Walked<T>
    xs: List<T>
end

impl Iter for Walked<T>
    fn each_while(w: Walked<T>, yield: (T) -> Bool) -> Bool
        walk(w, yield)
    end
end

fn walk<T>(w: Walked<T>, yield: (T) -> Bool) -> Bool
    xs = w.xs
    Iter.all_match(xs, yield)
end
`
	nodes, fa, err := frontend.New(frontend.Config{}).CheckStdlibSource("tasks", probe, frontend.Mode{Tests: true})
	if err != nil {
		t.Fatalf("the front end rejects the probe, so it tests nothing: %v", err)
	}
	var unnamed []string
	prevHook := irDeclineUnnamed
	irDeclineUnnamed = func(fn string) { unnamed = append(unnamed, fn) }
	defer func() { irDeclineUnnamed = prevHook }()

	_, res, err := GenerateEditedStdlibTestIR("tasks", nodes, fa, DeclineHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(unnamed) > 0 {
		t.Errorf("declined without naming a reason: %v", unnamed)
	}
	built := false
	for _, m := range res.IRModules() {
		for _, f := range m.Funcs() {
			if strings.HasPrefix(f.Sym().Name(), "tasks.walk<Int") {
				built = true
			}
		}
	}
	if !built {
		t.Error("tasks.walk<Int> was not built: a member of the closure after the declined impl declined too")
	}
}

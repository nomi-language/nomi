package vm_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
)

// reachOrderSource reaches shout through eight impls of one dispatched
// method, so which impl the walk visits first decides the chain by which
// shout's outside call is reported.
const reachOrderSource = `import std::io

interface Named
    fn name(value: self) -> String
end

struct A end
struct B end
struct C end
struct D end
struct E end
struct F end
struct G end
struct H end

fn shout(text: String)
    io.print(text)
end

impl Named for A
    fn name(_value: A) -> String
        shout("a")
        "a"
    end
end

impl Named for B
    fn name(_value: B) -> String
        shout("b")
        "b"
    end
end

impl Named for C
    fn name(_value: C) -> String
        shout("c")
        "c"
    end
end

impl Named for D
    fn name(_value: D) -> String
        shout("d")
        "d"
    end
end

impl Named for E
    fn name(_value: E) -> String
        shout("e")
        "e"
    end
end

impl Named for F
    fn name(_value: F) -> String
        shout("f")
        "f"
    end
end

impl Named for G
    fn name(_value: G) -> String
        shout("g")
        "g"
    end
end

impl Named for H
    fn name(_value: H) -> String
        shout("h")
        "h"
    end
end

fn label(value: Named) -> String
    Named.name(value)
end

fn main()
    io.print(label(A{}))
end
`

// TestReach_TheWalkIsTheSameEveryTime walks one program many times in one
// process and requires every answer that names how a function was reached
// (the outside calls' chains, the crossing sites' chains, the reached set
// in order) to be the same each time. A walk whose order followed a Go
// map's would report a different chain from run to run.
func TestReach_TheWalkIsTheSameEveryTime(t *testing.T) {
	prog, err := irbuild.AnalyzeSource("order", reachOrderSource)
	if err != nil {
		t.Fatalf("the front end rejects this: %v", err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	var main *ir.Func
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				entry, main = m, f
			}
		}
	}
	if main == nil {
		t.Fatal("main was not retained")
	}
	mods := res.IRModules()
	roots := vm.MainRoots(entry, main)
	walk := func() string {
		var b strings.Builder
		for _, c := range vm.ReachedOutsideCalls(entry, mods, roots, vm.EdgeRoot, nil, nil).Calls {
			fmt.Fprintf(&b, "call %s %s %v\n", c.At, c.Crossing, c.Chain)
		}
		for _, s := range vm.ReachedCrossingSites(entry, mods, roots, nil, func(string) bool { return true }) {
			fmt.Fprintf(&b, "site %s %s %v\n", s.At, s.Name, s.Chain)
		}
		for _, f := range vm.ReachedFuncs(entry, mods, roots, nil) {
			fmt.Fprintf(&b, "func %s\n", f.Name())
		}
		return b.String()
	}
	first := walk()
	if !strings.Contains(first, "shout") {
		t.Fatalf("no answer names shout, so this checks no chain through the dispatch:\n%s", first)
	}
	for i := 1; i < 40; i++ {
		if got := walk(); got != first {
			t.Fatalf("walk %d answers differently from the first:\n%s", i+1, expectation.LineDiff(first, got))
		}
	}
}

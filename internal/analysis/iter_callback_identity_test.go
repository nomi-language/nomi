package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// Every std function a checker rule recognizes a call of (the
// iteration-callback table, `Struct.update`, `Release.release`, ...) is
// one a std module declares, so renaming or moving one fails here instead
// of silently taking its rule away: `break` and `continue` from a
// callback, a patch's update positions, a release.
func TestStdRuleFuncsAreDeclared(t *testing.T) {
	lib := loadStd()
	declared := map[analysis.StdFunc]bool{}
	for _, fa := range lib.Files {
		for _, id := range fa.StdFuncs() {
			declared[id] = true
		}
	}
	listed := analysis.StdRuleFuncs()
	if len(declared) == 0 || len(listed) == 0 {
		t.Fatalf("read %d std functions and %d listed ones, so this test checks nothing", len(declared), len(listed))
	}
	for _, f := range listed {
		if !declared[f] {
			t.Errorf("a checker rule recognizes calls of %+v, which no std module declares", f)
		}
	}
}

// Every std type a checker rule recognizes a checked type or a call's
// qualifier as is one a std module declares, so renaming or moving one
// fails here instead of silently taking its rule away: a Result's `?`, a
// Duration's value ranges, a Struct.update patch.
func TestStdRuleTypesAreDeclared(t *testing.T) {
	lib := loadStd()
	declared := map[analysis.StdType]bool{}
	for _, fa := range lib.Files {
		for _, sym := range fa.Definitions {
			switch sym.Kind {
			case analysis.SymbolStruct, analysis.SymbolEnum, analysis.SymbolType, analysis.SymbolInterface:
				declared[analysis.StdType{Module: fa.Origin, Name: sym.Name}] = true
			}
		}
	}
	listed := analysis.StdRuleTypes()
	if len(declared) == 0 || len(listed) == 0 {
		t.Fatalf("read %d std types and %d listed ones, so this test checks nothing", len(declared), len(listed))
	}
	for _, s := range listed {
		if !declared[s] {
			t.Errorf("a checker rule recognizes %+v, which no std module declares", s)
		}
	}
}

// checkIterSource checks src, one file over the stdlib, through the
// iteration-callback placement check as the front end runs it.
func checkIterSource(src string) []analysis.TypeError {
	fa, nodes, errs := checkSourceNodesWithStdlib(src)
	return append(errs, analysis.AnalyzeIterSensitivity(fa, nodes)...)
}

// Another type imported under the name `Iter` has no iteration callbacks:
// its `map` is not std's `Iter.map`, so a `break` in the lambda it takes is
// one nothing catches. The callee the checker resolved decides, not the
// name the call is written with.
func TestIterCallback_AnotherTypeNamedIterCatchesNothing(t *testing.T) {
	errs := checkIterSource(`import std::random.{Generator as Iter}

fn f() -> Iter<Int>
    Iter.map(Iter.int(1..=6), fn x -> if x > 2 then break else x)
end
`)
	if len(errs) == 0 {
		t.Fatal("the front end admits a `break` in Generator.map's callback, which no iteration catches")
	}
	const want = "'break' can only be used inside an iter-callback"
	for _, e := range errs {
		if strings.Contains(e.Message, want) {
			return
		}
	}
	t.Fatalf("want an error containing %q, got %v", want, errs)
}

// std's Iter imported under another name keeps its callbacks: the call
// reaches std's `Iter.map`, `Iter.reduce` and `Iter.loop` whatever it is
// written with.
func TestIterCallback_IterUnderAnotherNameCatches(t *testing.T) {
	errs := checkIterSource(`import std::iter.{Iter as Seq}

fn f(xs: List<Int>) -> (List<Int>, Int, Int)
    ys = Seq.map(xs, fn x -> if x > 2 then break else x * 10) |> Seq.to_list()
    total = xs |> Seq.reduce(0, fn acc, x -> if x > 2 then break acc else acc + x)
    n = Seq.loop(0, fn i -> if i >= 3 then break i * 10 else i + 1)
    (ys, total, n)
end
`)
	expectClean(t, errs)
}

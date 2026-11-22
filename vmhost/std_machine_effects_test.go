package vmhost

import (
	"slices"
	"sort"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/std"
)

// Every std function the builder lowers every call of to an operation of
// the machine (irbuild.StdLowersToMachine) is answered by StdEffects, with
// no effect of its own: its body never runs, and the operation computes and
// calls only the functions it is passed. The modules holding one are found
// from the stdlib's analysis, so a function that comes to lower that way
// is held here without a list naming it.
func TestStdEffects_AMachineOperationIsAnswered(t *testing.T) {
	lib := std.Load()
	byModule := map[string][]*ast.FuncDef{}
	ids := map[*ast.FuncDef]analysis.StdFunc{}
	for name, fa := range lib.Files {
		for decl, id := range fa.StdFuncs() {
			if fd, ok := decl.(*ast.FuncDef); ok && irbuild.StdLowersToMachine(id) {
				byModule[name] = append(byModule[name], fd)
				ids[fd] = id
			}
		}
	}
	modules := make([]string, 0, len(byModule))
	for name := range byModule {
		modules = append(modules, name)
	}
	sort.Strings(modules)
	for _, want := range []string{"iter", "ranges", "sets"} {
		if !slices.Contains(modules, want) {
			t.Errorf("std::%s declares no function the builder lowers to an operation of the machine", want)
		}
	}
	count := 0
	for _, name := range modules {
		p, err := LoadEmbeddedStdlib(name)
		if err != nil {
			t.Fatalf("std::%s: %v", name, err)
		}
		names, answered := p.StdEffects()
		for _, fd := range byModule[name] {
			count++
			at := DeclAt{Line: fd.Line, Col: fd.Col}
			id := ids[fd]
			if !answered[at] {
				t.Errorf("std::%s %s.%s: the builder lowers it to an operation of the machine, and the walk has no answer for it", name, id.Owner, id.Name)
			}
			if got := names[at]; got != "" {
				t.Errorf("std::%s %s.%s: has the effect %q, want none: the operation it lowers to has no effect of its own", name, id.Owner, id.Name, got)
			}
		}
	}
	t.Logf("%d functions in %d modules lower to an operation of the machine", count, len(modules))
}

// A call of such a function that passes a function that acts may run it,
// so it acts on what it is passed, and one passed only pure functions is
// plain: an Iter stage, a Set's and a Range's `impl Iter` member alike.
func TestEffectMarks_AMachineOperationActsOnWhatItIsPassed(t *testing.T) {
	marksCase{
		src: `import std::io

fn main()
    _ = Iter.map(["a"], io.print) |> Iter.to_list()
    _ = Set.each_while(Set["b"], fn s -> io.print(s) == Unit)
    _ = Range.each_while(1..3, fn n -> n < 2)
end
`,
		marked: []string{"3:main", "4:Iter.map", "4:Iter.to_list", "5:Set.each_while", "5:io.print"},
		hovers: map[string]string{
			"Iter.map":         heldLine,
			"Set.each_while":   heldLine,
			"Range.each_while": "",
		},
	}.run(t)
}

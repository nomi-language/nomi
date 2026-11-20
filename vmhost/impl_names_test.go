package vmhost

import (
	"slices"
	"strings"
	"testing"
)

// An impl block's function is named by its block, `Interface.func for Type`
// or `Type.func` for an inherent one, whichever use of it the builder
// lowers first: a program that uses each function first through an
// operator, interpolation or a dispatch, and one that uses it first by a
// qualified call, give every function the same name.
func TestImplFunctionNames_DoNotDependOnWhichUseLowersFirst(t *testing.T) {
	const decls = "import std::io\n\n" +
		"struct Loud\n    n: Int\nend\n\n" +
		"impl Display for Loud\n    fn to_string(l: Loud) -> String\n        \"loud ${l.n}\"\n    end\nend\n\n" +
		"impl Loud\n    pub fn twice(l: Loud) -> Int\n        l.n * 2\n    end\nend\n\n" +
		"struct Score\n    points: Int\nend\n\n" +
		"impl Equatable for Score\n    fn equal(a: Score, b: Score) -> Bool\n        a.points == b.points\n    end\nend\n\n" +
		"struct Box<T>\n    v: T\nend\n\n" +
		"impl Display for Box<T>\n    fn to_string(_b: Box<T>) -> String\n        \"box\"\n    end\nend\n\n" +
		"struct Pair\n    a: Int\nend\n\n" +
		"derive Debug for Pair\n\n"
	implicit := "    io.print(\"${Loud{n: 1}}\")\n" +
		"    io.inspect(Score{points: 1} == Score{points: 2})\n" +
		"    shown: Display = Box{v: 1}\n" +
		"    io.print(Display.to_string(shown))\n" +
		"    io.inspect(Pair{a: 1})\n"
	qualified := "    io.print(Display.to_string(Loud{n: 1}))\n" +
		"    io.inspect(Equatable.equal(Score{points: 1}, Score{points: 2}))\n" +
		"    io.print(Display.to_string(Box{v: 1}))\n" +
		"    io.print(Debug.inspect(Pair{a: 1}))\n"
	twice := "    io.inspect(Loud.twice(Loud{n: 2}))\n"
	names := func(body string) []string {
		t.Helper()
		p, err := LoadSource("main", decls+"fn main()\n"+body+"end\n")
		if err != nil {
			t.Fatalf("the program does not load: %v", err)
		}
		var out []string
		for _, m := range p.res.IR {
			for _, f := range m.Funcs() {
				if _, ok := f.Sym().Impl(); ok {
					out = append(out, f.Name())
				}
			}
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	first := names(implicit + qualified + twice)
	second := names(twice + qualified + implicit)
	if !slices.Equal(first, second) {
		t.Errorf("the impl functions' names depend on which use lowers first:\n%s\nand\n%s",
			strings.Join(first, "\n"), strings.Join(second, "\n"))
	}
	for _, want := range []string{
		"Display.to_string for Loud", "Loud.twice", "Equatable.equal for Score",
		"Display.to_string for Box<Int>", "Debug.inspect for Pair",
	} {
		if !slices.Contains(first, want) {
			t.Errorf("no impl function is named %q; the program's are:\n%s", want, strings.Join(first, "\n"))
		}
	}
}

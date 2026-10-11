package irbuild

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// The debug tables the builder records (irdebuginfo.go), read off a lowered
// program: each function's variables with their types and scopes, and its
// statement marks with their kinds and lines.

const debugInfoSrc = `import std/io

fn describe(pair: (Int, Int), limit: Int): String {
  (a, b) = pair
  total = a + b
  total = total * 2
  label = case total {
    0 -> "zero"
    n -> {
      doubled = n * 2
      "n=${doubled}"
    }
  }
  if total > limit {
    io.print("over")
  }
  steps = Iter.loop(|i = 0|
    if i == limit {
      break i
    } else {
      i + 1
    }
  )
  scaled = [1, 2]
    |> Iter.map(|x| x * total)
    |> Iter.to_list()
  "${label} ${steps} ${scaled}"
}

fn main() {
  io.print(describe((1, 2), 3))
}
`

func debugInfoFuncs(t *testing.T) map[string][]*ir.Func {
	t.Helper()
	p, err := AnalyzeSource("main", debugInfoSrc)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]*ir.Func{}
	seen := map[*ir.Func]bool{}
	var walk func(f *ir.Func)
	walk = func(f *ir.Func) {
		if seen[f] {
			return
		}
		seen[f] = true
		out[f.Name()] = append(out[f.Name()], f)
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if fv, ok := in.(*ir.FuncValue); ok {
					walk(fv.Body())
				}
			}
		}
	}
	var names []string
	for _, m := range res.IR {
		names = append(names, m.Name())
		if !strings.HasSuffix(m.Name(), "main.nomi") {
			continue
		}
		for _, f := range m.Funcs() {
			walk(f)
		}
	}
	if len(out["describe"]) != 1 {
		t.Fatalf("describe was not retained: %v in %v", out, names)
	}
	return out
}

func TestDebugInfo_EveryNamedValueIsAVariable(t *testing.T) {
	fns := debugInfoFuncs(t)
	var got []string
	for _, v := range fns["describe"][0].Vars() {
		got = append(got, fmt.Sprintf("%s: %s @%d-%d", v.Name, v.Type, v.Scope.Line(), v.Scope.EndLine()))
	}
	want := []string{
		"pair: (Int, Int) @3-28", "limit: Int @3-28",
		"a: Int @4-28", "b: Int @4-28",
		"total: Int @5-28", "total: Int @6-28",
		"n: Int @9-13", "doubled: Int @10-12",
		"label: String @7-28",
		"i: Int @17-22",
		"steps: Int @17-28",
		"scaled: List<Int> @24-28",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("describe's variables:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var lambda []string
	for _, f := range fns["lambda"] {
		for _, v := range f.Vars() {
			lambda = append(lambda, v.Name+": "+v.Type)
		}
	}
	if strings.Join(lambda, ", ") != "x: Int, total: Int" {
		t.Fatalf("the lambda's variables, its parameter and its capture: %v", lambda)
	}
}

// Each variable is declared where its name is written, which a debugger's
// go-to-binding opens: a parameter's name, the name of `name = ...`, each
// name of a destructuring and of a pattern, a loop's state name, and for a
// capture the binding it captured.
func TestDebugInfo_AVariableIsDeclaredAtItsName(t *testing.T) {
	decls := func(fs []*ir.Func) []string {
		var out []string
		for _, f := range fs {
			for _, v := range f.Vars() {
				d := v.Decl
				out = append(out, fmt.Sprintf("%s %d:%d-%d", v.Name, d.Line(), d.Col(), d.EndCol()))
			}
		}
		return out
	}
	fns := debugInfoFuncs(t)
	got := append(decls(fns["describe"]), decls(fns["lambda"])...)
	want := []string{
		"pair 3:13-17", "limit 3:31-36",
		"a 4:4-5", "b 4:7-8",
		"total 5:3-8", "total 6:3-8",
		"n 9:5-6", "doubled 10:7-14",
		"label 7:3-8",
		"i 17:22-23",
		"steps 17:3-8",
		"scaled 24:3-9",
		// The lambda's parameter, and its capture of the second `total`.
		"x 25:18-19", "total 6:3-8",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("declarations:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	src := `struct P {
  x: Int
  y: Int
}

fn main() {
  {x, y: why} = P{x: 1, y: 2}
  m = case Some(x) {
    Some(n) as whole -> (n, whole)
    None -> (0, None)
  }
  f = |(a, b): (Int, Int)| a + b
  _ = (why, m, f((1, 2)))
}
`
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, m := range res.IR {
		if !strings.HasSuffix(m.Name(), "main.nomi") {
			continue
		}
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				got = append(got, decls([]*ir.Func{f})...)
				for _, b := range f.Blocks() {
					for _, in := range b.Instrs() {
						if fv, isLambda := in.(*ir.FuncValue); isLambda {
							got = append(got, decls([]*ir.Func{fv.Body()})...)
						}
					}
				}
			}
		}
	}
	want = []string{"x 7:4-5", "why 7:10-13", "n 9:10-11", "whole 9:16-21", "m 8:3-4", "f 12:3-4", "a 12:9-10", "b 12:12-13"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("declarations:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestDebugInfo_StatementsArmsIterationsAndStagesAreMarked(t *testing.T) {
	fns := debugInfoFuncs(t)
	var got []string
	for _, b := range fns["describe"][0].Blocks() {
		for _, m := range b.Stmts() {
			got = append(got, fmt.Sprintf("%d %s", m.Pos.Line(), m.Kind))
		}
	}
	for _, want := range []string{
		"4 statement", "5 statement", "6 statement", "7 statement",
		"8 arm", "10 statement", "11 statement",
		"14 statement", "15 statement",
		"17 iteration", "18 statement", "19 statement", "21 statement",
		"24 statement", "25 stage", "26 stage", "27 statement",
	} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("no mark %q among %v", want, got)
		}
	}
	if err := ir.Lint(fns["describe"][0]); err != nil {
		t.Fatal(err)
	}
}

// A variable's type is spelled as an annotation spells it, type arguments
// and all, so a watch expression can be checked with the variable as a
// parameter of that type: every spelling below is written back into the
// fixture as a parameter type, and the front end accepts each.
func TestDebugInfo_TypesSpellAsAnnotations(t *testing.T) {
	src := `struct Box<T> {
  item: T
}

enum Tree<T> {
  Leaf
  Node {value: T}
}

fn main() {
  bs = [Box{item: Box{item: "s"}}]
  tr: Tree<Int> = Tree.Node{value: 1}
  g = |b: Box<Int>| b.item
  o: Maybe<Box<Int>> = None
  m: Map<String, Box<Int>> = {"a" => Box{item: 2}}
  pair = (Box{item: 1}, [Box{item: 2.5}])
  _ = (bs, tr, g, o, m, pair)
}
`
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() != "main" || !strings.HasSuffix(m.Name(), "main.nomi") {
				continue
			}
			for _, v := range f.Vars() {
				got = append(got, v.Name+": "+v.Type)
			}
		}
	}
	want := "bs: List<Box<Box<String>>>, tr: Tree<Int>, g: (Box<Int>) -> Int, o: Maybe<Box<Int>>, " +
		"m: Map<String, Box<Int>>, pair: (Box<Int>, List<Box<Float>>)"
	if strings.Join(got, ", ") != want {
		t.Fatalf("main's variables:\n%s\nwant:\n%s", strings.Join(got, ", "), want)
	}
	var params []string
	for i, v := range got {
		_, typ, _ := strings.Cut(v, ": ")
		params = append(params, fmt.Sprintf("p%d: %s", i, typ))
	}
	probe := src + "\nfn probe(" + strings.Join(params, ", ") + ") {\n  _ = (p0, p1, p2, p3, p4, p5)\n}\n"
	if _, err := AnalyzeSource("main", probe); err != nil {
		t.Fatalf("a spelling does not check as a parameter type: %v\n%s", err, probe)
	}
}

// Each pipeline is recorded with its head and stages, by their source, in
// order.
func TestDebugInfo_PipelinesAreRecorded(t *testing.T) {
	fns := debugInfoFuncs(t)
	var got []string
	for _, p := range fns["describe"][0].Pipes() {
		for _, s := range p.Stages {
			got = append(got, fmt.Sprintf("%d %s", s.Pos.Line(), s.Text))
		}
	}
	want := "24 [1, 2]\n25 |> Iter.map(|x| x * total)\n26 |> Iter.to_list()"
	if strings.Join(got, "\n") != want {
		t.Fatalf("describe's pipelines:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

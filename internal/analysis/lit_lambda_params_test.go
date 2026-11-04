package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const litLambdaTypes = `
struct Day { n: Int }
struct Date { n: Int }

fn name_of(d: Day) -> String {
  "${d.n}"
}
`

// paramTypeAt is the type the checker gave the parameter declared at
// line:col, as hover shows it.
func paramTypeAt(t *testing.T, fa *analysis.FileAnalysis, line, col int) string {
	t.Helper()
	sym := fa.Definitions[analysis.Pos{Line: line, Col: col}]
	if sym == nil || sym.Kind != analysis.SymbolParam {
		t.Fatalf("no parameter declared at %d:%d", line, col)
	}
	if sym.Type == nil {
		t.Fatalf("the parameter %s at %d:%d has no type", sym.Name, line, col)
	}
	return sym.Type.String()
}

func litLambdaErrors(errs []analysis.TypeError) string {
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, diagText(e)))
	}
	return strings.Join(got, "\n")
}

// In a collection literal with no expected type, an element's annotated
// lambda parameter types the unannotated parameter at the same place in the
// other elements, before or after it: inside tuples, records, a map's
// values, a vector, and nested lists. An element that is a named function
// types the places of the elements after it. Each unannotated parameter's
// type is what hover shows.
func TestLitLambdaParams_AnElementTypesTheOthers(t *testing.T) {
	src := litLambdaTypes + `
fn tuples() -> List<#(String, (Day) -> String)> {
  [
    #("a", fn d -> name_of(d)),
    #("b", fn d: Day -> "${d.n + 1}"),
    #("c", fn d -> name_of(d)),
  ]
}

fn records() -> List<String> {
  rs = [#{f: fn a, b: String -> "${a.n}${b}"}, #{f: fn _a: Day, b -> b}]
  _ = rs
  []
}

fn maps() -> Map<String, (Day) -> Int> {
  %{"a" => fn d -> d.n, "b" => fn d: Day -> d.n + 1}
}

fn vectors() -> Int {
  v = #[fn d -> d.n, fn _d: Day -> 0]
  _ = v
  0
}

fn nested() -> Int {
  ls = [[fn d: Day -> d.n], [fn d -> d.n * 2]]
  _ = ls
  0
}

fn named_first() -> Int {
  ns = [#("a", name_of), #("b", fn d -> "${d.n}")]
  _ = ns
  0
}
`
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("the checker rejects lambdas a sibling element types:\n%s", litLambdaErrors(errs))
	}
	for _, tc := range []struct {
		line, col int
		want      string
	}{
		{11, 15, "Day"},    // tuples, before the annotated element
		{13, 15, "Day"},    // tuples, after it
		{18, 17, "Day"},    // records: a, from the second element
		{18, 65, "String"}, // records: b, from the first
		{24, 15, "Day"},    // map values
		{28, 12, "Day"},    // vector
		{34, 33, "Day"},    // nested list
		{40, 36, "Day"},    // after a named function
	} {
		if got := paramTypeAt(t, fa, tc.line, tc.col); got != tc.want {
			t.Errorf("parameter at %d:%d: got %s, want %s", tc.line, tc.col, got, tc.want)
		}
	}
}

// Elements that annotate different types at one place disagree: the
// literal's element types differ, and the mismatch names both.
func TestLitLambdaParams_DisagreeingAnnotations(t *testing.T) {
	src := litLambdaTypes + `
fn main() {
  xs = [#("a", fn d: Day -> name_of(d)), #("b", fn d: Date -> "${d.n}"), #("c", fn d -> name_of(d))]
  _ = xs
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("the checker admits lambdas annotated `d: Day` and `d: Date` at the same place")
	}
	want := "10:42 list element type mismatch: expected #(String, (Day) -> String), got #(String, (Date) -> String)"
	if got := litLambdaErrors(errs); !strings.HasPrefix(got, want) || len(errs) != 1 {
		t.Fatalf("errors:\n%s\nwant only:\n%s", got, want)
	}
}

// With nothing to type it, each unannotated parameter is still an error, at
// the parameter, with a hint that one element's annotation is enough, and no
// follow-on mismatch or undetermined binding. An element after the lambda
// does not type it.
func TestLitLambdaParams_NothingTypesIt(t *testing.T) {
	src := litLambdaTypes + `
fn none() {
  xs = [#("a", fn d -> "${d}"), #("b", fn e -> "${e}")]
  _ = xs
}

fn named_after() {
  ys = [#("a", fn d -> "${d}"), #("b", name_of)]
  _ = ys
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("the checker admits list elements whose lambda parameters nothing types")
	}
	hint := func(name string) string {
		return "cannot infer type for parameter " + name + "\n" +
			"help: annotate the parameter with its type, as in `fn " + name + ": T -> ...`, in this element or another one: the parameter at the same place in every element takes it"
	}
	want := strings.Join([]string{
		"10:19 " + hint("d"),
		"10:43 " + hint("e"),
		"15:19 " + hint("d"),
	}, "\n")
	if got := litLambdaErrors(errs); got != want {
		t.Fatalf("errors:\n%s\nwant:\n%s", got, want)
	}
}

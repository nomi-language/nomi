package analysis_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// typeParamsIn collects every type parameter t names, following solved
// inference variables at every level.
func typeParamsIn(t analysis.Type, out map[*analysis.TypeParam_]bool) {
	t = analysis.ResolveTypeVar(t)
	switch v := t.(type) {
	case *analysis.TypeParam_:
		out[v] = true
	case *analysis.ListType:
		typeParamsIn(v.Elem, out)
	case *analysis.MapType:
		typeParamsIn(v.Key, out)
		typeParamsIn(v.Val, out)
	case *analysis.FuncType:
		for _, p := range v.Params {
			typeParamsIn(p, out)
		}
		typeParamsIn(v.Return, out)
	case *analysis.TupleType:
		for _, e := range v.Elems {
			typeParamsIn(e, out)
		}
	case *analysis.StructType:
		for _, a := range v.TypeArgs {
			typeParamsIn(a, out)
		}
	case *analysis.EnumType:
		for _, a := range v.TypeArgs {
			typeParamsIn(a, out)
		}
	case *analysis.InterfaceType:
		for _, a := range v.TypeArgs {
			typeParamsIn(a, out)
		}
	case *analysis.DistinctType:
		for _, a := range v.TypeArgs {
			typeParamsIn(a, out)
		}
	}
}

// isCallee reports a node whose recorded type is a function's declared
// signature (a callee name), which keeps its own parameters by design.
func isCallee(n ast.Node, t analysis.Type) bool {
	if _, fn := analysis.ResolveTypeVar(t).(*analysis.FuncType); !fn {
		return false
	}
	switch n.(type) {
	case *ast.FieldAccess, *ast.TypeIdent, *ast.Ident:
		return true
	}
	return false
}

// recordedOnLine is every type recorded for a node of the given kind on line,
// rendered and sorted.
func recordedOnLine(fa *analysis.FileAnalysis, kind string, line int) []string {
	var out []string
	for n, ty := range fa.ExprTypes {
		if n.NodeType() == kind && n.LineNum() == line {
			out = append(out, analysis.ResolveTypeVar(ty).String())
		}
	}
	sort.Strings(out)
	return out
}

// wantNoTypeParamsBetween fails for any value expression on lines first..last
// whose recorded type names a type parameter: none is in scope there.
func wantNoTypeParamsBetween(t *testing.T, fa *analysis.FileAnalysis, first, last int) {
	t.Helper()
	for n, ty := range fa.ExprTypes {
		if n.LineNum() < first || n.LineNum() > last || isCallee(n, ty) {
			continue
		}
		params := map[*analysis.TypeParam_]bool{}
		typeParamsIn(ty, params)
		if len(params) > 0 {
			t.Errorf("line %d %s recorded as %s, which names a type parameter", n.LineNum(), n.NodeType(), ty)
		}
	}
}

func wantRecorded(t *testing.T, fa *analysis.FileAnalysis, kind string, line int, want ...string) {
	t.Helper()
	got := recordedOnLine(fa, kind, line)
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("%s on line %d recorded as %q, want %q", kind, line, got, want)
	}
}

// A pipeline whose head has no type (an owner member that does not exist)
// reports that one error. Its stages are typed from what their callbacks
// solve, not left as `Iter<T>` with Iter.filter's own T, whose rigid T then
// made `x % 2` a second and third error.
func TestExprTypes_PipeAfterUntypedHead(t *testing.T) {
	src := `fn demo(): Int {
  n = 5
  evens = List.range(0, n)
    |> Iter.filter(|x| x % 2 == 0)
    |> Iter.to_list()
  Iter.count(evens)
}`
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "type 'List' has no member 'range'") {
		t.Fatalf("errors = %v, want only the unknown member", errs)
	}
	wantRecorded(t, fa, "Binary", 4, "Bool", "Int", "Iter<Int>")
	wantRecorded(t, fa, "Binary", 5, "List<Int>")
	wantNoTypeParamsBetween(t, fa, 2, 6)
}

// The same pipeline over a range: every stage has its instantiation.
func TestExprTypes_PipeAfterRange(t *testing.T) {
	src := `fn demo(): Int {
  n = 5
  evens = 0..n
    |> Iter.filter(|x| x % 2 == 0)
    |> Iter.to_list()
  Iter.count(evens)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
	wantRecorded(t, fa, "Binary", 4, "Bool", "Int", "Iter<Int>")
	wantRecorded(t, fa, "Binary", 5, "List<Int>")
	wantNoTypeParamsBetween(t, fa, 2, 6)
}

// Iter.map and Iter.reduce both declare a `U`. A seedless reduce inside the
// lambda passed to map gets its accumulator from its own callback; typed as
// reduce's rigid U, `a + b` was "type parameter U cannot use `+`", and the
// stages came out `U` and `Iter<U>`.
func TestExprTypes_NestedGenericsSharingAParameterName(t *testing.T) {
	src := `fn demo(): Int {
  sums = [[1, 2], [3]]
    |> Iter.map(|xs| {
      xs
      |> Iter.reduce(|a, b| a + b)
    })
    |> Iter.to_list()
  total = Iter.reduce([1, 2, 3], |a, b| a + b)
  Iter.count(sums) + total
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
	wantRecorded(t, fa, "Binary", 3, "Iter<Int>")
	wantRecorded(t, fa, "Binary", 5, "Int", "Int")
	wantRecorded(t, fa, "Lambda", 5, "(Int, Int) -> Int")
	wantRecorded(t, fa, "Binary", 7, "List<Int>")
	wantRecorded(t, fa, "Call", 8, "Int")
	wantNoTypeParamsBetween(t, fa, 2, 9)
}

// Two user generic functions that both name their parameter T (and two that
// both name it U), nested in one another: each call has its own.
func TestExprTypes_UserGenericsSharingAParameterName(t *testing.T) {
	src := `fn first<T>(x: T): T { x }
fn wrap<T>(x: T): List<T> { [x] }
fn apply<T, U>(x: T, f: (T) -> U): U { f(x) }
fn twice<U>(x: U): List<U> { [x, x] }
fn demo(): Int {
  a = wrap(first("s"))
  b = apply(1, |x| twice(x))
  c = apply(first(2), |x| wrap(first(x)))
  Iter.count(a) + Iter.count(b) + Iter.count(c)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
	wantRecorded(t, fa, "Call", 6, "List<String>", "String")
	wantRecorded(t, fa, "Call", 7, "List<Int>", "List<Int>")
	wantRecorded(t, fa, "Call", 8, "Int", "Int", "List<Int>", "List<Int>")
	wantNoTypeParamsBetween(t, fa, 6, 9)
}

// Owner-qualified generic calls, direct and piped, and a partial
// application: the open slot and the result share the parameter nothing
// solved, and the call that fills the slot solves both.
func TestExprTypes_GenericOwnerCallsAndPartialApplication(t *testing.T) {
	src := `fn demo(): Int {
  xs = List.concat([1], [2])
  m = Map.put(Map.empty(), "k", 1)
  ys = [3] |> List.concat(xs)
  f = Iter.map(xs, _)
  zs = f(|x| Int.to_string(x)) |> Iter.to_list()
  Iter.count(ys) + Iter.count(zs) + Map.size(m)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
	wantRecorded(t, fa, "Call", 2, "List<Int>")
	wantRecorded(t, fa, "Call", 3, "Map<String, Int>", "Map<String, Int>")
	wantRecorded(t, fa, "Binary", 4, "List<Int>")
	wantRecorded(t, fa, "Call", 5, "((Int) -> String) -> Iter<String>")
	wantRecorded(t, fa, "Binary", 6, "List<String>")
	wantNoTypeParamsBetween(t, fa, 2, 7)
}

// Inside a generic function its own parameters are the answer and stay; a
// callee's same-named parameter does not. `I: Iter<T>` projects T into
// Iter.reduce's own T, so the callback's element is the caller's T.
func TestExprTypes_GenericCallsInsideGenericFunctions(t *testing.T) {
	src := `fn keep<T>(xs: List<T>): List<T> {
  xs |> Iter.filter(|_x| True) |> Iter.to_list()
}
fn first<T, I>(it: I): Maybe<T> where I: Iter<T> {
  Iter.reduce(it, |_acc: Maybe<T> = None, x| { break Some(x) })
}
fn demo(): Int {
  Iter.count(keep([1])) + Maybe.with_default(first([2]), 0)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)

	own := func(name string) map[*analysis.TypeParam_]bool {
		sym := fa.ModuleScope.Lookup(name)
		if sym == nil || sym.Type == nil {
			t.Fatalf("no symbol for %s", name)
		}
		out := map[*analysis.TypeParam_]bool{}
		typeParamsIn(sym.Type, out)
		return out
	}
	keepParams, firstParams := own("keep"), own("first")
	for n, ty := range fa.ExprTypes {
		line := n.LineNum()
		if line > 6 || isCallee(n, ty) {
			continue
		}
		scope := keepParams
		if line >= 4 {
			scope = firstParams
		}
		params := map[*analysis.TypeParam_]bool{}
		typeParamsIn(ty, params)
		for tp := range params {
			if !scope[tp] {
				t.Errorf("line %d %s recorded as %s, naming a %s that is not the enclosing function's", line, n.NodeType(), ty, tp.Name_)
			}
		}
	}
	wantRecorded(t, fa, "Binary", 2, "Iter<T>", "List<T>")
	wantRecorded(t, fa, "Lambda", 5, "(Maybe<T>, T) -> Infallible")
	wantNoTypeParamsBetween(t, fa, 7, 9)
}

package analysis_test

import (
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// `break value` is `return value` then stop (spec §12), so a `break []`
// before a `break [x]` in an `Iter.map` callback is a `List<Int>`, as the
// same `return []` is: the empty literal's element type is solved by the
// callback's other results.
func TestBreakValue_EmptyListTakesTheCallbacksResult(t *testing.T) {
	src := `fn main() -> Unit {
  ys = [1, 2, 3]
    |> Iter.map(fn x -> {
      if x > 2 { break [] }
      if x > 1 { break [x] }
      [x, x]
    })
    |> Iter.to_list()
}`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	expectClean(t, errs)
	found := false
	for n, ty := range fa.ExprTypes {
		if l, ok := n.(*ast.ListLit); ok && len(l.Items) == 0 {
			found = true
			if got := fmt.Sprint(analysis.ResolveTypeVar(ty)); got != "List<Int>" {
				t.Fatalf("`break []` is typed %s, want List<Int>", got)
			}
		}
	}
	if !found {
		t.Fatal("no type recorded for `break []`")
	}
}

// A `break []` whose callback ends in a String is reported at the break.
func TestBreakValue_EmptyListAgainstAnotherResultRejected(t *testing.T) {
	src := `fn main() -> Unit {
  ys = [1, 2, 3]
    |> Iter.map(fn x -> {
      if x > 2 { break [] }
      "s"
    })
    |> Iter.to_list()
}`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch: the callback's result is String")
}

// A `break` value of a closed type is a result of the callback too, so a
// `break "s"` in an `Iter.map` callback whose tail is an Int is the same
// mismatch a `return "s"` there would be, reported at the break.
func TestBreakValue_ClosedTypeAgainstTheTailRejected(t *testing.T) {
	src := `fn main() -> Unit {
  _ = [1, 2, 3]
    |> Iter.map(fn x -> {
      if x > 2 { break "s" }
      x
    })
    |> Iter.to_list()
}`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch: the callback's result is Int, got String")
}

// Two `break` values of different closed types disagree with each other
// and with the tail.
func TestBreakValue_ClosedTypesDisagreeRejected(t *testing.T) {
	src := `fn main() -> Unit {
  _ = [1, 2, 3]
    |> Iter.map(fn x -> {
      if x > 2 { break "s" }
      if x > 1 { break True }
      "t"
    })
    |> Iter.to_list()
}`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch: the callback's result is String, got Bool")
}

// A `break` value that agrees with the tail stays clean.
func TestBreakValue_ClosedTypeAgreeingWithTheTail(t *testing.T) {
	src := `fn main() -> Unit {
  _ = [1, 2, 3]
    |> Iter.map(fn x -> {
      if x > 2 { break 0 }
      x
    })
    |> Iter.to_list()
}`
	expectClean(t, checkWithStdlib(src))
}

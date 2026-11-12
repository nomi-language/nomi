package analysis_test

import (
	"fmt"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// `break value` is `return value` then stop (spec §13), so a `break []`
// before a `break [x]` in an `Iter.map` callback is a `List<Int>`, as the
// same `return []` is: the empty literal's element type is solved by the
// callback's other results.
func TestBreakValue_EmptyListTakesTheCallbacksResult(t *testing.T) {
	src := `fn main() -> Unit
  ys = [1, 2, 3]
    |> Iter.map(fn x ->
      if x > 2 then break []
      if x > 1 then break [x]
      [x, x]
    end)
    |> Iter.to_list()
end`
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := loadStd()
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
	src := `fn main() -> Unit
  ys = [1, 2, 3]
    |> Iter.map(fn x ->
      if x > 2 then break []
      "s"
    end)
    |> Iter.to_list()
end`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch: the callback's result is String")
}

// A `break` value of a closed type is a result of the callback too, so a
// `break "s"` in an `Iter.map` callback whose tail is an Int is the same
// mismatch a `return "s"` there would be, reported at the break.
func TestBreakValue_ClosedTypeAgainstTheTailRejected(t *testing.T) {
	src := `fn main() -> Unit
  _ = [1, 2, 3]
    |> Iter.map(fn x ->
      if x > 2 then break "s"
      x
    end)
    |> Iter.to_list()
end`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch: the callback's result is Int, got String")
}

// Two `break` values of different closed types disagree with each other
// and with the tail.
func TestBreakValue_ClosedTypesDisagreeRejected(t *testing.T) {
	src := `fn main() -> Unit
  _ = [1, 2, 3]
    |> Iter.map(fn x ->
      if x > 2 then break "s"
      if x > 1 then break True
      "t"
    end)
    |> Iter.to_list()
end`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch: the callback's result is String, got Bool")
}

// A `break` value that agrees with the tail stays clean.
func TestBreakValue_ClosedTypeAgreeingWithTheTail(t *testing.T) {
	src := `fn main() -> Unit
  _ = [1, 2, 3]
    |> Iter.map(fn x ->
      if x > 2 then break 0
      x
    end)
    |> Iter.to_list()
end`
	expectClean(t, checkWithStdlib(src))
}

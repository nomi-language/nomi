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
	src := `fn main(): Unit {
  ys = [1, 2, 3]
    |> Iter.map(|x| {
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
	src := `fn main(): Unit {
  ys = [1, 2, 3]
    |> Iter.map(|x| {
      if x > 2 { break [] }
      "s"
    })
    |> Iter.to_list()
}`
	expectErrorContaining(t, checkWithStdlib(src), "break value type mismatch: the callback's result is String")
}

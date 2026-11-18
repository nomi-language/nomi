package frontend_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/std"
)

// A test checked against a std module's shared analysis (a reference
// editor's, frontend.Checker.CheckStdlibTest) leaves the module's own call
// records as they were. The copy it checks shares the module's records, and
// classifying them again, with a check that saw none of the module's bodies,
// made a `fn` declared inside one of them (std/sql's close_quietly prompt
// declares `purge`) a declared function: the module's next lowering
// declined the call. The added test's own nested `fn`s, called directly,
// from a lambda and from itself, are function values of their scope.
func TestCheckStdlibTest_LeavesTheModulesCalleesAsTheyWere(t *testing.T) {
	lib := std.Shared().Ensure("sql")
	if err := lib.Bodies("sql"); err != nil {
		t.Fatal(err)
	}
	fa := lib.Files["sql"]
	before := map[ast.Node]analysis.CalleeKind{}
	nested := 0
	for n, rc := range fa.Calls {
		before[n] = rc.Callee.Kind
		if c, ok := n.(*ast.Call); ok {
			if id, ok := c.Func.(*ast.Ident); ok && id.Name == "purge" {
				if rc.Callee.Kind != analysis.CalleeValue {
					t.Fatalf("std/sql's call of its prompt's nested `purge` is classified %v before any added test", rc.Callee.Kind)
				}
				nested++
			}
		}
	}
	if nested == 0 {
		t.Fatal("std/sql has no call of a nested `fn purge`, so nothing below is tested")
	}

	src, ok := std.ReadFile("sql")
	if !ok {
		t.Fatal("no std/sql source")
	}
	prefix := strings.TrimRight(string(src), "\n") + "\n\n"
	line := strings.Count(prefix, "\n") + 1
	added := prefix + `test "reference"
  fn twice(n: Int) -> Int
    n * 2
  end
  fn fact(k: Int) -> Int
    if k <= 1 then 1 else k * fact(k - 1)
  end
  apply = fn n: Int -> twice(n)
  assert twice(2) == 4
  assert apply(3) == 6
  assert fact(3) == 6
end
`
	test, copied, restore, checked, err := frontend.New(frontend.Config{}).CheckStdlibTest("sql", added, line)
	if err != nil {
		t.Fatalf("the front end rejects the added test, so nothing below is tested: %v", err)
	}
	if !checked || test == nil {
		t.Fatal("the added test was not checked against the shared analysis, so nothing below is tested")
	}
	defer restore()

	for n, kind := range before {
		if got := fa.Calls[n].Callee.Kind; got != kind {
			c, _ := n.(*ast.Call)
			t.Errorf("std/sql:%d: checking an added test reclassified the module's call from %v to %v", c.Line, kind, got)
		}
	}
	calls := 0
	ast.Inspect(test, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok {
			return true
		}
		id, ok := c.Func.(*ast.Ident)
		if !ok || (id.Name != "twice" && id.Name != "fact") {
			return true
		}
		calls++
		if rc := copied.Calls[c]; rc == nil || rc.Callee.Kind != analysis.CalleeValue {
			t.Errorf("line %d: the added test's call of its nested `%s` is not a function value: %+v", c.Line, id.Name, rc)
		}
		return true
	})
	if calls != 4 {
		t.Errorf("found %d calls of the added test's nested fns, want 4", calls)
	}
}

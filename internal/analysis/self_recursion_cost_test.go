package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The unconditional-recursion rule answers mayCall and mayExit once per
// node. mustRecurse asks both of every operand it passes, and each answer
// walks the operand's subtree, so a chain of n operators or n nested `if`s
// cost n² node visits: a 20000-term string concatenation spent seconds here.
func TestSelfRecursion_VisitsEachNodeOnce(t *testing.T) {
	const n = 2000
	for _, tc := range []struct{ name, body string }{
		{"a concatenation", `"a"` + strings.Repeat(` + "a"`, n)},
		{"a sum", "1" + strings.Repeat(" + 1", n)},
		{"nested ifs", strings.Repeat("if True { ", 200) + "1" + strings.Repeat(" } else { 2 }", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn f(): Int {\n  _x = " + tc.body + "\n  f()\n}\n"
			fa, _ := checkSourceWithModules(t, src, nil)
			sym := fa.ModuleScope.Lookup("f")
			fn, ok := sym.Node.(*ast.FuncDef)
			if !ok {
				t.Fatalf("no fn f: %#v", sym)
			}
			r := &selfRecursion{fa: fa, fn: fn, owner: fn.Name}
			if !r.mustRecurse(fn.Body, true, true) {
				t.Fatal("f calls itself on every path and the rule does not see it")
			}
			nodes := 0
			var count func(ast.Node)
			count = func(x ast.Node) {
				nodes++
				ast.Children(x, count)
			}
			count(fn.Body)
			if r.walked > 2*nodes {
				t.Errorf("%d answers computed for %d nodes", r.walked, nodes)
			}
		})
	}
}

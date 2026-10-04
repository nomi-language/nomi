package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// The Context value store, the `Type<T>` witness, and the two guards that keep
// this arm's assumptions from rotting into a wrong answer.

// TestContextValue_StdDeclaresTheShapeThisArmAssumes is the guard the arm's use
// of std's DECLARATION rests on.
//
// `contextValueFn.ctxResult` and the receiver's position are read from std
// rather than from the checker, which is channelCtorFromTarget's safe direction
// — but only because a std edit fails HERE instead of being silently coerced to
// the wrong kind there. Two things are asserted per row: the parameter list this
// arm indexes by position, and the result shape it takes from the row.
func TestContextValue_StdDeclaresTheShapeThisArmAssumes(t *testing.T) {
	// std's ANALYZED nodes, which is the tree the builder itself lowers — not a
	// re-parse, for the reason StdLib.Nodes' own comment gives.
	nodes := std.Load().Nodes["context"]
	if len(nodes) == 0 {
		t.Fatal("std/context has no analyzed nodes, so this guard is vacuous")
	}
	type want struct {
		params []string
		result string
		tps    []string
	}
	wants := map[string]want{
		"with_value": {params: []string{"Context", "T"}, result: "Context", tps: []string{"T"}},
		"value":      {params: []string{"Context", "Type<T>"}, result: "Maybe<T>", tps: []string{"T"}},
	}
	seen := map[string]bool{}
	for _, node := range nodes {
		impl, isImpl := node.(*ast.ImplBlock)
		if !isImpl || impl.Interface != nil || impl.Receiver == nil ||
			impl.Receiver.TypeString() != "Context" {
			continue
		}
		for _, item := range impl.Items {
			ext, isExtern := item.(*ast.ExternFunc)
			if !isExtern {
				continue
			}
			w, claimed := wants[ext.Name]
			if !claimed {
				continue
			}
			seen[ext.Name] = true
			if got := typeParamNamesOf(ext); !equalStrings(got, w.tps) {
				t.Errorf("std declares Context.%s over type parameters %v, want %v — the arm "+
					"assumes exactly one, solved by the checker", ext.Name, got, w.tps)
			}
			var params []string
			for _, p := range ext.Params {
				if p.TypeAnnotation == nil {
					params = append(params, "<untyped>")
					continue
				}
				params = append(params, p.TypeAnnotation.TypeString())
			}
			if !equalStrings(params, w.params) {
				t.Errorf("std declares Context.%s%v; this arm indexes parameters by POSITION and "+
					"assumes %v, so a reorder would key the store on the receiver",
					ext.Name, params, w.params)
			}
			result := "<none>"
			if ext.ReturnTypeExpr != nil {
				result = ext.ReturnTypeExpr.TypeString()
			}
			if result != w.result {
				t.Errorf("std declares Context.%s -> %s, want %s. contextValueFuncs takes the "+
					"result shape from its ROW (ctxResult), so a std edit here would produce a "+
					"call typed as the wrong thing", ext.Name, result, w.result)
			}
		}
	}
	for name := range wants {
		if !seen[name] {
			t.Errorf("std/context declares no `host fn %s` in `impl Context`, so contextValueFuncs' "+
				"row for it is dead and every call to it refuses", name)
		}
	}
}

func typeParamNamesOf(ext *ast.ExternFunc) []string {
	out := make([]string, 0, len(ext.TypeParams))
	for _, tp := range ext.TypeParams {
		out = append(out, tp.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

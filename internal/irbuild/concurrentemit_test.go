package irbuild

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestConcurrentEmit_TheTypeWalksDoNotMutateATypeDef checks that the type
// walks `zeroSizedOn` and `reachesOn` read the type graph and never write to
// it.
//
// A *typeDef is interned process-wide for every stdlib and rt-declared type
// (`opaqueDefs`, `stdEnumDefs`, `stdMarkerDefs`, `hostTypeDefs` are each a
// `sync.OnceValue`), so every `Generate` in a process walks the same pointers.
// A walk that stores anything on one (a cycle mark, a memo, a cached answer)
// makes its own bookkeeping visible to every concurrent lowering. Both walks
// feed layout decisions (which variant payloads get a slot, which fields are
// boxed), so a foreign goroutine's mark would make this one lay out the same
// Nomi differently: a wrong answer, not a torn read. The race window is a few
// instructions wide, so concurrent-lowering tests and `-race` rarely land in
// it; this check is structural instead.
//
// It forbids the walks from assigning through a selector at all, rather than
// naming one field, so a renamed or added field fails too. The per-walk state
// lives in the typePath parameter.
//
// Parsed, not grepped: a text scan of these function bodies would also match
// every `path = append(path, d)`, which is an assignment to a local.
func TestConcurrentEmit_TheTypeWalksDoNotMutateATypeDef(t *testing.T) {
	want := map[string]string{
		"zeroSizedOn": "types.go, native.go",
		"reachesOn":   "types.go",
	}

	fset := token.NewFileSet()
	seen := map[string]int{}
	for _, name := range []string{"types.go", "native.go"} {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, watched := want[fn.Name.Name]; !watched {
				continue
			}
			seen[fn.Name.Name]++
			goast.Inspect(fn.Body, func(n goast.Node) bool {
				var lhs []goast.Expr
				switch s := n.(type) {
				case *goast.AssignStmt:
					lhs = s.Lhs
				case *goast.IncDecStmt:
					lhs = []goast.Expr{s.X}
				default:
					return true
				}
				for _, target := range lhs {
					if sel, ok := target.(*goast.SelectorExpr); ok {
						t.Errorf("%s: %s assigns to %s — a type walk must not write to the "+
							"type graph. The *typeDefs are interned process-wide "+
							"(opaqueDefs, stdEnumDefs, stdMarkerDefs, hostTypeDefs are each a "+
							"sync.OnceValue), so per-walk state stored on one is shared with "+
							"every concurrent Generate. Carry it in the typePath parameter.",
							fset.Position(sel.Pos()), fn.Name.Name, exprText(sel))
					}
				}
				return true
			})
		}
	}

	for name, where := range want {
		if seen[name] == 0 {
			t.Errorf("%s was not found in %s, so this guard checked nothing for it. "+
				"If the walk was renamed, rename it here too; if it was deleted, delete "+
				"its row.", name, where)
		}
	}
}

// exprText renders a selector for the message, without a printer: the only
// shapes these walks can produce are `x.f` and `x.y.f`.
func exprText(e goast.Expr) string {
	switch v := e.(type) {
	case *goast.Ident:
		return v.Name
	case *goast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	case *goast.IndexExpr:
		return exprText(v.X) + "[...]"
	}
	return "an expression"
}

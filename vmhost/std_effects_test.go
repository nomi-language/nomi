package vmhost

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// StdEffects answers a public std `once` by its initializer, as it answers
// a function by its body: io's streams obtain the stream and have the
// effect std::io, and a constant has none, answered all the same.
func TestStdEffects_AnswersOnces(t *testing.T) {
	programs := map[string]*Program{}
	for _, tc := range []struct {
		module, name, effect string
	}{
		{"io", "stdin", "std::io"},
		{"io", "stdout", "std::io"},
		{"io", "stderr", "std::io"},
		{"float", "pi", ""},
		{"http", "ok", ""},
	} {
		p := programs[tc.module]
		if p == nil {
			var err error
			if p, err = LoadEmbeddedStdlib(tc.module); err != nil {
				t.Fatalf("std::%s: %v", tc.module, err)
			}
			programs[tc.module] = p
		}
		var decl *ast.OnceBinding
		for _, node := range p.prog.Entry().Nodes {
			ast.Inspect(node, func(n ast.Node) bool {
				if ob, ok := n.(*ast.OnceBinding); ok && ob.Public && ob.Name == tc.name {
					decl = ob
				}
				return decl == nil
			})
		}
		if decl == nil {
			t.Errorf("std::%s declares no public `once %s`", tc.module, tc.name)
			continue
		}
		names, answered := p.StdEffects()
		at := DeclAt{Line: decl.Line, Col: decl.Col}
		if !answered[at] {
			t.Errorf("std::%s `once %s`: the walk has no answer for it", tc.module, tc.name)
		}
		if got := names[at]; got != tc.effect {
			t.Errorf("std::%s `once %s`: has the effect %q, want %q", tc.module, tc.name, got, tc.effect)
		}
	}
}

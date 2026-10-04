package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// everyFuncDef is every *ast.FuncDef in the entry module, including the ones
// inside impl blocks and inside another function's body.
//
// Enumerated rather than assumed, because a walk that skips a node KIND looks
// finished from every angle except the one it skips — and a SYNTHESIZED `impl
// Debug for Pair<A, B>` is reached only through *ast.ImplBlock.Items.
func everyFuncDef(p *Program) []*ast.FuncDef {
	var out []*ast.FuncDef
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.FuncDef:
			out = append(out, v)
			if v.Body != nil {
				for _, s := range v.Body.Stmts {
					walk(s)
				}
			}
		case *ast.ImplBlock:
			for _, it := range v.Items {
				walk(it)
			}
		case *ast.TestDecl:
			if v.Body != nil {
				for _, s := range v.Body.Stmts {
					walk(s)
				}
			}
		}
	}
	for _, n := range p.Entry().Nodes {
		walk(n)
	}
	return out
}

// lastFuncDef is the entry module's last top-level `fn`, which is the one every
// witness in this file declares last.
func lastFuncDef(t *testing.T, p *Program) *ast.FuncDef {
	t.Helper()
	var last *ast.FuncDef
	for _, n := range p.Entry().Nodes {
		if fd, isFn := n.(*ast.FuncDef); isFn && !fd.ImplFunction {
			last = fd
		}
	}
	if last == nil {
		t.Fatal("the witness declares no top-level fn")
	}
	return last
}

// TestGeneric_TheFrontEndForecloses records which of this arm's call-site
// guards are unreachable from source, and fires if that stops being true.
//
// A test that SKIPS is a vacuous guard, so these are written as the property
// that actually holds: the checker rejects both witnesses before the builder
// sees them. `type argument mismatch` is therefore unreachable, and it is
// kept rather than deleted, because falsified it is a value lowered at the
// wrong type rather than a refusal, and that is the wrong side to be cheap on. tail.go's
// tailMemberSig keeps two predicates on the identical ground.
//
// If the front end ever admits either shape, this test fails and the builder's
// own refusal becomes the live one — which is the notification a reader needs,
// rather than a skip nobody reads.
func TestGeneric_TheFrontEndForecloses(t *testing.T) {
	cases := []struct{ name, src string }{{
		// A turbofish disagreeing with the argument. In this subset every type
		// parameter is also inferable from a parameter, so the two can
		// conflict — and bindTypeParam refuses the conflict rather than
		// letting either side win.
		name: "a turbofish that disagrees with the argument",
		src: "fn first<T>(x: T): T {\n  x\n}\n\n" +
			"fn main() {\n  _ = first<String>(7)\n}\n",
	}, {
		// One type parameter in two slots, bound to two types.
		name: "one type parameter bound to two types",
		src: "fn pair_of<T>(a: T, _b: T): T {\n  a\n}\n\n" +
			"fn main() {\n  _ = pair_of(1, \"x\")\n}\n",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := AnalyzeSource("main", tc.src); err == nil {
				t.Fatalf("the front end now ADMITS this shape, so irbuild's own " +
					"`type argument mismatch` refusal is live rather than a phantom: " +
					"assert it here instead of asserting the foreclosure")
			}
		})
	}
}

// TestGeneric_TheBoundFreeLineIsOneImplementation pins the predicate itself,
// because it is consumed from two sides and an agreement between two callers of
// one implementation is worth nothing unless the implementation is pinned.
//
// The rows are the reason the line is DERIVED rather than conventional: with
// zero bounds, typeParamDispatchIface's scan has nothing to scan and returns
// "", so a bound-free type parameter provably cannot host `T.method(x)`. And
// `TypeParam.Bounds` is read alongside WhereClauses because the analyzer
// SYNTHESIZES bounds into it — reading only the source-written half would admit
// a synthesized bound as bound-free.
func TestGeneric_TheBoundFreeLineIsOneImplementation(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"no type parameters at all", "fn plain(n: Int): Int {\n  n\n}\n", false},
		{"one bound-free type parameter", "fn first<T>(x: T): T {\n  x\n}\n", false},
		{"two bound-free type parameters", "fn pick<A, B>(a: A, _b: B): A {\n  a\n}\n", false},
		{"a where clause", "interface Shout {\n  fn shout(v: self): String\n}\n\n" +
			"fn announce<T>(x: T): String where T: Shout {\n  Shout.shout(x)\n}\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := AnalyzeSource("main", tc.src)
			if err != nil {
				t.Fatalf("front end rejected the witness: %v", err)
			}
			fd := lastFuncDef(t, p)
			if got := typeParamsNeedDictionary(fd); got != tc.want {
				t.Fatalf("typeParamsNeedDictionary(%s) = %v, want %v", fd.Name, got, tc.want)
			}
		})
	}
	// A SYNTHESIZED bound counts, and the guard would pass vacuously if the
	// analyzer never put one anywhere — so it is asserted that the population
	// is non-empty rather than assumed. `derive Debug` on a generic struct
	// synthesizes `impl Debug for Pair<A, B>` whose `inspect` carries
	// `A: Debug, B: Debug` in TypeParam.Bounds and nothing in WhereClauses.
	src := "struct Pair<A, B> {\n  first: A\n  second: B\n}\n\n" +
		"derive Debug for Pair\n\n" +
		"fn main() {\n  _ = Pair{first: 1, second: \"a\"}\n}\n"
	p, err := AnalyzeSource("main", src)
	if err != nil {
		t.Fatalf("front end rejected the synthesized-bound witness: %v", err)
	}
	synthesized := 0
	for _, fd := range everyFuncDef(p) {
		if len(fd.WhereClauses) > 0 || len(fd.TypeParams) == 0 {
			continue
		}
		for _, tp := range fd.TypeParams {
			if len(tp.Bounds) == 0 {
				continue
			}
			synthesized++
			if !typeParamsNeedDictionary(fd) {
				t.Fatalf("%s carries a synthesized bound on %s and was read as bound-free", fd.Name, tp.Name)
			}
		}
	}
	if synthesized == 0 {
		t.Fatal("no synthesized type-parameter bound exists anywhere in the witness, so the " +
			"TypeParam.Bounds half of typeParamsNeedDictionary is untested — the guard is vacuous")
	}
}

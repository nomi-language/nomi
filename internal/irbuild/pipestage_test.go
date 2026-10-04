package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestPipeStages_PrefixesAreCumulativeAndInnermostFirst pins the ORDER
// separately from the report, because the report can only show it for the
// shapes a fixture can reach and the rule is about every chain.
//
// The order is the cumulative prefixes with the leftmost value first: the
// innermost left operand, then each pipe node from the innermost out.
func TestPipeStages_PrefixesAreCumulativeAndInnermostFirst(t *testing.T) {
	// `a |> b() |> c()`, built as the parser builds it: left-nested.
	a := &ast.Ident{Name: "a", Line: 1, Col: 1}
	b := &ast.Call{Func: &ast.Ident{Name: "b", Line: 1, Col: 6}, Line: 1, Col: 6}
	c := &ast.Call{Func: &ast.Ident{Name: "c", Line: 1, Col: 13}, Line: 1, Col: 13}
	inner := &ast.Binary{Op: "|>", Left: a, Right: b, Line: 1, Col: 3}
	outer := &ast.Binary{Op: "|>", Left: inner, Right: c, Line: 1, Col: 10}

	prefixes := pipeStagePrefixes(outer)
	want := []ast.Node{a, inner, outer}
	if len(prefixes) != len(want) {
		t.Fatalf("got %d prefixes, want %d", len(prefixes), len(want))
	}
	for i := range want {
		if prefixes[i] != want[i] {
			t.Fatalf("prefix %d is %T at %p, want %T at %p",
				i, prefixes[i], prefixes[i], want[i], want[i])
		}
	}
	if pipeStagePrefixes(a) != nil {
		t.Fatalf("a non-pipe has no prefixes")
	}
	if !pipeStagesRecordable(prefixes) {
		t.Fatalf("a plain two-stage call chain must be recordable")
	}
}

// TestPipeStages_UnrecordableShapesStayRefused pins the door's width. Each of
// these has a stage list that is not a function of the spine, so admitting one
// would be a report that is wrong rather than absent.
func TestPipeStages_UnrecordableShapesStayRefused(t *testing.T) {
	call := func(name string) *ast.Call {
		return &ast.Call{Func: &ast.Ident{Name: name, Line: 1, Col: 1}, Line: 1, Col: 1}
	}
	pipe := func(left ast.Node, right ast.Node) *ast.Binary {
		return &ast.Binary{Op: "|>", Left: left, Right: right, Line: 1, Col: 1}
	}
	plain := pipe(&ast.Ident{Name: "a", Line: 1, Col: 1}, call("b"))

	cases := []struct {
		name string
		node ast.Node
	}{
		{
			// The inner pipe would record `a` and `a |> b()` of its own, and
			// then the outer the PARENTHESISED left operand again.
			"a parenthesised left operand",
			pipe(&ast.GroupedExpr{Expr: plain, Line: 1, Col: 1}, call("c")),
		},
		{
			// Same env, so the argument's own stages interleave into this
			// chain's list.
			"a nested pipe in a stage argument",
			pipe(&ast.Ident{Name: "x", Line: 1, Col: 1},
				&ast.Call{Func: &ast.Ident{Name: "f", Line: 1, Col: 1},
					Args: []ast.Node{plain}, Line: 1, Col: 1}),
		},
		{
			// A keyword stage consumes the piped value through its own
			// machinery; three of the four are refused as pipe stages anyway.
			"a keyword stage",
			pipe(&ast.Ident{Name: "x", Line: 1, Col: 1}, &ast.TryOp{Line: 1, Col: 1}),
		},
	}
	for _, tc := range cases {
		prefixes := pipeStagePrefixes(tc.node)
		if len(prefixes) == 0 {
			t.Fatalf("%s: expected a pipe chain", tc.name)
		}
		if pipeStagesRecordable(prefixes) {
			t.Fatalf("%s: must not be recordable", tc.name)
		}
	}
}

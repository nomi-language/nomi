package ast

import (
	"reflect"
	"testing"
)

// names renders nodes as NodeType names, for comparing walks.
func names(ns []Node) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.NodeType())
	}
	return out
}

func children(n Node) []Node {
	var out []Node
	Children(n, func(c Node) { out = append(out, c) })
	return out
}

// Children reaches a node through every way a field can hold one: directly,
// through an interface, in a slice, inside a plain struct (a case branch, a
// parameter), inside a struct held in an interface (an interpolated string's
// expression part), and stored by value (an attached test). Absent children,
// nil or typed nil, are skipped. It does not report n itself.
func TestChildren_ReachesEveryWayAFieldHoldsANode(t *testing.T) {
	x := &Ident{Name: "x"}
	guard := &FloatLit{Value: 1.5}
	interp := &StringInterp{Parts: []StringPart{StringText{Value: "a"}, StringExpr{Expr: x}}}
	c := &Case{
		Value: x,
		Branches: []CaseBranch{
			{Pattern: &WildcardPattern{}, Guard: guard, Body: interp},
			{Pattern: &WildcardPattern{}, Guard: (*FloatLit)(nil)},
		},
	}
	got := names(children(c))
	want := []string{"Ident", "WildcardPattern", "FloatLit", "StringInterp", "WildcardPattern"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Children(case) = %v, want %v", got, want)
	}
	if got := children(interp); len(got) != 1 || got[0] != Node(x) {
		t.Fatalf("Children(interp) = %v, want the expression part", names(got))
	}

	body := &Block{}
	fd := &FuncDef{
		AttachedTests:  []AttachedTest{{Body: &Block{}}},
		Params:         []Param{{Name: "p", TypeAnnotation: &SimpleType{Name: "Int"}}},
		ReturnTypeExpr: &SimpleType{Name: "Int"},
		Body:           body,
	}
	got = names(children(fd))
	want = []string{"AttachedTest", "SimpleType", "SimpleType", "Block"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Children(fn) = %v, want %v", got, want)
	}
	if at := children(fd)[0]; at != Node(&fd.AttachedTests[0]) {
		t.Fatalf("an attached test is reported by a copy, not its address in the slice")
	}

	Children(nil, func(Node) { t.Fatal("a nil node has children") })
	Children((*Block)(nil), func(Node) { t.Fatal("a typed nil node has children") })
}

// Inspect visits depth first, n before its children, and a false return
// prunes the subtree without stopping the walk.
func TestInspect_VisitsPreorderAndPrunes(t *testing.T) {
	inner := &Lambda{Body: &Block{Stmts: []Node{&ExprStmt{Expr: &IntLit{Value: 2}}}}}
	root := &Block{Stmts: []Node{
		&ExprStmt{Expr: inner},
		&ExprStmt{Expr: &IntLit{Value: 1}},
	}}
	var all []Node
	Inspect(root, func(n Node) bool { all = append(all, n); return true })
	want := []string{"Block", "ExprStmt", "Lambda", "Block", "ExprStmt", "IntLit", "ExprStmt", "IntLit"}
	if got := names(all); !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect = %v, want %v", got, want)
	}

	var pruned []Node
	Inspect(root, func(n Node) bool {
		pruned = append(pruned, n)
		_, isLambda := n.(*Lambda)
		return !isLambda
	})
	want = []string{"Block", "ExprStmt", "Lambda", "ExprStmt", "IntLit"}
	if got := names(pruned); !reflect.DeepEqual(got, want) {
		t.Fatalf("Inspect pruning the lambda = %v, want %v", got, want)
	}

	Inspect((*Block)(nil), func(Node) bool { t.Fatal("a typed nil node is visited"); return true })
}

// A type that holds no node anywhere is not walked: Children of a literal
// reports nothing, and the cached walk for it is the empty one.
func TestChildren_ATypeWithNoNodeFieldsHasNoWalk(t *testing.T) {
	if s := fieldsStep(reflect.TypeFor[IntLit]()); s != nil {
		t.Fatalf("IntLit has a field walk, though none of its fields can hold a node")
	}
	if s := valueStep(reflect.TypeFor[[]Trivia]()); s != nil {
		t.Fatalf("[]Trivia has a walk, though a Trivia holds no node")
	}
}

// Walks from many goroutines share the per-type cache; run under -race.
func TestInspect_ConcurrentWalksShareTheCache(t *testing.T) {
	done := make(chan int)
	for range 8 {
		go func() {
			tree := &Block{Stmts: []Node{&ExprStmt{Expr: &StringInterp{Parts: []StringPart{StringExpr{Expr: &Ident{Name: "x"}}}}}}}
			count := 0
			Inspect(tree, func(Node) bool { count++; return true })
			done <- count
		}()
	}
	for range 8 {
		if got := <-done; got != 4 {
			t.Errorf("a concurrent walk visited %d nodes, want 4", got)
		}
	}
}

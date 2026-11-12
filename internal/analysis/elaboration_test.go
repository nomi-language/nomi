package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

const elaboratedSrc = `struct Box<T>
    item: T
end

fn id<T>(x: T) -> T
    x
end

fn wrap<A>(x: A) -> Box<A>
    Box{item: id(x)}
end

fn twice(f: (Int) -> Int, n: Int) -> Int
    f(f(n))
end

fn run() -> Int
    b = wrap(3)
    n = id(b.item)
    twice(fn k -> k + n, 1)
end
`

// elabCalls is every call in nodes whose callee is written name.
func elabCalls(nodes []ast.Node, name string) []*ast.Call {
	var out []*ast.Call
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			if c, ok := n.(*ast.Call); ok && elabCalleeText(c.Func) == name {
				out = append(out, c)
			}
			return true
		})
	}
	return out
}

// TestVerifyElaboration_AnElaboratedFileReportsNothing: generic calls at a
// concrete type and at the caller's own parameter, a lambda, a value call
// through a parameter, and a struct literal of a generic struct are all
// recorded by the checker.
func TestVerifyElaboration_AnElaboratedFileReportsNothing(t *testing.T) {
	fa, nodes := checkNodes(t, elaboratedSrc)
	if got := VerifyElaboration(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
}

// TestVerifyElaboration_ReportsAnOpenTypeArgument opens the recorded
// instantiation of `id(b.item)` and holds the verifier to reporting it, and
// then removes it and holds the verifier to reporting that.
func TestVerifyElaboration_ReportsAnOpenTypeArgument(t *testing.T) {
	fa, nodes := checkNodes(t, elaboratedSrc)
	calls := elabCalls(nodes, "id")
	var call *ast.Call
	for _, c := range calls {
		if len(c.Args) == 1 {
			if _, ok := c.Args[0].(*ast.FieldAccess); ok {
				call = c
			}
		}
	}
	if call == nil {
		t.Fatal("no call id(b.item) in the source")
	}
	pos, _ := calleeKey(call.Func)
	ref := fa.References[pos]
	if ref == nil || ref.CallType == nil {
		t.Fatalf("the checker recorded no instantiation for id(b.item): %+v", ref)
	}
	open := &TypeVar{ID: 1 << 20}
	opened := *ref
	opened.CallType = &FuncType{Params: []Type{open}, Return: open}
	fa.References[pos] = &opened
	got := VerifyElaboration(fa, nodes)
	if !hasElabCategory(got, call, "call: generic callee, type argument open") {
		t.Fatalf("an open type argument at id(b.item) is not reported: %v", got)
	}
	bare := *ref
	bare.CallType = nil
	fa.References[pos] = &bare
	got = VerifyElaboration(fa, nodes)
	if !hasElabCategory(got, call, "call: generic callee, no instantiation recorded") {
		t.Fatalf("a missing instantiation at id(b.item) is not reported: %v", got)
	}
	fa.References[pos] = ref
}

// TestVerifyElaboration_ReportsAMissingCalleeAndType removes the callee's
// reference and an expression's type.
func TestVerifyElaboration_ReportsAMissingCalleeAndType(t *testing.T) {
	fa, nodes := checkNodes(t, elaboratedSrc)
	call := elabCalls(nodes, "wrap")[0]
	pos, _ := calleeKey(call.Func)
	ref := fa.References[pos]
	delete(fa.References, pos)
	arg := call.Args[0]
	delete(fa.ExprTypes, arg)
	got := VerifyElaboration(fa, nodes)
	fa.References[pos] = ref
	if !hasElabCategory(got, call, "call: no recorded callee") {
		t.Fatalf("a call with no callee recorded is not reported: %v", got)
	}
	if !hasElabCategory(got, arg, "expr: no type") {
		t.Fatalf("an argument with no type is not reported: %v", got)
	}
}

// TestVerifyElaboration_ANestedUnsolvedVariableIsClosed: `[]` passed to a
// generic callee is accepted by the checker (no value of its element type
// is made, undetermined.go). The checker closes the element as Int for
// lowering (closed_vars.go), so nothing is reported, while the type still
// reads `?N` to a user. With the closing taken away, the element is open
// again and reported.
func TestVerifyElaboration_ANestedUnsolvedVariableIsClosed(t *testing.T) {
	fa, nodes := checkNodes(t, `fn count<T>(_xs: List<T>) -> Int
    0
end

fn run() -> Int
    count([])
end
`)
	call := elabCalls(nodes, "count")[0]
	if got := VerifyElaboration(fa, nodes); len(got) > 0 {
		t.Fatalf("count([]) is reported though its element is closed: %v", got)
	}
	lt, ok := fa.ExprTypes[call.Args[0]].(*ListType)
	if !ok {
		t.Fatalf("[] has type %v, want a List", fa.ExprTypes[call.Args[0]])
	}
	v, ok := lt.Elem.(*TypeVar)
	if !ok || v.Resolved != nil {
		t.Fatalf("[]'s element is %v, want an unsolved variable", lt.Elem)
	}
	if v.Closed != TypeInt {
		t.Fatalf("[]'s element is closed as %v, want Int", v.Closed)
	}
	if got := lt.String(); !strings.HasPrefix(got, "List<?") {
		t.Fatalf("[]'s type displays as %s, want List<?N>", got)
	}
	v.Closed = nil
	got := VerifyElaboration(fa, nodes)
	if !hasElabCategory(got, call, "call: generic callee, type argument open (nested unsolved variable)") ||
		!hasElabCategory(got, call.Args[0], "expr: open type (nested unsolved variable)") {
		t.Fatalf("count([])'s open element type is not reported: %v", got)
	}
}

func hasElabCategory(vs []ElabViolation, n ast.Node, prefix string) bool {
	for _, v := range vs {
		if v.Node == n && strings.HasPrefix(v.Category, prefix) {
			return true
		}
	}
	return false
}

package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestImplBlockArgsAt_MatchesTheReceiver: the block's parameters are what
// its receiver, as the type builder resolved it, binds against a
// receiver's type, by name, whatever order the receiver names them in; a
// receiver of another type matches nothing.
func TestImplBlockArgsAt_MatchesTheReceiver(t *testing.T) {
	fa, nodes := checkNodes(t, `interface Show
    fn show(s: self) -> Int
end

struct Pair<A, B>
    a: A
    b: B
end

impl Show for Pair<B, A>
    fn show(_p: Pair<B, A>) -> Int
        1
    end
end

fn run() -> Int
    p = Pair{a: 1, b: "x"}
    Show.show(p)
end
`)
	var ib *ast.ImplBlock
	for _, n := range nodes {
		if b, ok := n.(*ast.ImplBlock); ok {
			ib = b
		}
	}
	var pair Type
	for _, ty := range fa.ExprTypes {
		if st, ok := ResolveClosed(ty).(*StructType); ok && st.Name == "Pair" && len(st.TypeArgs) == 2 && !ContainsTypeParam(st) {
			pair = st
		}
	}
	if ib == nil || pair == nil {
		t.Fatalf("no impl block (%v) or no Pair<Int, String> expression (%v)", ib, pair)
	}
	got, ok := ImplBlockArgsAt(fa, ib, pair)
	if !ok || len(got) != 2 || got["B"] == nil || got["B"].String() != "Int" || got["A"] == nil || got["A"].String() != "String" {
		t.Fatalf("Pair<Int, String> at `impl Show for Pair<B, A>`: want B Int and A String, got %v (ok %v)", got, ok)
	}
	if got, ok := ImplBlockArgsAt(fa, ib, TypeInt); ok {
		t.Fatalf("Int at `impl Show for Pair<B, A>`: want no match, got %v", got)
	}
}

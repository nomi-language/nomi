package irbuild

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// TestIRUntypedEmptyListDoesNotBindATypeParameter holds a property the
// per-call solving of a generic function's type arguments relies on
// (`unifyTypeParams`, used by stdinst.go and genericmono.go).
//
// `decode_list(items, 0, [], |item| T.from_json(item))` has two parameters whose
// annotations mention `T`, and `unifyTypeParams` is first-wins:
//
//	acc:         List<T>                        <- `[]`, an untyped empty list
//	decode_item: (Json) -> Result<T, ShapeError> <- a lambda
//
// `acc` is walked first. If the empty list bound `T` to its own sentinel, the
// instance would be built at the wrong argument. It does not, because
// `kindParts(kindEmptyList)` is empty and the `GenericType` arm finds an arity
// mismatch; nothing else guards it, which is why it is a test.
//
// Both directions are asserted, because the first alone would pass if the
// unifier stopped binding anything at all:
//
//   - the untyped accumulator binds nothing;
//   - `List<T>` against `List<Int>` binds `T` to Int.
func TestIRUntypedEmptyListDoesNotBindATypeParameter(t *testing.T) {
	g := &gen{}
	params := map[string]bool{"T": true}
	listOfT := &ast.GenericType{Name: "List", Params: []ast.TypeExpr{&ast.SimpleType{Name: "T"}}}

	fromEmpty := map[string]kind{}
	g.unifyTypeParams(listOfT, kindEmptyList, params, fromEmpty)
	if len(fromEmpty) != 0 {
		t.Fatalf("an untyped `[]` bound the type parameter: %v; `acc: List<T>` is unified before the lambda, so this would build the instance at the wrong argument", kindsOfMap(fromEmpty))
	}

	// The positive control, so a unifier that has stopped binding anything
	// cannot make the reading above look like a property.
	fromList := map[string]kind{}
	g.unifyTypeParams(listOfT, listKindIn(g, kindInt), params, fromList)
	if got, found := fromList["T"]; !found || got != kindInt {
		t.Fatalf("`List<T>` against `List<Int>` bound T to %v (found=%v), want Int", got.nomi(), found)
	}
}

// kindsOfMap renders a solved-parameter map for a failure message.
func kindsOfMap(m map[string]kind) string {
	var b strings.Builder
	for n, k := range m {
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(n + ":=" + k.nomi())
	}
	return b.String()
}

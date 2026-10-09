package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// `todo` lowers to one `ir.Todo`: a trap with a destination of the kind its
// position wants (internal/ir/todo.go says why it has one). The checker gives
// `todo` the bottom type, so the kind comes from where it sits:
//
//   - the caller's wanted kind, when the caller passes one (a function's
//     result, an annotated binding, a typed argument or field);
//   - otherwise the type the checker checked it against, or settled for it
//     once its arm or operand position decided one (`!todo` is Bool, `1 +
//     todo` Int; analysis's settleTodo), projected under the current
//     instantiation, so a `todo` in a generic body takes the instance's type;
//   - otherwise Infallible, the checker's own type for it: a `todo` in
//     statement position, whose value nothing reads, or an operand a consumer
//     retypes (irnever.go).
func (bl *irScalarBuilder) todo(t *ast.Todo, want kind) (ir.Temp, kind, bool, bool) {
	k := want
	// kindInvalid: sentinel — no wanted kind passed, not an operand's kind.
	if k == kindInvalid {
		k = bl.todoCheckedKind(t)
	}
	n := ir.NewTodo(bl.g.irNodePos(t), bl.f.NewTemp(), t.ReasonText())
	bl.b.Append(n)
	bl.g.irTypeTemp(bl.f, n.Dst(), k)
	return n.Dst(), k, false, true
}

// todoCheckedKind is the kind of the type the checker expected at t, or
// Infallible when it expected none this builder can represent.
func (bl *irScalarBuilder) todoCheckedKind(t *ast.Todo) kind {
	if bl.g.fa != nil {
		if ty, ok := bl.g.fa.ExpectedTypes[t]; ok {
			// kindInvalid: lookup — a type this builder cannot represent falls back to Infallible.
			if k := bl.g.project(ty); k != kindInvalid {
				return k
			}
		}
	}
	return kindNever
}

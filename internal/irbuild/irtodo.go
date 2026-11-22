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
//     instantiation, so a `todo` in a generic body takes the instance's type
//     (trapKind);
//   - otherwise Infallible, the checker's own type for it: a `todo` in
//     statement position, whose value nothing reads, or an operand a consumer
//     retypes (irnever.go).
func (bl *irScalarBuilder) todo(t *ast.Todo, want kind) (ir.Temp, kind, bool, bool) {
	k := bl.trapKind(t, want)
	n := ir.NewTodo(bl.g.irNodePos(t), bl.f.NewTemp(), t.ReasonText())
	bl.b.Append(n)
	bl.g.irTypeTemp(bl.f, n.Dst(), k)
	return n.Dst(), k, false, true
}

// unreachable lowers `unreachable "reason"` to one `ir.Unreachable`, a trap
// whose destination takes its kind exactly as a `todo`'s does.
func (bl *irScalarBuilder) unreachable(t *ast.Unreachable, want kind) (ir.Temp, kind, bool, bool) {
	k := bl.trapKind(t, want)
	n := ir.NewUnreachable(bl.g.irNodePos(t), bl.f.NewTemp(), t.ReasonText())
	bl.b.Append(n)
	bl.g.irTypeTemp(bl.f, n.Dst(), k)
	return n.Dst(), k, false, true
}

// trapKind is the kind t, a `todo` or an `unreachable`, is lowered at: want
// when the caller passes one, else the kind its position settled
// (settledKind), else Infallible.
func (bl *irScalarBuilder) trapKind(t ast.Node, want kind) kind {
	// kindInvalid: sentinel — no wanted kind passed, not an operand's kind.
	if want != kindInvalid {
		return want
	}
	// kindInvalid: lookup — a type this builder cannot represent leaves the trap Infallible.
	if k, settled := bl.g.settledKind(t); settled && k != kindInvalid {
		return k
	}
	return kindNever
}

// isTrapExpr reports whether n, through parentheses, is a `todo` or an
// `unreachable`: an expression that always traps.
func (g *gen) isTrapExpr(n ast.Node) bool {
	switch t := g.asTodo(n).(type) {
	case *ast.GroupedExpr:
		return g.isTrapExpr(t.Expr)
	case *ast.Todo, *ast.Unreachable:
		return true
	}
	return false
}

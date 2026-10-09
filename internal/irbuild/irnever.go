package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// `Infallible` is the type with no values. The checker gives it to an
// expression that never produces one (`todo`, a call to a function declared
// to return it, a block that only diverges) and lets it stand wherever any
// type is expected; the spec's use for it is the argument that rules a variant
// out (`Result<Int, Infallible>` is an `Ok`).
//
// Its kind is kindNever and its IR type `ir.NeverType`. Three rules make
// every type containing it lower, with no arm per shape:
//
//   - It is a leaf (irRetainedLeafKind), so it is admitted wherever a scalar
//     is: a parameter, a result, a binding, a field, a payload, a list
//     element, a prelude type argument, a map key. `Result<Int, Infallible>`
//     is an instance like `Result<Int, String>`, with its own identity, and
//     its Debug, equality and hash are the instance's, whose `Err` arm no
//     value reaches.
//   - Its register class is ir.ClassNone, so no register is spent on a
//     temporary nothing writes.
//   - A value of it stands where another kind is wanted. The value is never
//     produced, so the retyping copy after it (neverAs) never runs; the IR
//     accepts it because every type accepts Infallible (ir.ValType.Accepts).
//     A construct whose first-evaluated operand is one never runs either,
//     and lowers to that operand (neverOperand, neverCaseRegion).

// neverOperand lowers a construct that never runs because the operand it
// evaluates first is of Infallible: a call whose first argument is one
// (`Debug.inspect(e)` in the `Err(e)` arm of `Result<Int, Infallible>`'s
// derived Debug, `Some(todo)`), a unary or binary operator over one (`e ==
// f`), and a `case` on one (`case todo { ... }`). It lowers that operand and
// answers it retyped to the construct's checked type. Nothing after the
// operand runs, so nothing else is lowered: there is no impl to dispatch on a
// receiver no value has, and none is needed.
//
// The operand must be evaluated first for this to keep the program's order:
// a call's callee is a name (not a value an expression computes), and a
// binary operator's right operand counts only when the left one always runs
// before it and is not short-circuited (`and`, `or`). handled is false when n
// is none of these.
func (bl *irScalarBuilder) neverOperand(n ast.Node) (ir.Temp, kind, bool, bool, bool) {
	var lead []ast.Node
	var operand ast.Node
	switch t := n.(type) {
	case *ast.Call:
		if len(t.Args) == 0 || !bl.neverCalleeIsName(t.Func) {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		operand = t.Args[0]
	case *ast.Unary:
		operand = t.Right
	case *ast.Binary:
		switch {
		case bl.isNeverOperand(t.Left):
			operand = t.Left
		case t.Op != "and" && t.Op != "or" && t.Op != "|>" && bl.isNeverOperand(t.Right):
			lead, operand = []ast.Node{t.Left}, t.Right
		}
	case *ast.Case:
		operand = t.Value
	}
	if operand == nil || !bl.isNeverOperand(operand) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	for _, l := range lead {
		if _, _, _, ok := bl.lower(l); !ok {
			return ir.NoTemp, kindInvalid, false, false, true
		}
	}
	v, k, _, ok := bl.lowerNever(operand)
	if !ok || k != kindNever {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	// The construct never completes, so its value is of Infallible too: the
	// checker's type for it when it recorded one, which a consumer reads
	// directly, and otherwise Infallible, which the consumer retypes
	// (coerceEmpty) or reads as it is.
	// kindInvalid: lookup — no recorded type leaves the value Infallible.
	if want := bl.neverResultKind(n); want != kindInvalid && want != kindNever {
		if retyped, ok := bl.neverAs(n, v, k, want); ok {
			return retyped, want, false, true, true
		}
	}
	return v, k, false, true, true
}

// neverCaseRegion is caseRegion for `case s { ... }` where s is of
// Infallible: s never produces a value, so no arm runs. It lowers s and
// writes it, retyped, to the region's result, leaving the join open as
// caseRegion does.
func (bl *irScalarBuilder) neverCaseRegion(t *ast.Case, sig irFuncSig) (kind, bool) {
	want := sig.result
	if sig.inferResult {
		want = bl.neverResultKind(t)
	}
	// kindInvalid: reports — the decline note names the missing kind.
	if want == kindInvalid {
		irDeclineNote("a `case` on Infallible whose result has no kind")
		return kindInvalid, false
	}
	v, k, _, ok := bl.lowerNever(t.Value)
	if !ok || k != kindNever {
		return kindInvalid, false
	}
	if want != kindNever {
		if v, ok = bl.neverAs(t, v, k, want); !ok {
			return kindInvalid, false
		}
	}
	bl.resultCopy(t, v)
	return want, true
}

// isNeverOperand reports whether n is a `todo`, a call or a name of type
// Infallible: an operand that lowers to a value of kindNever.
func (bl *irScalarBuilder) isNeverOperand(n ast.Node) bool {
	if g, grouped := n.(*ast.GroupedExpr); grouped {
		return bl.isNeverOperand(g.Expr)
	}
	switch t := n.(type) {
	case *ast.Todo:
		return true
	case *ast.Ident:
		return bl.localKindOf(t.Name) == kindNever
	case *ast.Call:
		return bl.g.fa != nil && bl.g.project(bl.g.fa.ExprTypes[n]) == kindNever
	}
	return false
}

// localKindOf is the kind of the local name reads, or kindInvalid when name
// is not a local: bound in this body or an enclosing one, or a parameter.
func (bl *irScalarBuilder) localKindOf(name string) kind {
	for scope := bl; scope != nil; scope = scope.parent {
		if k, bound := scope.boundK[name]; bound {
			return k
		}
	}
	if l, bound := bl.g.lookup(name); bound {
		return l.k
	}
	return kindInvalid
}

// neverResultKind is the checker's type for n, or kindInvalid where it
// recorded none (a derived impl's body is not recorded).
func (bl *irScalarBuilder) neverResultKind(n ast.Node) kind {
	if bl.g.fa == nil {
		return kindInvalid
	}
	return bl.g.project(bl.g.fa.ExprTypes[n])
}

// lowerNever lowers an operand isNeverOperand accepted at kindNever.
func (bl *irScalarBuilder) lowerNever(n ast.Node) (ir.Temp, kind, bool, bool) {
	switch t := n.(type) {
	case *ast.GroupedExpr:
		return bl.lowerNever(t.Expr)
	case *ast.Todo:
		return bl.todo(t, kindNever)
	}
	return bl.lowerExact(n)
}

// neverCalleeIsName reports whether callee is a function's name, which
// evaluates nothing: `f`, `Type.method`, `module.f`.
func (bl *irScalarBuilder) neverCalleeIsName(callee ast.Node) bool {
	switch c := callee.(type) {
	case *ast.Ident:
		return !irQualIsLocal(bl, c.Name)
	case *ast.FieldAccess:
		switch o := c.Object.(type) {
		case *ast.TypeIdent:
			return true
		case *ast.Ident:
			return !irQualIsLocal(bl, o.Name)
		}
	}
	return false
}

// neverSettledKind is the kind the checker settled for n, an expression of
// type Infallible, from its position (analysis's settleTodo): Bool for `!n`,
// the other arm's type for an `if` arm. kindInvalid when it settled none, or
// one this builder cannot represent.
func (g *gen) neverSettledKind(n ast.Node) kind {
	if g.fa == nil {
		return kindInvalid
	}
	ty, ok := g.fa.ExpectedTypes[n]
	if !ok {
		return kindInvalid
	}
	return g.project(ty)
}

// neverAs retypes v, of kind have, to want when have is Infallible: `g(nev())`
// where g takes an Int, `!nev()`, `[todo, 1]`. ok is false for any other
// have, or a want with no IR type.
func (bl *irScalarBuilder) neverAs(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	if have != kindNever || want == kindNever || bl.g.irValType(want) == nil {
		return ir.NoTemp, false
	}
	n := ir.NewCopy(bl.g.irNodePos(at), bl.f.NewTemp(), v)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: want, copy: irCopyNever})
	return n.Dst(), true
}

package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// unitElselessSig is sig for t when t heads an `if` without `else`, which
// is Unit (analysis package doc, "If without else"): its arms are lowered
// against Unit, so an arm ending in an `assert` or a `dbg` drops its value
// as a Unit branch has none. Any other `if`, and one a test body's arms
// lower, keeps sig.
func unitElselessSig(t *ast.If, sig irFuncSig) irFuncSig {
	if sig.testArms || !(sig.inferResult || sig.result == kindUnit) {
		return sig
	}
	for l := t; ; {
		switch e := l.Else.(type) {
		case nil:
			sig.inferResult, sig.result, sig.elselessUnit = false, kindUnit, true
			return sig
		case *ast.If:
			l = e
		default:
			return sig
		}
	}
}

// droppedStatement reports whether body, an arm's value, is a statement
// whose value a block drops: an `assert` or `refute`, or a `dbg`.
func droppedStatement(body ast.Node) bool {
	switch b := body.(type) {
	case *ast.Assertion:
		return !b.Check
	case *ast.Dbg:
		return true
	}
	return false
}

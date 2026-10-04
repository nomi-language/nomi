package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// The self-position model: which argument positions hold the receiver, how
// many there are, and whether the RESULT is self-typed.
//
// # Why this is one type and one derivation
//
// "Argument zero is the receiver" is false in BOTH directions:
//
//   - ZERO self positions. `FromJson.from_json(json: Json): Result<self, …>`
//     declares self only in its RETURN. Probing argument zero there reads an
//     argument list that does not contain a receiver at all; the implementation
//     has to come from the type-parameter binding instead.
//   - MORE THAN ONE self position. `interface Cmp { fn cmp(a: self, b: self) }`
//     on two erased receivers: the implementation is selected from ONE
//     receiver and the table entry then asserts EVERY self position to that
//     same concrete type.
//   - A self position that is NOT index 0. The analyzer scans every parameter
//     for the one whose declared type IS `self` (`isSelfPositionParam`,
//     analysis/checker.go). Hardcoding argument 0 agrees with it only by
//     COINCIDENCE — every stdlib interface happens to declare self first — and
//     `interface Tagger { fn tag(label: String, target: self): String }` is the
//     shape that separates them.
//
// Each of those is two places computing "which argument is the receiver" and
// agreeing only by coincidence. So the answer is not a better rule, it is ONE
// derivation that every consumer reads.
//
// # What this is NOT the answer to
//
// A PROTOCOL CONFORMANCE question is a different question and must not be
// routed here. `iter.go` asks whether a declaration is shaped exactly like
// `Iter`'s `each_while(collection: self, yield: (T) -> Bool): Bool` — a fixed
// shape it then emits a fixed lowering for — and "self must be at index 0" is
// part of the SHAPE it requires, not an assumption about where receivers live.
// Collapsing the two would create a shared helper whose callers need different
// answers, which no test comparing them could ever catch. It stays separate,
// deliberately.
//
// # A composite CONTAINING self is not a self position
//
// `Struct.update(value: self, updates: Partial<self>)` has ONE self position,
// not two. The analyzer's `isSelfPositionParam` requires the resolved parameter
// type to BE the interface's `SelfParam`, which is index 0 here. The same rule
// holds on the
// result, but the result needs one more distinction than a parameter does —
// see selfResult.

// selfResult is what a method's declared return type says about self.
//
// Three values rather than a bool, because the middle one has a lowering of its
// own and reading it as `none` is what let `FromJson.from_json`'s shape look
// like an ordinary return. A bare `self` return erases to `any` in the table
// and is re-boxed by a dictionary-driven call; a return that merely CONTAINS
// self (`Result<self, DecodeError>`) cannot be erased that way, because the
// identity to re-box under sits inside a container this builder would have to
// rebuild. It is refused, and the point of the third value is that the refusal
// can name the right thing.
type selfResult uint8

const (
	// selfResultNone: the return type mentions no self.
	selfResultNone selfResult = iota
	// selfResultBare: the return type IS `self`.
	selfResultBare
	// selfResultNested: the return type CONTAINS self without being it.
	selfResultNested
)

// selfShape is one method's self-position model.
//
// Zero value is meaningful and is the shape of a method with no parameters and
// no return: `at` is read through recvAt, which reports -1 for an empty list,
// so no consumer needs to remember the sentinel.
type selfShape struct {
	// self is one entry per declared parameter: true where the parameter's
	// declared type IS `self`.
	self []bool
	// at is every self-typed position in ascending order. The RECEIVER is
	// at[0]; the rest are why gapMultiSelf exists.
	at []int
	// result says what the declared return type says about self.
	result selfResult
}

// selfShapeOf derives the model from a declared signature. This is the ONLY
// derivation; every consumer reads a selfShape rather than re-deriving one.
func selfShapeOf(params []ast.Param, ret ast.TypeExpr) selfShape {
	sh := selfShape{self: make([]bool, len(params))}
	for i := range params {
		if isSelfType(params[i].TypeAnnotation) {
			sh.self[i] = true
			sh.at = append(sh.at, i)
		}
	}
	switch {
	case ret == nil:
	case isSelfType(ret):
		sh.result = selfResultBare
	case typeExprHasSelf(ret):
		sh.result = selfResultNested
	}
	return sh
}

// selfTyped reports whether parameter i is a self position. Out-of-range is
// false rather than a panic: a short call is refused by its own arity check and
// this predicate is read while assembling the diagnostic.
func (s selfShape) selfTyped(i int) bool {
	return i >= 0 && i < len(s.self) && s.self[i]
}

// recvAt is the RECEIVER's parameter position, or -1 when the method declares
// none. The first self position and not argument zero — that difference is the
// whole reason this file exists.
func (s selfShape) recvAt() int {
	if len(s.at) == 0 {
		return -1
	}
	return s.at[0]
}

// multi reports the shape gapMultiSelf refuses: an implementation is selected
// from ONE receiver and every other self position would be asserted to that
// same concrete type with nothing making it true.
func (s selfShape) multi() bool { return len(s.at) > 1 }

// resultBareSelf reports a return type that IS `self`.
func (s selfShape) resultBareSelf() bool { return s.result == selfResultBare }

// resultNestedSelf reports a return type that CONTAINS self without being it.
func (s selfShape) resultNestedSelf() bool { return s.result == selfResultNested }

// typeExprHasSelf reports whether te mentions `self` anywhere inside it.
//
// EXHAUSTIVE over ast.TypeExpr's seven implementors, with no default arm, and
// that is deliberate: a walk that skips a node kind looks finished from every
// angle except the one it skips. `typeExprKindsAreCovered` fails if ast grows
// an eighth.
func typeExprHasSelf(te ast.TypeExpr) bool {
	switch t := te.(type) {
	case nil:
		return false
	case *ast.SelfType:
		return true
	case *ast.SimpleType:
		// A bare identifier, never `self`: the lexer gives `self` its own
		// token, so no SimpleType can carry that name.
		// TestSelfShape_SelfIsAlwaysItsOwnNode parses the spelling.
		return false
	case *ast.DotVariantType:
		return false
	case *ast.QualifiedType:
		return typeExprHasSelf(t.Member)
	case *ast.GenericType:
		for _, p := range t.Params {
			if typeExprHasSelf(p) {
				return true
			}
		}
		return false
	case *ast.FuncType:
		for _, p := range t.Params {
			if typeExprHasSelf(p) {
				return true
			}
		}
		return typeExprHasSelf(t.Return)
	case *ast.AnonStructType:
		for i := range t.Fields {
			if typeExprHasSelf(t.Fields[i].TypeAnnotation) {
				return true
			}
		}
		return false
	}
	// Unreachable while ast.TypeExpr has exactly the seven implementors
	// typeExprKindsAreCovered enumerates. Reached means ast has an eighth that
	// the guard missed: answer TRUE, because "might contain self" routes
	// the signature into a refusal and the alternative is a silent wrong
	// answer.
	return true
}

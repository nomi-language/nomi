package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// An embedded type widens into each enum that embeds it: a Circle is a Shape
// wherever a Shape is expected. The other direction is never implicit. A
// Shape may hold a variant other than Circle, so a Shape is a Circle only
// inside a match arm that says so (the narrowing of a name matched against
// `.Circle{}`). Every position that knows which way a value flows (an
// argument, an annotated binding, a field, a return, a list element, an impl
// function's signature against its interface's) compares with TypeAssignable
// or UnifyInto, which refuse the downcast, and its diagnostic carries
// embedsDowncastHint.

// embedsDowncast finds where have, flowing into want, would turn an enum into
// a type it embeds: at the top, or in a type argument, tuple element or
// record field where both sides have the same shape. It answers the enum and
// the embedded type.
func embedsDowncast(want, have Type) (*EnumType, Type, bool) {
	return embedsDowncastIn(want, have, 0)
}

func embedsDowncastIn(want, have Type, depth int) (*EnumType, Type, bool) {
	if want == nil || have == nil || isNilType(want) || isNilType(have) || depth > maxTypeRenderDepth {
		return nil, nil, false
	}
	depth++
	want, have = resolveTV(want), resolveTV(have)
	if et, ok := have.(*EnumType); ok && isEmbeddedTypeOf(want, et) {
		return et, want, true
	}
	pairs := func(ws, hs []Type) (*EnumType, Type, bool) {
		if len(ws) != len(hs) {
			return nil, nil, false
		}
		for i := range ws {
			if et, emb, ok := embedsDowncastIn(ws[i], hs[i], depth); ok {
				return et, emb, true
			}
		}
		return nil, nil, false
	}
	switch w := want.(type) {
	case *ListType:
		if h, ok := have.(*ListType); ok {
			return embedsDowncastIn(w.Elem, h.Elem, depth)
		}
	case *MapType:
		if h, ok := have.(*MapType); ok {
			return pairs([]Type{w.Key, w.Val}, []Type{h.Key, h.Val})
		}
	case *TupleType:
		if h, ok := have.(*TupleType); ok {
			return pairs(w.Elems, h.Elems)
		}
	case *StructType:
		if h, ok := have.(*StructType); ok && sameNominalIdentity(w.Origin, w.Name, h.Origin, h.Name) {
			return pairs(w.TypeArgs, h.TypeArgs)
		}
	case *EnumType:
		if h, ok := have.(*EnumType); ok && sameNominalIdentity(w.Origin, w.Name, h.Origin, h.Name) {
			return pairs(w.TypeArgs, h.TypeArgs)
		}
	case *DistinctType:
		if h, ok := have.(*DistinctType); ok && sameNominalIdentity(w.Origin, w.Name, h.Origin, h.Name) {
			return pairs(w.TypeArgs, h.TypeArgs)
		}
	case *InterfaceType:
		if h, ok := have.(*InterfaceType); ok && sameNominalIdentity(w.Origin, w.Name, h.Origin, h.Name) {
			return pairs(w.TypeArgs, h.TypeArgs)
		}
	case *AnonStructType:
		if h, ok := have.(*AnonStructType); ok && len(w.Fields) == len(h.Fields) {
			for _, f := range w.Fields {
				hf, found := scopedField(h, f.Name)
				if !found {
					return nil, nil, false
				}
				if et, emb, ok := embedsDowncastIn(f.Type, hf.Type, depth); ok {
					return et, emb, true
				}
			}
		}
	case *FuncType:
		// A function value flows the other way through its parameters.
		if h, ok := have.(*FuncType); ok && len(w.Params) == len(h.Params) {
			for i := range w.Params {
				if et, emb, ok := embedsDowncastIn(h.Params[i], w.Params[i], depth); ok {
					return et, emb, true
				}
			}
			return embedsDowncastIn(normalizeReturn(w.Return), normalizeReturn(h.Return), depth)
		}
	}
	return nil, nil, false
}

// embedsDowncastHint is the hint for a mismatch where have, flowing into
// want, would turn an enum into a type it embeds, and "" for any other
// mismatch.
func embedsDowncastHint(want, have Type) string {
	et, emb, ok := embedsDowncast(want, have)
	if !ok {
		return ""
	}
	name := embeddableTypeName(emb)
	pattern := "." + name + "{}"
	if _, distinct := emb.(*DistinctType); distinct {
		pattern = "." + name + "(_)"
	}
	return fmt.Sprintf("%s may hold a variant other than %s, and only a match narrows it: `case value { %s -> ... }`",
		et.Name, name, pattern)
}

// callbackDowncast is the error for a function value passed to a generic
// callee whose parameter is a function over an enum, when the value takes
// one of that enum's embedded types instead: `Iter.map(shapes, take)` with
// `take: (Circle) -> Int` and `shapes: List<Shape>`. A function value takes
// its parameters the other way from how it is passed, so `take` would be
// called with a Shape that may be a Dot. The failed unification leaves the
// callee's other type arguments (map's U) unsolved, which alone read as "U
// is not determined"; this names the parameter instead. want is the callee's
// parameter with the type arguments solved so far, have the argument's type.
func (c *checker) callbackDowncast(callee, arg ast.Node, want, have Type, label string, line, col int) (TypeError, bool) {
	wf, ok := resolveTV(want).(*FuncType)
	if !ok {
		return TypeError{}, false
	}
	hf, ok := resolveTV(have).(*FuncType)
	if !ok || len(wf.Params) != len(hf.Params) {
		return TypeError{}, false
	}
	for i := range wf.Params {
		passed := resolveTV(wf.Params[i])
		if passed == nil || containsTypeVar(passed) || ContainsTypeParam(passed) {
			continue
		}
		et, emb, ok := embedsDowncast(hf.Params[i], passed)
		if !ok {
			continue
		}
		fn := ""
		switch arg.(type) {
		case *ast.Ident, *ast.FieldAccess:
			fn = calleeText(arg)
		}
		subject := "the function"
		if _, lambda := arg.(*ast.Lambda); lambda {
			subject = "the lambda"
		}
		if fn != "" && fn != "?" {
			subject = "`" + fn + "`"
		} else {
			fn = ""
		}
		by := "the call"
		if callee != nil {
			if name := calleeText(callee); name != "?" {
				by = "`" + name + "`"
			}
		}
		takes, gets := c.typef("%s", hf.Params[i]), c.typef("%s", passed)
		name := embeddableTypeName(emb)
		msg := fmt.Sprintf("%s: %s takes %s %s, but %s calls it with %s %s, and %s %s may hold a variant other than %s",
			label, subject, articleFor(takes), takes, by, articleFor(gets), gets, articleFor(et.Name), et.Name, name)
		hint := embedsDowncastHint(hf.Params[i], passed)
		if _, top := passed.(*EnumType); top && len(wf.Params) == 1 {
			pattern := "." + name + "{}"
			if _, distinct := emb.(*DistinctType); distinct {
				pattern = "." + name + "(_)"
			}
			body := "..."
			if fn != "" {
				body = fn + "(value) ..."
			}
			hint = fmt.Sprintf("pass a function that takes %s %s and matches it first: `|value| case value { %s -> %s }`",
				articleFor(et.Name), et.Name, pattern, body)
		}
		return TypeError{Line: line, Col: col, Message: msg}.Spanning(arg).WithHint(hint), true
	}
	return TypeError{}, false
}

// addMismatch reports msg, a value of type have refused where want is
// expected, at line:col, with embedsDowncastHint.
func (c *checker) addMismatch(line, col int, want, have Type, msg string) {
	c.report(TypeError{Line: line, Col: col, Message: msg}.WithHint(embedsDowncastHint(want, have)))
}

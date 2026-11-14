package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Literals and patterns that name a type built on a built-in shape.
//
// A type built on a shape is named in front of the shape's literal:
// `Path[1, 2]` is a distinct type over a vector, `.Items[1]` a variant whose
// payload is one, `Tags["a"]` a distinct type over a set, `Pair(1, "x")` a
// distinct type over a tuple. The struct and map forms (`Point{...}`,
// `Kvs{...}`) are checkStructLit's and checkMapLit's. Which collection a
// bracket literal's name is built on comes from its declaration
// (bracket_literals.go); this file resolves the name and checks the
// elements against what the shape holds there.

// shapeTarget is what a literal naming a type built on its shape builds: a
// variant of enum, whose payload is inner, or the distinct type over inner.
type shapeTarget struct {
	name     string
	enum     *EnumType
	subs     map[*TypeParam_]Type
	canInfer bool
	distinct *DistinctType
	inner    Type
}

// resolveShapeTarget resolves te, the name after a literal's symbol, to a
// variant whose payload has the shape or a distinct type over the shape.
// spelled is the literal as its head reads (`Path[...]`), shape names the
// shape in messages, form is the type the shape is written as (`Vector<T>`)
// and fits reports whether a type has the shape. variants says whether a
// variant may be named: a tuple-payload variant is a call, `.V((a, b))`.
// ok is false after an error is reported.
func (c *checker) resolveShapeTarget(te ast.TypeExpr, line, col int, spelled, shape, form string, fits func(Type) bool, variants bool) (shapeTarget, bool) {
	name := te.TypeString()
	if variants {
		et, payload, _, posOK := c.resolveVariantPrefixInValuePos(te, line, col)
		if !posOK {
			return shapeTarget{}, false
		}
		if et != nil {
			if !fits(payload) {
				c.addError(line, col, fmt.Sprintf(
					"variant %s does not take a %s payload; `%s` requires a variant whose payload is `%s`",
					name, shape, spelled, form))
				return shapeTarget{}, false
			}
			et = enumAsDeclared(et)
			subs, canInfer := c.seedVariantTypeArgs(et)
			return shapeTarget{name: name, enum: et, subs: subs, canInfer: canInfer, inner: payload}, true
		}
	} else if dvt, ok := te.(*ast.DotVariantType); ok {
		c.addError(line, col, fmt.Sprintf(
			"a variant takes its tuple as one value: write `.%s((...))`", dvt.Name))
		return shapeTarget{}, false
	}
	named := c.reg.Lookup(name)
	if named == nil {
		c.report(TypeError{Line: line, Col: col, Message: fmt.Sprintf("undefined type %s", name)}.WithHint(didYouMean(name, c.typeNamesAt(line, col))))
		return shapeTarget{}, false
	}
	dt, ok := named.(*DistinctType)
	if !ok || !fits(dt.Inner) {
		what := ""
		if ok && dt.Inner != nil {
			what = fmt.Sprintf(" (it wraps %s)", dt.Inner)
		}
		c.addError(line, col, fmt.Sprintf(
			"%s is not a type over a %s%s; `%s` requires `type %s %s`",
			name, shape, what, spelled, name, form))
		return shapeTarget{}, false
	}
	if dt.Opaque && (c.fa == nil || !c.fa.inside(dt.OwningSourceFile)) {
		c.addError(line, col, fmt.Sprintf(
			"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", dt.Name))
		return shapeTarget{}, false
	}
	dt = c.freshDistinct(dt)
	return shapeTarget{name: name, distinct: dt, inner: distinctInnerOf(dt)}, true
}

// result is the type a literal builds once its elements are checked.
func (c *checker) shapeTargetResult(t shapeTarget) Type {
	if t.enum != nil {
		return c.instantiateVariantLit(t.enum, t.subs)
	}
	return t.distinct
}

// checkShapeElem checks one element of a named literal against elem, the
// type the shape holds there.
func (c *checker) checkShapeElem(t shapeTarget, item ast.Node, elem Type, what string) {
	if t.enum != nil {
		c.checkVariantPayloadValue(item, elem, t.subs, t.canInfer, what)
		return
	}
	ty := c.checkNodeExpecting(item, elem)
	if ty == nil {
		return
	}
	line, col := nodeLineCol(item)
	if !c.argMatchesParam(ty, elem, c.recPos(line, col), RecordingKindCallSite) {
		c.addMismatch(line, col, elem, ty, c.typef("%s: expected %s, got %s", what, elem, ty))
	}
}

func isVectorType(t Type) bool { _, ok := vectorElemType(t); return ok }
func isSetType(t Type) bool    { _, ok := setElemType(t); return ok }
func isTupleType(t Type) bool  { _, ok := resolveTypeVar(t).(*TupleType); return ok }

// checkNamedVectorLit types `Path[1, 2]` and `.Items[1, 2]`.
func (c *checker) checkNamedVectorLit(n *ast.ListLit) Type {
	spelled := n.TypeName.TypeString() + "[...]"
	t, ok := c.resolveShapeTarget(n.TypeName, n.Line, n.Col, spelled, "vector", "Vector<T>", isVectorType, true)
	if !ok {
		for _, item := range n.Items {
			c.checkNode(item)
		}
		return nil
	}
	elem, _ := vectorElemType(t.inner)
	for _, item := range n.Items {
		c.checkShapeElem(t, item, elem, t.name+" element type")
	}
	return c.shapeTargetResult(t)
}

// checkNamedSetLit types `Tags["a", "b"]`.
func (c *checker) checkNamedSetLit(n *ast.ListLit) Type {
	spelled := n.TypeName.TypeString() + "[...]"
	t, ok := c.resolveShapeTarget(n.TypeName, n.Line, n.Col, spelled, "set", "Set<T>", isSetType, true)
	if !ok {
		for _, item := range n.Items {
			c.checkNode(item)
		}
		return nil
	}
	elem, _ := setElemType(t.inner)
	for _, item := range n.Items {
		c.checkShapeElem(t, item, elem, t.name+" element type")
	}
	return c.shapeTargetResult(t)
}

// listPatternKind is the collection a bracket pattern takes apart: the
// one its subject is, through a distinct type over it or, for a pattern
// naming a variant (`.Items[x]`), that variant's payload. A List where the
// subject says nothing, whose check reports why.
func (c *checker) listPatternKind(n *ast.ListPattern, subject Type) ast.SeqKind {
	t := resolveTypeVar(subject)
	switch {
	case isVectorType(t):
		return ast.SeqVector
	case isSetType(t):
		return ast.SeqSet
	}
	switch t := t.(type) {
	case *EnumType:
		if n.TypeName != nil {
			mark, undeterminedMark := len(c.errors), len(c.undetermined)
			_, payload, _ := c.resolveVariantPrefixOrDot(n.TypeName, t)
			c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
			return seqKindOf(substituteScrutineeArgs(payload, t))
		}
	case *DistinctType:
		return seqKindOf(distinctInnerOf(t))
	}
	return ast.SeqList
}

// shapePatternTarget is the type a vector or set pattern takes apart at
// expectedTy: the vector or set itself, the one a distinct type over it
// wraps, or a variant's payload when the pattern names the variant. shape
// is "vector" or "set", and fits reports a type of the shape. It is nil
// after an error is reported.
func (c *checker) shapePatternTarget(typeName ast.TypeExpr, line, col int, expectedTy Type, shape string, fits func(Type) bool) Type {
	if fits(expectedTy) && typeName == nil {
		return expectedTy
	}
	if et, ok := expectedTy.(*EnumType); ok && typeName != nil {
		pet, payload, vname := c.resolveVariantPrefixOrDot(typeName, et)
		if pet == nil || vname == "" {
			c.addError(line, col, fmt.Sprintf(
				"%s pattern `%s[...]` names no variant of %s",
				shape, typeName.TypeString(), et.Name))
			return nil
		}
		if st, ok := typeName.(*ast.SimpleType); ok && !c.allowsBareVariantPattern(st.Name, Pos{Line: st.Line, Col: st.Col}) {
			c.addError(st.Line, st.Col, bareVariantPatternMessage(st.Name, et.Name))
		}
		if pet.Name != et.Name {
			c.addError(line, col, fmt.Sprintf("variant %s belongs to %s, not scrutinee type %s", vname, pet.Name, et.Name))
		}
		payload = substituteScrutineeArgs(payload, et)
		if !fits(payload) {
			c.addError(line, col, fmt.Sprintf(
				"variant %s does not take a %s payload; a `%s[...]` pattern requires a variant whose payload is one",
				vname, shape, typeName.TypeString()))
			return nil
		}
		return payload
	}
	if dt, ok := expectedTy.(*DistinctType); ok && !fits(dt) && fits(dt.Inner) {
		if typeName != nil {
			c.patternNamesDistinct(typeName, dt, line, col)
		}
		if dt.Opaque && (c.fa == nil || !c.fa.inside(dt.OwningSourceFile)) {
			c.addError(line, col, fmt.Sprintf(
				"cannot destructure opaque type '%s' outside its defining module", dt.Name))
			return nil
		}
		return distinctInnerOf(dt)
	}
	if typeName != nil {
		c.addError(line, col, fmt.Sprintf(
			"%s pattern `%s[...]` requires a type over a %s, got %s",
			shape, typeName.TypeString(), shape, expectedTy))
		return nil
	}
	c.addError(line, col, fmt.Sprintf("%s pattern requires a %s, got %s", shape, shape, expectedTy))
	return nil
}

// vectorRestMessage is the error at a `..` of a vector or set pattern that
// a pattern other than a name follows.
const vectorRestMessage = "the rest of a %s pattern is a name or nothing: `..rest` or `..`"

// checkSpreadRest reports a rest after the `..` of a vector or set pattern
// that is not a name or `_`.
func (c *checker) checkSpreadRest(n *ast.ListPattern, shape string) {
	switch n.TailSpread.(type) {
	case nil, *ast.IdentPattern, *ast.WildcardPattern:
		return
	}
	c.addError(n.SpreadLine, n.SpreadCol, fmt.Sprintf(vectorRestMessage, shape))
}

// checkVectorPattern checks `[first, ..rest]`, `[.., last]` and the rest
// over a vector: each element pattern against the element type, and the
// rest, a name or `_`, against the vector type, since it binds a vector of
// the elements in between.
func (c *checker) checkVectorPattern(n *ast.ListPattern, expectedTy Type) {
	c.checkSpreadRest(n, "vector")
	check := func(elem, vec Type) {
		for _, h := range n.Heads {
			c.checkPattern(h, elem)
		}
		if rest := n.Rest(); rest != nil {
			c.checkPattern(rest, vec)
		}
		for _, t := range n.Tails {
			c.checkPattern(t, elem)
		}
	}
	if expectedTy == nil {
		check(nil, nil)
		return
	}
	vec := c.shapePatternTarget(n.TypeName, n.Line, n.Col, expectedTy, "vector", isVectorType)
	if vec == nil {
		check(nil, nil)
		return
	}
	elem, _ := vectorElemType(vec)
	check(elem, vec)
}

// setPatternElementMessage is the error for something written in a set
// pattern's element place that is not a constant: an element is a value
// the set must contain.
const setPatternElementMessage = "an element of a set pattern is a constant the set contains, not a place to bind: %s"

// setPatternElement reports whether e may be an element of a set pattern:
// a number, string or codepoint literal, which is the value to look up.
func setPatternElement(e ast.Node) bool {
	switch e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.DecimalLit, *ast.StringLit, *ast.CodepointLit:
		return true
	}
	return false
}

// checkSetPattern checks `["admin", ..rest]` over a set: each element is a
// constant the set must contain, checked against the element type, and the
// rest, a name or `_` after a last `..`, binds the set of the other
// elements.
func (c *checker) checkSetPattern(n *ast.ListPattern, expectedTy Type) {
	c.checkSpreadRest(n, "set")
	if len(n.Tails) > 0 {
		c.addError(n.SpreadLine, n.SpreadCol, "set pattern `..` must be the last element")
	}
	var set, elem Type
	if expectedTy != nil {
		set = c.shapePatternTarget(n.TypeName, n.Line, n.Col, expectedTy, "set", isSetType)
		elem, _ = setElemType(set)
	}
	for _, e := range n.Heads {
		switch v := e.(type) {
		case *ast.WildcardPattern:
			c.addError(v.Line, v.Col, fmt.Sprintf(setPatternElementMessage, "`_` names no value; `..` matches the other elements"))
			continue
		case *ast.IdentPattern:
			c.addError(v.Line, v.Col, fmt.Sprintf(setPatternElementMessage, fmt.Sprintf("`%s` would bind a name; `..%s` binds the other elements, and a guard tests a value in scope (`when Set.contains(s, %s)`)", v.Name, v.Name, v.Name)))
			continue
		}
		if !setPatternElement(e) {
			line, col := nodeLineCol(e)
			c.addError(line, col, fmt.Sprintf(setPatternElementMessage, "write a number, string or codepoint"))
			continue
		}
		if elem == nil {
			c.checkNode(e)
			continue
		}
		ty := c.checkNodeExpecting(e, elem)
		if ty != nil && !c.litOpenElemFits(elem, ty) {
			c.report(errAt(e, c.typef("set pattern element type mismatch: expected %s, got %s", elem, ty)).WithHint(c.mismatchHint(elem, ty)))
		}
	}
	if rest := n.Rest(); rest != nil {
		c.checkPattern(rest, set)
	}
}

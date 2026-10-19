package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Literals and patterns that name a type built on a built-in shape.
//
// Every literal of a built-in shape starts with that shape's symbol, and a
// type built on the shape writes its name right after it: `#Path[1, 2]` is a
// distinct type over a vector, `#.Items[1]` a variant whose payload is one,
// `%Tags["a"]` a distinct type over a set, `#Pair(1, "x")` a distinct type
// over a tuple. The struct and map forms (`#Point{...}`, `%Kvs{...}`) are
// checkStructLit's and checkMapLit's. The parser knows the shape from the
// token after the name; this file resolves the name and checks the elements
// against what the shape holds there.

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
// spelled is the literal as its head reads (`#Path[...]`), shape names the
// shape in messages, form is the type the shape is written as (`Vector<T>`)
// and fits reports whether a type has the shape. variants says whether a
// variant may be named: a tuple-payload variant is a call, `.V(#(a, b))`.
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
			"a variant takes its tuple as one value: write `.%s(#(...))`", dvt.Name))
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
	if dt.Opaque && (c.fa == nil || c.fa.FilePath != dt.OwningSourceFile) {
		c.addError(line, col, fmt.Sprintf(
			"constructor of opaque type '%s' is private to its defining module — use an exported constructor function", dt.Name))
		return shapeTarget{}, false
	}
	return shapeTarget{name: name, distinct: dt, inner: dt.Inner}, true
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

// checkNamedVectorLit types `#Path[1, 2]` and `#.Items[1, 2]`.
func (c *checker) checkNamedVectorLit(n *ast.VectorLit) Type {
	spelled := "#" + n.TypeName.TypeString() + "[...]"
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

// checkNamedSetLit types `%Tags["a", "b"]`.
func (c *checker) checkNamedSetLit(n *ast.SetLit) Type {
	spelled := "%" + n.TypeName.TypeString() + "[...]"
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

// checkNamedTupleLit types `#Pair(1, "x")`: a distinct type over a tuple,
// built from the tuple's elements.
func (c *checker) checkNamedTupleLit(n *ast.TupleLit) Type {
	spelled := "#" + n.TypeName.TypeString() + "(...)"
	t, ok := c.resolveShapeTarget(n.TypeName, n.Line, n.Col, spelled, "tuple", "#(...)", isTupleType, false)
	if !ok {
		for _, item := range n.Items {
			c.checkNode(item)
		}
		return nil
	}
	tt := resolveTypeVar(t.inner).(*TupleType)
	if len(tt.Elems) != len(n.Items) {
		for _, item := range n.Items {
			c.checkNode(item)
		}
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"%s wraps %s, a tuple of %d elements; `#%s(...)` has %d",
			t.name, tt, len(tt.Elems), t.name, len(n.Items)))
		return t.distinct
	}
	for i, item := range n.Items {
		c.checkShapeElem(t, item, tt.Elems[i], fmt.Sprintf("element %d of %s", i+1, t.name))
	}
	return t.distinct
}

// checkNamedTuplePattern checks `#Pair(a, b)`, which takes a distinct type
// over a tuple apart along with its tuple.
func (c *checker) checkNamedTuplePattern(n *ast.TuplePattern, expectedTy Type) {
	untyped := func() {
		for _, sub := range n.Patterns {
			c.checkPattern(sub, nil)
		}
	}
	if expectedTy == nil {
		untyped()
		return
	}
	name := n.TypeName.TypeString()
	dt, ok := expectedTy.(*DistinctType)
	if !ok {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"tuple pattern `#%s(...)` takes apart a distinct type over a tuple, but the value is %s %s",
			name, articleFor(expectedTy.String()), expectedTy))
		untyped()
		return
	}
	if !distinctTypeNameMatches(name, dt.Name) {
		c.addError(n.Line, n.Col, fmt.Sprintf("distinct type pattern %s does not match type %s", name, dt.Name))
		untyped()
		return
	}
	if dt.Opaque && (c.fa == nil || c.fa.FilePath != dt.OwningSourceFile) {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"cannot destructure opaque type '%s' outside its defining module", dt.Name))
		untyped()
		return
	}
	tt, ok := resolveTypeVar(dt.Inner).(*TupleType)
	if !ok {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"%s wraps %s, not a tuple; take it apart with `%s(x)`", dt.Name, dt.Inner, name))
		untyped()
		return
	}
	if len(n.Patterns) != len(tt.Elems) {
		c.addError(n.Line, n.Col, fmt.Sprintf("tuple pattern has %d elements but expected %d", len(n.Patterns), len(tt.Elems)))
		untyped()
		return
	}
	for i, sub := range n.Patterns {
		c.checkPattern(sub, tt.Elems[i])
	}
}

// shapePatternTarget is the type a vector or set pattern takes apart at
// expectedTy: the vector or set itself, the one a distinct type over it
// wraps, or a variant's payload when the pattern names the variant. shape
// is "vector" or "set", sigil its symbol, and fits reports a type of the
// shape. It is nil after an error is reported.
func (c *checker) shapePatternTarget(typeName ast.TypeExpr, line, col int, expectedTy Type, shape, sigil string, fits func(Type) bool) Type {
	if fits(expectedTy) && typeName == nil {
		return expectedTy
	}
	if et, ok := expectedTy.(*EnumType); ok && typeName != nil {
		pet, payload, vname := c.resolveVariantPrefixOrDot(typeName, et)
		if pet == nil || vname == "" {
			c.addError(line, col, fmt.Sprintf(
				"%s pattern `%s%s[...]` names no variant of %s",
				shape, sigil, typeName.TypeString(), et.Name))
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
				"variant %s does not take a %s payload; a `%s%s[...]` pattern requires a variant whose payload is one",
				vname, shape, sigil, typeName.TypeString()))
			return nil
		}
		return payload
	}
	if dt, ok := expectedTy.(*DistinctType); ok && !fits(dt) && fits(dt.Inner) {
		if typeName != nil && !distinctTypeNameMatches(typeName.TypeString(), dt.Name) {
			c.addError(line, col, fmt.Sprintf(
				"%s pattern prefix %s does not match scrutinee type %s", shape, typeName.TypeString(), dt.Name))
		}
		if dt.Opaque && (c.fa == nil || c.fa.FilePath != dt.OwningSourceFile) {
			c.addError(line, col, fmt.Sprintf(
				"cannot destructure opaque type '%s' outside its defining module", dt.Name))
			return nil
		}
		return dt.Inner
	}
	if typeName != nil {
		c.addError(line, col, fmt.Sprintf(
			"%s pattern `%s%s[...]` requires a type over a %s, got %s",
			shape, sigil, typeName.TypeString(), shape, expectedTy))
		return nil
	}
	c.addError(line, col, fmt.Sprintf("%s pattern requires a %s, got %s", shape, shape, expectedTy))
	return nil
}

// checkVectorPattern checks `#[first, ..rest]`, `#[.., last]` and the rest:
// each element pattern against the element type, and the rest, a name or
// `_`, against the vector type, since it binds a vector of the elements in
// between.
func (c *checker) checkVectorPattern(n *ast.VectorPattern, expectedTy Type) {
	check := func(elem, vec Type) {
		for _, h := range n.Heads {
			c.checkPattern(h, elem)
		}
		if n.Rest != nil {
			c.checkPattern(n.Rest, vec)
		}
		for _, t := range n.Tails {
			c.checkPattern(t, elem)
		}
	}
	if expectedTy == nil {
		check(nil, nil)
		return
	}
	vec := c.shapePatternTarget(n.TypeName, n.Line, n.Col, expectedTy, "vector", "#", isVectorType)
	if vec == nil {
		check(nil, nil)
		return
	}
	elem, _ := vectorElemType(vec)
	check(elem, vec)
}

// setPatternElementMessage is the error for something written in a set
// pattern's element place that would bind a name: an element is a value the
// set must contain, looked up as a map pattern's key is.
const setPatternElementMessage = "an element of a set pattern is a value to look up, not a place to bind: %s"

// checkSetPattern checks `%["admin", ..rest]`: each element is an
// expression whose value the set must contain, checked against the element
// type, and the rest, a name or `_`, binds the set of the other elements.
func (c *checker) checkSetPattern(n *ast.SetPattern, expectedTy Type) {
	var set, elem Type
	if expectedTy != nil {
		set = c.shapePatternTarget(n.TypeName, n.Line, n.Col, expectedTy, "set", "%", isSetType)
		elem, _ = setElemType(set)
	}
	for _, e := range n.Elems {
		switch v := e.(type) {
		case *ast.Placeholder:
			c.addError(v.Line, v.Col, fmt.Sprintf(setPatternElementMessage, "`_` names no value; `..` matches the other elements"))
			continue
		case *ast.Ident:
			if c.fa.References[Pos{Line: v.Line, Col: v.Col}] == nil {
				c.addError(v.Line, v.Col, fmt.Sprintf(setPatternElementMessage, fmt.Sprintf("`%s` is not in scope, and `..%s` binds the other elements", v.Name, v.Name)))
				continue
			}
		}
		if elem == nil {
			c.checkNode(e)
			continue
		}
		ty := c.checkNodeExpecting(e, elem)
		if ty != nil && !c.litOpenElemFits(elem, ty) {
			c.report(errAt(e, c.typef("set pattern element type mismatch: expected %s, got %s", elem, ty)).WithHint(mismatchHint(elem, ty)))
		}
	}
	if n.Rest != nil {
		c.checkPattern(n.Rest, set)
	}
}

// LiteralSigil is the symbol a literal headed by the name of t starts with:
// '#' for a struct, an enum (whose variants a literal names), and a
// distinct type over a tuple or vector; '%' for a distinct type over a map
// or set; 0 for a type whose name heads no shape literal.
func LiteralSigil(t Type) byte {
	switch t := resolveTypeVar(t).(type) {
	case *StructType, *EnumType:
		return '#'
	case *DistinctType:
		inner := resolveTypeVar(t.Inner)
		switch {
		case isTupleType(inner), isVectorType(inner):
			return '#'
		case isSetType(inner):
			return '%'
		}
		if _, ok := inner.(*MapType); ok {
			return '%'
		}
	}
	return 0
}

package analysis

import "github.com/nomi-language/nomi/internal/ast"

// DisplayForm is how an interpolated value renders (DisplayRender).
type DisplayForm uint8

const (
	// DisplayText is a String, which is its own text.
	DisplayText DisplayForm = iota + 1
	// DisplayScalar is an Int, a Float, a Bool or a Decimal, which the
	// machine renders in place; std's impl for each is that rendering.
	DisplayScalar
	// DisplayStructural is a value no part of which has an impl of its
	// own: a tuple, an anonymous struct, a List, a Map or a Vector of Int,
	// Float, Bool and String at any depth, or a collection whose element
	// type is a closed variable (an empty one). The machine's structural
	// text is its Display text.
	DisplayStructural
	// DisplayParts is a tuple or an anonymous struct with a part that is
	// not structural: each part renders by its own record (Parts), and
	// the texts are laid out as the tuple or struct is.
	DisplayParts
	// DisplayImpl is a value of a declared type, or of a std container
	// with a part that is not structural: it renders through the
	// type's `impl Display` (Method), written, derived or std's.
	DisplayImpl
	// DisplayInterface is an interface value: it renders through the impl
	// of the value it holds, which the running program finds.
	DisplayInterface
	// DisplayTypeParam is a value of a type parameter of the enclosing
	// declaration, which its `Display` bound renders, or the `self` of an
	// interface default, which its `where self: Display` bound renders.
	// The rendering is its instance's: the IR builder resolves it at each
	// instance.
	DisplayTypeParam
)

// DisplayRender is how one interpolated value renders by Display
// (FileAnalysis.HoleDisplays): the checker's decision, which the IR builder
// lowers.
type DisplayRender struct {
	Form DisplayForm
	// Type is the value's type, with every closed variable its closed type.
	Type Type
	// Method is, for DisplayImpl, the impl's `to_string`: a function of
	// an impl block, a derive's synthesized one or a `host fn`. Nil when
	// no single impl is the type's (a std generic container whose impl
	// the index does not hold by receiver).
	Method ast.ImplMethodDecl
	// File is the home module key of Method's file (Program.ImplMethodFile).
	File string
	// Parts is, for DisplayParts, each item's or field's record, in the
	// type's order.
	Parts []*DisplayRender
}

// recordHoleDisplays records how each interpolation hole the file's bodies
// hold renders, once every body is checked and its variables closed.
func (c *checker) recordHoleDisplays() {
	if c.fa == nil || len(c.displayHoles) == 0 {
		return
	}
	p := c.fa.Program()
	for _, hole := range c.displayHoles {
		t, ok := c.fa.ExprTypes[hole]
		if !ok || t == nil {
			continue
		}
		if c.fa.HoleDisplays == nil {
			c.fa.HoleDisplays = map[ast.Node]*DisplayRender{}
		}
		c.fa.HoleDisplays[hole] = displayRenderOf(p, t, 0)
	}
	c.displayHoles = nil
}

// displayRenderOf is how a value of type t renders by Display.
func displayRenderOf(p *Program, t Type, depth int) *DisplayRender {
	t = ResolveClosed(t)
	r := &DisplayRender{Type: Elaborate(t)}
	if depth > 32 {
		r.Form = DisplayImpl
		return r
	}
	switch tt := resolveTypeVar(t).(type) {
	case *PrimitiveType:
		switch tt.Name_ {
		case "String":
			r.Form = DisplayText
			return r
		case "Int", "Float", "Decimal":
			r.Form = DisplayScalar
			return r
		}
	case *EnumType:
		if tt.Name == "Bool" && len(tt.TypeArgs) == 0 && isStdBool(tt) {
			r.Form = DisplayScalar
			return r
		}
	case *TypeParam_:
		r.Form = DisplayTypeParam
		return r
	case *InterfaceType:
		if tt.DefaultSelf {
			// The `self` of an interface default is the implementor, which
			// the default is lowered for: its rendering is the instance's,
			// as a type parameter's is.
			r.Form = DisplayTypeParam
			return r
		}
		r.Form = DisplayInterface
		return r
	case *ConjunctionType:
		// A conjunction renders as an interface value does: by the held
		// value's impl, which a Display member guarantees.
		r.Form = DisplayInterface
		return r
	case *TupleType:
		if textDisplay(t, 0) {
			r.Form = DisplayStructural
			return r
		}
		r.Form = DisplayParts
		for _, e := range tt.Elems {
			r.Parts = append(r.Parts, displayRenderOf(p, e, depth+1))
		}
		return r
	case *AnonStructType:
		if textDisplay(t, 0) {
			r.Form = DisplayStructural
			return r
		}
		r.Form = DisplayParts
		for _, f := range tt.Fields {
			r.Parts = append(r.Parts, displayRenderOf(p, f.Type, depth+1))
		}
		return r
	}
	if textDisplay(t, 0) || emptyCollection(t) {
		r.Form = DisplayStructural
		return r
	}
	r.Form = DisplayImpl
	if name := concreteTypeName(resolveTypeVar(t)); name != "" && p != nil {
		if m := implMethodOf(p, "Display", "to_string", resolveTypeVar(t), name); m != nil {
			r.Method = m
			r.File, _ = p.ImplMethodFile(m)
		}
	}
	return r
}

// isStdBool reports whether e is the prelude's Bool.
func isStdBool(e *EnumType) bool {
	if b, ok := TypeBool.(*EnumType); ok && b == e {
		return true
	}
	return len(e.Variants) == 2 && e.Variants[0].Name == "False" && e.Variants[1].Name == "True"
}

// textDisplay reports whether t renders as the machine's structural
// text: an Int, a Float, a Bool or a String, or a tuple, an anonymous
// struct, a List, a Map or a Vector of such values at any depth.
func textDisplay(t Type, depth int) bool {
	if depth > 32 {
		return false
	}
	t = ResolveClosed(t)
	switch tt := resolveTypeVar(t).(type) {
	case *PrimitiveType:
		return tt.Name_ == "Int" || tt.Name_ == "Float" || tt.Name_ == "String"
	case *EnumType:
		return tt.Name == "Bool" && len(tt.TypeArgs) == 0 && isStdBool(tt)
	case *TupleType:
		for _, e := range tt.Elems {
			if !textDisplay(e, depth+1) {
				return false
			}
		}
		return len(tt.Elems) > 0
	case *AnonStructType:
		for _, f := range tt.Fields {
			if !textDisplay(f.Type, depth+1) {
				return false
			}
		}
		return len(tt.Fields) > 0
	case *ListType:
		return closedElement(tt.Elem) || textDisplay(tt.Elem, depth+1)
	case *MapType:
		return closedElement(tt.Key) && closedElement(tt.Val) ||
			textDisplay(tt.Key, depth+1) && textDisplay(tt.Val, depth+1)
	}
	if elem, ok := vectorElemType(t); ok {
		return closedElement(elem) || textDisplay(elem, depth+1)
	}
	return false
}

// emptyCollection reports a Set or a Vector whose element type is a closed
// variable: no value of the element type is made, so it is empty, and its
// text is structural.
func emptyCollection(t Type) bool {
	if elem, ok := setElemType(t); ok {
		return closedElement(elem)
	}
	if elem, ok := vectorElemType(t); ok {
		return closedElement(elem)
	}
	return false
}

// closedElement reports an element type that is a variable the checker
// closed (closed_vars.go).
func closedElement(t Type) bool {
	v, ok := resolveTypeVar(t).(*TypeVar)
	return ok && v.Closed != nil
}

// displaysThrough reports whether r, or a part of it, renders through one
// of impls.
func (r *DisplayRender) displaysThrough(impls map[ast.Node]bool) bool {
	if r == nil {
		return false
	}
	if r.Method != nil && impls[r.Method] {
		return true
	}
	for _, p := range r.Parts {
		if p.displaysThrough(impls) {
			return true
		}
	}
	return false
}

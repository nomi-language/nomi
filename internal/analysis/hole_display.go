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
	// Parts is, for DisplayParts, each item's record in the tuple's order,
	// or each field's in the order of the fields' names
	// (AnonStructType.FieldsByName).
	Parts []*DisplayRender
}

// displayUse is a value the program renders by Display, whose rendering
// the checker records once the file's variables are closed: an
// interpolation hole (at, of the type the checker gave it), or, with
// failure set, the error of type t a fixed typed literal's handler or a
// `!` over a Result shows when it fails (at the *ast.TaggedString or the
// *ast.Unwrap).
type displayUse struct {
	at      ast.Node
	t       Type
	failure bool
}

// recordDisplays records how each value the file's bodies render by
// Display renders (displayRenderOf): a hole's in HoleDisplays, a typed
// literal's failure in its TypedLiteral's Failure, a `!`'s in
// UnwrapFailures.
func (c *checker) recordDisplays() {
	if c.fa == nil || len(c.displayUses) == 0 {
		return
	}
	for _, use := range c.displayUses {
		if !use.failure {
			t, ok := c.fa.ExprTypes[use.at]
			if !ok || t == nil {
				continue
			}
			if c.fa.HoleDisplays == nil {
				c.fa.HoleDisplays = map[ast.Node]*DisplayRender{}
			}
			c.fa.HoleDisplays[use.at] = c.fa.displayRenderOf(t, 0)
			continue
		}
		switch at := use.at.(type) {
		case *ast.TaggedString:
			if rec := c.fa.TypedLiterals[at]; rec != nil {
				rec.Failure = c.fa.displayRenderOf(use.t, 0)
			}
		case *ast.Unwrap:
			if c.fa.UnwrapFailures == nil {
				c.fa.UnwrapFailures = map[*ast.Unwrap]*DisplayRender{}
			}
			c.fa.UnwrapFailures[at] = c.fa.displayRenderOf(use.t, 0)
		}
	}
	c.displayUses = nil
}

// DisplayAt is r, a rendering the checker recorded in a generic body, at
// an instance of the body: by answers the type each of the body's type
// parameters stands for there (ReplaceTypeParams' form). The body is
// checked once, so a value of a type parameter records DisplayTypeParam,
// alone or as a part. At an instance where its type is known the answer
// is the rendering a value written at that type records
// (displayRenderOf), read against fa's program, whose impls the
// instance's types have. resolved is false, and r itself, for a rendering
// with no type parameter in it, and where a parameter has no answer.
func (fa *FileAnalysis) DisplayAt(r *DisplayRender, by func(*TypeParam_) (Type, bool)) (at *DisplayRender, resolved bool) {
	if fa == nil || r == nil || !r.perInstance() {
		return r, false
	}
	t, all := ReplaceTypeParams(resolveTypeVar(ResolveClosed(r.Type)), by)
	if !all {
		return r, false
	}
	if ContainsTypeParam(t) {
		return r, false
	}
	return fa.displayRenderOf(t, 0), true
}

// perInstance reports whether r, or a part of it, is a type parameter's
// rendering, which each instance of the body resolves.
func (r *DisplayRender) perInstance() bool {
	if r.Form == DisplayTypeParam {
		return r.Type != nil
	}
	for _, p := range r.Parts {
		if p != nil && p.perInstance() {
			return true
		}
	}
	return false
}

// ImplCallee is the callee a call of m, an impl function a record names
// (a rendering's DisplayRender.Method, an operator's
// OperatorImpl.Method), reaches: what the checker records for a call
// written of it, with the block that declares m (Callee.Impl), the file
// that declares it and its std identity.
func (fa *FileAnalysis) ImplCallee(m ast.ImplMethodDecl) Callee {
	out := Callee{Kind: CalleeImpl, Decl: m}
	if b := fa.implBlockHolding(m); b != nil {
		out.Impl = b
	}
	return fa.withStd(fa.withDeclaringFile(out))
}

// DisplayOf is how a value of type t renders by Display, read against
// fa's program, whose impls t's types have: what the checker records for
// an interpolation hole of that type. The IR builder asks it for a value
// it renders by Display where nothing written records one (`io.print(x)`,
// `Display.to_string(x)`, the error `main` returns), as it asks
// InterfaceCallee for a call it writes. Nil for a nil fa or t.
func (fa *FileAnalysis) DisplayOf(t Type) *DisplayRender {
	if fa == nil || t == nil {
		return nil
	}
	return fa.displayRenderOf(t, 0)
}

// displayRenderOf is how a value of type t renders by Display.
func (fa *FileAnalysis) displayRenderOf(t Type, depth int) *DisplayRender {
	t = ResolveClosed(t)
	if refinementOf(t) != nil {
		// A refinement renders as its base (refinement.go).
		t = RefinementBase(t)
	}
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
		if isStdType(tt, stdBoolType) && len(tt.TypeArgs) == 0 {
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
			r.Parts = append(r.Parts, fa.displayRenderOf(e, depth+1))
		}
		return r
	case *AnonStructType:
		if textDisplay(t, 0) {
			r.Form = DisplayStructural
			return r
		}
		r.Form = DisplayParts
		for _, f := range tt.FieldsByName() {
			r.Parts = append(r.Parts, fa.displayRenderOf(f.Type, depth+1))
		}
		return r
	}
	if textDisplay(t, 0) || emptyCollection(t) {
		r.Form = DisplayStructural
		return r
	}
	r.Form = DisplayImpl
	if m := fa.implMethodFor(resolveTypeVar(t), "Display", "to_string"); m != nil {
		r.Method = m
		r.File, _ = fa.Program().ImplMethodFile(m)
	}
	return r
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
		return isStdType(tt, stdBoolType) && len(tt.TypeArgs) == 0
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

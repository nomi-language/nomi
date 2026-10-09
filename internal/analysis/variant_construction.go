package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// Construction through a struct-shaped or `embeds` variant.
//
// The rule: `Enum.V` followed by a construction syntax takes exactly the
// construction syntaxes the variant's shape takes.
//
//   - A struct-shaped variant, declared inline (`Rect {w: Float, h: Float}`) or
//     embedded (`embeds Circle`), takes a struct's two forms: the brace form
//     `Shape.Rect{...}` (checkStructVariantLit) and the record call form
//     `Shape.Rect({...})`, which this file checks. Omitted fields take their
//     defaults in both. A struct has no positional call form, so neither does
//     the variant.
//   - An embedded wrapping distinct takes the distinct's call form:
//     `Shape.Id(5)` is checked as `Id(5)` is and builds an `Id`.
//   - An embedded zero-sized type takes no arguments: `Shape.Expired`.
//
// Passing an already-built value of the embedded type is not construction.
// `Shape.Circle(c)` with `c: Circle` is rejected with a message naming the
// forms that work; the value widens to `Shape` at any `Shape`-typed position
// on its own, which is what a caller holding one wants.

// variantCtor is a call callee that names a struct-shaped or embedded variant.
type variantCtor struct {
	et        *EnumType
	vd        *VariantDef
	label     string // `Shape.Rect`, for diagnostics
	line, col int    // the variant name's position
}

// variantCtorCallee reports whether callee names a struct-shaped or embedded
// variant of the enum fnTy constructs. Positional and bare variants are not
// claimed: their call forms are ordinary function calls.
func (c *checker) variantCtorCallee(callee ast.Node, fnTy Type) (variantCtor, bool) {
	var et *EnumType
	switch t := fnTy.(type) {
	case *FuncType:
		et, _ = t.Return.(*EnumType)
	case *EnumType:
		et = t
	}
	if et == nil {
		return variantCtor{}, false
	}
	var name string
	var line, col int
	switch fn := callee.(type) {
	case *ast.FieldAccess:
		if fn.Field == nil || !c.isVariantQualifier(fn, et) {
			return variantCtor{}, false
		}
		name, line, col = fn.Field.Name, fn.Field.Line, fn.Field.Col
	case *ast.DotVariant:
		if fn.ResolvedEnum == "" {
			return variantCtor{}, false
		}
		name, line, col = fn.Name, fn.Line, fn.Col
	default:
		return variantCtor{}, false
	}
	vd := variantDefNamed(et, name)
	if vd == nil {
		return variantCtor{}, false
	}
	switch vd.Kind {
	case VariantStruct:
	case VariantEmbedded:
		// An embedded ENUM has no construction form of its own and does not
		// widen; its variant keeps the ordinary call path.
		switch vd.Embedded.(type) {
		case *StructType, *DistinctType:
		default:
			if !embeddedZeroSized(vd) {
				return variantCtor{}, false
			}
		}
	default:
		return variantCtor{}, false
	}
	return variantCtor{et: et, vd: vd, label: et.Name + "." + name, line: line, col: col}, true
}

// isVariantQualifier reports whether `Q.V` names a variant of et: the
// reference recorded at V is an enum variant, or Q spells the enum itself.
func (c *checker) isVariantQualifier(fa *ast.FieldAccess, et *EnumType) bool {
	if c.fa != nil {
		if sym, ok := c.fa.References[Pos{Line: fa.Field.Line, Col: fa.Field.Col}]; ok && sym != nil {
			for sym.Resolved != nil {
				sym = sym.Resolved
			}
			return sym.Kind == SymbolEnumVariant
		}
	}
	switch obj := fa.Object.(type) {
	case *ast.TypeIdent:
		return obj.Name == et.Name
	case *ast.FieldAccess:
		return obj.Field != nil && obj.Field.Name == et.Name
	}
	return false
}

// embeddedZeroSized reports whether an embedded variant's type carries no
// value, so the variant is a value rather than a constructor.
func embeddedZeroSized(vd *VariantDef) bool {
	if dt, ok := vd.Embedded.(*DistinctType); ok {
		return dt.Inner == nil
	}
	return vd.DataType == nil || IsZeroSized(vd.DataType)
}

// checkVariantCtorCall checks `Enum.V(args)` for a struct-shaped or embedded
// variant. It returns false only for the shapes it leaves to the ordinary
// call path: a placeholder (partial application) of a distinct embed, and a
// distinct embed whose inner type mentions a type parameter.
func (c *checker) checkVariantCtorCall(n *ast.Call, form variantCtor) (Type, bool) {
	vd := form.vd
	if vd.Kind == VariantEmbedded {
		if embeddedZeroSized(vd) {
			c.checkArgs(n.Args)
			c.addError(form.line, form.col, zeroSizedCallMessage(form))
			return form.et, true
		}
		if dt, ok := vd.Embedded.(*DistinctType); ok {
			if callHasPlaceholder(n.Args) || ContainsTypeParam(dt.Inner) {
				return nil, false
			}
			c.checkDistinctEmbedArgs(n, form, dt)
			return form.et, true
		}
	}
	return c.checkStructShapedVariantCall(n, form), true
}

// structShapedVariantCallee reports whether callee names a struct-shaped
// variant, inline or embedded, whose construction takes a record.
func (c *checker) structShapedVariantCallee(callee ast.Node, fnTy Type) bool {
	form, ok := c.variantCtorCallee(callee, fnTy)
	if !ok {
		return false
	}
	if form.vd.Kind == VariantStruct {
		return true
	}
	_, isStruct := form.vd.Embedded.(*StructType)
	return isStruct
}

// openVariantEnum gives each of a generic enum's parameters that a variant
// construction left unsolved a fresh inference variable, as an ordinary
// variant's constructor call does: `Shape.UserId(3)` in `enum Shape<T>` is a
// `Shape<?1>` that fits a `Shape<Int>` parameter, not a `Shape<T>` that
// fits nothing.
func (c *checker) openVariantEnum(ty Type) Type {
	et, ok := ty.(*EnumType)
	if !ok || len(et.TypeParamDefs) == 0 {
		return ty
	}
	return instantiateUnboundCalleeParams(et, c.fnTypeParams, c)
}

// checkArgs walks call arguments so nested expressions are still checked
// when the call itself is rejected.
func (c *checker) checkArgs(args []ast.Node) {
	for _, arg := range args {
		c.checkNode(arg)
	}
}

// checkStructShapedVariantCall is the record call form `Enum.V({...})` of a
// struct-shaped variant, inline or embedded. A literal argument (including the
// empty `{}`, which parses as a zero-statement block) is checked exactly as the
// brace form `Enum.V{...}` is; an argument whose type is an anonymous record is
// checked by its field types, as a struct's call form checks one.
func (c *checker) checkStructShapedVariantCall(n *ast.Call, form variantCtor) Type {
	if len(n.Args) != 1 || isNamedArg(n.Args[0]) {
		c.checkArgs(n.Args)
		c.addError(form.line, form.col, fmt.Sprintf(
			"%s is a struct-shaped variant and has no positional form; construct it with %s{...} or %s({...})",
			form.label, form.label, form.label))
		return form.et
	}
	arg := n.Args[0]
	if lit, ok := anonStructLitArg(arg); ok {
		return c.checkStructVariantLit(lit, form.et, form.vd.Name, form.line, form.col)
	}
	return c.checkVariantRecordArg(arg, c.checkNode(arg), form)
}

// checkVariantRecordArg checks a NON-LITERAL argument to a struct-shaped
// variant's call form, given its type: the piped value of `rec |> Shape.V()`
// or the argument of `Shape.V(rec)`.
func (c *checker) checkVariantRecordArg(at ast.Node, argTy Type, form variantCtor) Type {
	line, col := nodeLineCol(at)
	if line == 0 {
		line, col = at.LineNum(), 1
	}
	if st, ok := form.vd.Embedded.(*StructType); ok && sameNominal(resolveTypeVar(argTy), st) {
		c.addError(line, col, alreadyBuiltMessage(form, st.Name,
			form.label+"{...} or "+form.label+"({...})"))
		return form.et
	}
	anon, ok := resolveTypeVar(argTy).(*AnonStructType)
	if !ok {
		c.addError(line, col, fmt.Sprintf(
			"%s expects an anonymous struct literal {...} matching its fields, got %s",
			form.label, formatTypeForError(argTy)))
		return form.et
	}
	if st, ok := form.vd.Embedded.(*StructType); ok {
		c.checkAnonStructTypeAgainstStruct(at, anon, st)
		return form.et
	}
	// An inline struct variant's fields are checked as a struct of the same
	// shape; the enum's parameters stand in for the struct's.
	shape := &StructType{
		Origin:        form.et.Origin,
		Name:          form.label,
		Fields:        form.vd.Fields,
		TypeParams:    form.et.TypeParams,
		TypeParamDefs: form.et.TypeParamDefs,
		TypeArgs:      form.et.TypeArgs,
	}
	solved, ok := c.checkAnonStructTypeAgainstStruct(at, anon, shape).(*StructType)
	if !ok || solved == shape || len(solved.TypeArgs) == 0 {
		return form.et
	}
	return &EnumType{
		Origin:           form.et.Origin,
		Name:             form.et.Name,
		Variants:         form.et.Variants,
		TypeParams:       form.et.TypeParams,
		TypeParamDefs:    form.et.TypeParamDefs,
		TypeArgs:         solved.TypeArgs,
		Opaque:           form.et.Opaque,
		OwningSourceFile: form.et.OwningSourceFile,
	}
}

// checkDistinctEmbedArgs checks `Enum.D(args)` for an embedded wrapping
// distinct D exactly as `D(args)` is checked, except that an argument that is
// already a D is named for what it is.
func (c *checker) checkDistinctEmbedArgs(n *ast.Call, form variantCtor, dt *DistinctType) {
	if tup, ok := dt.Inner.(*TupleType); ok && len(tup.Elems) >= 2 {
		c.checkTupleDistinctArgs(n, dt, tup)
		return
	}
	if len(n.Args) != 1 || isNamedArg(n.Args[0]) {
		c.checkArgs(n.Args)
		inner := formatTypeForError(dt.Inner)
		c.addError(form.line, form.col, fmt.Sprintf(
			"%s builds %s %s, which wraps %s, so %s(...) takes one argument, %s %s; got %d",
			form.label, articleFor(dt.Name), dt.Name, inner, form.label, articleFor(inner), inner, len(n.Args)))
		return
	}
	c.checkDistinctEmbedArg(n.Args[0], c.checkNodeExpecting(n.Args[0], dt.Inner), form, dt)
}

// checkDistinctEmbedArg checks one argument, already typed, against an
// embedded wrapping distinct's inner type.
func (c *checker) checkDistinctEmbedArg(at ast.Node, argTy Type, form variantCtor, dt *DistinctType) {
	if argTy == nil {
		return
	}
	line, col := nodeLineCol(at)
	if line == 0 {
		line, col = at.LineNum(), 1
	}
	if sameNominal(resolveTypeVar(argTy), dt) {
		c.addError(line, col, alreadyBuiltMessage(form, dt.Name,
			fmt.Sprintf("%s(<%s>)", form.label, dt.Inner)))
		return
	}
	c.checkDistinctInner(at, argTy, form.label, dt)
}

// checkPipedVariantCtor checks `value |> Enum.V()`: the piped value is the
// construction's one argument.
func (c *checker) checkPipedVariantCtor(call *ast.Call, piped ast.Node, pipedTy Type, form variantCtor) Type {
	vd := form.vd
	if vd.Kind == VariantEmbedded && embeddedZeroSized(vd) {
		c.checkArgs(call.Args)
		c.addError(form.line, form.col, zeroSizedCallMessage(form))
		return form.et
	}
	if len(call.Args) != 0 {
		c.checkArgs(call.Args)
		c.addError(form.line, form.col, fmt.Sprintf(
			"%s takes the piped value as its only argument, got %d more", form.label, len(call.Args)))
		return form.et
	}
	if dt, ok := vd.Embedded.(*DistinctType); ok && vd.Kind == VariantEmbedded {
		c.checkDistinctEmbedArg(piped, pipedTy, form, dt)
		return form.et
	}
	return c.checkVariantRecordArg(piped, pipedTy, form)
}

// checkCallee checks a call's callee. A struct-shaped variant is allowed there
// and nowhere else; see rejectStructVariantValue.
func (c *checker) checkCallee(callee ast.Node) Type {
	prev := c.calleeNode
	c.calleeNode = callee
	defer func() { c.calleeNode = prev }()
	return c.checkNode(callee)
}

// rejectStructVariantValue rejects a struct-shaped variant, inline or
// embedded, named anywhere but a call's callee: `f = Shape.Rect`,
// `Iter.map(recs, Shape.Rect)`. A struct has no function value — `Circle` is
// not one — so neither does a variant shaped like one. Its record call form
// `Shape.Rect({...})` is a construction syntax, not a call of a function the
// variant names. A positional variant (`Shape.Dot`) and an embedded distinct
// (`Shape.Id`) are functions of their payload and stay first-class.
func (c *checker) rejectStructVariantValue(node ast.Node, ty Type) Type {
	if ty == nil || node == c.calleeNode {
		return ty
	}
	if _, isFunc := ty.(*FuncType); !isFunc {
		return ty
	}
	form, ok := c.variantCtorCallee(node, ty)
	if !ok {
		return ty
	}
	switch form.vd.Kind {
	case VariantStruct:
	case VariantEmbedded:
		if _, isStruct := form.vd.Embedded.(*StructType); !isStruct {
			return ty
		}
	default:
		return ty
	}
	c.addError(form.line, form.col, fmt.Sprintf(
		"%s is a struct-shaped variant and is not a function value; build it with %s{...} or %s({...}), inside a lambda where a function is needed",
		form.label, form.label, form.label))
	// Untyped, so a later call of the rejected value does not add a second
	// error about the arguments it was given.
	return nil
}

func zeroSizedCallMessage(form variantCtor) string {
	return fmt.Sprintf("%s is a zero-sized variant and takes no arguments; write %s", form.label, form.label)
}

func alreadyBuiltMessage(form variantCtor, typeName, forms string) string {
	a := articleFor(typeName)
	return fmt.Sprintf(
		"%s constructs %s %s with %s; this argument is already %s %s — use it where %s %s is expected and it widens",
		form.label, a, typeName, forms, a, typeName, articleFor(form.et.Name), form.et.Name)
}

// anonStructLitArg is a call-form argument as an anonymous struct literal
// when it is one. `{}` parses as a zero-statement block and is the empty
// literal here, as checkStructCallForm treats it.
func anonStructLitArg(arg ast.Node) (*ast.StructLit, bool) {
	switch a := arg.(type) {
	case *ast.StructLit:
		if a.TypeName == nil && a.Spread == nil {
			return a, true
		}
	case *ast.Block:
		if len(a.Stmts) == 0 {
			return &ast.StructLit{Line: a.Line, Col: a.Col}, true
		}
	}
	return nil, false
}

func isNamedArg(n ast.Node) bool {
	_, ok := n.(*ast.NamedArg)
	return ok
}

// sameNominal reports whether t is the nominal struct or distinct want.
func sameNominal(t, want Type) bool {
	switch w := want.(type) {
	case *StructType:
		s, ok := t.(*StructType)
		return ok && s.Name == w.Name && (s.Origin == "" || w.Origin == "" || s.Origin == w.Origin)
	case *DistinctType:
		d, ok := t.(*DistinctType)
		return ok && d.Name == w.Name && (d.Origin == "" || w.Origin == "" || d.Origin == w.Origin)
	}
	return false
}

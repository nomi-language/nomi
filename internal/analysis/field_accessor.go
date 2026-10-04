package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// checkFieldAccessor types the field accessor shorthand `.name` against the
// type its position expects. The accessor is a function of one argument, so
// the expected type must be a function type `(S) -> R` whose parameter S is
// already known: an annotation, a declared parameter, or a generic
// parameter an earlier argument or the piped value pinned. That is the same
// outside-in walk a `.Variant` resolves by (checkDotVariant), and it has the
// same limit: nothing later in the program is consulted.
//
// The accessor's type is `(S) -> F`, F being the type of the last field in
// the chain. The caller unifies it with the expected type as it would a
// lambda's, which is how R gets solved in `Iter.map(users, .name)`.
//
// Each segment resolves through fieldTypeFromObject, which records the
// field's symbol at the segment's position for hover and go-to-definition.
func (c *checker) checkFieldAccessor(n *ast.FieldAccessor, expected Type) Type {
	spelling := n.Spelling()
	expected = resolveTypeVar(expected)
	ft, isFunc := expected.(*FuncType)
	if !isFunc {
		if expected == nil {
			c.addError(n.Line, n.Col, fmt.Sprintf(
				"`%s` reads a field only where a function is expected; write `x%s` to read it from a value",
				spelling, spelling))
		} else {
			c.addError(n.Line, n.Col, c.typef(
				"`%s` reads a field only where a function is expected, and %s is expected here; write `x%s` to read it from a value",
				spelling, expected, spelling))
		}
		return nil
	}
	if len(ft.Params) != 1 {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"`%s` is a function of one argument, and the function expected here takes %d", spelling, len(ft.Params)))
		return nil
	}
	param := resolveTypeVar(ft.Params[0])
	if !c.accessorParamKnown(param) {
		c.addError(n.Line, n.Col, fmt.Sprintf(
			"`%s` needs the record type it reads from; annotate the binding, e.g. `f: (User) -> String = %s`",
			spelling, spelling))
		return nil
	}

	cur := param
	for _, seg := range n.Path {
		obj := resolveTypeVar(cur)
		if !accessorReadable(obj) {
			c.addError(seg.Line, seg.Col, c.typef(
				"`%s` reads a field of a struct, a record or a tuple, and %s is none of those",
				spelling, obj))
			return nil
		}
		pos := Pos{Line: seg.Line, Col: seg.Col}
		ty, found := fieldTypeFromObject(c, obj, seg.Name, pos)
		if !found {
			c.reportAccessorMissingField(obj, seg)
			return nil
		}
		if ty == nil {
			// fieldTypeFromObject reported it (an opaque struct's field).
			return nil
		}
		cur = ty
	}

	if c.fa != nil {
		if c.fa.FieldAccessors == nil {
			c.fa.FieldAccessors = map[*ast.FieldAccessor]Type{}
		}
		c.fa.FieldAccessors[n] = param
	}
	return &FuncType{Params: []Type{param}, Return: cur}
}

// accessorParamKnown reports whether the expected function's parameter says
// what the accessor reads from. An unsolved inference variable, and a type
// parameter of the callee that no earlier argument solved, say nothing; a
// type parameter of the enclosing function is a known type, which its
// bounds' `field` requirements may give fields.
func (c *checker) accessorParamKnown(param Type) bool {
	switch p := param.(type) {
	case nil:
		return false
	case *TypeVar:
		return false
	case *TypeParam_:
		return c.fnTypeParams[p.Name_] == p
	}
	return true
}

// accessorReadable reports whether a field accessor can read from a value of
// type t: a struct, an anonymous struct, a tuple (`.0`), or an interface or
// bounded type parameter through its `field` requirements. An enum's fields
// belong to its variants and are not read through an accessor.
func accessorReadable(t Type) bool {
	switch t.(type) {
	case *StructType, *AnonStructType, *TupleType, *InterfaceType, *TypeParam_:
		return true
	}
	return false
}

// reportAccessorMissingField reports a segment naming no field of obj, in the
// words checkFieldAccess uses for `x.nmae`.
func (c *checker) reportAccessorMissingField(obj Type, seg *ast.Ident) {
	switch t := obj.(type) {
	case *StructType:
		c.report(TypeError{Line: seg.Line, Col: seg.Col, Message: fmt.Sprintf(
			"struct '%s' has no field '%s'", t.Name, seg.Name)}.WithHint(didYouMean(seg.Name, fieldNames(canonicalStructForFieldAccess(c, t).Fields))))
	case *InterfaceType:
		c.addError(seg.Line, seg.Col, fmt.Sprintf(
			"interface '%s' has no field '%s'", t.Name, seg.Name))
	case *TypeParam_:
		c.addError(seg.Line, seg.Col, fmt.Sprintf(
			"type parameter `%s` has no field '%s' (a type parameter exposes only the fields its interface bounds declare)", t.Name_, seg.Name))
	default:
		c.addError(seg.Line, seg.Col, c.typef("%s has no field '%s'", obj, seg.Name))
	}
}

// fieldAccessorPipeStageMessage is the error for `user |> .name`: a pipe
// stage is a function the piped value is passed to, and the read of a field
// from that value is written `user.name`.
func fieldAccessorPipeStageMessage(n *ast.FieldAccessor) string {
	s := n.Spelling()
	return fmt.Sprintf("`%s` is not a pipe stage; read the field from the value instead, as in `x%s`", s, s)
}

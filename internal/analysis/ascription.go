package analysis

import (
	"fmt"
	"maps"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// Type ascription (spec §5 *Type ascription*): `value as Type` checks value
// with Type as its expected type, exactly as `x: Type = value` does, and is
// a Type. It converts nothing, so it admits only what an annotated binding
// admits, and a mismatch is the binding's error at the value. Its use is
// to give a type where no annotation can stand: an untagged literal or an
// empty collection inside an anonymous struct literal or an argument.

// checkAscription checks n and answers its type.
func (c *checker) checkAscription(n *ast.Ascription) Type {
	declared, err := c.resolveBodyType(n.Type)
	if err != nil {
		if te, ok := err.(TypeError); ok {
			c.errors = append(c.errors, te)
		} else {
			c.addError(n.Line, n.Col, err.Error())
		}
		return c.checkNode(n.Value)
	}
	if c.fa != nil && c.fa.References != nil && !c.annotationTrial && n.Line > 0 {
		// A hover on `as` shows the type it gives the value.
		c.fa.References[Pos{Line: n.Line, Col: n.Col}] = &Symbol{
			Name: "as", Kind: SymbolArgHint, Pos: Pos{Line: n.Line, Col: n.Col}, Type: declared, Span: len("as"),
		}
	}
	redundant := c.ascriptionRedundant(n.Value, declared)
	valTy := c.checkNodeExpecting(n.Value, declared)
	if valTy == nil {
		return declared
	}
	subs := map[*TypeParam_]Type{}
	if err := c.unifyInto(declared, valTy, subs); err != nil {
		c.report(errAt(n.Value, c.typef(
			"type mismatch: expected %s, got %s", declared, valTy)).
			WithHint(conversionHint(valTy, declared, n.Value)).
			WithHint(c.mismatchHint(declared, valTy)))
		return declared
	}
	if redundant && c.fa != nil && !c.annotationTrial {
		if c.fa.RedundantAscriptions == nil {
			c.fa.RedundantAscriptions = map[*ast.Ascription]bool{}
		}
		c.fa.RedundantAscriptions[n] = true
	}
	return declared
}

// ascriptionRedundant reports whether value, checked with no expected
// type, already has the type want: the `as` then says nothing. The check is
// a trial, whose errors and recorded types are put back.
func (c *checker) ascriptionRedundant(value ast.Node, want Type) bool {
	if c.fa == nil || c.annotationTrial || !typedAlone(value) {
		return false
	}
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	exprTypes, expectedTypes, patternTypes := maps.Clone(c.fa.ExprTypes), maps.Clone(c.fa.ExpectedTypes), maps.Clone(c.fa.PatternTypes)
	saved := saveTypeVars(want)
	prev := c.annotationTrial
	c.annotationTrial = true
	ty := c.checkNode(value)
	ok := len(c.errors) == mark && defaultDetermines(ty) && TypesEqual(resolveTypeVar(ty), want) && ty.String() == want.String()
	c.annotationTrial = prev
	saved.restore()
	c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
	c.fa.ExprTypes, c.fa.ExpectedTypes, c.fa.PatternTypes = exprTypes, expectedTypes, patternTypes
	return ok
}

// typedAlone reports whether value is a name, a field read or a number
// literal, whose type no expected type changes and whose check records
// nothing beside its type: the values an `as` can be redundant on that the
// trial checks. A literal, a collection or a call is checked once, against
// the ascribed type, as written.
func typedAlone(value ast.Node) bool {
	switch v := value.(type) {
	case *ast.GroupedExpr:
		return typedAlone(v.Expr)
	case *ast.Ident, *ast.FieldAccess, *ast.IntLit, *ast.FloatLit, *ast.DecimalLit:
		return true
	}
	return false
}

// conversionHint is the help at `value as Type` where value has a type of
// its own that `as` would have to convert, the reading `as` has in Rust and
// TypeScript: it names the function that converts.
func conversionHint(have, want Type, value ast.Node) string {
	from, to := have.String(), want.String()
	fn, ok := conversions[[2]string{from, to}]
	if !ok {
		return ""
	}
	return fmt.Sprintf("`as` doesn't convert a value; write `%s(%s)`", fn, format.RenderNode(value))
}

// conversions are the std functions that convert between the scalar types
// an `as` is most often written to convert between, by (from, to).
var conversions = map[[2]string]string{
	{"Int", "Float"}:        "Int.to_float",
	{"Float", "Int"}:        "Float.to_int",
	{"String", "Int"}:       "String.to_int",
	{"String", "Float"}:     "String.to_float",
	{"Int", "String"}:       "Int.to_string",
	{"Float", "String"}:     "Float.to_string",
	{"Decimal", "Float"}:    "Decimal.to_float",
	{"Decimal", "Int"}:      "Decimal.to_int",
	{"Byte", "Int"}:         "Byte.to_int",
	{"Codepoint", "Int"}:    "Codepoint.to_int",
	{"Bool", "String"}:      "Display.to_string",
	{"Decimal", "String"}:   "Decimal.to_string",
	{"Codepoint", "String"}: "Codepoint.to_string",
}

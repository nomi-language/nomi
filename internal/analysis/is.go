package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// An `is` test, `value is Pattern`, is a Bool: whether the value matches the
// pattern, which is checked against the value's type as a `case` arm's
// pattern is, with the same errors. It binds nothing and has no
// exhaustiveness to check.

// checkIs checks `value is Pattern`.
func (c *checker) checkIs(n *ast.Is) Type {
	valueTy := c.checkNode(n.Value)
	if n.Pattern == nil {
		return TypeBool
	}
	bound := c.rejectIsBindings(n)
	if valueTy == nil {
		return TypeBool
	}
	before := len(c.errors)
	c.checkPattern(n.Pattern, valueTy)
	if !bound && len(c.errors) == before && c.patternsExhaustiveOver(valueTy, []ast.Node{n.Pattern}) {
		c.report(TypeError{Line: n.Line, Col: n.Col, Message: c.typef(
			"`%s` matches every value of type %s, so this test is always True", patternSource(n.Pattern), valueTy)})
	}
	return TypeBool
}

// rejectIsBindings reports each name the pattern of an `is` test binds, and
// answers whether there was one: the test binds nothing, and the binding
// form is `if Pattern = value`. A discard name (`_rest`) binds nothing and is
// allowed.
func (c *checker) rejectIsBindings(n *ast.Is) bool {
	bound := false
	pattern := patternSource(n.Pattern)
	value := UnwrapSource(n.Value)
	for _, site := range patternSites(n.Pattern) {
		if ast.IsDiscardName(site.name) {
			continue
		}
		bound = true
		e := TypeError{Line: site.pos.Line, Col: site.pos.Col, Message: fmt.Sprintf(
			"an `is` test binds nothing, so its pattern cannot name `%s`", site.name)}
		if id, ok := n.Pattern.(*ast.IdentPattern); ok && id.Name == site.name {
			e = e.WithHint(fmt.Sprintf("to compare the value with `%s`, write `%s == %s`", site.name, value, site.name))
		} else if namedByAs(n.Pattern, site.pos) {
			e = e.WithHint(fmt.Sprintf("to use `%s`, match with `if %s = %s { ... }`", site.name, pattern, value)).
				WithHint(fmt.Sprintf("to test only, remove `as %s`", site.name))
		} else {
			e = e.WithHint(fmt.Sprintf("to use `%s`, match with `if %s = %s { ... }`", site.name, pattern, value)).
				WithHint(fmt.Sprintf("to test only, write `_` where `%s` is", site.name))
		}
		c.report(e)
	}
	return bound
}

// namedByAs reports whether the name at pos is an `as` pattern's, `P as
// name`, somewhere in pattern.
func namedByAs(pattern ast.Node, pos Pos) bool {
	found := false
	ast.Inspect(pattern, func(n ast.Node) bool {
		if as, ok := n.(*ast.AsPattern); ok && as.NameLine == pos.Line && as.NameCol == pos.Col {
			found = true
		}
		return !found
	})
	return found
}

// isOrHint is the hint for an `or` whose left operand ends in an `is` test
// and whose right operand is not a Bool: the author wrote `x is A or B` for
// `x is (A or B)`.
func isOrHint(n *ast.Binary) string {
	if n.Op != "or" {
		return ""
	}
	test := trailingIsTest(n.Left)
	if test == nil {
		return ""
	}
	return fmt.Sprintf("`or` here joins two Bool values; to test several patterns, put them in parentheses: `%s is (%s or %s)`",
		UnwrapSource(test.Value), patternSource(test.Pattern), UnwrapSource(n.Right))
}

// trailingIsTest is the `is` test that ends n, written last in a run of
// `and`, `or` and `not` without parentheses, or nil.
func trailingIsTest(n ast.Node) *ast.Is {
	for {
		switch v := n.(type) {
		case *ast.Is:
			return v
		case *ast.Binary:
			if v.Op != "and" && v.Op != "or" {
				return nil
			}
			n = v.Right
		case *ast.Unary:
			if v.Op != "not" {
				return nil
			}
			n = v.Right
		default:
			return nil
		}
	}
}

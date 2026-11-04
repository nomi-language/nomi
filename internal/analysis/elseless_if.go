package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An `if` with no `else`, and an `else if` chain with no final `else`, is
// `Maybe<T>` when its branches have a value of a type T other than Unit:
// `Some` of the value of the branch that runs, or `None` when none does
// (spec §8 *`if`/`else`*). A branch that is Unit, or that leaves (`return`,
// `break`, a `?` that propagates, a call that never returns), keeps the
// `if` a Unit statement. Nothing flattens: a branch that is already a
// `Maybe<U>` makes the `if` a `Maybe<Maybe<U>>`.

// ifChainLacksElse reports whether n, followed through its `else if`
// links, ends with no `else`.
func ifChainLacksElse(n *ast.If) bool {
	for {
		switch e := n.Else.(type) {
		case nil:
			return true
		case *ast.If:
			if e == nil {
				return true
			}
			n = e
		default:
			return false
		}
	}
}

// ifChainLinks is n and each `if` its `else if`s chain to.
func ifChainLinks(n *ast.If) []*ast.If {
	links := []*ast.If{n}
	for {
		e, ok := n.Else.(*ast.If)
		if !ok || e == nil {
			return links
		}
		links = append(links, e)
		n = e
	}
}

// checkElselessIf checks n, the head of a chain with no final `else`. The
// chain's branches are checked as the branches of an `if` with an `else`
// are, against the Maybe's payload when the position expects a Maybe and
// against Unit otherwise, as a statement's are; their joined type T makes
// the chain `Maybe<T>`, unless it is Unit or the branches all leave.
func (c *checker) checkElselessIf(n *ast.If, condTy, expected Type, patternInput bool, narrowSym *Symbol, narrowTy Type) Type {
	want := Type(TypeUnit)
	if inner := maybePayload(expected); inner != nil {
		want = inner
	}
	links := ifChainLinks(n)
	if c.elselessLinks == nil {
		c.elselessLinks = map[*ast.If]bool{}
	}
	for _, l := range links {
		c.elselessLinks[l] = true
	}
	bare := c.checkIfBranches(n, condTy, want, patternInput, narrowSym, narrowTy)
	for _, l := range links {
		delete(c.elselessLinks, l)
	}
	ty := Type(TypeUnit)
	if bare != nil && !isUnitLike(bare) && !isInfallibleLike(bare) {
		if maybe := c.maybeOf(bare); maybe != nil {
			ty = maybe
			if c.fa.ElselessIfs == nil {
				c.fa.ElselessIfs = map[*ast.If]Type{}
			}
			c.fa.ElselessIfs[n] = maybe
		}
	}
	for _, l := range links {
		c.registerControlFlowHover(l.Line, l.Col, "if", condTy, ty, true, patternInput && l == n)
	}
	return ty
}

// maybePayload is T when t is `Maybe<T>`, or nil.
func maybePayload(t Type) Type {
	et, ok := resolveTypeVar(t).(*EnumType)
	if !ok || et.Name != "Maybe" || len(et.TypeArgs) != 1 || !isMaybeEnum(et) {
		return nil
	}
	return et.TypeArgs[0]
}

// isMaybeEnum reports whether et is the prelude's Maybe: an enum of that
// name whose variants are `Some` with one value and a bare `None`.
func isMaybeEnum(et *EnumType) bool {
	if len(et.Variants) != 2 {
		return false
	}
	some, none := et.Variants[0], et.Variants[1]
	if some.Name != "Some" {
		some, none = none, some
	}
	return some.Name == "Some" && some.DataType != nil && none.Name == "None" && none.DataType == nil
}

// maybeOf is `Maybe<inner>` for std's Maybe, or nil when the file reaches
// none: std's own module scope holds it, and a file of std itself sees it
// through its own scope.
func (c *checker) maybeOf(inner Type) Type {
	var et *EnumType
	if scope := c.fa.Program().StdlibModuleScopes["maybe"]; scope != nil {
		if sym := scope.LookupLocal("Maybe"); sym != nil {
			et, _ = sym.Type.(*EnumType)
		}
	}
	if et == nil {
		if sym := c.lookupEnumSymByName("Maybe"); sym != nil {
			et, _ = sym.Type.(*EnumType)
		}
	}
	if et == nil || len(et.TypeParamDefs) != 1 || !isMaybeEnum(et) {
		return nil
	}
	return instantiateVariantEnum(et, map[*TypeParam_]Type{et.TypeParamDefs[0]: inner})
}

// elselessIfAt is the `if` with no `else` that expr's value comes from,
// through parentheses and a block's tail, with its Maybe type, or nil.
func (c *checker) elselessIfAt(expr ast.Node) (*ast.If, Type) {
	for expr != nil {
		switch e := expr.(type) {
		case *ast.GroupedExpr:
			expr = e.Expr
		case *ast.ExprStmt:
			expr = e.Expr
		case *ast.Block:
			expr = tailExpr(e)
		case *ast.If:
			if ty, ok := c.fa.ElselessIfs[e]; ok {
				return e, ty
			}
			return nil, nil
		default:
			return nil, nil
		}
	}
	return nil, nil
}

// elselessIfHint is the help on a mismatch whose value is an `if` with no
// `else`, a Maybe where want is expected: when want is the Maybe's payload
// the `if` lacks its `else`, and when want is Unit the `if` was meant as a
// statement. Otherwise it is empty. purpose says what the `else` would let
// the value do (`return`, `pass`).
func (c *checker) elselessIfHint(want Type, expr ast.Node, purpose string) string {
	n, ty := c.elselessIfAt(expr)
	if n == nil || want == nil {
		return ""
	}
	inner := maybePayload(ty)
	switch {
	case isUnitLike(want):
		return elselessStatementHint
	case inner != nil && TypesEqual(resolveTypeVar(want), resolveTypeVar(inner)):
		name := c.typef("%s", want)
		article := "a"
		if name != "" && strings.ContainsRune("AEIOU", rune(name[0])) {
			article = "an"
		}
		return fmt.Sprintf("an `if` without `else` is a `Maybe`, `None` when no branch runs; add an `else` to %s %s %s", purpose, article, name)
	}
	return ""
}

// elselessStatementHint is the help on an `if` with no `else` whose branch
// has a value where only Unit is accepted.
const elselessStatementHint = "an `if` without `else` whose branch has a value is a `Maybe`; " +
	"to run the branch only for its effect, bind its value to `_` inside the branch"

// elselessNonFinalHint is the help on a non-final statement that is an `if`
// with no `else` whose branch has a value, or "".
func (c *checker) elselessNonFinalHint(expr ast.Node) string {
	if n, _ := c.elselessIfAt(expr); n == nil {
		return ""
	}
	return elselessStatementHint + ", or add an `else` and use the `if`'s value"
}

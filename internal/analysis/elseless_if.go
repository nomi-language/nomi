package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/parser"
)

// An `if` with no `else`, and an `else if` chain with no final `else`, is
// a statement, of type Unit (spec §8 *`if`/`else`*). Its branches are Unit,
// leave (`return`, `break`, a `?` that propagates, a call that never
// returns) or end in a statement whose value a block drops (`assert`,
// `dbg`). Any other branch type is an error, a `Maybe` included: its fix
// adds `else None` for a Maybe, and drops the value with `_ = ...` where a
// statement drops it.

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
// are, against the expected type when it is a Maybe (so the error's
// `else None` fix leaves a branch like `Some(.Circle(1.0))` resolved) and
// against Unit otherwise, as a statement's are. The chain is Unit when they
// are Unit or all leave, and an error otherwise.
func (c *checker) checkElselessIf(n *ast.If, condTy, expected Type, patternInput bool, narrowSym *Symbol, narrowTy Type) Type {
	want := Type(TypeUnit)
	if maybePayload(expected) != nil {
		want = expected
	}
	dropped := c.statementIf == n || isUnitLike(expected)
	links := ifChainLinks(n)
	if c.elselessLinks == nil {
		c.elselessLinks = map[*ast.If]bool{}
	}
	for _, l := range links {
		c.elselessLinks[l] = true
	}
	prevDropped := c.elselessDropped
	c.elselessDropped = dropped
	joined := c.checkIfBranches(n, condTy, want, patternInput, narrowSym, narrowTy)
	c.elselessDropped = prevDropped
	for _, l := range links {
		delete(c.elselessLinks, l)
	}
	var ty Type = TypeUnit
	switch {
	case joined == nil:
		// A branch was an error, reported already.
		ty = nil
	case isUnitLike(joined) || isInfallibleLike(joined):
	default:
		c.reportElselessValue(links, joined, dropped)
		ty = nil
		if !dropped && maybePayload(joined) != nil {
			// The `else None` the error asks for gives the `if` this
			// type, so what follows checks as it will once it is written.
			ty = joined
		}
	}
	for _, l := range links {
		c.registerControlFlowHover(l.Line, l.Col, "if", condTy, ty, true, patternInput && l == n)
	}
	return ty
}

// endsInStatement reports whether block ends in a statement whose value a
// block drops: an `assert` or `refute`, or a `dbg`.
func endsInStatement(block *ast.Block) bool {
	return isIgnorableStatementExpr(tailExpr(block))
}

// isBareValue reports whether t, a branch's type, is neither Unit nor a
// leave: the branch types an `if` without `else` refuses.
func isBareValue(t Type) bool {
	return t != nil && !isUnitLike(t) && !isInfallibleLike(t)
}

// reportElselessValue reports the first branch of links, a chain with no
// final `else`, whose value is not Unit, or the head's when no branch's own
// type says (joined is the branches' type). Where a statement drops the
// value (dropped), the fix writes `_ = ` before it; otherwise, for a Maybe
// branch, the fix adds `else None`, and for any other branch the help says
// to add an `else`.
func (c *checker) reportElselessValue(links []*ast.If, joined Type, dropped bool) {
	at, ty, link := tailExpr(links[0].Then), joined, links[0]
	for _, l := range links {
		tail := tailExpr(l.Then)
		if bt := c.fa.ExprTypes[tail]; tail != nil && isBareValue(bt) && !endsInStatement(l.Then) {
			at, ty, link = tail, bt, l
			break
		}
	}
	if at == nil || ty == nil {
		return
	}
	name := c.typef("%s", ty)
	article := "a"
	if name != "" && strings.ContainsRune("AEIOU", rune(name[0])) {
		article = "an"
	}
	text := format.RenderNode(at)
	short := text != "" && !strings.Contains(text, "\n") && len(text) <= 32
	head := fmt.Sprintf("an `if` without `else` is a statement, so its branch must be `Unit`; this branch is %s `%s`", article, name)
	if dropped {
		write := "Write `_ = ...` before it in the branch"
		if short {
			write = "Write `_ = " + text + "` in the branch"
		}
		e := TypeError{Message: fmt.Sprintf("%s, which this statement drops. %s, or add an `else`", head, write)}
		e = c.placeElselessError(e, at)
		e.Fix = c.dropValueFix(links, link, at)
		c.report(e.WithHint("`_ = ...` runs the branch for its effect and drops its value"))
		return
	}
	if maybePayload(ty) != nil {
		e := TypeError{Message: head + ". Add `else None` to make the `if` a `" + name + "`"}
		e = c.placeElselessError(e, at)
		e.Fix = c.elseNoneFix(links)
		c.report(e.WithHint("an `if` has a value only with an `else`; `else None` gives it `None` when no branch runs"))
		return
	}
	e := TypeError{Message: head + ". Add an `else` to give the `if` a value"}
	e = c.placeElselessError(e, at)
	c.report(e.WithHint("an `if` has a value only with an `else`, whose branch gives the value when no other branch runs"))
}

// elseNoneFix adds `else None` to the chain links: after the last branch
// of a one-line chain, and as an `else` branch before the `end` of a block
// one.
func (c *checker) elseNoneFix(links []*ast.If) *parser.Fix {
	head := links[0]
	sp, ok := spanOf(head)
	if !ok {
		return nil
	}
	const title = "Add `else None`"
	if head.Inline {
		at := parser.Pos{Line: sp.EndLine, Col: sp.EndCol}
		return &parser.Fix{Title: title, Edits: []parser.Edit{{Start: at, End: at, Text: " else None"}}}
	}
	if sp.EndCol < 4 {
		return nil
	}
	indent := strings.Repeat(" ", sp.EndCol-4)
	at := parser.Pos{Line: sp.EndLine, Col: sp.EndCol - 3}
	return &parser.Fix{Title: title, Edits: []parser.Edit{{Start: at, End: at, Text: "else\n" + indent + "    None\n" + indent}}}
}

// placeElselessError is e at the extent of at, a branch's value.
func (c *checker) placeElselessError(e TypeError, at ast.Node) TypeError {
	e.Line, e.Col = exprStartLineCol(at)
	if sp, ok := spanOf(at); ok {
		e.Line, e.Col, e.EndLine, e.EndCol = sp.StartLine, sp.StartCol, sp.EndLine, sp.EndCol
	}
	return e
}

// dropValueFix writes `_ = ` before at, the value of link's branch, in a
// chain (links) a statement drops. A binding cannot stand inline, so a
// one-line `if` of one link is written in block form, its value on the
// line below the condition; a one-line chain gets no fix.
func (c *checker) dropValueFix(links []*ast.If, link *ast.If, at ast.Node) *parser.Fix {
	sp, ok := spanOf(at)
	if !ok {
		return nil
	}
	const title = "Drop the value with `_ = ...`"
	if !link.Inline {
		at := parser.Pos{Line: sp.StartLine, Col: sp.StartCol}
		return &parser.Fix{Title: title, Edits: []parser.Edit{{Start: at, End: at, Text: "_ = "}}}
	}
	head := links[0]
	ifSp, ok := spanOf(head)
	if len(links) > 1 || !ok || ifSp.StartLine != sp.StartLine || head.Cond == nil {
		return nil
	}
	cond := format.RenderNode(head.Cond)
	if head.CondPattern != nil {
		cond = format.RenderNode(head.CondPattern) + " = " + cond
	}
	indent := strings.Repeat(" ", ifSp.StartCol-1)
	text := "if " + cond + " then\n" + indent + "    _ = " + format.RenderNode(at) + "\n" + indent + "end"
	return &parser.Fix{Title: title, Edits: []parser.Edit{{
		Start: parser.Pos{Line: ifSp.StartLine, Col: ifSp.StartCol},
		End:   parser.Pos{Line: sp.EndLine, Col: sp.EndCol},
		Text:  text,
	}}}
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

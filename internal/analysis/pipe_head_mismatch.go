package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// PipeToStringHint ends the help on a pipe stage that takes a String and is
// piped a value whose type implements Display. The language server's quick
// fix recognizes it and inserts that stage.
const PipeToStringHint = "add `|> Display.to_string()` before this stage"

// pipeHead is the place a pipe stage takes the value piped into it: the
// slot of `String.pad_start(6)` that `x` fills in `x |> String.pad_start(6)`.
type pipeHead struct {
	pipe *ast.Binary
	// call is the stage's call, nil for a stage that is not one.
	call *ast.Call
	// slot is the parameter the value fills.
	slot int
	// hole is the `_` the value fills, nil when it fills the first argument.
	hole ast.Node
	// name is the parameter a named `_` names (`w` in `f(1, w: _)`), empty
	// for a positional one.
	name string
}

// pipeFill is one slot a pipe's value fills in its stage call: parameter
// slot, named by the `_` at call.Args[arg], or the prepended first argument
// when arg is -1.
type pipeFill struct {
	slot int
	arg  int
}

// newPipeHead is the place pipe n's value goes in its stage call.
func newPipeHead(n *ast.Binary, call *ast.Call, fill pipeFill) *pipeHead {
	p := &pipeHead{pipe: n, call: call, slot: fill.slot}
	if call != nil && fill.arg >= 0 && fill.arg < len(call.Args) {
		switch a := call.Args[fill.arg].(type) {
		case *ast.Placeholder:
			p.hole = a
		case *ast.NamedArg:
			p.hole, p.name = a.Value, a.Name
		}
	}
	return p
}

// site is the node a mismatch is reported at: the `_` the value fills, or
// the stage's callee, which is what refuses the value.
func (p *pipeHead) site() ast.Node {
	if p.hole != nil {
		return p.hole
	}
	if p.call != nil {
		return p.call.Func
	}
	return p.pipe.Right
}

// stageName is the stage as an error names it: its callee as written, or
// "this stage".
func (p *pipeHead) stageName() string {
	callee := p.pipe.Right
	if p.call != nil {
		callee = p.call.Func
	}
	if text := calleeText(callee); text != "?" {
		return "`" + text + "`"
	}
	return "this stage"
}

// argName is the argument the value fills as an error names it: "`w`" for a
// named `_`, otherwise its position ("its first argument").
func (p *pipeHead) argName() string {
	if p.name != "" {
		return "`" + p.name + "`"
	}
	return "its " + argOrdinal(p.slot)
}

// source is the expression the piped value comes from: the stage before
// this one, or the pipe's head.
func (p *pipeHead) source() ast.Node {
	left := ungroupExpr(p.pipe.Left)
	if b, ok := left.(*ast.Binary); ok && b.Op == "|>" {
		return b.Right
	}
	return left
}

// mismatch is the error for a pipe stage that refuses the value piped into
// it, want being the parameter's type and have the value's: `String.pad_start`
// takes a String as its first argument, but the pipe passes a Fahrenheit. It
// is placed on the stage, notes where the value comes from, and carries the
// pipe-specific help for the two types.
func (c *checker) pipeHeadMismatch(p *pipeHead, want, have Type) TypeError {
	wantS, haveS := c.typePair(want, have)
	msg := fmt.Sprintf("%s takes %s as %s, but the pipe passes %s",
		p.stageName(), withArticle(wantS), p.argName(), withArticle(haveS))
	return c.pipeHeadError(p, msg, want, have)
}

// pipeHeadNotImplemented is the error for a pipe stage whose parameter is
// an interface the piped value's type does not implement.
func (c *checker) pipeHeadNotImplemented(p *pipeHead, iface string, have Type) TypeError {
	haveS := c.typef("%s", have)
	msg := fmt.Sprintf("%s takes %s as %s, but the pipe passes %s, which does not implement %s",
		p.stageName(), withArticle(iface), p.argName(), withArticle(haveS), iface)
	return c.pipeHeadError(p, msg, nil, have)
}

func (c *checker) pipeHeadError(p *pipeHead, msg string, want, have Type) TypeError {
	e := errAt(p.site(), msg)
	if r, ok := relatedAt(p.source(), c.typef("the piped value is %s", withArticle(c.typef("%s", have)))); ok {
		e = e.WithRelated(r)
	}
	if want != nil {
		e = e.WithHint(c.pipeHeadHint(want, have, ungroupExpr(p.pipe.Left)))
	}
	return e
}

// pipeHeadHint is the help for a value of type have piped into a stage
// that takes a want, when one fix is right for the two types: destructure a
// distinct value to reach the type inside it, render a Display value as
// the String the stage takes, handle a Maybe or Result whose payload the
// stage takes, or convert between Int, Float and Decimal. value is the
// pipe's head.
func (c *checker) pipeHeadHint(want, have Type, value ast.Node) string {
	w, h := resolveTV(want), resolveTV(have)
	if w == nil || h == nil || ContainsTypeParam(w) {
		return ""
	}
	if dt, ok := h.(*DistinctType); ok && dt.Inner != nil && len(dt.TypeParams) == 0 && TypesEqual(resolveTV(dt.Inner), w) {
		return c.distinctUnwrapHint(dt, value)
	}
	if w == TypeString && h != TypeString && c.hasImpl(h, "Display") {
		return c.typef("%s implements Display; ", h) + PipeToStringHint
	}
	if et, ok := h.(*EnumType); ok && len(et.TypeArgs) > 0 && TypesEqual(resolveTV(et.TypeArgs[0]), w) {
		switch {
		case et.Name == "Maybe" && len(et.TypeArgs) == 1:
			return "a Maybe may be None; handle it before this stage: give a fallback with `|> Maybe.with_default(...)`, match it with `case`, or unwrap it with `try` in a function that returns a Maybe"
		case et.Name == "Result" && len(et.TypeArgs) == 2:
			return "a Result may be an Err; handle it before this stage: give a fallback with `|> Result.with_default(...)`, match it with `case`, or unwrap it with `try` in a function that returns a Result"
		}
	}
	conversions := map[[2]Type]string{
		{TypeInt, TypeFloat}:     "Int.to_float",
		{TypeInt, TypeDecimal}:   "Decimal.from_int",
		{TypeDecimal, TypeFloat}: "Decimal.to_float",
	}
	if fn, ok := conversions[[2]Type{h, w}]; ok {
		return fmt.Sprintf("convert the %s first with `|> %s()`", h, fn)
	}
	return ""
}

// hasImpl reports whether t's type has an impl of the interface iface,
// without recording a demand for it as typeImplementsInterface does: help
// text is not a use.
func (c *checker) hasImpl(t Type, iface string) bool {
	name := concreteTypeName(t)
	if name == "" || c.fa == nil {
		return false
	}
	declared := c.fa.Impls[name][iface]
	if !declared && c.fa.ProjectImpls != nil {
		declared = c.fa.ProjectImpls.Impls[name][iface]
	}
	return declared && c.implReceiverMatches(t, iface)
}

// typePair renders two types for one message, qualified as typef qualifies
// them when their names collide.
func (c *checker) typePair(a, b Type) (string, string) {
	s := c.typef("%s\x00%s", a, b)
	first, second, _ := strings.Cut(s, "\x00")
	return first, second
}

// withArticle is a type's name with the article it reads with ("a String",
// "an Int"), or the name alone when it does not start with a letter (a tuple,
// a function type, a record).
func withArticle(name string) string {
	if name == "" {
		return name
	}
	switch first := name[0]; {
	case strings.ContainsRune("AEIOaeio", rune(first)):
		return "an " + name
	case (first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z'):
		return "a " + name
	}
	return name
}

// argOrdinal names the argument at 0-based slot i: "first argument".
func argOrdinal(i int) string {
	words := []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth"}
	if i >= 0 && i < len(words) {
		return words[i] + " argument"
	}
	return fmt.Sprintf("argument %d", i+1)
}

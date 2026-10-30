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
	// hole is the `&1` of a `then` stage's partial capture the value
	// fills, nil when it fills the first argument of a call stage, and
	// name the parameter a named one names (`text` in `then &label(4,
	// text: &1)`), empty for a positional one.
	hole ast.Node
	name string
}

// pipeFill is the slot a pipe's value fills in its stage call: the
// prepended first argument.
type pipeFill struct {
	slot int
}

// newPipeHead is the place pipe n's value goes in its stage call.
func newPipeHead(n *ast.Binary, call *ast.Call, fill pipeFill) *pipeHead {
	return &pipeHead{pipe: n, call: call, slot: fill.slot}
}

// site is the node a mismatch is reported at: the `&1` the value fills, or
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

// argName is the argument the value fills as an error names it: "`text`"
// for a named `&1`, otherwise its position ("its first argument").
func (p *pipeHead) argName() string {
	if p.name != "" {
		return "`" + p.name + "`"
	}
	return "its " + argOrdinal(p.slot)
}

// checkThenCaptureTakes reports a `then` or `tap` stage whose partial
// capture does not take the piped value at its `&1`: `x |> then &label(4,
// &1)` with x a Fahrenheit and label's second parameter a String. The
// capture's parameter is the callee's, so the value must fit it as it would
// fit the argument written there.
func (c *checker) checkThenCaptureTakes(pipe *ast.Binary, capture *ast.Capture, piped, want Type) {
	call := capture.PartialCall()
	if call == nil || piped == nil || want == nil {
		return
	}
	if c.argMatchesParam(piped, want, c.recPos(pipe.Line, pipe.Col), RecordingKindInterfaceTypedParam) {
		return
	}
	p := &pipeHead{pipe: pipe, call: call}
	positional := 0
	for _, a := range call.Args {
		value, name := a, ""
		if na, named := a.(*ast.NamedArg); named {
			value, name = na.Value, na.Name
		}
		if ph, open := value.(*ast.Placeholder); open && ph.N == 1 {
			p.hole, p.name, p.slot = ph, name, positional
			break
		}
		if name == "" {
			positional++
		}
	}
	c.report(c.pipeHeadMismatch(p, want, piped).WithHint(c.defaultedFuncValueHint(want, piped, pipe.Left)).WithHint(mismatchHint(want, piped)))
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
			return "a Maybe may be None; handle it before this stage: give a fallback with `|> Maybe.with_default(...)`, match it with `case`, or propagate it with `?` in a function that returns a Maybe"
		case et.Name == "Result" && len(et.TypeArgs) == 2:
			return "a Result may be an Err; handle it before this stage: give a fallback with `|> Result.with_default(...)`, match it with `case`, or propagate it with `?` in a function that returns a Result"
		}
	}
	conversions := map[[2]Type]string{
		{TypeInt, TypeFloat}:     "Int.to_float",
		{TypeInt, TypeDecimal}:   "Int.to_decimal",
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
	return c.fa.HasImpl(name, iface) && c.implReceiverMatches(t, iface)
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

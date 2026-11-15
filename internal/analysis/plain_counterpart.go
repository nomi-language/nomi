package analysis

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/parser"
)

// PlainCounterpart is a call `Owner.name(value, ...)` whose result is a
// Maybe while value's type is a refinement whose own function of that name
// and arity answers the payload itself: `List.first(names)` with `names` a
// NonEmptyList, whose `NonEmptyList.first(names)` is the element. The
// program means the same either way; the hint names the plain call
// (FileAnalysis.PlainCounterparts, FileHints).
type PlainCounterpart struct {
	// Refinement is the value's type as the counterpart's owner is
	// written: `NonEmptyList`.
	Refinement string
	// Func is the function's name: `first`.
	Func string
	// Value spells the value as written (`names`), "" for one no name
	// spells.
	Value string
	// Payload is the counterpart's result, the Maybe's payload: `String`.
	Payload string
	// Owner is the extent of the callee's owner (`List`), which the fix
	// rewrites; zero when the refinement's name does not reach its type in
	// the file, where the fix would name another type or none.
	Owner ast.Span
	// Callee is the extent of the callee, `List.first`, which the hint
	// spans.
	Callee ast.Span
}

// notePlainCounterpart records call as a PlainCounterpart when it is one:
// value is its first argument (a pipe's head for a stage), valueTy that
// argument's type, result the call's type and arity its argument count,
// the piped value included.
func (c *checker) notePlainCounterpart(call *ast.Call, value ast.Node, valueTy, result Type, arity int) {
	if call == nil || value == nil || c.fa == nil {
		return
	}
	payload := maybePayload(result)
	if payload == nil {
		return
	}
	callee, ok := call.Func.(*ast.FieldAccess)
	if !ok || callee.Field == nil {
		return
	}
	switch callee.Object.(type) {
	case *ast.TypeIdent, *ast.FieldAccess:
	default:
		return
	}
	dt := refinementOf(valueTy)
	if dt == nil || dt.Name == "" || calleeText(callee.Object) == dt.Name {
		return
	}
	sym, _ := c.resolveTypeMethodSymbol(dt.Name, callee.Field.Name, dt.Origin)
	if sym == nil {
		return
	}
	ft, ok := resolveTypeVar(sym.Type).(*FuncType)
	if !ok || len(ft.Params) != arity || ft.DefaultCount != 0 || maybePayload(ft.Return) != nil {
		return
	}
	calleeSpan, ok := spanOf(callee)
	if !ok {
		return
	}
	pc := &PlainCounterpart{
		Refinement: dt.Name,
		Func:       callee.Field.Name,
		Payload:    resolveTypeVar(payload).String(),
		Callee:     calleeSpan,
	}
	if text := calleeText(unparen(value)); !strings.Contains(text, "?") {
		pc.Value = text
	}
	if visible := refinementOf(c.reg.Lookup(dt.Name)); visible != nil && visible.Name == dt.Name && visible.Origin == dt.Origin {
		if sp, ok := spanOf(callee.Object); ok {
			pc.Owner = sp
		}
	}
	if c.fa.PlainCounterparts == nil {
		c.fa.PlainCounterparts = map[*ast.Call]*PlainCounterpart{}
	}
	c.fa.PlainCounterparts[call] = pc
}

// plainCounterpartHints are the hints for fa's PlainCounterparts, from
// nodes. A call whose Maybe a `!` unwraps carries a fix that calls the
// counterpart and drops the `!`; any other reads the Maybe, which the
// counterpart's result is not, so it carries none.
func plainCounterpartHints(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	unwrapped := map[*ast.Call]*ast.Unwrap{}
	var calls []*ast.Call
	for _, node := range nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Unwrap:
				if call, ok := unparen(v.Expr).(*ast.Call); ok {
					unwrapped[call] = v
				}
			case *ast.Call:
				if fa.PlainCounterparts[v] != nil {
					calls = append(calls, v)
				}
			}
			return true
		})
	}
	var out []TypeError
	for _, call := range calls {
		pc := fa.PlainCounterparts[call]
		if pc.Callee.StartLine <= 0 {
			continue
		}
		value := pc.Value
		if value == "" {
			value = "..."
		}
		subject := "the value"
		if pc.Value != "" {
			subject = "`" + pc.Value + "`"
		}
		e := TypeError{
			Line: pc.Callee.StartLine, Col: pc.Callee.StartCol, EndLine: pc.Callee.EndLine, EndCol: pc.Callee.EndCol,
			Code: HintPlainCounterpart,
			Message: fmt.Sprintf("%s is %s: `%s.%s(%s)` is %s, not a Maybe",
				subject, withArticle(pc.Refinement), pc.Refinement, pc.Func, value, withArticle(pc.Payload)),
		}
		if u := unwrapped[call]; u != nil && pc.Owner.StartLine > 0 && u.Line > 0 {
			e.Fix = &parser.Fix{
				Title: fmt.Sprintf("Call `%s.%s` and drop the `!`", pc.Refinement, pc.Func),
				Edits: []parser.Edit{
					{Start: parser.Pos{Line: pc.Owner.StartLine, Col: pc.Owner.StartCol}, End: parser.Pos{Line: pc.Owner.EndLine, Col: pc.Owner.EndCol}, Text: pc.Refinement},
					{Start: parser.Pos{Line: u.Line, Col: u.Col}, End: parser.Pos{Line: u.Line, Col: u.Col + 1}, Text: ""},
				},
			}
		}
		out = append(out, e)
	}
	return out
}

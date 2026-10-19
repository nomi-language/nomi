package hoverdoc

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Hover on a lambda's `fn` and `->`, and on a function declaration's `fn`.
//
// A lambda's `fn` and the `->` after its parameters show its function type
// with the parameters' names (`fn monster: Monster -> List<String>`), a
// seeded parameter's default, and where the parameter types came from: the
// parameter of the call it is passed to, the piped value of a `then` or
// `tap` stage, the expected type, or the annotation. A declaration's `fn`
// shows what its name shows.

// capture claims the `&` of a capture (`&(&1 > 2)`, `&Int.max(&1, 0)`),
// which shows the function's type and where its parameter types came from,
// as a lambda's `fn` does, and each `&N` of its body, which shows which of
// the function's parameters it is. A partial capture's open arguments keep
// the hover of the parameter they leave open.
func (s *shapeSearch) capture(n *ast.Capture) {
	size := spanSize(spanOf(n))
	if size <= 0 {
		size = 1 << 30
	}
	s.claim(n.Line, n.Col, 1, size, func() string { return s.renderCapture(n) })
	for _, h := range n.Holes() {
		if p, ok := h.(*ast.PlaceholderParam); ok {
			s.claim(p.Line, p.Col, 1+len(strconv.Itoa(p.N)), size, func() string { return s.renderCaptureParam(n, p) })
		}
	}
}

// captureSignature is the capture's function type, `(Int, Int) -> Int`,
// and its parameters' types; ok is false when the checker gave it none.
func (s *shapeSearch) captureSignature(n *ast.Capture) (sig string, params []string, ok bool) {
	ft, _ := analysis.ResolveTypeVar(s.fa.ExprTypes[n]).(*analysis.FuncType)
	if ft == nil {
		return "", nil, false
	}
	params = make([]string, len(ft.Params))
	for j, p := range ft.Params {
		if params[j] = s.pipeType(p); params[j] == "" {
			params[j] = "_"
		}
	}
	result := s.pipeType(ft.Return)
	if result == "" {
		result = "_"
	}
	return "(" + strings.Join(params, ", ") + ") -> " + result, params, true
}

// renderCapture is the hover of a capture's `&`.
func (s *shapeSearch) renderCapture(n *ast.Capture) string {
	sig, _, ok := s.captureSignature(n)
	if !ok {
		return ""
	}
	out := fence(sig)
	if from := s.captureSource(n); from != "" {
		out += "\n\n" + from
	}
	return out
}

// captureSource says where a capture's parameter types came from: the
// parameter of the call it is passed to, the piped value of a `then` or
// `tap` stage, the callee of a partial capture, or the expected type.
func (s *shapeSearch) captureSource(n *ast.Capture) string {
	if arg, ok := s.captureArgs[n]; ok {
		if fills := s.filledParam(arg); fills != "" {
			return "Fills " + fills + ", which gives its parameter types."
		}
	}
	if stage := s.captureStages[n]; stage != "" {
		return "The `" + stage + "` stage's capture; `&1` is the piped value."
	}
	if call := n.PartialCall(); call != nil {
		if callee := calleeText(call.Func); callee != "" {
			return "A partial application of `" + callee + "`: its parameters are `" + callee + "`'s at the `&N` it leaves open, with their names and defaults."
		}
	}
	if want := s.fa.ExpectedTypes[n]; want != nil {
		if spelled := s.pipeType(want); spelled != "" {
			return "Parameter types from the expected type `" + spelled + "`."
		}
	}
	return ""
}

// renderCaptureParam is the hover of the `&N` h of the capture n.
func (s *shapeSearch) renderCaptureParam(n *ast.Capture, h *ast.PlaceholderParam) string {
	sig, params, ok := s.captureSignature(n)
	if !ok || h.N < 1 || h.N > len(params) {
		return ""
	}
	return fence(sig) + "\n\n" +
		"`&" + strconv.Itoa(h.N) + "` is the capture's " + ordinalWord(h.N) + " parameter, of type `" + params[h.N-1] + "`."
}

// ordinalWord spells n as an ordinal: first, second, ..., 12th.
func ordinalWord(n int) string {
	words := []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth"}
	if n >= 1 && n <= len(words) {
		return words[n-1]
	}
	return strconv.Itoa(n) + "th"
}

// lambdaArg is where a lambda stands as a call's argument: the call and
// the parameter it fills.
type lambdaArg struct {
	call *ast.Call
	slot int
}

// lambda claims the `fn` and the `->` of l.
func (s *shapeSearch) lambda(l *ast.Lambda) {
	size := spanSize(spanOf(l))
	if size <= 0 {
		size = 1 << 30
	}
	render := func() string { return s.renderLambda(l) }
	s.claim(l.Line, l.Col, 2, size, render)
	if l.ArrowLine > 0 {
		s.claim(l.ArrowLine, l.ArrowCol, 2, size, render)
	}
}

// funcKeyword claims the `fn` of a function declaration: the hover of its
// name.
func (s *shapeSearch) funcKeyword(fd *ast.FuncDef) {
	if fd.FnCol <= 0 || fd.Line <= 0 {
		return
	}
	size := spanSize(spanOf(fd))
	if size <= 0 {
		size = 1 << 30
	}
	s.claim(fd.Line, fd.FnCol, 2, size, func() string {
		sym, _, _, ok := s.fa.TokenAt(analysis.Pos{Line: fd.Line, Col: fd.Col})
		if !ok {
			return ""
		}
		return RenderForEditor(sym, s.fa)
	})
}

// noteLambdaArgs records each lambda among call's arguments with the
// parameter it fills. piped is whether a pipe passes call its first
// argument.
func (s *shapeSearch) noteLambdaArgs(call *ast.Call) {
	piped := s.pipedCalls[call]
	positional := 0
	for _, a := range call.Args {
		var fn ast.Node
		slot := -1
		switch a := a.(type) {
		case *ast.NamedArg:
			fn = a.Value
			for j, n := range paramNamesOf(s.calleeSymbol(call.Func)) {
				if n == a.Name {
					slot = j
				}
			}
		default:
			fn = a
			slot = positional
			if piped {
				slot++
			}
			positional++
		}
		if slot < 0 {
			continue
		}
		switch fn := fn.(type) {
		case *ast.Lambda:
			if s.lambdaArgs == nil {
				s.lambdaArgs = map[*ast.Lambda]lambdaArg{}
			}
			s.lambdaArgs[fn] = lambdaArg{call: call, slot: slot}
		case *ast.Capture:
			if s.captureArgs == nil {
				s.captureArgs = map[*ast.Capture]lambdaArg{}
			}
			s.captureArgs[fn] = lambdaArg{call: call, slot: slot}
		}
	}
}

func (s *shapeSearch) renderLambda(l *ast.Lambda) string {
	ft, _ := analysis.ResolveTypeVar(s.fa.ExprTypes[l]).(*analysis.FuncType)
	if ft == nil {
		return ""
	}
	typeOr := func(t analysis.Type) string {
		if spelled := s.pipeType(t); spelled != "" {
			return spelled
		}
		return "_"
	}
	params := make([]string, len(l.Params))
	for i, p := range l.Params {
		params[i] = ParamDisplayName(p)
		if i < len(ft.Params) {
			params[i] += ": " + typeOr(ft.Params[i])
		}
		if p.Default != nil {
			def := renderConstValue(p.Default)
			if id, ok := p.Default.(*ast.Ident); ok {
				def = id.Name
			}
			if def == "" {
				def = "…"
			}
			params[i] += " = " + def
		}
	}
	sig := "fn "
	if len(params) > 0 {
		sig += strings.Join(params, ", ") + " "
	}
	sig += "-> " + typeOr(ft.Return)
	out := fence(sig)
	if from := s.lambdaSource(l); from != "" {
		out += "\n\n" + from
	}
	return out
}

// lambdaSource says where a lambda's parameter types came from, and for a
// lambda passed as an argument, which parameter it fills.
func (s *shapeSearch) lambdaSource(l *ast.Lambda) string {
	annotated := len(l.Params) > 0
	for _, p := range l.Params {
		if p.TypeAnnotation == nil {
			annotated = false
		}
	}
	if arg, ok := s.lambdaArgs[l]; ok {
		fills := s.filledParam(arg)
		switch {
		case fills == "":
		case len(l.Params) == 0:
			return "Fills " + fills + "."
		case annotated:
			return "Fills " + fills + "; parameter types from the annotation."
		default:
			return "Fills " + fills + ", which gives its parameter types."
		}
	}
	switch stage := s.lambdaStages[l]; {
	case stage != "" && len(l.Params) > 0 && !annotated:
		return "The `" + stage + "` stage's lambda; its parameter is the piped value."
	case stage != "":
		return "The `" + stage + "` stage's lambda."
	}
	if annotated {
		return "Parameter types from the annotation."
	}
	if want := s.fa.ExpectedTypes[l]; want != nil && len(l.Params) > 0 {
		if spelled := s.pipeType(want); spelled != "" {
			return "Parameter types from the expected type `" + spelled + "`."
		}
	}
	return ""
}

// filledParam is the parameter a lambda argument fills, as
// "`Maybe.map`'s `f: (T) -> U`", or "" when it is not known.
func (s *shapeSearch) filledParam(arg lambdaArg) string {
	callee := calleeText(arg.call.Func)
	if callee == "" {
		return ""
	}
	sym := s.calleeSymbol(arg.call.Func)
	if p, ok := declaredParam(sym, arg.slot); ok {
		text := ParamDisplayName(p)
		if p.TypeAnnotation != nil {
			text += ": " + p.TypeAnnotation.TypeString()
		}
		return "`" + callee + "`'s `" + text + "`"
	}
	return "argument " + strconv.Itoa(arg.slot+1) + " of `" + callee + "`"
}

// declaredParam is the declared parameter at slot of the function sym
// names.
func declaredParam(sym *analysis.Symbol, slot int) (ast.Param, bool) {
	if sym == nil {
		return ast.Param{}, false
	}
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	var params []ast.Param
	switch n := real.Node.(type) {
	case *ast.FuncDef:
		params = n.Params
	case *ast.ExternFunc:
		params = n.Params
	case *ast.InterfaceMethod:
		params = n.Params
	}
	if slot < 0 || slot >= len(params) {
		return ast.Param{}, false
	}
	return params[slot], true
}

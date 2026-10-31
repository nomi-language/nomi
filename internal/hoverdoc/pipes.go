package hoverdoc

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// Hover on a pipe's `|>`: the type flowing into the stage, what the stage
// does with it, and the type that comes out. The left side of `|>` is the
// whole chain above it, so this is the one place an intermediate type of a
// pipeline shows.
//
// Every type shown is one the checker recorded (FileAnalysis.ExprTypes of
// the pipe's left side and of the pipe). A pipe whose left side has no type
// shows nothing, and one whose stage failed to type shows the incoming type
// and the stage without a result.

// pipe claims both characters of the `|>` of b. The parser gives a `?`
// stage (`|> f()?`) and a stage-prefixed `if` or `case` (`|> case f() {`) a
// second pipe around the first, at the `?` or the keyword or at column 0; that pipe has no `|>` of its own, and the hover of the `|>`
// before it describes both.
func (s *shapeSearch) pipe(b *ast.Binary) {
	if pipeWithoutOperator(b) {
		if inner, ok := b.Left.(*ast.Binary); ok && inner.Op == "|>" {
			if s.pipeOuter == nil {
				s.pipeOuter = map[*ast.Binary]*ast.Binary{}
			}
			s.pipeOuter[inner] = b
		}
		return
	}
	size := spanSize(spanOf(b))
	if size <= 0 {
		size = 1 << 30
	}
	s.claim(b.Line, b.Col, 2, size, func() string { return s.renderPipe(b) })
}

// pipeWithoutOperator reports a pipe the parser built around a prefixed
// stage, whose position is not a `|>`.
func pipeWithoutOperator(b *ast.Binary) bool {
	if b.Col == 0 {
		return true
	}
	switch r := b.Right.(type) {
	case *ast.If:
		return r.StagePrefix && r.Line == b.Line && r.Col == b.Col
	case *ast.TryOp:
		return r.Expr == nil && r.Line == b.Line && r.Col == b.Col
	case *ast.Case:
		return r.StagePrefix && r.Line == b.Line && r.Col == b.Col
	}
	return false
}

// pipeStage is one stage's part of the hover: how the stage is written in
// the summary line, the type it answers ("" when the checker recorded none),
// and the lines that explain it.
type pipeStage struct {
	text   string
	out    string
	detail []string
}

func (s *shapeSearch) renderPipe(b *ast.Binary) string {
	in := s.pipeType(s.fa.ExprTypes[b.Left])
	if in == "" {
		return ""
	}
	stages := []pipeStage{s.describeStage(b, in)}
	cur := b
	for outer := s.pipeOuter[cur]; outer != nil; outer = s.pipeOuter[cur] {
		prev := stages[len(stages)-1].out
		if prev == "" {
			break
		}
		stages = append(stages, s.describeStage(outer, prev))
		cur = outer
	}
	var line strings.Builder
	line.WriteString("`" + in + "`")
	var details []string
	for _, st := range stages {
		line.WriteString(" → `" + st.text + "`")
		if st.out != "" {
			line.WriteString(" → `" + st.out + "`")
		}
		details = append(details, st.detail...)
		if st.out == "" {
			break
		}
	}
	return strings.Join(append([]string{line.String()}, details...), "\n\n")
}

// pipeType is a recorded type as the hover spells it, or "" for no type or
// one the checker left unsolved.
func (s *shapeSearch) pipeType(t analysis.Type) string {
	if t == nil {
		return ""
	}
	spelled := t.String()
	if strings.Contains(spelled, "?") {
		return ""
	}
	if analysis.ContainsTypeParam(t) {
		return spelled
	}
	return displayValueType(t)
}

// describeStage is the stage of pipe b, which takes a value of type in.
func (s *shapeSearch) describeStage(b *ast.Binary, in string) pipeStage {
	out := s.pipeType(s.fa.ExprTypes[b])
	switch r := b.Right.(type) {
	case *ast.Call:
		return s.callStage(r, in, out)
	case *ast.Unwrap:
		// `|> String.to_int()!`: the call's result, unwrapped.
		if call, isCall := r.Expr.(*ast.Call); isCall {
			st := s.callStage(call, in, out)
			st.text += "!"
			return st
		}
	case *ast.Unary:
		// `|> not Set.contains(seen)`: the stage's result, negated.
		if r.Op == "not" {
			inner := *b
			inner.Right = r.Right
			st := s.describeStage(&inner, in)
			st.text = "not " + st.text
			st.out = out
			return st
		}
	case *ast.Then:
		if r.Capture != nil {
			return pipeStage{text: "then " + format.RenderNode(r.Capture), out: out,
				detail: []string{"`&1` is the `" + in + "`; the stage answers the capture's result."}}
		}
		return pipeStage{text: "then fn " + lambdaParams(r.Lambda) + " -> …", out: out,
			detail: []string{lambdaBinds(r.Lambda, in) + "; the stage answers the lambda's result."}}
	case *ast.Tap:
		if r.Capture != nil {
			return pipeStage{text: "tap " + format.RenderNode(r.Capture), out: out,
				detail: []string{"`&1` is the `" + in + "`; the capture runs for its effect and the value passes on unchanged."}}
		}
		return pipeStage{text: "tap fn " + lambdaParams(r.Lambda) + " -> …", out: out,
			detail: []string{lambdaBinds(r.Lambda, in) + "; the lambda runs for its effect and the value passes on unchanged."}}
	case *ast.TryOp:
		return s.tryStage(r, in, out)
	case *ast.Dbg:
		if r.Expr != nil {
			return pipeStage{text: "dbg …"}
		}
		return pipeStage{text: "dbg", out: out,
			detail: []string{"Prints the value with its source line and passes it on unchanged."}}
	case *ast.If:
		return pipeStage{text: "if", out: out,
			detail: []string{"Branches on the piped `" + in + "`."}}
	case *ast.Case:
		return pipeStage{text: "case", out: out,
			detail: []string{"Matches the piped `" + in + "` against the arms."}}
	case *ast.Todo:
		return pipeStage{text: "todo",
			detail: []string{"Stops the program here when it runs; the pipeline has the type its position expects."}}
	case *ast.Unreachable:
		return pipeStage{text: "unreachable",
			detail: []string{"Claims the program never gets here, and traps if it does; the pipeline has the type its position expects."}}
	case *ast.Assertion:
		return pipeStage{text: assertionWord(r)}
	case *ast.Lambda:
		return pipeStage{text: "fn " + lambdaParams(r) + " -> …"}
	}
	return pipeStage{text: stageName(b.Right)}
}

func assertionWord(a *ast.Assertion) string {
	if a.Refute {
		return "refute"
	}
	return "assert"
}

// stageName is a stage that is not a call, as written: `f`, `.name`.
func stageName(n ast.Node) string {
	if t := calleeText(n); t != "" {
		return t
	}
	if acc, ok := n.(*ast.FieldAccessor); ok && acc != nil {
		parts := make([]string, len(acc.Path))
		for i, id := range acc.Path {
			parts[i] = id.Name
		}
		return "." + strings.Join(parts, ".")
	}
	return "…"
}

// lambdaParams is a lambda's parameter list as written.
func lambdaParams(l *ast.Lambda) string {
	if l == nil {
		return ""
	}
	names := make([]string, len(l.Params))
	for i, p := range l.Params {
		names[i] = ParamDisplayName(p)
	}
	return strings.Join(names, ", ")
}

// lambdaBinds says what a `then` or `tap` lambda's parameter binds.
func lambdaBinds(l *ast.Lambda, in string) string {
	if l == nil || len(l.Params) != 1 {
		return "The lambda takes the `" + in + "`"
	}
	p := l.Params[0]
	if p.Destructure != nil {
		return "`" + ParamDisplayName(p) + "` matches the `" + in + "`"
	}
	return "`" + p.Name + "` is the `" + in + "`"
}

// tryStage is a `?` stage: it unwraps a Result or a Maybe, or returns the
// failure from the function, lambda or test that encloses it.
func (s *shapeSearch) tryStage(t *ast.TryOp, in, out string) pipeStage {
	st := pipeStage{text: "?", out: out}
	if t.Expr != nil {
		st.text, st.out = "…?", ""
		return st
	}
	failure := "the failure"
	if strings.HasPrefix(in, "Result<") {
		failure = "the `Err`"
	} else if strings.HasPrefix(in, "Maybe<") {
		failure = "`None`"
	}
	boundary := ""
	if sym, _, _, ok := s.fa.TokenAt(analysis.Pos{Line: t.Line, Col: t.Col}); ok && sym.TryOp != nil {
		boundary = sym.TryOp.Boundary
	}
	switch {
	case out == "":
	case boundary == "lambda":
		st.detail = []string{"Answers the success value, or returns " + failure + " from the enclosing lambda."}
	case boundary != "":
		st.detail = []string{"Answers the success value, or returns " + failure + " from `" + boundary + "`."}
	default:
		st.detail = []string{"Answers the success value, or returns " + failure + "."}
	}
	return st
}

// callStage is a call stage: the callee with its parameters, its
// signature, and the parameter the piped value fills.
func (s *shapeSearch) callStage(call *ast.Call, in, out string) pipeStage {
	name := calleeText(call.Func)
	if name == "" {
		name = "…"
	}
	sym := s.calleeSymbol(call.Func)
	st := pipeStage{out: out}
	if sym != nil && sym.Kind == analysis.SymbolEnumVariant {
		st.text = name + "(…)"
		st.detail = []string{firstFence(renderVariant(sym, false)), "The piped value is the payload."}
		return st
	}
	if sym != nil && isTypeSymbol(sym) {
		st.text = name + "(…)"
		if sig := firstFence(RenderForEditor(sym, s.fa)); sig != "" {
			st.detail = append(st.detail, sig)
		}
		st.detail = append(st.detail, "Builds the type from the piped value.")
		return st
	}
	names := paramNamesOf(sym)
	ft, _ := s.calleeFuncType(call.Func, sym).(*analysis.FuncType)
	if names == nil && ft != nil {
		// A function value's parameters have no names: write their types.
		names = make([]string, len(ft.Params))
		for i, p := range ft.Params {
			names[i] = s.pipeType(p)
			if names[i] == "" {
				names[i] = "_"
			}
		}
	}
	st.text = name + "(" + strings.Join(names, ", ") + ")"
	if sym != nil {
		if sig := firstFence(RenderWithAnalysis(sym, s.fa)); sig != "" {
			st.detail = append(st.detail, sig)
		}
	}
	if sym != nil || ft != nil {
		st.detail = append(st.detail, fillPhrase(paramNamesOf(sym)))
	}
	return st
}

// isTypeSymbol reports a symbol naming a type, whose call form builds it.
func isTypeSymbol(sym *analysis.Symbol) bool {
	real := sym
	if real.Resolved != nil {
		real = real.Resolved
	}
	switch real.Node.(type) {
	case *ast.StructDef, *ast.TypeDef, *ast.EnumDef:
		return real.Kind != analysis.SymbolFunction
	}
	return false
}

// calleeFuncType is the callee's function type: the instantiation the call
// recorded, or the callee expression's type.
func (s *shapeSearch) calleeFuncType(callee ast.Node, sym *analysis.Symbol) analysis.Type {
	if sym != nil && sym.CallType != nil {
		return sym.CallType
	}
	return analysis.ResolveTypeVar(s.fa.ExprTypes[callee])
}

// fillPhrase says which parameter the piped value fills: the first.
func fillPhrase(names []string) string {
	if len(names) > 0 {
		return "The piped value fills `" + names[0] + "`."
	}
	return "The piped value fills argument 1."
}

// paramNamesOf is the declared parameter names of the function sym names,
// or nil for a symbol with no declaration (a function value).
func paramNamesOf(sym *analysis.Symbol) []string {
	if sym == nil {
		return nil
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
	default:
		return nil
	}
	if d, ok := sym.DispatchImpl.(*ast.FuncDef); ok {
		params = d.Params
	}
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = ParamDisplayName(p)
	}
	return names
}

// calleeSymbol is the symbol the analysis records at the last name of a
// callee: `filter` of `Iter.filter`.
func (s *shapeSearch) calleeSymbol(callee ast.Node) *analysis.Symbol {
	var line, col int
	switch c := callee.(type) {
	case *ast.Ident:
		line, col = c.Line, c.Col
	case *ast.TypeIdent:
		line, col = c.Line, c.Col
	case *ast.FieldAccess:
		if c.Field == nil {
			return nil
		}
		line, col = c.Field.Line, c.Field.Col
	case *ast.DotVariant:
		line, col = c.Line, c.Col+1
	default:
		return nil
	}
	sym, _, _, ok := s.fa.TokenAt(analysis.Pos{Line: line, Col: col})
	if !ok {
		return nil
	}
	return sym
}

// calleeText is a callee as written: `f`, `Iter.filter`, `Shape.Dot`.
func calleeText(n ast.Node) string {
	switch c := n.(type) {
	case *ast.Ident:
		return c.Name
	case *ast.TypeIdent:
		return c.Name
	case *ast.DotVariant:
		return "." + c.Name
	case *ast.FieldAccess:
		obj := calleeText(c.Object)
		if obj == "" || c.Field == nil {
			return ""
		}
		return obj + "." + c.Field.Name
	}
	return ""
}

// firstFence is the first fenced block of a hover, fence included.
func firstFence(md string) string {
	const open = "```nomi\n"
	i := strings.Index(md, open)
	if i < 0 {
		return ""
	}
	end := strings.Index(md[i+len(open):], "\n```")
	if end < 0 {
		return ""
	}
	return md[i : i+len(open)+end+len("\n```")]
}

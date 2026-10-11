package irbuild

// The debug tables a retained function carries (internal/ir/debuginfo.go):
// its variables, recorded where each name becomes visible, and its statement
// marks, recorded where each statement, arm, iteration and pipe stage
// starts lowering.

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irDebugName reports whether name is one a programmer wrote and a debugger
// should show: not a discard, and not a name the builder or the front end
// made up (`__destr_3_9`, `$state`).
func irDebugName(name string) bool {
	if name == "" || ast.IsDiscardName(name) || name[0] == '$' {
		return false
	}
	return len(name) < 2 || name[:2] != "__"
}

// irAddVar records name, held in t, as a variable of f visible from (line,
// col) to the end of the block, arm, lambda or function that encloses it in
// root, and bound at decl. A nil root, a synthesized line or a made-up name
// records nothing but the first; the scope then reaches the end of the
// function.
func (g *gen) irAddVar(f *ir.Func, root ast.Node, name string, t ir.Temp, k kind, line, col int, decl ir.Pos) {
	if f == nil || t == ir.NoTemp || !irDebugName(name) || !emittableLine(line) || col < 1 {
		return
	}
	endLine, endCol := irScopeEnd(root, line, col)
	f.AddVar(name, t, debugSpelling(k), ir.Spanning(g.nomiPath, line, col, endLine, endCol), decl)
}

// irNameSite is the extent of name written at (line, col), its end
// exclusive and its columns bytes as the parser's spans are, or the zero
// Pos for a position the source does not have.
func (g *gen) irNameSite(name string, line, col int) ir.Pos {
	if !emittableLine(line) || col < 1 {
		return ir.Pos{}
	}
	return ir.Spanning(g.nomiPath, line, col, line, col+len(name))
}

// irBindingSite is where pattern n writes the name it binds: the name
// itself, found inside n (`Some(n)`'s payload, a struct pattern's field
// binding, the name after `as`), or n's own position when it is not found.
func irBindingSite(n ast.Node, name string) (int, int) {
	line, col := 0, 0
	ast.Inspect(n, func(m ast.Node) bool {
		if line != 0 {
			return false
		}
		switch p := m.(type) {
		case *ast.AsPattern:
			if p.Name == name && p.NameLine > 0 {
				line, col = p.NameLine, p.NameCol
			}
		case *ast.IdentPattern:
			if p.Name == name {
				line, col = p.Line, p.Col
			}
		case *ast.Ident:
			if p.Name == name {
				line, col = p.Line, p.Col
			}
		case *ast.EnumPattern:
			if p.Binding == name && p.BindingCol > 0 {
				line, col = p.Line, p.BindingCol
			}
		case *ast.StructPattern:
			for _, f := range p.Fields {
				if f.Binding == name && f.BindingLine > 0 {
					line, col = f.BindingLine, f.BindingCol
					break
				}
			}
		}
		return line == 0
	})
	if line == 0 {
		return nodePos(n)
	}
	return line, col
}

// debugSpelling is k as a type annotation spells it, which a debugger's watch
// expression is type-checked against: kind.nomi() with the type arguments of
// every generic struct or enum instance written out (`Box<Int>`, not the
// declaration's bare `Box`), at any depth.
func debugSpelling(k kind) string {
	if k.comp != nil {
		return respell(k.comp.nomi, k.comp.parts)
	}
	if k.tag == tagNamed && k.def != nil {
		// A user generic's instance is spelled as its declaration, a prelude
		// instance (`Maybe<Box>`) with its arguments' plain spellings.
		args := k.def.genericArgs
		if len(args) == 0 {
			args = k.def.preludeArgs
		}
		if len(args) > 0 {
			base, _, _ := strings.Cut(k.def.nomi, "<")
			spelled := make([]string, len(args))
			for i, a := range args {
				spelled[i] = debugSpelling(a)
			}
			return base + "<" + strings.Join(spelled, ", ") + ">"
		}
	}
	return k.nomi()
}

// respell is a composite's spelling with each component's own spelling
// replaced by its debugSpelling. The spelling names the components in the
// order parts lists them, so each is found after the one before it.
func respell(text string, parts []kind) string {
	var b strings.Builder
	rest := text
	for _, p := range parts {
		plain, better := p.nomi(), debugSpelling(p)
		i := strings.Index(rest, plain)
		if i < 0 {
			return text
		}
		b.WriteString(rest[:i])
		b.WriteString(better)
		rest = rest[i+len(plain):]
	}
	b.WriteString(rest)
	return b.String()
}

// declareVar is irAddVar for the body this builder lowers, for a name
// bound where it is written at (line, col).
func (bl *irScalarBuilder) declareVar(name string, t ir.Temp, k kind, line, col int) {
	bl.g.irAddVar(bl.f, bl.scopeRoot(), name, t, k, line, col, bl.g.irNameSite(name, line, col))
}

// scopeRoot is the source a variable's scope is measured in: the nearest
// enclosing builder's root, since a builder that lowers no declaration of
// its own (an adapter, a default) lowers inside its parent's.
func (bl *irScalarBuilder) scopeRoot() ast.Node {
	for b := bl; b != nil; b = b.parent {
		if b.root != nil {
			return b.root
		}
	}
	return nil
}

// irScopeEnd is the end of the innermost block, `case` arm, lambda or
// function in root whose extent contains (line, col), or MaxScopeLine when
// nothing in root does.
func irScopeEnd(root ast.Node, line, col int) (int, int) {
	endLine, endCol := ir.MaxScopeLine, 0
	best := ast.Span{}
	consider := func(s ast.Span) {
		if s.IsZero() || !spanHolds(s, line, col) {
			return
		}
		if best.IsZero() || spanHolds(best, s.StartLine, s.StartCol) {
			best = s
		}
	}
	ast.Inspect(root, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.Block:
			// A block records its braces rather than a span.
			if t.EndLine != 0 {
				consider(ast.Span{StartLine: t.Line, StartCol: t.Col, EndLine: t.EndLine, EndCol: t.EndCol})
			}
		case *ast.Lambda:
			consider(t.Span)
		case *ast.FuncDef:
			consider(t.Span)
		case *ast.Case:
			// An arm reaches from its pattern to the next arm, or to the
			// end of the `case`.
			for i, br := range t.Branches {
				if isNilNode(br.Pattern) {
					continue
				}
				sl, sc := irStartPos(br.Pattern)
				arm := ast.Span{StartLine: sl, StartCol: sc, EndLine: t.Span.EndLine, EndCol: t.Span.EndCol}
				if i+1 < len(t.Branches) && !isNilNode(t.Branches[i+1].Pattern) {
					arm.EndLine, arm.EndCol = irStartPos(t.Branches[i+1].Pattern)
				}
				consider(arm)
			}
		}
		return true
	})
	if !best.IsZero() {
		endLine, endCol = best.EndLine, best.EndCol
	}
	if endLine < line || (endLine == line && endCol < col) {
		return line, col
	}
	return endLine, endCol
}

// spanHolds reports whether (line, col) is inside s.
func spanHolds(s ast.Span, line, col int) bool {
	if line < s.StartLine || line > s.EndLine {
		return false
	}
	if line == s.StartLine && col < s.StartCol {
		return false
	}
	return line != s.EndLine || col <= s.EndCol
}

// markStmt marks the next instruction of the open block as the start of a
// statement at n.
func (bl *irScalarBuilder) markStmt(n ast.Node, kind ir.StmtKind) {
	if isNilNode(n) || bl.b == nil || bl.synthesized {
		return
	}
	// A node the parser did not build has no extent, and is code the
	// builder or a desugaring made up: never a stop.
	if sc, has := n.(interface{ GetSpan() ast.Span }); !has || sc.GetSpan().IsZero() {
		return
	}
	line, col := irStartPos(n)
	if !emittableLine(line) || col < 1 {
		return
	}
	bl.b.MarkStmt(bl.g.irPos(line, col), kind)
}

// irStartPos is where n's text starts: its first token, which for a call is
// the callee rather than the `(` its position names.
func irStartPos(n ast.Node) (int, int) {
	if sc, has := n.(interface{ GetSpan() ast.Span }); has {
		if s := sc.GetSpan(); !s.IsZero() && s.StartLine > 0 {
			return s.StartLine, s.StartCol
		}
	}
	return nodePos(n)
}

// declareBind records the name a Bind introduces, visible from the Bind,
// whose position is where the name is written.
func (bl *irScalarBuilder) declareBind(b *ir.Bind, k kind) {
	p := b.Pos()
	bl.declareBindAt(b, k, p.Line(), p.Col())
}

// declareBindAt is declareBind for a Bind positioned at the construct that
// binds the name (a destructuring statement, a pattern, a loop's state)
// rather than at the name, which is written at (line, col).
func (bl *irScalarBuilder) declareBindAt(b *ir.Bind, k kind, line, col int) {
	p := b.Pos()
	if p.Synthesized() {
		return
	}
	name := b.Sym().Name()
	bl.g.irAddVar(bl.f, bl.scopeRoot(), name, b.Dst(), k, p.Line(), p.Col(), bl.g.irNameSite(name, line, col))
}

// declareCapture records a value a closure captured under its Nomi name,
// visible in the whole of the closure's body, which starts after its
// parameters, and bound at decl, the binding it captured.
func (bl *irScalarBuilder) declareCapture(name string, t ir.Temp, k kind, decl ir.Pos) {
	if bl.root == nil {
		return
	}
	line, col := irStartPos(bl.root)
	if lam, isLambda := bl.root.(*ast.Lambda); isLambda {
		if n := len(lam.Params); n > 0 && emittableLine(lam.Params[n-1].Line) {
			// Past the last parameter, so the parameters list first.
			line, col = lam.Params[n-1].Line, lam.Params[n-1].Col+1
		}
		if lam.Body != nil && lam.Body.EndLine != 0 {
			line, col = lam.Body.Line, lam.Body.Col
		}
	}
	bl.g.irAddVar(bl.f, bl.scopeRoot(), name, t, k, line, col, decl)
}

// capturedDecl is where the binding a closure captures from its parent as
// src was bound: the parent's variable of that name held in src, or the
// zero Pos.
func (bl *irScalarBuilder) capturedDecl(name string, src ir.Temp) ir.Pos {
	if bl.parent == nil || bl.parent.f == nil {
		return ir.Pos{}
	}
	vars := bl.parent.f.Vars()
	for i := len(vars) - 1; i >= 0; i-- {
		if vars[i].Name == name && vars[i].Temp == src {
			return vars[i].Decl
		}
	}
	return ir.Pos{}
}

// stageAfter arranges for stage to be marked once left, the operand piped
// into it, is lowered, and answers the undo for when the pipe is done.
func (bl *irScalarBuilder) stageAfter(left, stage ast.Node) func() {
	if bl.stages == nil {
		bl.stages = map[ast.Node]ast.Node{}
	}
	bl.stages[left] = stage
	return func() { delete(bl.stages, left) }
}

// stageReached marks the stage n feeds, if n is a pipe's left operand.
func (bl *irScalarBuilder) stageReached(n ast.Node) {
	if stage, piped := bl.stages[n]; piped {
		delete(bl.stages, n)
		bl.markStmt(stage, ir.StmtStage)
	}
}

// pipeVal is one lowered value of a pipeline not yet finished.
type pipeVal struct {
	node ast.Node
	text string
	t    ir.Temp
	pos  ir.Pos
}

// pipeValue records n's value t when n is a pipeline's head or one of its
// stages (a pipe whose right operand is the stage), and records the whole
// pipeline as an ir.Pipe when n is its outermost pipe.
func (bl *irScalarBuilder) pipeValue(n ast.Node, t ir.Temp) {
	if bl.f == nil || bl.synthesized || t == ir.NoTemp {
		return
	}
	_, isLeft := bl.stages[n]
	pipe, isPipe := n.(*ast.Binary)
	isPipe = isPipe && pipe.Op == "|>"
	switch {
	case isPipe:
		right := pipe.Right
		for {
			grouped, isGrouped := right.(*ast.GroupedExpr)
			if !isGrouped {
				break
			}
			right = grouped.Expr
		}
		bl.pushPipeVal(right, "|> "+pipeText(right), t)
		if !isLeft {
			bl.finishPipe(pipe)
		}
	case isLeft:
		bl.pushPipeVal(n, pipeText(n), t)
	}
}

func (bl *irScalarBuilder) pushPipeVal(n ast.Node, text string, t ir.Temp) {
	line, col := irStartPos(n)
	if !emittableLine(line) || col < 1 {
		return
	}
	bl.pipeVals = append(bl.pipeVals, pipeVal{node: n, text: text, t: t, pos: bl.g.irPos(line, col)})
}

// finishPipe records the values of the pipeline root spans as one ir.Pipe.
func (bl *irScalarBuilder) finishPipe(root *ast.Binary) {
	span := root.GetSpan()
	if span.IsZero() || !emittableLine(span.StartLine) {
		bl.pipeVals = nil
		return
	}
	var stages []ir.PipeStage
	keep := bl.pipeVals[:0]
	for _, v := range bl.pipeVals {
		if spanHolds(span, v.pos.Line(), v.pos.Col()) {
			stages = append(stages, ir.PipeStage{Text: v.text, Temp: v.t, Pos: v.pos})
			continue
		}
		keep = append(keep, v)
	}
	bl.pipeVals = keep
	if len(stages) < 2 {
		return
	}
	bl.f.AddPipe(ir.Pipe{Pos: ir.Spanning(bl.g.nomiPath, span.StartLine, span.StartCol, span.EndLine, span.EndCol),
		Stages: stages})
}

// pipeText is a stage's source on one line, cut to pipeTextLimit runes.
func pipeText(n ast.Node) string {
	text := strings.Join(strings.Fields(renderNode(n)), " ")
	if r := []rune(text); len(r) > pipeTextLimit {
		text = string(r[:pipeTextLimit-1]) + "…"
	}
	return text
}

// pipeTextLimit is how many runes of a stage's source the pipeline view
// shows.
const pipeTextLimit = 60

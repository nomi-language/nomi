package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// captureFunc lowers a capture (`&(&1 > 2)`, `&Int.max(&1, 0)`, `&#Point{x:
// &1, y: 0}`; ast.Capture) as the lambda it stands for: a function whose
// body is the capture's body with each `&N` read as parameter N. Nothing
// is evaluated when the function is made; a call of it evaluates the
// whole body, as the equivalent `fn` does. A partial capture
// (`&add(1, &1)`) takes the callee's parameters at its open slots
// (partialApplication).
//
// The body is the capture's own expression, so it lowers as the same
// expression written in a lambda would, with what the checker recorded for
// it. While it lowers, holeSubst reads each `&N` as the lambda's parameter
// N, and the lambda captures the body's other names as it captures any
// local.
func (bl *irScalarBuilder) captureFunc(t *ast.Capture) (ir.Temp, kind, bool, bool) {
	no := func(why string) (ir.Temp, kind, bool, bool) {
		irDeclineNote(why)
		return ir.NoTemp, kindInvalid, false, false
	}
	if bl.g.fa == nil {
		return no("a capture with no analysis")
	}
	if call := t.PartialCall(); call != nil {
		return bl.partialApplication(t, call)
	}
	ft, _ := analysis.ResolveTypeVar(bl.g.srcFA().ExprTypes[t]).(*analysis.FuncType)
	holes := t.Holes()
	if ft == nil || len(holes) == 0 {
		return no("a capture the checker gave no function type")
	}
	for _, h := range holes {
		if n := ast.HoleNumber(h); n < 1 || n > len(ft.Params) {
			return no("an `&N` past the arity the checker gave its capture")
		}
	}
	kinds := make([]kind, len(ft.Params))
	for i, p := range ft.Params {
		kinds[i] = bl.g.project(p)
		if !irCallableValueKind(kinds[i]) && kinds[i] != kindUnit {
			return no("a capture's parameter outside the domain: " + kinds[i].nomi())
		}
	}
	lam := &ast.Lambda{Line: t.Line, Col: t.Col, EndLine: t.Line, EndCol: t.Col}
	// The parameters are the checker's function type's, by number: `&N`
	// reads parameter N, a parameter no `&N` names is taken and ignored,
	// and one `&N` may be read several times. A struct literal's `&N`s are
	// called by their fields' names, as the checker names them
	// (analysis.CaptureParams); any other's by none.
	slots := partialSlots{kinds: kinds, names: make([]string, len(kinds))}
	if params := analysis.CaptureParams(t); len(params) == len(kinds) {
		for i, p := range params {
			slots.names[i] = p.Name
		}
	}
	for i := range kinds {
		lam.Params = append(lam.Params, ast.Param{Name: captureParamName(t, i+1), Line: t.Line, Col: t.Col})
	}
	subst := make(map[ast.Node]string, len(holes))
	for _, h := range holes {
		subst[h] = lam.Params[ast.HoleNumber(h)-1].Name
	}
	lam.Body = &ast.Block{Line: t.Line, Col: t.Col, Stmts: []ast.Node{t.Body}}
	if bl.partialOpen == nil {
		bl.partialOpen = map[*ast.Lambda]partialSlots{}
	}
	bl.partialOpen[lam] = slots
	defer delete(bl.partialOpen, lam)
	defer bl.g.substHoles(subst)()
	return bl.lambda(lam)
}

// captureParamName is the name the lambda a capture stands for gives its
// parameter n: one no Nomi program can spell, so no parameter shadows a
// name the body reads.
func captureParamName(t ast.Node, n int) string {
	line, col := nodePos(t)
	return "%open" + strconv.Itoa(line) + "." + strconv.Itoa(col) + "." + strconv.Itoa(n)
}

// substHoles has holeRead read each node of subst as its name until the
// function it answers is called.
func (g *gen) substHoles(subst map[ast.Node]string) func() {
	if g.holeSubst == nil {
		g.holeSubst = map[ast.Node]string{}
	}
	for n, name := range subst {
		g.holeSubst[n] = name
	}
	return func() {
		for n := range subst {
			delete(g.holeSubst, n)
		}
	}
}

// holeRead is n, or the name a capture's body reads it as while that body
// lowers (holeSubst).
func (g *gen) holeRead(n ast.Node) ast.Node {
	if name, ok := g.holeSubst[n]; ok {
		line, col := nodePos(n)
		return &ast.Ident{Name: name, Line: line, Col: col}
	}
	return n
}

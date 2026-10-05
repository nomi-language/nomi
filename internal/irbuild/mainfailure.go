package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A `fn main` declared to return `Result<T, E>` fails when it returns
// `Err(e)`: the run prints e and exits 1. The text is e's Display rendering
// when E implements Display (a String prints as itself), and its Debug
// rendering otherwise.
//
// Which rendering applies is a fact about E, which only the builder knows, so
// the entry gen builds one function, `main failure`, taking an E and
// answering that String, and records it on the module
// (ir.Module.SetMainFailure). The VM calls it with the payload of the `Err`
// main returned. A main whose result is not a Result gets none, and neither
// does an E this builder cannot render; the VM then prints the payload's
// structural text.

// irMainFailureKey is the identity of the entry's `main failure` function.
type irMainFailureKey struct{ main *ast.FuncDef }

// irMainFailureName is the function's display name. The space keeps it from
// colliding with any Nomi name.
const irMainFailureName = "main failure"

// irBuildMainFailure builds and records the entry's `main failure` function
// when the entry's `fn main` returns a Result.
func (g *gen) irBuildMainFailure() {
	if g.stdModule != "" || g.irMod == nil {
		return
	}
	if g.reg != nil && len(g.reg.gens) > 0 && g.reg.gens[0] != g {
		return
	}
	var main *ast.FuncDef
	for _, n := range g.nodes {
		if fd, isFunc := n.(*ast.FuncDef); isFunc && fd.Name == "main" && !fd.ImplFunction && len(fd.Params) == 0 {
			main = fd
			break
		}
	}
	if main == nil || main.ReturnTypeExpr == nil || main.Body == nil {
		return
	}
	result := g.typeOf(main.ReturnTypeExpr)
	anchor := g.preludeByName["Result"]
	if anchor == nil || result.tag != tagNamed || result.def == nil || result.def.preludeOf == nil ||
		result.def.preludeOf.spec != anchor.spec || len(result.def.preludeArgs) != 2 {
		return
	}
	errK := result.def.preludeArgs[1]
	g.irDeclineOpen(irMainFailureName)
	if !irCallOperandKind(errK) {
		irDeclineNote("an error type outside the call domain: " + errK.nomi())
		return
	}
	sym := g.irCalleeSym(irMainFailureKey{main: main}, irMainFailureName)
	at := ast.Node(main.ReturnTypeExpr)
	fn, ok := g.irMainFailureFunc(main, sym, errK, at, false)
	if !ok {
		fn, ok = g.irMainFailureFunc(main, sym, errK, at, true)
	}
	if !ok {
		irDeclineNote("an error type with no rendering: " + errK.nomi())
		return
	}
	g.irModule().AddFunc(fn)
	if err := ir.LintModuleAdded(g.irModule()); err != nil {
		panic("irbuild: main failure: " + err.Error())
	}
	g.irMod.SetMainFailure(sym)
}

// irMainFailureFunc builds `main failure` over one parameter of kind errK,
// rendering it through Display, or through Debug when debug is set. ok is
// false when that rendering does not lower: no Display impl for errK, or a
// kind this builder cannot render.
func (g *gen) irMainFailureFunc(main *ast.FuncDef, sym *ir.Symbol, errK kind, at ast.Node, debug bool) (*ir.Func, bool) {
	param := ast.Param{Name: "error", Line: main.Line, Col: main.Col}
	sh := g.irFuncShellAt(main, []ast.Param{param}, irFuncSig{result: kindString, name: irMainFailureName, origin: irFromModule},
		[]kind{errK}, sym)
	if sh == nil {
		return nil, false
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: kindString, sides: sh.sides,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	src := sh.paramTemps[0]
	pos := g.irNodePos(at)
	var text ir.Temp
	switch {
	case debug && irScalarLeafKind(errK):
		r := ir.NewRenderDebug(pos, bl.f.NewTemp(), src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		text = r.Dst()
	case debug:
		var rendered bool
		if text, rendered = bl.debugOver(at, src, errK); !rendered {
			return nil, false
		}
	case errK == kindString:
		// Display is the identity on a String.
		text = src
	case irScalarLeafKind(errK) || isDecimalKind(errK):
		r := ir.NewRenderDisplay(pos, bl.f.NewTemp(), src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		text = r.Dst()
	default:
		var rendered bool
		if text, rendered = bl.displayHole(at, src, errK, true); !rendered {
			return nil, false
		}
	}
	cp := ir.NewCopy(pos, sh.result, text)
	bl.b.Append(cp)
	bl.side(sh.result, irScalarSide{k: kindString})
	bl.b.SetTerm(ir.NewReturn(pos, sh.result))
	if err := ir.Lint(sh.fn); err != nil {
		return nil, false
	}
	return sh.fn, true
}

package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A `fn main` declared to return `Result<T, E>` fails when it returns
// `Err(e)`: the run prints e and exits 1. An entry boot, which always returns
// `Result<App, E>`, fails the same way, and its renderer is the `boot
// failure` function irBuildBootFailure records with ir.Module.SetBootFailure. The text is e's Display rendering
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
	anchor, _ := g.preludeNamed("Result")
	if anchor == nil || result.tag != tagNamed || result.def == nil || result.def.preludeOf == nil ||
		result.def.preludeOf.spec != anchor.spec || len(result.def.preludeArgs) != 2 {
		return
	}
	errK := result.def.preludeArgs[1]
	if sym := g.irBuildFailureRender(main, irMainFailureKey{main: main}, irMainFailureName, errK); sym != nil {
		g.irMod.SetMainFailure(sym)
	}
}

// irBootFailureKey is the identity of an entry boot's `boot failure`
// function.
type irBootFailureKey struct{ boot *ast.FuncDef }

// irBootFailureName is the `boot failure` function's display name.
const irBootFailureName = "boot failure"

// irBuildBootFailure builds and records the `boot failure` function of entry
// boot fd, whose error type is errK, once fd itself is retained: the VM
// renders the payload of an `Err` the boot returns through it, as it renders
// main's (ir.Module.SetBootFailure).
func (g *gen) irBuildBootFailure(fd *ast.FuncDef, errK kind) {
	if g.irMod == nil {
		return
	}
	boot := g.irCalleeSym(fd, fd.Name)
	if g.irMod.FuncFor(boot) == nil {
		return
	}
	if sym := g.irBuildFailureRender(fd, irBootFailureKey{boot: fd}, irBootFailureName, errK); sym != nil {
		g.irMod.SetBootFailure(boot, sym)
	}
}

// irBuildFailureRender builds the function name, keyed by key, that renders
// an error of kind errK for decl's failure (Display, else Debug), adds it to
// this unit's module and answers its symbol, or nil when errK has no
// rendering this builder can lower.
func (g *gen) irBuildFailureRender(decl *ast.FuncDef, key any, name string, errK kind) *ir.Symbol {
	g.irDeclineOpen(name)
	if !irCallOperandKind(errK) {
		irDeclineNote("an error type outside the call domain: " + errK.nomi())
		return nil
	}
	sym := g.irCalleeSym(key, name)
	at := ast.Node(decl.ReturnTypeExpr)
	fn, ok := g.irErrorRenderFunc(decl, name, sym, errK, at, false)
	if !ok {
		fn, ok = g.irErrorRenderFunc(decl, name, sym, errK, at, true)
	}
	if !ok {
		irDeclineNote("an error type with no rendering: " + errK.nomi())
		return nil
	}
	g.irModule().AddFunc(fn)
	if err := g.irLintAdded(); err != nil {
		panic("irbuild: " + name + ": " + err.Error())
	}
	return sym
}

// irErrorRenderFunc builds the function name, sym, over one parameter of
// kind errK, rendering it through Display, or through Debug when debug is
// set: `main failure`, and a backtick typed literal's failure
// (irliteralcell.go). ok is false when that rendering does not lower: no
// Display impl for errK, or a kind this builder cannot render.
func (g *gen) irErrorRenderFunc(node ast.Node, name string, sym *ir.Symbol, errK kind, at ast.Node, debug bool) (*ir.Func, bool) {
	line, col := nodePos(node)
	param := ast.Param{Name: "error", Line: line, Col: col}
	sh := g.irFuncShellAt(node, []ast.Param{param}, irFuncSig{result: kindString, name: name, origin: irFromModule},
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
	if err := g.irLint(sh.fn); err != nil {
		return nil, false
	}
	return sh.fn, true
}

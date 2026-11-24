package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irLocal is the value a body holds for one local: a binding, a pattern's
// name, a lambda's parameter, a capture, a nested fn, or a parameter of
// the function being lowered.
type irLocal struct {
	// temp is the temporary the value lives in, or ir.NoTemp for a
	// parameter of the function, which a read names by sym (ir.RefLocal).
	temp ir.Temp
	k    kind
	// sym is a function parameter's declaration identity.
	sym *ir.Symbol
}

// declareLocal records that the local whose name is written at (line, col)
// holds t at kind k in this body: the declaration the checker resolves a
// read of it to (identRef) stands there. A later declaration at the same
// position, the same source lowered again, replaces it.
func (bl *irScalarBuilder) declareLocal(line, col int, t ir.Temp, k kind) {
	bl.declareKey(analysis.Pos{Line: line, Col: col}, t, k)
}

// declareKey records that the local whose key is key holds t at kind k in
// this body: a written declaration's position, or a key madeLocal made.
func (bl *irScalarBuilder) declareKey(key analysis.Pos, t ir.Temp, k kind) {
	if bl.locals == nil {
		bl.locals = map[analysis.Pos]irLocal{}
	}
	bl.locals[key] = irLocal{temp: t, k: k}
}

// heldLocal is the local id reads, as this body or an enclosing one holds
// it (readKey, localAt), and whether id reads one.
func (bl *irScalarBuilder) heldLocal(id *ast.Ident) (irLocal, bool) {
	key, local, _ := bl.readKey(id)
	if !local {
		return irLocal{}, false
	}
	for scope := bl; scope != nil; scope = scope.parent {
		if l, held := scope.localAt(key); held {
			return l, true
		}
	}
	return irLocal{}, false
}

// boundHere is the local id reads when this body holds it in a temporary:
// a binding, a pattern's name, a capture, and not a parameter of the
// function.
func (bl *irScalarBuilder) boundHere(id *ast.Ident) (irLocal, bool) {
	key, local, _ := bl.readKey(id)
	if !local {
		return irLocal{}, false
	}
	l, held := bl.localAt(key)
	return l, held && l.temp != ir.NoTemp
}

// madeLocal is a fresh key for a local the builder declares, which no
// source declares: a line below every source line, so it never meets a
// written declaration's position, and one per local, so two the builder
// declares at one written node's position stay apart.
func (g *gen) madeLocal() analysis.Pos {
	g.madeKeys++
	return analysis.Pos{Line: -g.madeKeys}
}

// madeLambda gives each parameter of lam, a lambda the builder makes, a
// fresh key, and answers them in parameter order.
func (g *gen) madeLambda(lam *ast.Lambda) []analysis.Pos {
	keys := make([]analysis.Pos, len(lam.Params))
	for i := range keys {
		keys[i] = g.madeLocal()
	}
	g.setMadeParams(lam, keys)
	return keys
}

// setMadeParams records keys as the keys of lam's parameters.
func (g *gen) setMadeParams(lam *ast.Lambda, keys []analysis.Pos) {
	if g.madeParams == nil {
		g.madeParams = map[*ast.Lambda][]analysis.Pos{}
	}
	g.madeParams[lam] = keys
}

// paramKey is the key of lam's parameter i: its written position, or the
// key the builder gave it when it made lam (madeLambda).
func (g *gen) paramKey(lam *ast.Lambda, i int) analysis.Pos {
	if keys, made := g.madeParams[lam]; made {
		return keys[i]
	}
	p := lam.Params[i]
	return analysis.Pos{Line: p.Line, Col: p.Col}
}

// madeRead is an identifier the builder writes (madeIdent) that reads the
// local declared under key.
func (g *gen) madeRead(name string, key analysis.Pos, line, col int) *ast.Ident {
	id := g.madeIdent(name, line, col)
	if g.madeRefs == nil {
		g.madeRefs = map[*ast.Ident]analysis.Pos{}
	}
	g.madeRefs[id] = key
	return id
}

// readKey is the key of the local t reads: the position of the declaration
// the checker resolved a written t to (identRef) when it is a body's
// (bodyBinding), or the key a made read carries (madeRead). local is false
// for a read of something no body binds (a function, a `once`), and for an
// identifier the builder wrote that names no local; known is false for a
// written identifier with no reference.
func (bl *irScalarBuilder) readKey(t *ast.Ident) (key analysis.Pos, local, known bool) {
	g := bl.g
	if key, made := g.madeRefs[t]; made {
		return key, true, true
	}
	if g.unchecked[t] {
		return analysis.Pos{}, false, true
	}
	sym := g.identRef(t)
	if sym == nil {
		return analysis.Pos{}, false, false
	}
	if g.bodyBinding(sym) {
		return sym.Pos, true, true
	}
	return analysis.Pos{}, false, true
}

// localAt is the local declared at pos that this body holds: its own, or,
// for the function's root body, a parameter of the function. A callee's
// default lowered at a call site sees its own parameters only.
func (bl *irScalarBuilder) localAt(pos analysis.Pos) (irLocal, bool) {
	if l, held := bl.locals[pos]; held {
		return l, true
	}
	if bl.defaultScope || bl.parent != nil {
		return irLocal{}, false
	}
	l, param := bl.sh.declared[pos]
	return l, param
}

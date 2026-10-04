package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irUnreachableHost is the VM intrinsic an unreachable call lowers to. It
// faults with its one String operand when control arrives, which it never
// does: see holeBoundCall.
const irUnreachableHost = "vm.unreachable"

// holeBoundCall lowers a bound dispatch whose receiver is a HOLE of the
// instance being built: a type parameter the checker left unsolved and the
// builder filled with Unit (stdInst.holes). `Maybe.hash(None)` instantiates
// the derived `Hashable.hash` at such a T, and its Some arm calls
// `Hashable.hash` on the payload. No value of a hole is ever built, so that
// call is unreachable, and Unit has no Hashable impl to call. It lowers to an
// explicit fault naming the hole rather than declining the instance.
//
// ONLY THE BUILDER'S OWN HOLES QUALIFY. A receiver whose type the checker
// solved, to Unit or anything else, is not a hole and takes the ordinary
// route, so a genuine missing impl still declines and stays BLOCKED: the
// instance must record a hole (irCheckerHoles, from the checker's signature
// at the instantiating call), hold no checker-solved Unit argument, and the
// interface must have no impl at the receiver's kind.
func (bl *irScalarBuilder) holeBoundCall(t *ast.Call, ti *ast.TypeIdent, method string) (ir.Temp, kind, bool, bool, bool) {
	unhandled := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	cur := bl.g.stdInstCur
	if cur == nil || len(cur.holes) == 0 || len(t.Args) == 0 {
		return unhandled()
	}
	// Every argument the instance holds at Unit must be a hole, so a Unit
	// value in this body can only be a hole's.
	var names []string
	for i, tp := range cur.tps {
		if i < len(cur.args) && cur.args[i] == kindUnit {
			if !cur.holes[tp] {
				return unhandled()
			}
			names = append(names, tp)
		}
	}
	if len(names) == 0 {
		return unhandled()
	}
	spelling := ti.Name + "." + method
	var byRecv map[kind][]*stdFunc
	if cur.holes[ti.Name] {
		// `T.hash(x)` with T a hole: the owner itself is the hole.
		names = []string{ti.Name}
	} else {
		if bl.g.std == nil {
			return unhandled()
		}
		var isIface bool
		if byRecv, isIface = bl.g.std.byIface[spelling]; !isIface {
			return unhandled()
		}
		// The receiver is a local holding the hole's value. The positions of
		// a derive-synthesized body coincide, so the checker's per-call
		// signature cannot say which call it describes; the local's kind,
		// the instance's record of its holes and the interface's lack of an
		// impl at that kind do.
		id, isIdent := t.Args[0].(*ast.Ident)
		if !isIdent {
			return unhandled()
		}
		k, bound := bl.boundK[id.Name]
		if !bound || k != kindUnit || len(byRecv[kindUnit]) != 0 {
			return unhandled()
		}
	}
	// The method's result, which every impl of a bound interface agrees on.
	result := kindInvalid
	for _, fs := range byRecv {
		for _, f := range fs {
			// kindInvalid: sentinel — the first impl names the result.
			if result == kindInvalid {
				result = f.result
			} else if f.result != result {
				return unhandled()
			}
		}
	}
	if ft := bl.g.checkedCallSignature(t); byRecv == nil && ft != nil && ft.Return != nil {
		result = bl.g.project(ft.Return)
	}
	// kindInvalid: sentinel — no impl or signature named the result.
	if result == kindInvalid || (result != kindUnit && !irCallableValueKind(result)) {
		return unhandled()
	}
	pos := bl.g.irPos(t.Line, t.Col)
	text := fmt.Sprintf("%s dispatches on %s, which the compiler filled with Unit in %s<%s> "+
		"because the call left it unconstrained; no value of it is ever built",
		spelling, strings.Join(names, ", "), cur.f.key, kindsNomi(cur.args))
	s := ir.NewString(pos, bl.f.NewTemp(), text)
	bl.b.Append(s)
	bl.side(s.Dst(), irScalarSide{k: kindString})
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(irUnreachableHost, irUnreachableHost), s.Dst())
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: result, deferrable: true})
	return c.Dst(), result, false, true, true
}

// irCheckerHoles is the type parameters of fd the checker left unsolved at a
// call whose instantiated signature is ft, and the builder filled with Unit
// (args): each is found only at positions where ft holds an unresolved type
// variable. A parameter the checker solved anywhere in the signature is not
// a hole. nil when there is none.
func irCheckerHoles(fd *ast.FuncDef, tps []string, args []kind, ft *analysis.FuncType) map[string]bool {
	if fd == nil || ft == nil || len(ft.Params) != len(fd.Params) {
		return nil
	}
	params := make(map[string]bool, len(tps))
	for _, tp := range tps {
		params[tp] = true
	}
	unsolved, solved := map[string]bool{}, map[string]bool{}
	for i, p := range fd.Params {
		if p.TypeAnnotation != nil {
			irTypeParamReadings(p.TypeAnnotation, ft.Params[i], params, unsolved, solved)
		}
	}
	if fd.ReturnTypeExpr != nil && ft.Return != nil {
		irTypeParamReadings(fd.ReturnTypeExpr, ft.Return, params, unsolved, solved)
	}
	var holes map[string]bool
	for i, tp := range tps {
		if unsolved[tp] && !solved[tp] && i < len(args) && args[i] == kindUnit {
			if holes == nil {
				holes = map[string]bool{}
			}
			holes[tp] = true
		}
	}
	return holes
}

// irTypeParamReadings walks a declared annotation beside the checker's type at
// that position, recording each type parameter the checker left an
// unresolved variable (unsolved) or gave a type (solved).
func irTypeParamReadings(ann ast.TypeExpr, actual analysis.Type, params, unsolved, solved map[string]bool) {
	actual = irResolvedType(actual)
	switch a := ann.(type) {
	case *ast.SimpleType:
		if !params[a.Name] {
			return
		}
		if _, open := actual.(*analysis.TypeVar); open {
			unsolved[a.Name] = true
		} else {
			solved[a.Name] = true
		}
	case *ast.GenericType:
		var parts []analysis.Type
		switch t := actual.(type) {
		case *analysis.EnumType:
			parts = t.TypeArgs
		case *analysis.StructType:
			parts = t.TypeArgs
		case *analysis.ListType:
			parts = []analysis.Type{t.Elem}
		case *analysis.MapType:
			parts = []analysis.Type{t.Key, t.Val}
		}
		if len(parts) != len(a.Params) {
			return
		}
		for i, p := range a.Params {
			irTypeParamReadings(p, parts[i], params, unsolved, solved)
		}
	}
}

// irResolvedType follows a checker type variable to what it resolved to.
func irResolvedType(t analysis.Type) analysis.Type {
	for {
		v, isVar := t.(*analysis.TypeVar)
		if !isVar || v.Resolved == nil {
			return t
		}
		t = v.Resolved
	}
}

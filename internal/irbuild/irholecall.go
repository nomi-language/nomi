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
// builder filled with Unit (stdInst.holes). `Maybe.to_string(None)`
// instantiates the derived `Display.to_string` at such a T, and its Some arm
// calls `Display.to_string` on the payload. No value of a hole is ever built,
// so that call is unreachable, and Unit has no Display impl to call. It lowers
// to an explicit fault naming the hole rather than declining the instance.
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
	names := bl.g.unitHoles()
	if len(names) == 0 || len(t.Args) == 0 {
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
	v := bl.holeUnreachable(t, t.Line, t.Col, spelling+" dispatches on", names, result)
	return v, result, false, true, true
}

// holeCompare lowers `a < b`, `<=`, `>` or `>=` over two values of a HOLE of
// the instance being built: `item < m` in `Iter.min<T>` reached by
// `Iter.min([])`, where the checker left T unsolved because the call makes
// no value of it. The comparison is the operator's spelling of a
// `Comparable.compare` dispatch on the hole, unreachable for holeBoundCall's
// reason, and lowers to the same explicit fault. Unit has no Comparable
// impl, so an ordering on Unit in a body whose every Unit argument is a hole
// can only be on a hole's values.
func (bl *irScalarBuilder) holeCompare(t *ast.Binary, op ir.CompareOp, k kind) (ir.Temp, bool) {
	if !op.Ordering() || k != kindUnit {
		return ir.NoTemp, false
	}
	names := bl.g.unitHoles()
	if len(names) == 0 {
		return ir.NoTemp, false
	}
	return bl.holeUnreachable(t, t.Line, t.Col, "`"+t.Op+"` compares values of", names, kindBool), true
}

// unitHoles is the type parameters of the std instance being built that the
// builder filled with Unit, or nil when there is none or when any argument
// the instance holds at Unit is not a hole: then a Unit value in the body
// need not be a hole's.
func (g *gen) unitHoles() []string {
	cur := g.stdInstCur
	if cur == nil || len(cur.holes) == 0 {
		return nil
	}
	var names []string
	for i, tp := range cur.tps {
		if i < len(cur.args) && cur.args[i] == kindUnit {
			if !cur.holes[tp] {
				return nil
			}
			names = append(names, tp)
		}
	}
	return names
}

// holeUnreachable emits the `vm.unreachable` call that stands for what, a
// use of the holes names that no run reaches, answering a value of result.
func (bl *irScalarBuilder) holeUnreachable(at ast.Node, line, col int, what string, names []string, result kind) ir.Temp {
	cur := bl.g.stdInstCur
	pos := bl.g.irPos(line, col)
	text := fmt.Sprintf("%s %s, which the compiler filled with Unit in %s<%s> "+
		"because the call left it unconstrained; no value of it is ever built",
		what, strings.Join(names, ", "), cur.f.key, kindsNomi(cur.args))
	s := ir.NewString(pos, bl.f.NewTemp(), text)
	bl.b.Append(s)
	bl.side(s.Dst(), irScalarSide{k: kindString})
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(at), bl.g.irCalleeSym(irUnreachableHost, irUnreachableHost), s.Dst())
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: result, deferrable: true})
	return c.Dst()
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

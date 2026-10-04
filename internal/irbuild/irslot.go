package irbuild

// Storage with a type and no value.
//
// A bytecode VM has no return-the-value path: an `if` in expression position
// is arms that jump to a join, and the answer must land in a named slot the
// join can read. So storage for an answer is a node, `ir.Slot`.
//
// Named function results and branch-valued bindings declare typed Slots.
// The function signature supplies the former's type; the analyzer's resolved
// binding supplies the latter's. Branch arms must agree with that type.
//
// `ir.Slot` carries an `*ir.Type`, whose identity is the pointer and whose
// name is display only; the builder's `kind` is what it knows about the
// value, and it stays on the builder's side (see irarith.go).

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irFuncShell is one function's `ir.Func` before its body is lowered: the
// container, its declaration identity, its parameters, its entry block and
// its RESULT SLOT.
//
// It is built by `funcDecl` rather than by the body builder, so the result
// slot and the `return` belong to the function's own namespace and the
// retained graph is a whole function.
//
// The shell is opened for every function the builder lowers a body for, and
// discarded when the shape declines. That costs one `ir.Func`, one block, one
// `ir.Slot` and one interned Symbol per declined function, and mints no
// identifier through `gen.uniq`, which is the discipline `irScalarBuilder`
// follows for the body.
type irFuncShell struct {
	fn *ir.Func
	// entry is the block the body's first instruction goes into.
	entry *ir.Block
	// result is the temporary the body writes and Return reads. Named
	// functions declare it with a Slot; lambda joins define it in arm Copies.
	result ir.Temp
	// slot is the result slot's declaration node.
	slot *ir.Slot
	// syms maps visible local names to declaration identities. Lexical arms
	// clone this table. A pattern binding installs a fresh symbol even when
	// it shadows an outer name; reads and closure captures keep that identity.
	syms map[string]*ir.Symbol
	// params is the temporary each declared parameter's value arrives in,
	// keyed on the Nomi name a body can read. A DESTRUCTURING parameter's
	// synthesized `__destr_<line>_<col>` name is in here too and no body can
	// spell it, which is exactly what the parameter is: see irparam.go.
	params map[string]ir.Temp
	// paramTemps is the temporary of each declared parameter, in order.
	paramTemps []ir.Temp
	// frame is the kind of each temporary the destructuring prologue names.
	frame *irFuncFrame
	// prologue is how many of the entry block's leading instructions come
	// before the body: the result `ir.Slot`, and then the `ir.Proj`s and
	// `ir.Bind`s a destructuring parameter's names come out of.
	prologue int
	// patternOK is false when a destructuring parameter did not lower. The
	// prologue is then partial — some names are bound at an invalid kind and
	// some projections were never built — so the retained shape declines
	// rather than retaining a graph with a hole in it.
	patternOK bool
	// sides is what the PROLOGUE recorded per temporary: each `ir.Proj`'s
	// kind. `irScalarBuilder` starts its own list from this one, so the
	// body's facts and the prologue's are one list with one indexing.
	sides []irScalarSide
	// at is the declaration node every PROLOGUE instruction takes its
	// position from — `g.irNodePos(fd)`, which is what `g.destructure`'s
	// nodes already carry. Held on the shell because `gen.hold` has no
	// `ast.Node` parameter and has to record a Copy while the prologue is
	// open; see case.go.
	at ast.Node
}

// localSym returns the visible identity, interning a new declaration if absent.
func (s *irFuncShell) localSym(name string) *ir.Symbol {
	if sym := s.syms[name]; sym != nil {
		return sym
	}
	sym := ir.NewSymbol(name)
	s.syms[name] = sym
	return sym
}

// irFuncShellFor opens a shell for fd, or answers nil. `params[i]` is the kind
// of declared parameter i.
//
// A nil answer means the result kind has no `ir.Type`: a return type the
// builder cannot place has no slot to size, and `ir.NewSlot` rejects one.
//
// EVERY DECLARED PARAMETER IS DECLARED HERE, a destructuring or a discarded
// one included: the call site has exactly one operand for each. See irparam.go.
func (g *gen) irFuncShellFor(fd *ast.FuncDef, sig irFuncSig, params []kind) *irFuncShell {
	if sig.decl == nil {
		return nil
	}
	return g.irFuncShellWithCallee(fd, sig, params, g.irCalleeSym(sig.decl, fd.Name))
}

// irFuncShellWithCallee uses the producer's declaration identity. An impl
// specialization is identified by its implItem, not by its shared source AST.
func (g *gen) irFuncShellWithCallee(fd *ast.FuncDef, sig irFuncSig, params []kind, callee *ir.Symbol) *irFuncShell {
	return g.irFuncShellAt(fd, fd.Params, sig, params, callee)
}

// irFuncShellAt is irFuncShellWithCallee for a function positioned at node,
// which need not be a declaration: a field-default accessor has no `fn` of its
// own and sits at the default expression.
func (g *gen) irFuncShellAt(node ast.Node, declared []ast.Param, sig irFuncSig, params []kind, callee *ir.Symbol) *irFuncShell {
	ty := g.irTypeOf(sig.result)
	if ty == nil {
		return nil
	}
	at := g.irNodePos(node)
	fn := ir.NewFuncFor(at, callee)
	sh := &irFuncShell{fn: fn, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{},
		patternOK: true, at: node}
	sh.frame = newIRFuncFrame(fn)
	for i, p := range declared {
		// The SHAPE of the value this parameter receives, read off the kind
		// the caller holds. A parameter's temporary is written by nothing, so
		// this is the only place the graph can learn it. An under-sized
		// `params` declines to state a shape rather than read a neighbour's.
		shape := ir.ValUnknown
		if i < len(params) {
			shape = irParamShape(params[i])
		}
		t := fn.AddParam(sh.paramSym(p), shape)
		if i < len(params) {
			g.irTypeTemp(fn, t, params[i])
			sh.frame.setKind(t, params[i])
		}
		sh.params[p.Name] = t
		sh.paramTemps = append(sh.paramTemps, t)
	}
	sh.entry = fn.NewBlock(at, "entry")
	sh.result = fn.NewTemp()
	g.irTypeTemp(fn, sh.result, sig.result)
	sh.slot = ir.NewSlot(at, sh.result, ty)
	sh.entry.Append(sh.slot)
	sh.prologue = len(sh.entry.Instrs())
	return sh
}

// paramValue is declared parameter i as a value the prologue can destructure.
func (sh *irFuncShell) paramValue(i int, k kind) expr {
	return expr{t: sh.paramTemps[i], k: k}
}

// paramSym is the declaration identity of one parameter.
//
// A DISCARDED parameter gets a MINTED symbol rather than an interned one,
// because `syms` is keyed on the NAME and `_` is not a name: two discarded
// parameters are two declarations that happen to be spelled alike, and
// interning them would give one function two `Param`s sharing one identity.
// Nothing can read either — `_` is not spellable as an expression — so the
// identity's only job is to be distinct.
func (s *irFuncShell) paramSym(p ast.Param) *ir.Symbol {
	if ast.IsDiscardName(p.Name) {
		return ir.NewSymbol(p.Name)
	}
	return s.localSym(p.Name)
}

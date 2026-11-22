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
	"github.com/nomi-language/nomi/internal/analysis"
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
	// params is the temporary each declared parameter's value arrives in,
	// keyed on the Nomi name a body can read. A DESTRUCTURING parameter's
	// synthesized `__destr_<line>_<col>` name is in here too and no body can
	// spell it: the body reads the names its pattern binds (irScalarBuild).
	params map[string]ir.Temp
	// declared is each plain parameter a body can read, by the position its
	// name is written at, with its kind and its declaration identity.
	declared map[analysis.Pos]irLocal
	// paramTemps is the temporary of each declared parameter, in order, and
	// paramKinds the kind of each, kindInvalid past the kinds the caller
	// gave.
	paramTemps []ir.Temp
	paramKinds []kind
	// unmatched is the function's NoMatch block for a pattern test whose
	// failure edge no value takes (passOrFail), nil until one needs it.
	unmatched *ir.Block
	// at is the node the function is positioned at: its declaration, or the
	// expression a default's accessor lowers.
	at ast.Node
}

// irFuncShellFor opens a shell for fd, or answers nil. `params[i]` is the kind
// of declared parameter i.
//
// A nil answer means the result kind has no `ir.Type`: a return type the
// builder cannot place has no slot to size, and `ir.NewSlot` rejects one.
//
// EVERY DECLARED PARAMETER IS DECLARED HERE, a destructuring or a discarded
// one included: the call site has exactly one operand for each.
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
	// The value domain's entry for a body's parameters and result
	// (irValueDomainKind): a bound-free generic declaration's own shell may
	// hold its type parameters' kinds, which no other shell does.
	for _, k := range append([]kind{sig.result}, params...) {
		if !irValueDomainKind(k) && !irMentionsTypeParam(k) {
			return nil
		}
	}
	at := g.irNodePos(node)
	fn := ir.NewFuncFor(at, callee)
	sh := &irFuncShell{fn: fn, params: map[string]ir.Temp{},
		declared: map[analysis.Pos]irLocal{}, at: node}
	for i, p := range declared {
		// The SHAPE of the value this parameter receives, read off the kind
		// the caller holds. A parameter's temporary is written by nothing, so
		// this is the only place the graph can learn it. An under-sized
		// `params` declines to state a shape rather than read a neighbour's.
		shape := ir.ValUnknown
		if i < len(params) {
			shape = irParamShape(params[i])
		}
		psym := sh.paramSym(p)
		t := fn.AddParam(psym, shape)
		k := kindInvalid
		if i < len(params) {
			k = params[i]
			g.irTypeTemp(fn, t, k)
		}
		sh.params[p.Name] = t
		if p.Destructure == nil && !ast.IsDiscardName(p.Name) {
			sh.declared[analysis.Pos{Line: p.Line, Col: p.Col}] = irLocal{temp: ir.NoTemp, k: k, sym: psym}
		}
		sh.paramTemps = append(sh.paramTemps, t)
		sh.paramKinds = append(sh.paramKinds, k)
		if i < len(params) {
			g.irAddVar(fn, node, p.Name, t, params[i], p.Line, p.Col, g.irNameSite(p.Name, p.Line, p.Col))
		}
	}
	sh.entry = fn.NewBlock(at, "entry")
	sh.result = fn.NewTemp()
	g.irTypeTemp(fn, sh.result, sig.result)
	sh.slot = ir.NewSlot(at, sh.result, ty)
	sh.entry.Append(sh.slot)
	return sh
}

// paramSym is the declaration identity of one parameter: a symbol of its
// own, which a read of it names (irLocal.sym), as each discarded parameter
// has one too, so no two parameters share an identity.
func (s *irFuncShell) paramSym(p ast.Param) *ir.Symbol {
	return ir.NewSymbol(p.Name)
}

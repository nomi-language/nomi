package irbuild

// `std/supervisors` in retained bodies.
//
// A Supervisor is a retained value kind: a boot result field, an app-field
// read, and the parameter of std's Nomi-bodied `Supervisor.new` and
// `Supervisor.flush`. `Supervisor.spawn(sup, || body)` is a host crossing
// spelled with the `rt.SupervisorSpawn` target, and
// `Supervisor.spawn_all(sup, list, f)` one spelled `rt.SupervisorSpawnAll`,
// answering supervised `Task<Unit>` handles. The two module-private externs
// the Nomi bodies call, `new_exact` and `flush_bounded`, are frame-taking rt
// hosts (irFrameHost). The VM runs all of them on the rt supervisor
// runtime.
//
// A call that omits trailing defaulted arguments of a std function names the
// `_ARITY<n>` wrapper the declaring module emits. irStdArityRetain builds that
// wrapper a second time as an ordinary `ir.Func` only the VM reads: the
// written parameters, the omitted defaults lowered in the declaring module's
// scope as fillParamDefaults does, and a call of the full body. A call whose
// named arguments skip a defaulted parameter fills that hole with the
// module's `_DEFAULT<slot>` accessor, which irStdSlotRetain builds the same
// way. The Go the declaring module emits for a wrapper or accessor is a stub
// recorded as not lowered (stdUnloweredWrappers), so a native program that
// reaches one is refused.

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"

	"github.com/nomi-language/nomi/rt"
)

// irSupervisorKind reports the std/supervisors Supervisor kind.
func irSupervisorKind(k kind) bool {
	return k == stdHostOriginKind("std/supervisors", "Supervisor")
}

// supervisorSpawn lowers `Supervisor.spawn(sup, || body)`. It admits a
// zero-parameter lambda literal whose result is Unit,
// and a `Task<Unit>` handle. Its owner resolved to std's `Supervisor`
// declaration (stdIntrinsicOwner).
func (bl *irScalarBuilder) supervisorSpawn(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.rowsUnrecorded(t) || namedArgNode(t.Args) != nil || len(t.Args) != 2 {
		return no()
	}
	lam, isLambda := t.Args[1].(*ast.Lambda)
	if !isLambda || len(lam.Params) != 0 {
		return no()
	}
	// The supervisor operand is a read, an app field or a call, written
	// directly or piped (`Supervisor.new(...) |> Supervisor.spawn(...)`). A
	// read emits no statement of its own; a call forces its own operands
	// first and the Go reader spells it inline in the spawn.
	switch t.Args[0].(type) {
	case *ast.Ident, *ast.Call:
	case *ast.FieldAccess:
		if _, app := bl.g.appRead(t.Args[0]); !app {
			return no()
		}
	default:
		return no()
	}
	sup, sk, _, ok := bl.lower(t.Args[0])
	if !ok || !irSupervisorKind(sk) {
		return no()
	}
	fn, fk, _, ok := bl.lower(lam)
	if !ok || funcResult(fk) != kindUnit {
		return no()
	}
	handle, built := bl.g.taskHandleKind(kindUnit)
	if !built {
		return no()
	}
	return bl.concHostEmit(t, "Supervisor.spawn", handle, sup, fn)
}

// irStdArity identifies one std function's arity-reduced wrapper, which is its
// own declaration: a function with two trailing defaults has two wrappers.
type irStdArity struct {
	f     *stdFunc
	arity int
}

// irGoDefaults is the value of each Go-stated std enum field default, keyed by
// the constant the builder's `expr` text spells in its place.
var irGoDefaults = map[string]int64{
	"rt.BackoffDefaultMaxRestarts": rt.BackoffDefaultMaxRestarts,
	"rt.BackoffDefaultMaxElapsed":  int64(rt.BackoffDefaultMaxElapsed),
}

// irStdArityRetain builds the `_ARITY<arity>` wrapper of f for the VM, when
// the full body is retained in this module and every omitted default lowers.
func (g *gen) irStdArityRetain(f *stdFunc, fd *ast.FuncDef, arity int) {
	// VM-only: no Go names what the build resolves.
	if g.irMod == nil || arity >= len(f.params) {
		return
	}
	// A body the index already records stays: it lives in the module the
	// index hands to programs. A second build of the same declaration (a
	// probe replaying a cached module's bodies onto a fresh gen) builds into a
	// module nothing links, and replacing the record with that body would make
	// every later caller name a function no program holds.
	recorded := f
	if f.canon != nil {
		recorded = f.canon
	}
	if recorded.irArity[arity] != nil {
		return
	}
	full := g.irCalleeSym(fd, f.key)
	if g.irMod.FuncFor(full) == nil {
		return
	}
	for _, p := range fd.Params[:arity] {
		if p.Destructure != nil {
			return
		}
	}
	g.pushScope()
	defer g.popScope()
	params := make([]kind, arity)
	for i, p := range fd.Params[:arity] {
		params[i] = f.params[i]
		if !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: f.params[i]})
		}
	}
	short := *fd
	short.Params = fd.Params[:arity]
	sig := irFuncSig{result: f.result, decl: fd, name: stdArityName(f, arity), origin: irFromStd}
	sh := g.irFuncShellWithCallee(&short, sig, params, g.irCalleeSym(irStdArity{f, arity}, stdArityName(f, arity)))
	if sh == nil {
		return
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: f.result, sides: sh.sides,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}, vmOnly: true}
	args := make([]ir.Temp, len(f.params))
	for i, p := range fd.Params[:arity] {
		args[i] = sh.params[p.Name]
	}
	if !bl.callDefaults(&fnSig{decl: fd, params: f.params}, args) {
		return
	}
	at := g.irNodePos(fd)
	call := ir.NewCall(at, sh.fn.NewTemp(), ir.OrdinaryCall, full, args...)
	bl.b.Append(call)
	bl.side(call.Dst(), irScalarSide{k: f.result})
	cp := ir.NewCopy(at, sh.result, call.Dst())
	bl.b.Append(cp)
	bl.side(sh.result, irScalarSide{k: f.result})
	bl.b.SetTerm(ir.NewReturn(at, sh.result))
	if ir.Lint(sh.fn) != nil {
		return
	}
	g.irModule().AddFunc(sh.fn)
	if err := ir.LintModuleAdded(g.irModule()); err != nil {
		panic("irbuild: std arity wrapper: " + err.Error())
	}
	if f.canon != nil {
		f = f.canon
	}
	if f.irArity == nil {
		f.irArity = map[int]*ir.Func{}
	}
	f.irArity[arity] = sh.fn
}

// stdShortCallPlan plans a call writing some of a defaulted std function's
// parameters, as stdlibInvoke spells it. Arguments are
// evaluated in the plan's evaluation order, every one but the last forced; a
// call whose named arguments leave a HOLE forces the last too and fills each
// hole with the declaring module's `_DEFAULT<slot>` accessor over the slots
// before it, whose VM body irStdSlotRetain built, forcing every fill but the
// last so each default runs once. The callee is the arity
// wrapper for the highest claimed slot, or the full body when that is the last.
func (bl *irScalarBuilder) stdShortCallPlan(t *ast.Call, owner, method, module string) (*irQualPlan, irQualArgs, bool) {
	var f *stdFunc
	for _, c := range bl.g.stdShortCandidates(owner, method, module) {
		if c.lowerable() {
			if f != nil {
				return nil, irQualArgs{}, false
			}
			f = c
		}
	}
	if f == nil || f.decl == nil {
		return nil, irQualArgs{}, false
	}
	canon := f
	if f.canon != nil {
		canon = f.canon
	}
	plan, planned := argSlotPlan(t.Args, f.params, stdParamNames(f))
	if !planned {
		return nil, irQualArgs{}, false
	}
	width := len(t.Args)
	_, contiguous := contiguousSlots(plan)
	if !contiguous {
		width = highestSlot(plan)
	}
	claimed := make(map[int]bool, len(plan.slots))
	for _, slot := range plan.slots {
		claimed[slot] = true
	}
	var holes []int
	for slot := range width {
		if claimed[slot] {
			continue
		}
		if !callDefaultFill(f, slot) || canon.irSlot[slot] == nil {
			return nil, irQualArgs{}, false
		}
		holes = append(holes, slot)
	}
	var body *ir.Func
	if width == len(f.params) {
		body = canon.irBody
	} else if arity, ok := stdCallArity(f, width); ok && arity == width {
		body = canon.irArity[width]
	}
	if body == nil {
		return nil, irQualArgs{}, false
	}
	args := irQualArgs{temps: make([]ir.Temp, width), kinds: make([]kind, width), mobile: make([]bool, width), ok: true}
	for n, i := range plan.order {
		val := argExpr(t.Args[i])
		src, k, mobile, ok := bl.lower(val)
		if !ok {
			return nil, irQualArgs{}, false
		}
		if n == len(plan.order)-1 && bl.recording > 0 && bl.qualRecord == t && !irHeldValue(bl.f, src, bl.sides) {
			// Named twice, in its row and in the call: held once, as
			// irQualLowerArgs holds a qualified call's final operand.
			cp := ir.NewCopy(bl.g.irNodePos(val), bl.f.NewTemp(), src)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyHold})
			src, mobile = cp.Dst(), true
		}
		slot := plan.slots[i]
		args.temps[slot], args.kinds[slot], args.mobile[slot] = src, k, mobile
	}
	if bl.qualRecord == t {
		// Inside an assertion subject, recordedQualCall records the written
		// operands' rows after the call, in the order they are written.
		if namedArgNode(t.Args) != nil {
			return nil, irQualArgs{}, false
		}
		written := irQualArgs{temps: slices.Clone(args.temps[:len(t.Args)]), kinds: slices.Clone(args.kinds[:len(t.Args)]),
			mobile: slices.Clone(args.mobile[:len(t.Args)]), ok: true}
		bl.qualRecordArgs, bl.qualRecordSeen = written, true
	}
	// Holes fill in ascending order, each accessor taking every slot below its
	// own. Every hole but the last is forced, so a later accessor's prefix carries the earlier default's value and
	// each default runs once.
	for _, slot := range holes {
		prefix := irQualArgs{temps: slices.Clone(args.temps[:slot]), kinds: slices.Clone(args.kinds[:slot]),
			mobile: slices.Clone(args.mobile[:slot]), ok: true}
		if !irSlotSignature(prefix.kinds, f.params[:slot], f.params[slot]) {
			return nil, irQualArgs{}, false
		}
		p := &irQualPlan{token: irSlotDefault{f, slot}, name: fmt.Sprintf("%s default %d", f.key, slot+1), result: f.params[slot], sym: canon.irSlot[slot].Sym()}
		v, _, _, ok := bl.qualEmit(t, prefix, p)
		if !ok {
			return nil, irQualArgs{}, false
		}
		args.temps[slot], args.kinds[slot], args.mobile[slot] = v, f.params[slot], false
	}
	if !irSlotSignature(args.kinds, f.params[:width], f.result) {
		return nil, irQualArgs{}, false
	}
	return &irQualPlan{token: f, name: stdArityName(f, width), result: f.result, sym: body.Sym()}, args, true
}

// irSlotSignature is irQualSignature over a call's slots rather than its
// written arguments, which a hole makes differ.
func irSlotSignature(kinds, params []kind, result kind) bool {
	if len(kinds) != len(params) || (!irCallableValueKind(result) && result != kindUnit) {
		return false
	}
	for i, k := range kinds {
		if k != params[i] || !irCallOperandKind(k) {
			return false
		}
	}
	return true
}

// stdShortCall lowers `Owner.method(args)` when it writes a prefix of a
// defaulted std function's parameters and the declaring module retained the
// arity wrapper for the VM. handled is false when the call is not that shape,
// so the caller's ordinary routes answer it. module is the std module a
// whole-module qualifier names (`supervisors.Supervisor.new(...)`), or "" for
// a bare owner, which a local declaration of the same name claims instead.
func (bl *irScalarBuilder) stdShortCall(t *ast.Call, owner, method, module string) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	// A stdlib module's own test body calls the module's types as the std
	// types they are, against arity wrappers the cached lowering already
	// built (stdTestOwnType).
	if g.std == nil || (g.stdModule != "" && !bl.stdTestOwnType()) || bl.rowsUnrecorded(t) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if !bl.stdShortCallShape(t, owner, method, module) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	plan, args, ok := bl.stdShortCallPlan(t, owner, method, module)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	v, k, mobile, ok := bl.qualEmit(t, args, plan)
	return v, k, mobile, ok, true
}

// stdShortCallShape reports that Owner.method names one lowerable std
// function and the call writes fewer arguments than it declares. A bare
// owner the program declares itself (a local `Date` beside std's) names the
// program's type, never std's.
func (bl *irScalarBuilder) stdShortCallShape(t *ast.Call, owner, method, module string) bool {
	if bl.g.std == nil {
		return false
	}
	if module == "" && !bl.stdTestOwnType() {
		if _, local := bl.g.types[owner]; local {
			return false
		}
		if _, local := bl.g.ifaces[owner]; local {
			return false
		}
	}
	fs := bl.g.stdShortCandidates(owner, method, module)
	return len(fs) == 1 && fs[0].lowerable() && len(t.Args) < len(fs[0].params)
}

// stdShortCandidates is the std functions `Owner.method` names: every
// module's for a bare owner, and module's own for a qualified one.
func (g *gen) stdShortCandidates(owner, method, module string) []*stdFunc {
	fs := g.std.byType[owner+"."+method]
	if module == "" {
		return fs
	}
	var out []*stdFunc
	for _, f := range fs {
		if f.module == module {
			out = append(out, f)
		}
	}
	return out
}

// goDefault is a std enum field's Go-stated default n at kind k: an Int, or a
// wrapping distinct over one such as Duration.
func (bl *irScalarBuilder) goDefault(at ast.Node, k kind, n int64) ir.Temp {
	c := ir.NewInt(bl.g.irNodePos(at), bl.f.NewTemp(), n)
	bl.b.Append(c)
	if k == kindInt {
		return c.Dst()
	}
	if k.tag != tagNamed || !irWrappingDistinct(k.def) || k.def.inner != kindInt {
		return ir.NoTemp
	}
	m := ir.NewMakeDistinct(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(k.def), c.Dst())
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: k, pureMake: true})
	return m.Dst()
}

// supervisorSpawnAll lowers `Supervisor.spawn_all(sup, source, f)`:
// positional arguments evaluated in order, a List source, a one-parameter
// function over its element returning Unit, and a `List<Task<Unit>>` of
// handles. rt takes the source as a List, so a lazy Iter declines.
func (bl *irScalarBuilder) supervisorSpawnAll(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if bl.rowsUnrecorded(t) || namedArgNode(t.Args) != nil || len(t.Args) != 3 {
		return no()
	}
	args := bl.irQualLowerArgs(t)
	if !args.ok || !irSupervisorKind(args.kinds[0]) {
		return no()
	}
	src, fn := args.kinds[1], args.kinds[2]
	if src.tag != tagList || src.comp == nil {
		return no()
	}
	elem := src.comp.parts[0]
	if fn.tag != tagFunc || fn.comp == nil || len(funcParams(fn)) != 1 || funcParams(fn)[0] != elem || funcResult(fn) != kindUnit {
		return no()
	}
	handle, built := bl.g.taskHandleKind(kindUnit)
	if !built {
		return no()
	}
	result := bl.g.listKind(handle)
	// kindInvalid: lookup — a List over a handle with no interned kind declines.
	if result == kindInvalid {
		return no()
	}
	return bl.concHostEmit(t, "Supervisor.spawn_all", result, args.temps...)
}

// irStdSlotRetain builds the `_DEFAULT<slot>` accessor of f for the VM: the
// parameters before slot, and slot's default lowered in the declaring module's
// scope with those parameters in hand.
func (g *gen) irStdSlotRetain(f *stdFunc, fd *ast.FuncDef, slot int) {
	// VM-only: no Go names what the build resolves.
	if g.irMod == nil {
		return
	}
	for _, p := range fd.Params[:slot] {
		if p.Destructure != nil {
			return
		}
	}
	g.pushScope()
	defer g.popScope()
	params := make([]kind, slot)
	for i, p := range fd.Params[:slot] {
		params[i] = f.params[i]
		if !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: f.params[i]})
		}
	}
	short := *fd
	short.Params = fd.Params[:slot]
	name := stdSlotName(f, slot)
	sig := irFuncSig{result: f.params[slot], decl: fd, name: name, origin: irFromStd}
	sh := g.irFuncShellWithCallee(&short, sig, params, g.irCalleeSym(irSlotDefault{f, slot}, fmt.Sprintf("%s default %d", f.key, slot+1)))
	if sh == nil {
		return
	}
	bl := &irScalarBuilder{g: g, sh: sh, f: sh.fn, b: sh.entry, returnKind: f.params[slot], sides: sh.sides,
		bound: map[string]ir.Temp{}, boundK: map[string]kind{}, vmOnly: true}
	args := make([]ir.Temp, slot+1)
	for i, p := range fd.Params[:slot] {
		args[i] = sh.params[p.Name]
	}
	args[slot] = ir.NoTemp
	filled := *fd
	filled.Params = fd.Params[:slot+1]
	if !bl.callDefaults(&fnSig{decl: &filled, params: f.params[:slot+1]}, args) {
		return
	}
	at := g.irNodePos(fd)
	cp := ir.NewCopy(at, sh.result, args[slot])
	bl.b.Append(cp)
	bl.side(sh.result, irScalarSide{k: f.params[slot]})
	bl.b.SetTerm(ir.NewReturn(at, sh.result))
	if ir.Lint(sh.fn) != nil {
		return
	}
	g.irModule().AddFunc(sh.fn)
	if err := ir.LintModuleAdded(g.irModule()); err != nil {
		panic("irbuild: std slot accessor: " + err.Error())
	}
	if f.canon != nil {
		f = f.canon
	}
	if f.irSlot == nil {
		f.irSlot = map[int]*ir.Func{}
	}
	f.irSlot[slot] = sh.fn
}

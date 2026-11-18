package irbuild

// `std/supervisors` in retained bodies.
//
// A Supervisor is a retained value kind: a boot result field, an app-field
// read, and the parameter of std's Nomi-bodied `Supervisor.new` and
// `Supervisor.flush`. `Supervisor.spawn(sup, fn -> body)` is a host crossing
// to `rt.SupervisorSpawn`, and `Supervisor.spawn_all(sup, source, f)` one to
// `rt.SupervisorSpawnAll`, answering supervised `Task<Unit>` handles. The two module-private externs
// the Nomi bodies call, `new_exact` and `flush_bounded`, are frame-taking rt
// hosts (irFrameHost). The VM runs all of them on the rt supervisor
// runtime.
//
// A call that leaves out defaulted parameters of a std function fills each
// with the module's `_DEFAULT<slot>` accessor over the slots before it,
// which irStdSlotRetain builds with the builder every default accessor
// uses (irSlotAccessor), and calls the full body.

import (
	"fmt"
	"slices"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irSupervisorKind reports the std/supervisors Supervisor kind.
func irSupervisorKind(k kind) bool {
	return k == stdHostOriginKind("std/supervisors", "Supervisor")
}

// irStreamKind reports std/io's Reader, Writer or File, an rt.Reader,
// rt.Writer or rt.File value only host calls look inside (rt/streams.go,
// rt/filehandle.go), or std/fs/host's FileSystem, the rt.FileSystem
// `std::fs::testing.run` and `fs.within` build (rt/files.go), or std/process/host's
// Child and Fake, the rt.ChildProcess and rt.ProcessFake std/process and
// std/process/testing hold (rt/processes.go), or std/http/host's Client and
// Exchange, the rt.HTTPClient and rt.HTTPExchange std/http holds
// (rt/network.go), or std/csv/host's Records and std/xml/host's Tokens,
// the readers std/csv and std/xml hold (rt/formats.go).
func irStreamKind(k kind) bool {
	if k.tag != tagNamed {
		return false
	}
	return slices.Contains(irStreamKinds(), k)
}

// irStreamKinds is irStreamKind's rows, resolved once: stdHostSpecs and
// stdHostDefs are fixed for the process.
var irStreamKinds = sync.OnceValue(func() []kind {
	return []kind{
		stdHostOriginKind("std/io", "Reader"), stdHostOriginKind("std/io", "Writer"),
		stdHostOriginKind("std/io", "File"), stdHostOriginKind("std/fs/host", "FileSystem"),
		stdHostOriginKind("std/process/host", "Child"), stdHostOriginKind("std/process/host", "Fake"),
		stdHostOriginKind("std/http/host", "Client"), stdHostOriginKind("std/http/host", "Exchange"),
		stdHostOriginKind("std/http/host", "Listener"), stdHostOriginKind("std/http/host", "Incoming"),
		stdHostOriginKind("std/http/host", "Certificate"), stdHostOriginKind("std/http/host", "TlsConfig"),
		stdHostOriginKind("std/http/host", "Conn"), stdHostOriginKind("std/http/host", "TcpListener"),
		stdHostOriginKind("std/http/host", "Mux"), stdHostOriginKind("std/http/host", "Jar"),
		stdHostOriginKind("std/http/host", "Handle"),
		stdHostOriginKind("std/csv/host", "Records"), stdHostOriginKind("std/xml/host", "Tokens"),
	}
})

// supervisorSpawn lowers `Supervisor.spawn(sup, body)`: a supervisor, a
// zero-parameter function whose result is Unit (a lambda literal or any other
// function value), and a `Task<Unit>` handle. Its owner resolved to std's
// `Supervisor` declaration (stdIntrinsicOwner).
func (bl *irScalarBuilder) supervisorSpawn(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	bl.mustBePlacedIntrinsic(t, 2, "Supervisor.spawn")
	args := bl.irQualLowerArgs(t)
	if !args.ok || !irSupervisorKind(args.kinds[0]) {
		return no()
	}
	fk := args.kinds[1]
	if fk.tag != tagFunc || fk.comp == nil || len(funcParams(fk)) != 0 || funcResult(fk) != kindUnit {
		return no()
	}
	handle := bl.g.taskHandleKind(kindUnit)
	return bl.concHostEmit(t, "Supervisor.spawn", handle, args.temps...)
}

// stdShortCallPlan plans a call writing some of a defaulted std function's
// parameters: the written arguments in the plan's evaluation order at their
// slots, then each omitted slot filled with the declaring module's
// `_DEFAULT<slot>` accessor (fillFromAccessors), and a call of the full
// body.
func (bl *irScalarBuilder) stdShortCallPlan(t *ast.Call, owner, method, module string) (*irQualPlan, irQualArgs, bool) {
	var f *stdFunc
	for _, c := range bl.g.stdShortCandidates(owner, method, module) {
		if c.lowerable() {
			if f != nil {
				panic("irbuild: stdShortCallPlan: stdShortCallShape already required exactly one lowerable candidate")
			}
			f = c
		}
	}
	if f == nil || f.decl == nil {
		panic("irbuild: stdShortCallPlan: stdShortCallShape already required one lowerable candidate with a declaration")
	}
	return bl.stdShortCallPlanFor(t, f)
}

// stdShortCallPlanFor is stdShortCallPlan's plan against f, whose full body
// and default accessors are the ones its canonical function
// records: the cached lowering's, or a per-program instance's view
// (stdUnretainedShortCall).
func (bl *irScalarBuilder) stdShortCallPlanFor(t *ast.Call, f *stdFunc) (*irQualPlan, irQualArgs, bool) {
	canon := f
	if f.canon != nil {
		canon = f.canon
	}
	plan, planned := bl.g.callPlan(t, len(f.params))
	if !planned || canon.irBody == nil {
		return nil, irQualArgs{}, false
	}
	for _, slot := range plan.defaulted {
		if !callDefaultFill(f, slot) || canon.irSlot[slot] == nil {
			return nil, irQualArgs{}, false
		}
	}
	width := len(f.params)
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
	// Each omitted slot takes its parameter's kind, which its accessor
	// answers; the written ones must already be theirs, since each accessor
	// takes the slots before its own.
	for _, slot := range plan.defaulted {
		args.kinds[slot] = f.params[slot]
	}
	if !irSlotSignature(args.kinds, f.params, f.result) {
		return nil, irQualArgs{}, false
	}
	// The omitted slots fill as every other call's do (fillFromAccessors):
	// the written operands held, then each slot's accessor in ascending
	// order over the slots before it, each held, so a later default reads
	// the earlier one's value and each runs once.
	home := defaultHome{accessor: func(slot int) *ir.Symbol { return canon.irSlot[slot].Sym() }}
	if !bl.fillFromAccessors(t, args.kinds, args.temps, args.mobile, home, width) {
		return nil, irQualArgs{}, false
	}
	return &irQualPlan{token: f, name: f.key, result: f.result, sym: canon.irBody.Sym()}, args, true
}

// irSlotSignature reports whether a call's slots, which a hole makes differ
// from its written arguments, are params kind for kind, each an operand a
// call carries, with a result a call answers.
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
// defaulted std function's parameters and the declaring module retained its
// body and default accessors for the VM. handled is false when the call is not that shape,
// so the caller's ordinary routes answer it. module is the std module a
// whole-module qualifier names (`supervisors.Supervisor.new(...)`), or "" for
// a bare owner, which a local declaration of the same name claims instead.
func (bl *irScalarBuilder) stdShortCall(t *ast.Call, owner, method, module string) (ir.Temp, kind, bool, bool, bool) {
	g := bl.g
	// A stdlib module's own test body calls the module's types as the std
	// types they are, against default accessors the cached lowering already
	// built (stdTestOwnType). Another std module's body calls the accessors
	// the callee's module built, once that module is lowered
	// (stdShortCalleeLowered): `NaiveDateTime.from_instant(i)` in
	// std/json, whose default `Duration.seconds(0)` names what std/json
	// need not import.
	if g.std == nil || bl.rowsUnrecorded(t) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if g.stdModule != "" && !bl.stdTestOwnType() && !bl.stdShortCalleeLowered(t, owner, method, module) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	if !bl.stdShortCallShape(t, owner, method, module) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	plan, args, ok := bl.stdShortCallPlan(t, owner, method, module)
	if !ok {
		if v, k, mobile, lowered, handled := bl.stdUnretainedShortCall(t, owner, method, module); handled {
			return v, k, mobile, lowered, true
		}
		return ir.NoTemp, kindInvalid, false, false, true
	}
	v, k, mobile, ok := bl.qualEmit(t, args, plan)
	return v, k, mobile, ok, true
}

// stdShortCalleeLowered reports whether a std module's short positional
// call of another module's function names a body and default accessors that
// module has already lowered. A module lowered later builds them then, so
// the call declines through the ordinary routes, as it did before they
// existed.
func (bl *irScalarBuilder) stdShortCalleeLowered(t *ast.Call, owner, method, module string) bool {
	if namedArgNode(t.Args) != nil {
		return false
	}
	fs := bl.g.stdShortCandidates(owner, method, module)
	if len(fs) != 1 || !fs[0].lowerable() || fs[0].pkg == bl.g.pkg {
		return false
	}
	f, canon := fs[0], fs[0]
	if f.canon != nil {
		canon = f.canon
	}
	plan, planned := bl.g.callPlan(t, len(f.params))
	if !planned || len(plan.defaulted) == 0 || canon.irBody == nil {
		return false
	}
	for _, slot := range plan.defaulted {
		if canon.irSlot[slot] == nil {
			return false
		}
	}
	return true
}

// stdUnretainedShortCall lowers a call that writes a prefix of a defaulted
// std function's parameters, or names some of them, when the cache retained
// neither the function nor its default accessors because its body reaches a
// call a program builds for itself (stdUnretainedMonoBody: a generic
// sibling, a std interface default): `Decimal.format(d, locale)`, whose
// `fraction` defaults to `scale_digits(Decimal.scale(d))`. A default is
// Nomi of f's module, so it is lowered there: the instance's gen builds f's
// default accessors beside its body, as the cached
// lowering builds a retained function's (stdInstances.build), and the call
// is planned against them as stdShortCallPlan plans a retained one. handled
// is false when the function is not such a body.
func (bl *irScalarBuilder) stdUnretainedShortCall(t *ast.Call, owner, method, module string) (ir.Temp, kind, bool, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool, bool) { return ir.NoTemp, kindInvalid, false, false, false }
	s := bl.g.stdInsts
	if s == nil {
		return no()
	}
	var f *stdFunc
	for _, c := range bl.g.stdShortCandidates(owner, method, module) {
		if c.lowerable() {
			if f != nil {
				return no()
			}
			f = c
		}
	}
	if f == nil || f.decl == nil || len(f.decl.Params) != len(f.params) || !s.stdUnretainedMonoBody(f) {
		return no()
	}
	inst, why := s.instantiate(f, nil, nil, bl.g)
	if inst == nil {
		bl.g.dc.note(why)
		return ir.NoTemp, kindInvalid, false, false, true
	}
	plan, args, ok := bl.stdShortCallPlanFor(t, inst.view)
	if !ok {
		return ir.NoTemp, kindInvalid, false, false, true
	}
	v, k, mobile, lowered := bl.qualEmit(t, args, plan)
	return v, k, mobile, lowered, true
}

// stdShortCallLeavesAHole reports that a call stdShortCallShape accepts
// names a slot past one it leaves to its default (`width:` past `style`).
func (bl *irScalarBuilder) stdShortCallLeavesAHole(t *ast.Call, owner, method, module string) bool {
	fs := bl.g.stdShortCandidates(owner, method, module)
	if len(fs) != 1 || fs[0].decl == nil {
		return false
	}
	plan, planned := bl.g.callPlan(t, len(fs[0].params))
	if !planned {
		return false
	}
	_, contiguous := contiguousSlots(plan)
	return !contiguous
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
	return len(fs) == 1 && fs[0].lowerable() && bl.g.leavesDefaults(t, len(fs[0].params))
}

// stdShortCandidates is the std functions `Owner.method` names: every
// module's for a bare owner, and module's own for a qualified one. With no
// owner, `module.method` names the std file's top-level function.
func (g *gen) stdShortCandidates(owner, method, module string) []*stdFunc {
	if owner == "" {
		if f := g.std.byFile[module+"."+method]; f != nil {
			return []*stdFunc{f}
		}
		return nil
	}
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

// stdDefault is a std enum field's default n, an rt constant, at kind k: an
// Int, or a wrapping distinct over one such as Duration.
func (bl *irScalarBuilder) stdDefault(at ast.Node, k kind, n int64) ir.Temp {
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
// positional arguments evaluated in order, any Iter source viewed as the
// sequence rt takes, a one-parameter function over its element returning
// Unit, and a `List<Task<Unit>>` of handles.
func (bl *irScalarBuilder) supervisorSpawnAll(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	bl.mustBePlacedIntrinsic(t, 3, "Supervisor.spawn_all")
	args := bl.irQualLowerArgs(t)
	if !args.ok || !irSupervisorKind(args.kinds[0]) {
		return no()
	}
	// The source is viewed as the sequence rt takes, as Task.spawn_all's
	// is.
	view, elem, ok := bl.iterPlainView(argExpr(t.Args[1]), args.temps[1], args.kinds[1])
	if !ok {
		return no()
	}
	args.temps[1] = view
	fn := args.kinds[2]
	if fn.tag != tagFunc || fn.comp == nil || len(funcParams(fn)) != 1 || funcParams(fn)[0] != elem || funcResult(fn) != kindUnit {
		return no()
	}
	handle := bl.g.taskHandleKind(kindUnit)
	result := bl.g.listKind(handle)
	// kindInvalid: lookup — a List over a handle with no interned kind declines.
	if result == kindInvalid {
		panic("irbuild: supervisorSpawnAll: List<Task<Unit>> is always interned")
	}
	return bl.concHostEmit(t, "Supervisor.spawn_all", result, args.temps...)
}

// irStdSlotRetain builds the `_DEFAULT<slot>` accessor of f for the VM
// with the one accessor builder (irSlotAccessor), in g, the declaring
// module's scope, and records it on f's canonical function.
func (g *gen) irStdSlotRetain(f *stdFunc, fd *ast.FuncDef, slot int) {
	if g.irMod == nil {
		return
	}
	sym := g.irCalleeSym(irSlotDefault{f, slot}, fmt.Sprintf("%s default %d", f.key, slot+1))
	fn := g.irSlotAccessor(fd, &fnSig{decl: fd, params: f.params}, sym, stdSlotName(f, slot), slot, irFromStd)
	if fn == nil {
		return
	}
	if f.canon != nil {
		f = f.canon
	}
	if f.irSlot == nil {
		f.irSlot = map[int]*ir.Func{}
	}
	f.irSlot[slot] = fn
}

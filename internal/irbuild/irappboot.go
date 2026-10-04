package irbuild

import (
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A program's boot, the application-field reads (`App.field`) and `with`
// overrides against the app it publishes, and dispatch on an erased field
// value.
//
// irBootRetain builds an entry file's boot as an ordinary `ir.Func` taking
// a Startup and answering the app value, which only the VM reads; `irbuild.go`
// records the entry unit's boot on its module with `ir.Module.SetBoot`,
// retained or not, so a VM can refuse a program whose boot it cannot run.
//
// A Context is a retained value kind: `Context.root()`, a field of the
// declared result struct and `Context.with_timeout` make, pass or derive one;
// every other Context operation keeps its boundary. A field of a local interface's
// type takes a concrete value that
// is erased to the interface's existential. `defer`, `try` and inline loops keep their boundaries,
// because boot's cleanup and failure reporting are program-lifetime rules the
// retained body does not model. An anonymous record result is not retained.
//
// The entry boot a `tests` group's `boot` line calls is the same function,
// recorded on the test file's module with `ir.Module.AddTestBoot`; the VM
// starts it with `Machine.BootTest` (irtestgroup.go), and an app-field read in
// a function its cases call reads that app.
//
// Outside boot, a read is a RefAppField. A `with` statement lowers to a
// Store that holds for the rest of the activation; a block other than the
// activation's own body reads the scope in force before its first `with` and
// restores it with a StoreScope at its exit (irdefer.go's irWithScope). The
// Context field is read as a RefContext, the active execution context, and
// rebound by a StoreContext with a floor, context entry and deadline release.
// A field of a local interface's type is
// read as its existential, written by
// native erasure, and consumed by an erased dispatch (irinterfacecall.go),
// whose bodies `irRecordImpls` lists on the module for the VM.

// irContextKind reports the prelude Context kind.
func irContextKind(k kind) bool {
	return k == stdHostOriginKind("std/context", "Context")
}

// irDynamicKind reports std/dynamic's Dynamic. Its value is an rt.Dynamic,
// and only host calls look inside it.
func irDynamicKind(k kind) bool {
	return k == stdHostOriginKind("std/dynamic", "Dynamic")
}

// irBootStructKind is a boot result declaration: a struct whose fields hold
// retained values, including the Context, task and channel handles and
// supervisors, or an interface's existential.
func irBootStructKind(d *typeDef) bool {
	if d == nil || d.isEnum || d.isDistinct || !d.lowerable || len(d.fields) == 0 {
		return false
	}
	for i := range d.fields {
		f := &d.fields[i]
		if f.boxed || !(irRetainedValueKind(f.k) || irExistentialKind(f.k)) {
			return false
		}
	}
	return true
}

// irBootRecordKind is an anonymous record a boot may answer: every field a
// retained value, a Context among them as the analyzer requires.
func irBootRecordKind(k kind) bool {
	if k.tag != tagAnonStruct || k.comp == nil || len(k.comp.names) == 0 || len(k.comp.names) != len(k.comp.parts) {
		return false
	}
	for _, p := range k.comp.parts {
		if !irRetainedValueKind(p) && !irExistentialKind(p) {
			return false
		}
	}
	return true
}

// irExistentialKind is an interface's existential, a local interface's or a
// sibling file's. A value of it is the concrete value itself in the VM, and a
// call through it dispatches on that value's type (irIfaceMethodSym).
func irExistentialKind(k kind) bool {
	return k.tag == tagIface && k.iface != nil
}

// irErases reports whether a value of kind have enters a position of
// existential kind want by native erasure: a declared, non-enum type with a
// lowered impl of the interface.
func (g *gen) irErases(want, have kind) bool {
	if irStdDisplayExistential(want) && (irDisplayErasedScalar(have) || g.irDisplayErasedNamed(want, have)) {
		// A value held as std Display's existential: the erased rendering
		// answers a scalar's own Display text and calls a declared type's
		// retained impl (vm.displayErased).
		return true
	}
	if irStdStructExistential(want) {
		// std's universal Struct: every declared struct and anonymous
		// record implements it structurally, and the VM holds the value.
		return (have.tag == tagNamed && have.def != nil && !have.def.isEnum && !have.def.isDistinct &&
			irRetainedValueKind(have)) || (have.tag == tagAnonStruct && irRetainedRecordKind(have))
	}
	if _, scalar := irDispatchScalarName(have); scalar {
		// A builtin scalar with a local impl (`impl Numberish for Int`): the
		// VM dispatches on the scalar's own type name (vm.runtimeTypeName).
		return irExistentialKind(want) && g.implements(want.iface, have)
	}
	return irExistentialKind(want) && have.tag == tagNamed && have.def != nil && !have.def.isEnum &&
		irRetainedValueKind(have) && g.implements(want.iface, have)
}

// irErasesFields reports whether an anonymous record of kind have enters a
// position of record kind want field by field: the same field names, and each
// field either the same kind or entering its position by erasure (irErases).
// A boot answering `{logger: Console{}}` for `{logger: Logger}` is the case.
func (g *gen) irErasesFields(want, have kind) bool {
	if want.tag != tagAnonStruct || have.tag != tagAnonStruct || want.comp == nil || have.comp == nil ||
		len(want.comp.names) != len(have.comp.names) || len(want.comp.parts) != len(have.comp.parts) ||
		len(want.comp.names) != len(want.comp.parts) {
		return false
	}
	erased := false
	for i, name := range want.comp.names {
		if have.comp.names[i] != name {
			return false
		}
		w, h := want.comp.parts[i], have.comp.parts[i]
		switch {
		case w == h:
		case g.irErases(w, h):
			erased = true
		default:
			return false
		}
	}
	return erased
}

// irDispatchScalarName is the type name the VM's dispatch reads off a boxed
// builtin scalar (vm.runtimeTypeName), for the scalars an existential may
// hold.
func irDispatchScalarName(k kind) (string, bool) {
	switch k {
	case kindInt:
		return "Int", true
	case kindFloat:
		return "Float", true
	case kindBool:
		return "Bool", true
	case kindString:
		return "String", true
	}
	return "", false
}

// irStdStructExistential is std's universal `Struct` interface's
// existential (`List<Struct>`'s element).
func irStdStructExistential(k kind) bool {
	return irExistentialKind(k) && k.iface.nomi == "Struct"
}

// irDisplayErasedNamed is a declared, non-generic type whose `impl Display`
// an erased Display rendering can call: the program's own (recorded by
// irRecordDisplayImpls) or a retained std body (irRecordStdDisplay).
func (g *gen) irDisplayErasedNamed(want, have kind) bool {
	if have.tag != tagNamed || have.def == nil || have.def.genericOf != nil || have.def.preludeOf != nil || !irRetainedValueKind(have) {
		return false
	}
	if g.implements(want.iface, have) {
		return true
	}
	if g.std == nil {
		return false
	}
	f := stdPick(g.std.byIface["Display.to_string"][have], []kind{have})
	if f != nil && f.canon != nil {
		f = f.canon
	}
	return f != nil && f.irBody != nil
}

// irDisplayErasedScalar is a scalar whose Display text the VM's erased
// Display rendering answers itself (vm.displayErased).
func irDisplayErasedScalar(k kind) bool {
	return k == kindInt || k == kindFloat || k == kindBool || k == kindString || isDecimalKind(k) || irByteValueKind(k)
}

// irBootRetain builds the program boot's body for the VM. It emits nothing
// and mints no Go identifier; the parameters are bound for the build under
// their Nomi names, which no Go reader spells.
func (g *gen) irBootRetain(fd *ast.FuncDef) {
	// VM-only: no Go names what the build resolves.
	// Every refusal below names its reason: a boot the VM finds unretained
	// reports the recorded reason for it, and one with none recorded would
	// read "no decline reason recorded".
	if fd.Body == nil || fd.ReturnTypeExpr == nil {
		g.irDeclineOpen(fd.Name)
		irDeclineNote("a boot without a body or a declared result")
		return
	}
	result := g.typeOf(fd.ReturnTypeExpr)
	// A named application struct, or an anonymous record
	// (`{tag: String, execution: Context}`), whose fields the VM's boot
	// publishes by name alike.
	if !(result.tag == tagNamed && irBootStructKind(result.def)) && !irBootRecordKind(result) {
		g.irDeclineOpen(fd.Name)
		irDeclineNote("a boot result whose fields are not all retained values: " + result.nomi())
		return
	}
	g.pushScope()
	defer g.popScope()
	params := make([]kind, len(fd.Params))
	for i, p := range fd.Params {
		k := g.typeOf(p.TypeAnnotation)
		params[i] = k
		if !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: k})
		}
	}
	sig := irFuncSig{result: result, decl: fd, name: fd.Name, origin: irFromModule}
	sh := g.irFuncShellWithCallee(fd, sig, params, g.irCalleeSym(fd, fd.Name))
	if sh == nil {
		g.irDeclineOpen(fd.Name)
		irDeclineNote("no function shell")
		return
	}
	previous, previousResult := g.irBoot, g.result
	g.irBoot, g.result = true, result
	defer func() { g.irBoot, g.result = previous, previousResult }()
	g.irBodyObserve(irBodyModuleFn, true)
	g.irScalarLower(fd, sig, nil, sh, result)
}

// irDeclaresBoot reports whether this program starts an app: a program boot,
// or a boot in one of the entry's `tests` groups. An app-field read in a
// program that starts none has nothing to find.
func (g *gen) irDeclaresBoot() bool {
	if g.bootDecl() != nil {
		return true
	}
	nodes := g.nodes
	if g.reg != nil && len(g.reg.gens) > 0 {
		if g.reg.gens[0] == nil {
			return false
		}
		nodes = g.reg.gens[0].nodes
	}
	for _, n := range nodes {
		if t, isTest := n.(*ast.TestDecl); isTest && testDeclaresBoot(t) {
			return true
		}
	}
	return false
}

func testDeclaresBoot(t *ast.TestDecl) bool {
	if t.Boot != nil {
		return true
	}
	if t.Body == nil {
		return false
	}
	for _, s := range t.Body.Stmts {
		if child, isTest := s.(*ast.TestDecl); isTest && testDeclaresBoot(child) {
			return true
		}
	}
	return false
}

// irRecordImpls records, on this unit's module, every retained impl body a
// dispatched call can select: a local interface's method implemented for a
// declared non-enum type, keyed by the type identity the VM's values carry.
func (g *gen) irRecordImpls() {
	g.irRecordDisplayImpls()
	g.irRecordRowDebugImpls()
	g.irRecordKeyImpls()
	for _, d := range g.implOrder {
		var typ *ir.Symbol
		if name, scalar := irDispatchScalarName(d.recv); scalar {
			typ = g.irTypes().Symbol(irScalarTypeToken{name}, name)
		} else if d.recv.tag == tagNamed && d.recv.def != nil && !d.recv.def.isEnum {
			typ = g.irTypeSym(d.recv.def)
		}
		if d.iface == nil || typ == nil {
			continue
		}
		for _, m := range d.iface.order {
			it := d.items[m.name]
			if it == nil {
				continue
			}
			fn := g.irCalleeSym(it, m.name)
			if g.irMod.FuncFor(fn) == nil {
				continue
			}
			method := g.irIfaceMethodSym(d.iface, m)
			if method == nil {
				continue
			}
			g.irMod.Implement(method, typ, fn)
		}
	}
}

// irScalarTypeToken identifies a builtin scalar's type symbol in the table.
type irScalarTypeToken struct{ name string }

// irRecordDisplayImpls records every retained `impl Display` body of a
// declared, non-generic type, which an erased Display rendering selects by
// the value's runtime type name (ir.Module.ImplementDisplay).
func (g *gen) irRecordDisplayImpls() {
	for _, d := range g.implOrder {
		if d.ifaceName != "Display" || !d.lowerable || d.recv.tag != tagNamed || d.recv.def == nil ||
			d.recv.def.genericOf != nil || d.recv.def.preludeOf != nil {
			continue
		}
		it := d.items["to_string"]
		if it == nil {
			continue
		}
		fn := g.irCalleeSym(it, "to_string")
		if g.irMod.FuncFor(fn) == nil {
			continue
		}
		g.irMod.ImplementDisplay(g.irTypeSym(d.recv.def), fn)
	}
}

// irRecordRowDebugImpls records every retained HAND-WRITTEN `impl Debug` body
// of a user's declared, non-generic type (ir.Module.ImplementRowDebug), which
// an assertion's `values:` rows render such a value through. A derived or
// universal Debug is synthesized and not recorded, and neither is a std
// type's: those values keep their structural row (a Decimal reads `1.50`, not
// Debug's `1.50d`).
func (g *gen) irRecordRowDebugImpls() {
	if g.stdModule != "" {
		return
	}
	for _, d := range g.implOrder {
		if d.ifaceName != "Debug" || d.synth || !d.lowerable || d.recv.tag != tagNamed || d.recv.def == nil ||
			d.recv.def.genericOf != nil || d.recv.def.preludeOf != nil {
			continue
		}
		it := d.items["inspect"]
		if it == nil {
			continue
		}
		fn := g.irCalleeSym(it, "inspect")
		if g.irMod.FuncFor(fn) == nil {
			continue
		}
		g.irMod.ImplementRowDebug(g.irTypeSym(d.recv.def), fn)
	}
}

// irRecordKeyImpls records every retained HAND-WRITTEN `impl Equatable` and
// `impl Hashable` body of a declared record type of this unit, a generic
// instance's among them (ir.Module.ImplementEquatable). A Map key, a Set
// element and `==` over a container answer through them at any depth. A
// derived impl is structural, which is what the machine answers without an
// entry, so it is not recorded.
//
// It reads implsByIface rather than implOrder because a generic type's
// per-instance blocks (registerImplAt) are only there, and sorts by type name
// so the module's entries do not follow map order.
func (g *gen) irRecordKeyImpls() {
	for _, iface := range []string{"Equatable", "Hashable"} {
		method := "equal?"
		if iface == "Hashable" {
			method = "hash"
		}
		type entry struct {
			typ, fn *ir.Symbol
		}
		var entries []entry
		for recv, d := range g.implsByIface[iface] {
			if d == nil || d.synth || !d.lowerable || !irKeyImplRecv(recv) {
				continue
			}
			it := d.items[method]
			if it == nil {
				continue
			}
			fn := g.irCalleeSym(it, method)
			if g.irMod.FuncFor(fn) == nil {
				continue
			}
			entries = append(entries, entry{g.irTypeSym(recv.def), fn})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].typ.Name() < entries[j].typ.Name() })
		for _, e := range entries {
			if iface == "Hashable" {
				g.irMod.ImplementHashable(e.typ, e.fn)
			} else {
				g.irMod.ImplementEquatable(e.typ, e.fn)
			}
		}
	}
}

// irKeyImplRecv is a receiver whose values are records the machine keys by
// their declared type: a struct, an enum or a distinct, not a prelude or std
// container instance (a Set's own equality is the structural `==`, and
// routing it back through its body would recurse), not an opaque rt leaf
// (Byte, Bytes) and not a scalar.
func irKeyImplRecv(k kind) bool {
	if k.tag != tagNamed || k.def == nil || irScalarLeafKind(k) || isDecimalKind(k) {
		return false
	}
	d := k.def
	return d.preludeOf == nil && d.genStructOf == nil && d.genHostOf == nil && !d.rtOpaque
}

// irIfaceMethodSym is the symbol a dispatched call to interface method m of d
// names and an impl of it is recorded under. A local interface's method is
// this unit's; a sibling file's (d a mirror) is the DECLARING unit's, interned
// in that unit's table as irSiblingCalleeSym interns a sibling `fn`, so a
// dispatch in one file finds an impl recorded in another. nil when the
// declaring unit's gen or its declaration is not available.
func (g *gen) irIfaceMethodSym(d *ifaceDef, m *ifaceMethod) *ir.Symbol {
	if d.foreign == "" {
		return g.irCalleeSym(m, d.nomi+"."+m.name)
	}
	if g.reg == nil || d.unit < 0 || d.unit >= len(g.reg.gens) {
		return nil
	}
	owning := g.reg.gens[d.unit]
	if owning == nil {
		return nil
	}
	for _, od := range owning.ifaceOrder {
		if od.decl == d.decl && od.foreign == "" {
			if om := od.methods[m.name]; om != nil {
				return owning.irCalleeSym(om, od.nomi+"."+om.name)
			}
		}
	}
	return nil
}

// appFieldStore lowers the value of w and one Store over the read's
// field-contract symbol (a StoreContext for the Context field, which also
// floors and bounds the active context). The store holds for the rest of the
// activation unless the enclosing block's saved scope restores it first
// (withStmt).
func (bl *irScalarBuilder) appFieldStore(w *ast.With) bool {
	read, ok := bl.g.appRead(w.Target)
	if !ok {
		irDeclineNote("an app-field write the checker did not resolve")
		return false
	}
	k := bl.g.appFieldKind(read)
	switch {
	case !bl.g.irDeclaresBoot() && !irContextKind(k) && !bl.testApp:
		irDeclineNote("an app-field write in a program with no boot")
		return false
	// kindInvalid: sentinel — a write with no declared field contract declines.
	case k == kindInvalid || !(irRetainedValueKind(k) || irExistentialKind(k)):
		irDeclineNote("an app-field write of an unretained field")
		return false
	}
	val, vk, _, ok := bl.lower(w.Value)
	if !ok || (vk != k && !bl.g.irErases(k, vk)) {
		return false
	}
	name := read.Field.Name
	sym := bl.g.irTypes().Symbol(scopedFieldToken{name, k}, "$"+name)
	s := ir.NewStoreAppField(bl.g.irNodePos(w), sym, val)
	if irContextKind(k) {
		s = ir.NewStoreContext(bl.g.irNodePos(w), sym, val)
	}
	bl.b.Append(s)
	bl.irDiscardStmtUnit(w)
	return true
}

// appFieldRead lowers `MyApp.field`: one RefAppField over the same
// field-contract symbol a write uses, spelled with a scoped lookup. The
// Context field is a RefContext, which answers the active execution context
// rather than a published value. A program with no boot publishes nothing a
// read could find, so such a read declines.
func (bl *irScalarBuilder) appFieldRead(t *ast.FieldAccess, read analysis.AppRead) (ir.Temp, kind, bool, bool) {
	k := bl.g.appFieldKind(read)
	// kindInvalid: sentinel — a read with no declared field contract declines.
	if k == kindInvalid || !(irRetainedValueKind(k) || irExistentialKind(k)) || (bl.inTest && !bl.testApp) || (!bl.g.irDeclaresBoot() && !irContextKind(k)) {
		return ir.NoTemp, kindInvalid, false, false
	}
	name := read.Field.Name
	sym := bl.g.irTypes().Symbol(scopedFieldToken{name, k}, "$"+name)
	if irContextKind(k) {
		r := ir.NewRefContext(bl.g.irNodePos(t), bl.f.NewTemp(), sym)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: k})
		return r.Dst(), k, r.Stable(), true
	}
	r := ir.NewRefAppField(bl.g.irNodePos(t), bl.f.NewTemp(), sym)
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: k})
	return r.Dst(), k, r.Stable(), true
}

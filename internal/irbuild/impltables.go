package irbuild

import "github.com/nomi-language/nomi/internal/ast"

// The impl tables, and the blocks of a generic interface registered for an
// instance only when its impls are asked for.
//
// A gen files the impl blocks it registers in three tables: implsByIface
// (a receiver's impl of an interface), instImpls (a receiver's impls of
// one generic interface at several type arguments) and implItemsByDecl
// (each impl function by the declaration it lowers). This file is the only
// code that reads or writes them; TestImplTablesAreReadThroughTheAccessor
// fails for a read anywhere else.
//
// # On demand
//
// An instance of a generic type is given its template's impl blocks when it
// is built (registerInstanceImpls). Registering a block resolves its
// interface, and a generic interface's arguments are types the block names
// at the instance: `impl Deeper<Box<Box<T>>> for Box<T>` at Box<Int> names
// Box<Box<Int>>, whose block names Box<Box<Box<Int>>>, so registering every
// instance's blocks at once builds a chain without end. So a block whose
// interface takes type arguments (the checker's record of the block has
// them, analysis.ImplTypeArgs.Args) is filed as pending on the instance
// (registerInstanceBlock), and registered when a reader first asks for an
// impl of that instance: implAt, instImplsAt, implsAtRecv, implItemsAt and
// implsOf resolve the receiver's pending blocks first (resolveImpls). Each
// registration builds the instances its interface names, with their own
// blocks of a generic interface pending, so the chain stops after one step,
// and the impls built are those of the types a lowered body asks about.
//
// Every other block is registered when the instance is built, for two
// reasons. The machine selects an Equatable, Hashable, Display or Debug
// body by a value's runtime type from tables filled by walking every
// registered impl (irRecordKeyImpls, irRecordDisplayImpls, the erased
// renderings), where no reader names the receiver, so those impls must
// exist once the instance does; none of those interfaces is generic. And a
// block that cannot be built refuses its instance (registerInstanceImpls),
// which a registration after the instance is in use cannot. A pending
// block that cannot be built is refused alone, and the call that asked for
// it declines.
//
// A block of a generic interface is only ever reached through a receiver a
// reader names: a call, a boxing (bindsImpl asks for the impl of the value's
// type), a type's impls (implsOf). So a walk over the tables that names no
// receiver (resolvedImpls, implTable: the dispatch entries of
// erased instances, the key tables, a lookup by declaration) sees every
// block anything reached.

// instBlock is one impl block of a template registered for one instance,
// now or when the instance's impls are first asked for.
type instBlock struct {
	tpl  *genericTemplate
	decl *ast.ImplBlock
	// recv is the gen's def of the instance, its own or its mirror of
	// another file's, and args its type arguments in the gen's kinds.
	recv kind
	args []kind
	// inst is the owner's instance, whose checker types the frame carries.
	inst *typeDef
}

// pendingBlock is a block of a generic interface filed on its instance,
// with the key of its interface (ifaceKey), which a lookup of the
// instance's impls of that interface registers it for.
type pendingBlock struct {
	instBlock
	key string
}

// registerInstanceBlock registers b now, or files it as pending on its
// instance when its interface takes type arguments. It reports why a block
// registered now cannot be built.
func (g *gen) registerInstanceBlock(b instBlock) (string, string) {
	if rec := g.implBlockRecord(b.decl); rec != nil && len(rec.Args) > 0 {
		if g.pendingImpls == nil {
			g.pendingImpls = map[*typeDef][]pendingBlock{}
		}
		g.pendingImpls[b.recv.def] = append(g.pendingImpls[b.recv.def], pendingBlock{b, ifaceKey(rec.Origin, rec.Interface)})
		return "", ""
	}
	_, why, detail := g.registerInstanceBlockNow(b)
	return why, detail
}

// registerInstanceBlockNow registers b under its instance's frame when the
// instance meets the block's `where` bound (instanceMeetsWhere), and queues
// its functions to be built. It answers the block's def, nil outside the
// bound, and why it cannot be built.
func (g *gen) registerInstanceBlockNow(b instBlock) (*implDef, string, string) {
	if !g.instanceMeetsWhere(b.decl, b.tpl, b.inst) {
		// No checked program calls a function of a block at an instance
		// outside its `where` bound.
		return nil, "", ""
	}
	defer g.enterTypeScope(b.tpl.scope)()
	g.pushGenericSubst(substImpl, b.tpl, b.args, b.inst.genericTypes)
	id := g.registerImplAt(b.decl, b.recv)
	g.popGenericSubst()
	id.typeScope = b.tpl.scope
	if !id.lowerable {
		return id, id.why, id.whyDetail
	}
	// The frame is carried on the queue entry, not left to be re-derived at
	// build time. flushGenericImpls runs after this frame has been popped,
	// and the BODY is where `T` has to resolve.
	g.genericImplQueue = append(g.genericImplQueue,
		&genericImplInst{impl: id, tpl: b.tpl, args: b.args, recv: b.inst})
	return id, "", ""
}

// resolveImpls registers the blocks pending on recv's instance that wants
// answers true for. A block that cannot be built is refused alone: its
// instance is already in use.
func (g *gen) resolveImpls(recv kind, wants func(pendingBlock) bool) {
	if recv.tag != tagNamed || recv.def == nil {
		return
	}
	pending := g.pendingImpls[recv.def]
	var now, later []pendingBlock
	for _, b := range pending {
		if wants(b) {
			now = append(now, b)
		} else {
			later = append(later, b)
		}
	}
	if len(now) == 0 {
		return
	}
	// Taken before any is registered, since a registration may ask for
	// this instance's impls again.
	if len(later) == 0 {
		delete(g.pendingImpls, recv.def)
	} else {
		g.pendingImpls[recv.def] = later
	}
	for _, b := range now {
		id, _, _ := g.registerInstanceBlockNow(b.instBlock)
		if substVerify && id != nil && id.lowerable && id.ifaceKey != b.key {
			panic("irbuild: the block " + id.label() + " was pending under the interface key " + b.key +
				" and is filed under " + id.ifaceKey + ", so a lookup by its key would not have registered it")
		}
	}
}

// anyBlock resolves every pending block of a receiver.
func anyBlock(pendingBlock) bool { return true }

// keyed resolves the pending blocks of a receiver whose interface's key is
// key.
func keyed(key string) func(pendingBlock) bool {
	return func(b pendingBlock) bool { return b.key == key }
}

// implAt is recv's impl of the interface whose key is key (implsKey), or
// nil.
func (g *gen) implAt(key string, recv kind) *implDef {
	g.resolveImpls(recv, keyed(key))
	return g.implsByIface[key][recv]
}

// instImplsAt is recv's impls of the generic interface whose key is key
// when it implements it at several type arguments (instimpl.go), or nil.
func (g *gen) instImplsAt(key string, recv kind) []*implDef {
	g.resolveImpls(recv, keyed(key))
	return g.instImpls[key][recv]
}

// implsAtRecv is recv's impl of each interface, by key, where it has one
// impl of it.
func (g *gen) implsAtRecv(recv kind) map[string]*implDef {
	g.resolveImpls(recv, anyBlock)
	out := map[string]*implDef{}
	for key, byRecv := range g.implsByIface {
		if d := byRecv[recv]; d != nil {
			out[key] = d
		}
	}
	return out
}

// resolvedImpls is every receiver's impl of the interface whose key is
// key, among the blocks registered so far. A block pending on an instance
// is not among them, which nothing that names no receiver needs (see the
// head of this file).
func (g *gen) resolvedImpls(key string) map[kind]*implDef {
	return g.implsByIface[key]
}

// implTable is every receiver's impl of every interface, by key, among the
// blocks registered so far, with the pending blocks of each of recvs
// registered first.
func (g *gen) implTable(recvs ...kind) map[string]map[kind]*implDef {
	for _, r := range recvs {
		g.resolveImpls(r, anyBlock)
	}
	return g.implsByIface
}

// implItemsAt is the items this gen registered for decl (ownDecl), with
// the blocks pending on recv that may hold it registered first: a block
// that writes decl, or, for an interface's function, block, the block the
// record names as inheriting it, else every pending block.
func (g *gen) implItemsAt(decl ast.Node, block *ast.ImplBlock, recv kind) []*implItem {
	g.resolveImpls(recv, func(b pendingBlock) bool { return g.mayHold(b, decl, block) })
	return g.implItemsOf(decl)
}

// mayHold reports whether pending block b may hold an item of decl: it
// writes decl, or, for an interface's function, it is block, the block the
// record names as inheriting it, or block is nil.
func (g *gen) mayHold(b pendingBlock, decl ast.Node, block *ast.ImplBlock) bool {
	if fd, ok := g.ownDecl(decl).(*ast.FuncDef); ok {
		return blockWrites(b.decl, fd)
	}
	return block == nil || sameImplBlock(b.decl, block)
}

// pendingTemplate is the template of a block pending on one of this gen's
// instances that holds an item of decl, a function it writes or, with
// block, the interface's function block inherits, with the function's
// name; nil when no pending block does. Every such block is one template's.
func (g *gen) pendingTemplate(decl ast.Node, block *ast.ImplBlock) (*genericTemplate, string) {
	var name string
	switch fn := decl.(type) {
	case *ast.FuncDef:
		name = fn.Name
	case *ast.InterfaceMethod:
		if block == nil {
			return nil, ""
		}
		name = fn.Name
	default:
		return nil, ""
	}
	for _, pending := range g.pendingImpls {
		for _, b := range pending {
			if g.mayHold(b, decl, block) {
				return b.tpl, name
			}
		}
	}
	return nil, ""
}

// implItemsOf is the items this gen registered for decl (ownDecl), among
// the blocks registered so far.
func (g *gen) implItemsOf(decl ast.Node) []*implItem {
	return g.implItemsByDecl[g.ownDecl(decl)]
}

// implsOf is every impl block registered for the receiver k: the module's own
// blocks in source order, and for a generic instance the blocks registered
// against it per instance (registerInstanceImpls), which implOrder does not
// hold, its pending blocks included.
func (g *gen) implsOf(k kind) []*implDef {
	if k.def == nil || k.def.genericOf == nil {
		return g.implOrder
	}
	g.resolveImpls(k, anyBlock)
	var out []*implDef
	for _, it := range k.def.genericOf.impls {
		found := false
		for _, byRecv := range g.implsByIface {
			if d := byRecv[k]; d != nil && d.decl == it.decl {
				out = append(out, d)
				found = true
				break
			}
		}
		if found {
			continue
		}
		// One of the instance's impls of a generic interface at several
		// type arguments (instimpl.go).
		for _, byRecv := range g.instImpls {
			for _, d := range byRecv[k] {
				if d.decl == it.decl {
					out = append(out, d)
				}
			}
		}
	}
	return out
}

// fileImplItem files it, a function of a registered block, under src, the
// declaration it lowers.
func (g *gen) fileImplItem(src ast.Node, it *implItem) {
	if g.implItemsByDecl == nil {
		g.implItemsByDecl = map[ast.Node][]*implItem{}
	}
	g.implItemsByDecl[src] = append(g.implItemsByDecl[src], it)
}

// fileImpl files d, a resolved block that is no operator impl, under its
// interface and receiver. Two impls of one interface declaration for one
// type is a coherence error the analyzer rejects: both are refused and
// neither is picked, so ordering never decides. Two inherent blocks for one
// type are merged (mergeInherent).
func (g *gen) fileImpl(d *implDef) {
	byRecv := g.implsByIface[d.ifaceKey]
	if byRecv == nil {
		byRecv = map[kind]*implDef{}
		g.implsByIface[d.ifaceKey] = byRecv
	}
	if g.registerInstImpl(byRecv, d) {
		return
	}
	if prior, dup := byRecv[d.recv]; dup && d.ifaceKey == "" && d.iface == nil && prior.iface == nil && mergeInherent(prior, d) {
		// Two inherent blocks for one type, as a `type`'s generated
		// `try_from` and `unchecked` beside the block its file writes: one
		// table of the type's functions, each lowered with its own block.
		return
	} else if dup {
		// Impls of two interfaces of one name are two keys and never meet
		// here.
		prior.lowerable, prior.why, prior.whyDetail = false, "duplicate impl block", d.label()
		d.lowerable, d.why, d.whyDetail = false, "duplicate impl block", d.label()
		return
	}
	byRecv[d.recv] = d
}

// registerInstImpl files d among its receiver's impls of a generic interface
// when the receiver implements that interface at several type arguments,
// and reports whether it did. A first impl stays in implsByIface; a second
// at other arguments moves both here.
func (g *gen) registerInstImpl(byRecv map[kind]*implDef, d *implDef) bool {
	if d.instArgs == nil {
		return false
	}
	byIface := g.instImpls[d.ifaceKey]
	if held := byIface[d.recv]; len(held) > 0 {
		for _, o := range held {
			if sameKinds(o.instArgs, d.instArgs) {
				o.lowerable, o.why, o.whyDetail = false, "duplicate impl block", d.label()
				d.lowerable, d.why, d.whyDetail = false, "duplicate impl block", d.label()
				return true
			}
		}
		byIface[d.recv] = append(held, d)
		return true
	}
	prior, held := byRecv[d.recv]
	if !held || prior.instArgs == nil || sameKinds(prior.instArgs, d.instArgs) {
		return false
	}
	delete(byRecv, d.recv)
	if g.instImpls == nil {
		g.instImpls = map[string]map[kind][]*implDef{}
	}
	if byIface == nil {
		byIface = map[kind][]*implDef{}
		g.instImpls[d.ifaceKey] = byIface
	}
	byIface[d.recv] = []*implDef{prior, d}
	return true
}

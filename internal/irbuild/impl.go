package irbuild

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Interfaces, impl blocks, and the three ways a Nomi call finds its callee.
//
// # Three call shapes
//
// Interface dispatch is mostly not dynamic dispatch.
//
//   - **Concrete qualifier.** `Dog.speak(rex)`, and also `Speech.speak(rex)`
//     when `rex` is statically a `Dog`: the qualifier plus the receiver's
//     static type name exactly one function. Lowered to a direct call. This is
//     the common case in real Nomi.
//   - **Existential.** An interface-typed parameter, field or element has lost
//     its concrete type, so the callee is a run-time fact. The call is a
//     dispatch under the method's symbol (erasedInterfacePlan,
//     irIfaceMethodSym), and the VM selects the impl by the type name the
//     value carries (irboxed.go).
//   - **Type-parameter qualifier.** `T.from_json(x)` in a bounded generic
//     function. The function is lowered as an instance per type argument, in
//     which `T` is a concrete type and the call is the first shape. See
//     dict.go.
//
// `self` is an ordinary parameter in whatever position the interface declared
// it (selfShapeOf, selfpos.go), not "argument zero", and an interface function
// may have no self-typed parameter at all (`Literal`, `FromJson`, `App`).
//
// # A default is monomorphized, not shared
//
// `interface Identity { fn name(v: self) -> String; fn describe(v: self) -> String
// { "I am " + Identity.name(v) } }` gives every implementor a `describe`. The
// body is lowered once per implementing type, with `self` bound to that
// concrete type, so `Identity.name(v)` inside it is itself a direct call. An
// impl block that supplies the function overrides it by already occupying the
// name.
//
// # Derives are not special-cased
//
// `analysis.LowerDerives` and `SynthesizeUniversalDebug` have already run
// by the time this package sees the AST, so `derive Equatable for Dog` arrives
// as an ordinary `impl Equatable for Dog { ... }` whose body is ordinary Nomi.
// It lowers through exactly the path a hand-written impl takes, so a derive
// has no separate implementation that could disagree with its Nomi body.
//
// SynthesizeUniversalDebug appends an `impl Debug for T` for every opaque and
// host type, at a fabricated position in the synth line band. Refusing one by
// name would blame a programmer for a construct nobody wrote. So a synthesized block is
// lowered speculatively: if its body does not lower cleanly, its refusals are
// discarded and the impl is marked unlowerable, which makes every site that
// would dispatch to it refuse at its own position, which is a position
// somebody wrote.

// ifaceDef is one `interface` declaration.
//
// A pointer to one of these is the interface's identity, as *typeDef is a
// struct's. It is what distinguishes `Greeter.greet` from `Farewell.greet` on
// a type implementing both: nothing in the path compares the method name
// alone.
type ifaceDef struct {
	nomi    string
	decl    *ast.InterfaceDef
	methods map[string]*ifaceMethod
	order   []*ifaceMethod
	// lowerable is false for an interface this builder cannot represent at
	// all; why names the construct for the refusal.
	lowerable bool
	why       string
	whyDetail string
	// useSiteOnly says the reason in `why` is reported at a USE and never at
	// the declaration.
	//
	// `ifaceDecl`'s own header states the rule for a non-dispatchable
	// function: it contributes no refusal there, because an interface whose
	// functions cannot be dispatched on an erased value is still usable
	// through concrete qualifiers, and refusing the declaration would make a
	// file unlowerable for a gap it may never exercise. A generic interface
	// is that case one level up, the whole declaration and not one of its
	// functions, and this field is the rule applied there.
	//
	// It is a SECOND FIELD instead of flipping `lowerable`. `lowerable` is read
	// by `typeOf`'s interface arms and `inferred.go`'s `solvedIface` arm to
	// decide whether a value may be ERASED to this interface, and the TEMPLATE
	// may not: `existential(d)` takes its identity from the `*ifaceDef`
	// pointer, so a value held as `Chooser` with no arguments would be one
	// existential for every instantiation. A value held as `Chooser<Int>` is
	// the existential of that instantiation's own def, and a call on a
	// concrete receiver reaches the impl block's instance (ifaceinst.go). The
	// only thing this field changes is whether the DECLARATION spends a
	// refusal on something the file may never reach.
	useSiteOnly bool
	// stdShared marks one of stdIfaceDefs' PROCESS-WIDE defs, set in exactly one
	// place — that table's builder — and nowhere else.
	//
	// It is the same question `stdIfaceOf` answers by scanning, reduced to a
	// field because `kind.packageNeutral` asks it and that predicate must not
	// depend on the spec TABLE: that dependency is an INITIALIZATION CYCLE once
	// the enum family has a container payload (`stdEnumDefs -> mapKindIn ->
	// internComp -> shareableParts -> packageNeutral -> stdIfaceOf ->
	// stdIfaceDefs -> stdIfaceSpecs -> stdEnumKind -> stdEnumDefs`), which the
	// Go compiler rejects. A predicate over KINDS must not reach a table built
	// over kinds.
	//
	// Still pointer identity and still not a name, which is the property
	// stdIfaceOf's own comment insists on: the flag is set only on the defs that
	// table builds, so a USER's `pub interface Display` has its own def with the
	// flag clear. `stdIfaceOf` remains for the callers that need the spec INDEX.
	stdShared bool
	// universal marks a STRUCTURAL marker interface: conformance is a property
	// of the value's shape rather than of an `impl` block anyone wrote.
	//
	// Set in exactly one place — structIfaceDef's builder — and read in exactly
	// one — `bindsImpl`. Both halves are named here because the field is the
	// only thing standing between "no impl of Struct is lowered for Dog", which
	// is TRUE and is not the question, and a refusal at every boxing site.
	//
	// It says nothing about DISPATCH: a universal def carries no methods, so
	// every interface-qualified call on an erased receiver still refuses through
	// the ordinary chain. See structiface.go.
	universal bool
	// foreign is the analyzer's file key of the declaring file when this def
	// is a mirror of a sibling file's interface. Two gens each hold an
	// *ifaceDef for one Nomi interface, as they hold two mirrors of one type
	// (foreign.go): pointer equality is the
	// identity within a gen. A mirror dispatches through the declaring
	// unit's method symbol (irIfaceMethodSym), so one interface has one set
	// of recorded impls however many files name it. See existential.go.
	foreign string
	// unit is the declaring module's index in the program, or -1 for a local
	// declaration. Read to reach the DECLARING file's analysis, which is what
	// decides whether one of its defaults means the same thing here.
	unit int
	// instOf is the generic interface DECLARATION this def is an instantiation
	// of, or nil for an ordinary def. An instance's dispatch symbols are named
	// by the template and its arguments (instMethodSym). See ifaceinst.go.
	instOf *ast.InterfaceDef
	// instArgs is an instance's type arguments, in the template's parameter
	// order; nil for an ordinary def. With instOf it is the instance's
	// identity (ifaceInstKey).
	instArgs []kind
	// stdOrigin is the declaring std module (`std/hashable`) of a std
	// interface's def built from std's declaration, a generic one's
	// instances included, and "" otherwise. Its dispatch symbols are the
	// program's (stdProgMethodSym). See stdprogiface.go.
	stdOrigin string
}

// ifaceMethod is one function an interface declares, required or default.
type ifaceMethod struct {
	name string
	decl *ast.InterfaceMethod
	// params are the declared parameter kinds, with a self-typed position
	// holding kindInvalid — it has no kind until an implementing type
	// supplies one.
	params []kind
	result kind
	// shape is the self-position model: which positions are self-typed, which
	// of them is the receiver, and what the RETURN type says about self.
	//
	// ONE field rather than three (`isSelf`, `selfAt`, `selfResult`), and
	// derived in ONE place (selfShapeOf), so no two sites compute "which
	// argument is the receiver" separately and agree only by coincidence, and
	// existential.go's mirror cannot copy part of it. See selfpos_test.go.
	shape selfShape
	// table reports that the method's signature can be dispatched:
	// methodSignatureGap names no gap in it.
	//
	// It may be set for a method that cannot be dispatched on an erased
	// receiver. `Zero.zero() -> self` has no receiver, so `why` names that
	// gap and every erased-receiver site refuses. See methodSignatureGap.
	table bool
	// why names what stops an erased-receiver dispatch, reported at a dispatch
	// site. It may be non-empty while table is set.
	why string
}

// dispatchable reports whether an erased receiver can select an implementation
// of this function.
func (m *ifaceMethod) dispatchable() bool { return m.table && m.why == "" }

// hasTable reports whether this function's signature has no gap
// (methodSignatureGap), which is a property of its signature and not of its
// receiver.
func (m *ifaceMethod) hasTable() bool { return m.table }

// implDef is one `impl Iface for Type` or inherent `impl Type` block.
type implDef struct {
	// siblingTemplate is the other file's template a block over it is
	// registered per instance for; the block itself lowers nothing.
	siblingTemplate *genericTemplate
	// stdRecvTemplate is the block over a std generic container this def
	// stands for; it lowers nothing, and each container kind a call needs
	// registers a def of its own (stdrecvimpl.go).
	stdRecvTemplate *stdRecvTemplate
	// recvType is the checker's type of the receiver (the type the checker
	// recorded for the block's header, at the instance the block is
	// registered at), nil where the record has none.
	recvType analysis.Type
	// ifaceName is the interface as written, "" for an inherent block. It is
	// recorded even when the interface is NOT declared in this module — an
	// `impl Debug for Member` still lowers its function, and
	// `Debug.inspect(m)` in the same module still reaches it directly. Only
	// the existential path needs the declaration.
	ifaceName string
	// ifaceKey is the interface's identity, the key d is filed under in
	// implsByIface and instImpls (implIfaceKey), "" for an inherent block.
	ifaceKey string
	iface    *ifaceDef
	// stdIface is the def of the std interface (or its instance) this block
	// implements when iface is nil because std declares it: what a value of
	// the receiver held as that interface dispatches through. iface stays
	// nil, so nothing else about the block changes. See stdprogiface.go.
	stdIface *ifaceDef
	recv     kind
	decl     *ast.ImplBlock
	// synth marks a block the front end produced from a `derive`, or the
	// name-only Debug of an opaque or a host type. Its refusals are never
	// reported at its own position.
	synth bool
	// ifaceSubst is the generic interface instantiation this block implements,
	// or nil. Non-nil exactly when `iface.instOf != nil`.
	//
	// Carried on the def rather than re-derived at lowering, for the reason
	// `genericImplInst` carries its own frame: `emitImpl` runs long after the
	// registering frame was popped, and a monomorphized default BODY is exactly
	// where a type parameter still has to resolve. See ifaceinst.go.
	ifaceSubst map[string]kind
	// ifaceSubstTypes is ifaceSubst as the checker's types, nil where the
	// impl block's record has none (substFrame).
	ifaceSubstTypes map[string]analysis.Type
	// instArgs is the interface type arguments this block implements, in
	// the interface's parameter order, or nil for a non-generic interface or
	// one whose arguments are not known here. A receiver may implement one
	// generic interface at several, each a block of its own (instimpl.go).
	instArgs []kind
	// oper is the stdlib operator interface this block implements, or nil.
	// Non-nil marks the one family that is registered in `g.operImpls` and
	// not in `g.implsByIface`, because a receiver may carry several impls of
	// one operator interface distinguished by their right-hand type and that
	// map's key drops it. `iface` stays nil for these: there is no
	// `*ifaceDef` to instantiate. See operimpl.go, which owns the mechanism
	// and the argument for the second index.
	oper *operIfaceSpec
	// operRhs and operOut are the interface's two type arguments as written on
	// this block's header — `Days` and `Day` for `impl Add<Days, Day> for Day`;
	// for `impl Steppable<Int> for Meters`, `Int` and `Maybe<Meters>`.
	// Read only when `oper != nil`; operRhs is the axis the dispatch key adds
	// and operOut is checked against the item's declared result.
	operRhs kind
	operOut kind

	items map[string]*implItem
	order []*implItem
	// gaps names the interface requirements this block does NOT supply a
	// lowerable function for, and why.
	//
	// A SECOND MAP RATHER THAN A FALSE `implItem.lowerable`. Most readers of
	// `d.items[name]` — among them `bindImpl`, `qualImplPlan` and
	// `operimpl.go` — test only `!= nil` and would take a refused item, naming a
	// body nobody built. An ABSENT item is the state every reader already
	// handles: it is what a block that simply does not supply the method looks
	// like.
	//
	// `implItem.lowerable` is therefore a hook with no writers (see
	// implwithhold_test.go).
	gaps map[string]implGap

	// typeScope is the block whose type declarations the block's receiver
	// is, for the universal Debug of a block-local type
	// (registerBlockLocalDebug). Pushed while the block is resolved and
	// lowered, since the receiver's name is visible only there.
	typeScope *blockTypeDecls

	// owner is the owner's name when this def holds an inherent block's owner
	// functions (ownerimpl.go), which have no receiver: recv is then
	// kindInvalid and names nothing.
	owner string

	lowerable bool
	why       string
	whyDetail string
}

// ownerName is the name a function of this block is qualified by:
// `Box.seven`, `Point.origin`.
func (d *implDef) ownerName() string {
	if d.owner != "" {
		return d.owner
	}
	return d.recv.nomi()
}

// implGap is one function an impl block does not supply, with the reason.
//
// `whole` is the UNIT the reason is about, carried on the value rather than
// re-derived from the text by the caller. `implItemSig` knows whether the row
// it matched is a property of ONE function's declaration or of the BLOCK's,
// and a caller matching on the `why` STRING would be a second implementation
// of that question.
type implGap struct {
	why    string
	detail string
	// whole marks a refusal about the BLOCK rather than about one function.
	whole bool
}

func (p implGap) named() bool { return p.why != "" }

// noteGap records that this block supplies no lowerable function for name.
func (d *implDef) noteGap(name string, gap implGap) {
	if d.gaps == nil {
		d.gaps = map[string]implGap{}
	}
	d.gaps[name] = implGap{why: gap.why, detail: gap.detail}
}

// mayWithhold reports whether this block may lower while supplying NO function
// for name.
//
// # The granularity rule, and its safety condition
//
// The unit this package decides lowerability about is the item: `emitImpl`
// lowers `d.order` one function at a time. Two sites could answer at the
// whole block instead: a written item with its own type-parameter binder
// (`implItemSig`'s `generic impl function`) and an inherited default carrying
// the interface method's own `why` (`inheritDefaults`). Answering there would
// let one method's refusal delete every sibling method and, because
// `registerInstanceImpls` turns an unlowerable impl into an unlowerable
// instance, the receiver type with them: `fn prefer<K>`, a default nobody
// wrote, would refuse `Holder{item: "ignored"}`.
//
// # Why this is a predicate and not a list of rows
//
// The condition for withholding is not that the gap is small. It is that
// nothing can dispatch to the method. If an erased receiver could dispatch
// to a withheld method, the dispatch would find no impl for this receiver at
// run time, on a program the checker accepted. `ifaceMethod.dispatchable` is
// that question, so it is asked here and not restated as a row list that a
// new gap row would join unnoticed.
//
// An erased-receiver call declines on `!m.dispatchable()`
// (erasedInterfacePlan), and `bindsImpl` may answer yes for a block with a
// withheld method only because the method it lacks is an undispatchable one.
//
// An inherent block has no interface and no dispatch, so a withheld member
// is reached only by a call that already handles an absent item.
func (d *implDef) mayWithhold(name string) bool {
	if d.iface == nil {
		return true
	}
	m := d.iface.methods[name]
	return m == nil || !m.dispatchable()
}

// implItem is one function an impl block supplies, or one interface default
// monomorphized for this block's receiver.
type implItem struct {
	// decl is the source function for a declared member. Inherited defaults
	// carry an interface method instead and have no FuncDef here.
	decl   *ast.FuncDef
	name   string
	params []kind
	result kind
	// params0 and body carry the parameters and body lowered. For an inherited
	// default these come from the INTERFACE's declaration, so the lowered
	// graph's positions point at the interface body, which is where the code
	// was written.
	params0 []ast.Param
	body    *ast.Block
	line    int
	// inherited is set for a monomorphized interface default.
	inherited bool
	// impl is the block this function belongs to, and instKey, for an
	// instance of a generic member, its type arguments: what its linkage
	// name is built from (link.go).
	impl    *implDef
	instKey string
	// public is `pub` on the declaration, which decides whether a call in
	// ANOTHER FILE may name this function type-qualified.
	//
	// It matters only for an INHERENT block, and that asymmetry is the
	// analyzer's rather than this builder's: a non-`pub` inherent impl function
	// called across a file boundary is "type 'Widget' has no member 'hidden'",
	// a front-end error, while an INTERFACE impl function with no `pub` at all
	// is accepted and runs. So an inherent one is checked and an interface one is not; see
	// siblingimpl.go, which is the only reader.
	public bool

	lowerable bool
}

// addImplItem registers it, a function of block d, under its name in d and
// under the declaration it lowers (implItemsByDecl, implItem.source): its
// `fn`, or for an inherited default the interface's
// function.
func (g *gen) addImplItem(d *implDef, it *implItem) {
	d.items[it.name] = it
	d.order = append(d.order, it)
	src, _ := it.source()
	if isNilNode(src) {
		return
	}
	if g.implItemsByDecl == nil {
		g.implItemsByDecl = map[ast.Node][]*implItem{}
	}
	g.implItemsByDecl[src] = append(g.implItemsByDecl[src], it)
}

// source is the declaration it lowers, as a checker record names it: its
// own `fn`
// (shellSource), or for an inherited default the interface's function with
// the block that inherits it.
func (it *implItem) source() (ast.Node, *ast.ImplBlock) {
	if it.decl != nil {
		return shellSource(it.decl), nil
	}
	if !it.inherited || it.impl == nil {
		return nil, nil
	}
	for _, d := range []*ifaceDef{it.impl.iface, it.impl.stdIface} {
		if d == nil {
			continue
		}
		if m := d.methods[it.name]; m != nil && m.decl != nil {
			return m.decl, it.impl.decl
		}
	}
	return nil, nil
}

// --- building ---------------------------------------------------------------

// declareIfaces registers every interface SHELL, before any type is resolved.
//
// It has to run before buildTypes, not after: a struct field or an enum
// payload may be interface-typed — an existential — and typeOf can only answer
// that once the name resolves to an *ifaceDef. Method signatures are resolved
// separately, in resolveIfaces, because they may name a declared struct and
// nothing in Nomi requires forward declaration in either direction.
//
// Everything the shell decides is knowable from the header alone, which is
// what makes the split possible rather than merely convenient.
func (g *gen) declareIfaces(nodes []ast.Node) {
	for _, n := range nodes {
		id, ok := n.(*ast.InterfaceDef)
		if !ok {
			continue
		}
		if _, dup := g.ifaces[id.Name]; dup {
			g.ifaces[id.Name].lowerable = false
			g.ifaces[id.Name].why = "duplicate interface declaration"
			continue
		}
		d := &ifaceDef{
			nomi:      id.Name,
			decl:      id,
			methods:   map[string]*ifaceMethod{},
			lowerable: true,
			unit:      -1,
		}
		switch {
		case len(id.TypeParams) > 0 || len(id.WhereClauses) > 0:
			// A generic interface's template is not erasable and not
			// dispatchable (an impl block resolves against its own instance,
			// ifaceinst.go). It is not refused at the declaration, which is
			// `ifaceDecl`'s own rule for a gap a file may never exercise.
			// See ifaceDef.useSiteOnly.
			d.lowerable, d.why, d.useSiteOnly = false, "generic interface", true
		}
		// A `variant` requirement contributes NOTHING here and refuses nothing:
		// Nomi has no operation that reaches a variant through the interface,
		// and the conformance obligation is discharged ENTIRELY by the analyzer
		// at the impl block, including for a non-enum receiver.
		g.ifaces[id.Name] = d
		if g.ifacesByDecl == nil {
			// A fixture may build a gen literal and not go through newGen.
			g.ifacesByDecl = map[ast.Node]*ifaceDef{}
		}
		g.ifacesByDecl[id] = d
		g.ifaceOrder = append(g.ifaceOrder, d)
	}
}

// resolveIfaces resolves every declared interface's function signatures, once
// the type table is complete.
func (g *gen) resolveIfaces() {
	for _, d := range g.ifaceOrder {
		if !d.lowerable {
			continue
		}
		g.resolveIface(d)
	}
}

func (g *gen) resolveIface(d *ifaceDef) {
	if d.stdOrigin != "" {
		// A std interface's declaration is std's source: its positions
		// resolve against std's file (stdprogiface.go).
		defer g.withStdSrc(strings.TrimPrefix(d.stdOrigin, "std/"))()
	} else if d.foreign != "" && d.unit >= 0 && d.unit != g.fileUnit {
		// A sibling file's interface (an instance of its generic
		// template): its signatures are that file's source, whose records
		// are in its analysis (srcFA).
		defer g.lowerNamesFrom(d.unit)()
	}
	for i := range d.decl.Methods {
		im := &d.decl.Methods[i]
		m := &ifaceMethod{
			name:   im.Name,
			decl:   im,
			result: kindUnit,
			// The ONE derivation. Everything below reads it; nothing below
			// re-scans the parameter list. See selfpos.go.
			shape: selfShapeOf(im.Params, im.ReturnTypeExpr),
		}
		for j := range im.Params {
			if m.shape.selfTyped(j) {
				// A self position has no kind until an implementing type
				// supplies one.
				m.params = append(m.params, kindInvalid)
				continue
			}
			// paramKind, so a destructuring parameter (`(a, b): (Int, Int)`,
			// `Point{x:, y:}`) has the kind its annotation or pattern head names;
			// a default body's prologue reads its names off that value.
			m.params = append(m.params, g.paramKind(im.Params[j]))
		}
		// A bare `self` return has no kind until an implementing type
		// supplies one, so it has none here either. A nested one
		// (`Maybe<self>`) is different: typeOf answers kindInvalid for it and
		// methodSignatureGap names it, which is why the model distinguishes
		// the two and does not carry a bool.
		if im.ReturnTypeExpr != nil && !m.shape.resultBareSelf() {
			m.result = g.typeOf(im.ReturnTypeExpr)
		}
		m.why = g.methodDispatchGap(d, im, m)
		// table follows the signature, not the receiver. A method with no
		// self-position parameter, or one returning self, still has it set:
		// an erased receiver cannot select an implementation of it, which is
		// what `why` above records and what every erased-receiver site
		// refuses under.
		//
		// An instance of a generic interface has its own methods:
		// `Gets<String>.got` and `Gets<Int>.got` are two, so a value held as
		// either dispatches to the impl at its own arguments. See
		// ifaceinst.go.
		if gap, _ := g.methodSignatureGap(im, m); gap == "" {
			m.table = true
		}
		d.methods[im.Name] = m
		d.order = append(d.order, m)
	}
}

// methodDispatchGap names what stops this function from being dispatched on an
// erased receiver, or "" when nothing does.
//
// Three std interfaces have no receiver to dispatch on: `FromJson.from_json`
// mentions self only inside its return, `App` declares no functions at all,
// and `Literal` declares one with no self anywhere in its parameters. That is
// a statement about the receiver, not about the signature, so it does not
// clear `table`.
//
// Precedence: the rows that make a signature unrepresentable outright outrank
// the receiver rows, and the per-parameter rows come after them. `hard` says
// which is which, so the order lives here and the rows live in one place
// each.
func (g *gen) methodDispatchGap(d *ifaceDef, im *ast.InterfaceMethod, m *ifaceMethod) string {
	gap, hard := g.methodSignatureGap(im, m)
	if hard {
		return gap
	}
	if why := erasedReceiverGap(m); why != "" {
		return why
	}
	return gap
}

// gapMultiSelf is the third erased-receiver row, and it is the one a corpus
// cannot find.
//
// `Comparable.compare(a: self, b: self)` selects its implementation from one
// receiver, and that implementation takes the receiver's concrete type at
// every other self position. Nothing makes that true: two
// `Comparable`-typed values may hold two different types, and the checker
// accepts it.
//
// This is a refusal and not a trap because the program is legal:
// `Comparable.compare` over a `shapes.Point` and a same-named `Point` from the
// entry file checks clean, and an answer would need the second operand's
// fields read structurally after dispatching on argument zero.
//
// It is an erased-receiver row and only that. A concrete qualifier
// (`Comparable.compare(p, q)` on two `Point`s) is a direct call with both
// operands statically typed and is unaffected, and so is a monomorphized
// interface default. See stdiface.go.
const gapMultiSelf = "interface function with more than one self-typed parameter"

// gapGenericIfaceFunc is an interface function with a type parameter of its
// own, or a `where` subject nothing binds: withheld from each implementing
// block and instantiated per call (methodinst.go).
const gapGenericIfaceFunc = "generic interface function"

// erasedReceiverGap names what stops an ERASED RECEIVER from selecting an
// implementation of m, from m's resolved SHAPE alone.
//
// Split out of methodDispatchGap because a STDLIB interface's *ifaceDef is
// built from a spec row rather than resolved from a declaration, so it never
// passes through methodDispatchGap — and two encodings of "which shapes cannot
// be dispatched on an erased receiver" is the drift this package's identity
// rules exist to prevent. See stdiface.go.
func erasedReceiverGap(m *ifaceMethod) string {
	switch {
	case m.shape.recvAt() < 0:
		return "interface function without a self-typed parameter"
	case m.shape.resultBareSelf():
		// A bare `self` result has no kind at an erased call site:
		// resolveIface resolves none for it.
		return "interface function returning self"
	case m.shape.multi():
		return gapMultiSelf
	}
	return ""
}

// gapNestedSelfResult is the row that says why a self inside a container is
// not the same question as a self return.
//
// `FromJson.from_json(json: Json) -> Result<self, Json.ShapeError>`,
// `Discrete.next(v: self) -> Maybe<self>`, `Steppable.step_by(…) -> Maybe<self>`
// are the three in std. typeOf answers kindInvalid for a self inside a
// container, so the method has no result kind.
//
// It is a signature row and it is `hard`, which keeps the diagnostic accurate.
// Without it `Parse.parse(text: String) -> Maybe<self>` would refuse as
// `interface function without a self-typed parameter`. Naming the receiver at
// a call whose problem is the return sends a reader to fix the wrong thing.
const gapNestedSelfResult = "interface function returning a type that contains self"

// methodSignatureGap names what in this function's signature stops every
// dispatch of it, or "" when nothing does. hard marks the rows that make the
// signature unrepresentable outright and not merely unrepresentable in one
// position; methodDispatchGap uses it for precedence.
//
// Nothing about the receiver appears here. The receiver rows are
// erasedReceiverGap's.
func (g *gen) methodSignatureGap(im *ast.InterfaceMethod, m *ifaceMethod) (string, bool) {
	switch {
	case g.ifaceMethodTypeParamIsOpen(im):
		// A type parameter NOTHING HAS BOUND — the method's own, or a `where`
		// clause subject that is not the interface's ground parameter.
		//
		// ONE arm and ONE derivation for the method's own type parameters and
		// its unbound `where` subjects: `fn pick<K>(…) where K: Comparable` has
		// both, and two arms with one answer would each mask the other's
		// absence. See ifaceinst.go.
		return gapGenericIfaceFunc, true
	case im.Extern:
		return "host-backed interface function", true
	case m.shape.resultNestedSelf():
		// Hard, so it outranks the receiver rows: a reader sent to the
		// receiver by a return-type obstacle fixes the wrong thing. See
		// gapNestedSelfResult.
		return gapNestedSelfResult, true
	}
	for j := range im.Params {
		// kindInvalid: reports — returns the refusal name its caller rejects under.
		if !m.shape.selfTyped(j) && m.params[j] == kindInvalid {
			return "non-scalar interface parameter type", false
		}
	}
	// A parameter default is not a gap here: a dispatch passes every
	// parameter whether or not it has a default. A call on an erased
	// receiver that omits one is filled from the interface's declaration
	// (erasedInterfacePlan).
	//
	// A bare self-typed return is not a gap here either. What it stops is
	// the erased-receiver route, reported by methodDispatchGap above. A
	// nested one is a gap and is handled above, hard.
	// kindInvalid: reports — returns the refusal name its caller rejects under.
	if im.ReturnTypeExpr != nil && !m.shape.resultBareSelf() && m.result == kindInvalid {
		return "non-scalar interface return type", false
	}
	return "", false
}

// buildImpls registers every impl block. The synthesized ones are lowered
// later, by settleImpls.
//
// Registration is complete before any body is lowered because an impl body
// may call any other impl in the program (a derived `Debug.inspect` on a
// struct with a struct field calls the field type's own inspect, and that
// type may be another file's), and because a synthesized block's
// lowerability has to be settled before an ordinary function body that
// dispatches to it is lowered.
func (g *gen) buildImpls(nodes []ast.Node) {
	for _, n := range nodes {
		ib, ok := n.(*ast.ImplBlock)
		if !ok {
			continue
		}
		g.registerImpl(ib)
	}
	g.registerBlockLocalDebug(g.blockTypeOrder)
}

// emitSynthImpls lowers the synthesized blocks among impls.
func (g *gen) emitSynthImpls(impls []*implDef) {
	for _, d := range impls {
		if !d.synth || !d.lowerable {
			continue
		}
		if !g.speculate(func() { g.emitImpl(d) }) {
			d.lowerable = false
			d.why = "impl block"
			d.whyDetail = d.label()
		}
	}
}

func (d *implDef) label() string {
	if d.ifaceName == "" {
		return d.ownerName()
	}
	return d.ifaceName + " for " + d.recv.nomi()
}

// implMemberLabel names one function of a block: interface-qualified when the
// block implements one, bare for an inherent block.
func implMemberLabel(d *implDef, method string) string {
	if d.ifaceName == "" {
		return method
	}
	return d.ifaceName + "." + method
}

func (g *gen) registerImpl(ib *ast.ImplBlock) {
	d := &implDef{
		decl:      ib,
		synth:     isSynthesized(ib),
		items:     map[string]*implItem{},
		lowerable: true,
	}
	g.implOrder = append(g.implOrder, d)

	if g.registerOwnerImpl(d, ib) {
		// An inherent block on a generic type or an interface: d holds its
		// owner functions. See ownerimpl.go.
		return
	}
	if tpl, ok := g.siblingImplTemplate(ib); ok {
		// An impl over another file's generic type, registered per instance.
		// See siblingtemplateimpl.go.
		g.registerSiblingTemplateImpl(d, ib, tpl)
		return
	}
	if g.registerStdRecvTemplate(d, ib) {
		// An impl over a std generic container, registered per container
		// kind when a call needs it. See stdrecvimpl.go.
		return
	}
	if len(ib.Generics) > 0 || len(ib.WhereClauses) > 0 {
		d.lowerable, d.why, d.whyDetail = false, "generic impl block", typeText(ib.Receiver)
		return
	}
	g.resolveImplDef(d, ib, kindInvalid)
}

func (g *gen) registerImplAt(ib *ast.ImplBlock, recv kind) *implDef {
	d := &implDef{
		decl:      ib,
		synth:     isSynthesized(ib),
		items:     map[string]*implItem{},
		lowerable: true,
	}
	g.resolveImplDef(d, ib, recv)
	return d
}

// resolveImplDef resolves d's interface, receiver, items and defaults, and
// registers it in g.implsByIface (or g.operImpls for an operator impl).
//
// `recv == kindInvalid` means "resolve the receiver from the declaration"; any
// other value is used verbatim. One body for both entry points, because a second
// copy is where the duplicate-impl rule, the default inheritance and the
// item-signature refusals would drift apart.
func (g *gen) resolveImplDef(d *implDef, ib *ast.ImplBlock, recv kind) {
	if ib.Interface != nil {
		name, named := ifaceHeaderName(ib.Interface)
		if !named {
			d.lowerable, d.why, d.whyDetail = false, "generic interface impl", typeText(ib.Interface)
			return
		}
		d.ifaceName = name
		// The interface is the one the checker recorded for the block
		// (implBlockRecord), however the header spells it: a program
		// module's, a sibling's through its mirror, a std module's lowered
		// with the program (`impl sql.ToSql for Status`), whose def a value
		// boxed as it in any file dispatches through, or std's.
		rec := g.implBlockRecord(ib)
		if rec != nil && rec.Interface == "" {
			rec = nil
		}
		var def *ifaceDef
		if rec != nil {
			def, _ = g.solvedIface(rec.Origin, rec.Interface)
		}
		// A GENERIC interface, monomorphized at this block's type arguments.
		// Asked FIRST, and the order is the whole safety argument: the template
		// def is deliberately unlowerable, so falling through would refuse the
		// very block this instantiation serves. The instance is the template
		// at the block's type arguments, solved under the frame a generic
		// receiver's instance was registered at. See ifaceinst.go.
		if tpl := g.genericTemplateDecl(def); tpl != nil {
			inst, why, detail := g.ifaceInstanceFor(tpl, ib)
			if why != "" {
				d.lowerable, d.why, d.whyDetail = false, why, detail
				return
			}
			d.iface, d.ifaceSubst, d.ifaceSubstTypes = inst.def, inst.subst, inst.types
			d.instArgs = ifaceTemplateArgs(tpl, inst.subst)
			// Live for every item signature AND for inheritDefaults below: an
			// item's annotation may name `T` and an inherited default's
			// parameter kinds are the INSTANCE's, already substituted.
			g.pushIfaceSubst(substIfaceImpl, d, inst.subst, inst.types)
			defer g.popIfaceSubst()
		} else if def != nil {
			d.iface = def
		} else if rec != nil {
			// A stdlib interface with an rt table (stdIfaceSpecs), or the
			// universal structural marker.
			d.iface = g.stdSharedIface(rec.Std())
		}
		// TYPE ARGUMENTS WRITTEN AGAINST AN INTERFACE WITH NO TEMPLATE HERE —
		// a STDLIB generic interface (`impl Add<Score, Score> for Score`).
		// Nothing to instantiate, so the key stays.
		//
		// AN EXPLICIT ARM RATHER THAN A FALL-THROUGH: a fall-through and a
		// deliberate decline print the same refusal. `Add`/`Subtract`/
		// `Multiply`/`Divide` are excluded from `stdIfaceSpecs` because
		// `stdIfaceSpec.matches` returns false on `len(decl.TypeParams) > 0`,
		// so std's shared defs hold none of them and there is no declaration
		// for this builder to instantiate.
		if d.iface == nil && rec != nil {
			// The four operator interfaces, which need no `*ifaceDef`: the
			// callee is chosen from a statically known left operand at all
			// three of their call shapes, each interface declares one method
			// with no default, and `bindImpl` returns immediately for a nil
			// iface. Claimed ahead of the decline below.
			//
			// It only decides ROUTE, and then falls through to the SHARED tail
			// below: the receiver resolution, the item signatures and the
			// default inheritance are the same questions for this family, and a
			// second copy of them is exactly the drift this function's own
			// comment exists to prevent. The divert is at the LAST step, the
			// registration — see the `d.oper != nil` arm there.
			g.admitOperImpl(d, ib, name, rec.Std())
			if gt, explicit := ifaceHeaderArgs(ib.Interface); explicit && d.oper == nil {
				// `impl Contains<Regex> for Text`: a std or sibling generic
				// interface at written arguments. The items carry their own
				// signatures; the arguments are the block's identity among
				// the receiver's impls of the interface (instimpl.go).
				args := make([]kind, len(gt))
				for i, te := range gt {
					// kindInvalid: lookup — an argument this builder cannot name declines the block.
					if args[i] = g.typeOf(te); args[i] == kindInvalid {
						d.lowerable, d.why, d.whyDetail = false, "generic interface impl", typeText(ib.Interface)
						return
					}
				}
				d.instArgs = args
			}
		}
		if d.iface == nil && rec == nil && (d.typeScope != nil || (name == "Debug" && d.synth && g.programStd)) {
			// The universal Debug the front end writes for an opaque type
			// declared in a body (FileBodies.BlockDebug, registered with its
			// block's scope), or synthesizes for every opaque and host type a
			// std module lowered with the program declares
			// (programStdModules, analysis.SynthesizeUniversalDebug). Neither has a record of
			// its interface: the first is std's Debug by construction, and
			// a std module has no prelude, so one that never names Debug
			// (std/url, std/money) resolves the second's header to nothing.
			// Without this its impls are filed under no interface, and a
			// program cannot hold a `Url` as a `Debug`.
			d.iface = g.stdDebugDef()
		}
		if d.iface == nil && d.oper == nil && rec != nil {
			// Any other std interface: the def a value of the receiver
			// held as it dispatches through. See stdprogiface.go.
			d.stdIface = g.stdProgIfaceForImpl(ib, rec)
		}
		d.ifaceKey = g.implIfaceKey(d, rec, name)
		if d.iface != nil && !d.iface.lowerable {
			d.lowerable, d.why, d.whyDetail = false, d.iface.why, name
			return
		}
	}
	// The caller's "resolve the receiver from the declaration" signal, not a
	// refused kind: registerImplAt always passes a real instance kind and
	// registerImpl always passes this.
	// kindInvalid: sentinel — an in-band request, never an operand.
	if recv == kindInvalid {
		var ok bool
		recv, ok = g.receiverKind(ib)
		if !ok {
			// The RECEIVER's own reason, exactly as the interface arm above
			// reports the INTERFACE's. An impl block adds no reason of its own
			// here: it is unlowerable because its receiver is, and that
			// declaration has already said why at its own position. Reporting
			// `impl for an unlowered type` instead would report one refusal
			// twice under two names. See cascadereason_test.go.
			if construct, detail, named := g.typeRefusal(ib.Receiver); named {
				d.lowerable, d.why, d.whyDetail = false, construct, detail
				return
			}
			// receiverKind also declines an INTERFACE receiver, which typeRefusal
			// answers "representable" for because an existential is. That case has
			// no type-level reason, so it takes this key.
			d.lowerable, d.why, d.whyDetail = false, "impl for an unlowered type", typeText(ib.Receiver)
			return
		}
	}
	d.recv = recv
	if t, ok := g.recordedTypeExpr(ib.Receiver); ok {
		// The checker's type of the receiver, at the instance whose frame
		// is live: what a default lowered for this block binds `self` to.
		d.recvType = g.checkerTypeArg(t)
	}

	for _, item := range ib.Items {
		if _, isOnce := item.(*ast.OnceBinding); isOnce {
			// An owner-level `once` is no function of the block: declareOnces
			// files it and the module loop lowers it. See once.go.
			continue
		}
		fd, ok := item.(*ast.FuncDef)
		if !ok {
			d.lowerable, d.why, d.whyDetail = false, "host-backed impl function", constructName(item)
			return
		}
		if src, written := item.(*ast.FuncDef); written && g.ownerFunc(ib, src) {
			// An owner function, built once by the block's owner def rather
			// than once per instance. See ownerimpl.go.
			continue
		}
		if g.expansiveImplItem(d, fd) && d.mayWithhold(fd.Name) {
			d.noteGap(fd.Name, implGap{why: "expansive impl function", detail: implMemberLabel(d, fd.Name)})
			continue
		}
		it, gap := g.implItemSig(d, fd)
		switch {
		case it != nil:
		case gap.whole || !d.mayWithhold(fd.Name):
			// A refusal about the BLOCK, or a per-function one the block may
			// NOT withhold because something can still dispatch to the name.
			d.lowerable, d.why, d.whyDetail = false, gap.why, gap.detail
			return
		default:
			d.noteGap(fd.Name, gap)
			continue
		}
		g.addImplItem(d, it)
	}
	g.inheritDefaults(d)

	// AN OPERATOR IMPL NEVER ENTERS `implsByIface`, and this return is the whole
	// of that claim: one line to check, rather than a property of eight
	// consultation sites. No lookup in that map ever sees an operator impl,
	// so no lookup of one receiver's impl there can lose an impl to the rhs
	// axis. See operimpl.go.
	//
	// The shape agreement is asked HERE and not earlier because it compares the
	// item's resolved parameter kinds against the header's type arguments, and
	// the items are resolved immediately above. It is `implItemSig`'s "is this
	// method declared on the interface" check for a family whose interface has
	// no `*ifaceDef` to ask.
	if d.oper != nil {
		if construct, detail := g.operShapeRefusal(d); construct != "" {
			d.lowerable, d.why, d.whyDetail = false, construct, detail
			return
		}
		g.registerOperImpl(d)
		return
	}
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
		// Two impls of one interface declaration for one type is a
		// coherence error the analyzer rejects. Both are refused and
		// neither is picked, so ordering never decides. Impls of two
		// interfaces of one name are two keys and never meet here.
		prior.lowerable, prior.why, prior.whyDetail = false, "duplicate impl block", d.label()
		d.lowerable, d.why, d.whyDetail = false, "duplicate impl block", d.label()
		return
	}
	byRecv[d.recv] = d
}

// mergeInherent adds the functions of d, an inherent block, to prior, an
// inherent block for the same type, when no name is in both, and reports
// whether it did. A name in both is a redeclaration the analyzer reports.
func mergeInherent(prior, d *implDef) bool {
	if !prior.lowerable || !d.lowerable {
		return false
	}
	for name := range d.items {
		if _, clash := prior.items[name]; clash {
			return false
		}
	}
	for name, it := range d.items {
		prior.items[name] = it
	}
	return true
}

// receiverKind resolves the type an impl block is for.
//
// A builtin scalar is admitted: `impl Numberish for Int` is legal under the
// orphan rule as long as the interface is local.
func (g *gen) receiverKind(ib *ast.ImplBlock) (kind, bool) {
	te := ib.Receiver
	switch q := te.(type) {
	case *ast.SimpleType:
	case *ast.GenericType:
		// `impl Show for List<Int>`: a std container at concrete arguments,
		// for an interface of the program's own (stdrecvimpl.go). A user
		// generic type's receiver is its template's (genericimpl.go).
		if !g.programIface(ib) {
			return kindInvalid, false
		}
		k := g.typeOf(te)
		if _, _, std := stdRecvParts(k); !std {
			return kindInvalid, false
		}
		return k, true
	case *ast.QualifiedType:
		// `impl Display for lib.Dog`: another file's type, which this file
		// may give an impl of an interface it declares or std declares.
		if _, simple := q.Member.(*ast.SimpleType); !simple {
			return kindInvalid, false
		}
	default:
		return kindInvalid, false
	}
	k := g.typeOf(te)
	// kindInvalid: reports — returns false; registerImpl rejects `impl for an unlowered type`.
	if k == kindInvalid || k.tag == tagIface {
		return kindInvalid, false
	}
	return k, true
}

// implItemSig resolves one impl function's signature, returning nil plus the
// refusal when it is outside the subset.
//
// The refusal carries the UNIT it is about. Exactly ONE row is a property of
// the function alone: a binder the FUNCTION introduces (`fn choose<K>`, or a
// function-level `where`), which only a CALL SITE can bind and which therefore
// says nothing about the block, the receiver or the interface. Every other row
// here is left whole: `decorator`, `impl function without a body`, a
// destructuring or unannotated parameter and an unrepresentable parameter or
// result type have no program that needs them narrowed, and `impl function not
// declared on the interface` is a statement about the BLOCK's conformance. See
// implDef.mayWithhold for the safety condition that applies to whichever rows
// are narrowed.
func (g *gen) implItemSig(d *implDef, fd *ast.FuncDef) (*implItem, implGap) {
	switch {
	case g.unboundTypeParams(fd.TypeParams) || g.unboundWhereSubjects(fd.WhereClauses):
		// QUALIFIED when there is an interface, matching the `impl function not
		// declared on the interface` row below. Withholding makes this operand
		// reachable at a CALL rather than only at the impl block, and a bare
		// `choose` at a call site names nothing a reader can find.
		return nil, implGap{why: "generic impl function", detail: implMemberLabel(d, fd.Name)}
	case len(fd.Decorators) > 0:
		return nil, implGap{why: "decorator", detail: fd.Name, whole: true}
	case fd.Body == nil:
		return nil, implGap{why: "impl function without a body", detail: fd.Name, whole: true}
	}
	it := &implItem{
		impl:      d,
		instKey:   g.substLinkKey(),
		decl:      fd,
		name:      fd.Name,
		params0:   fd.Params,
		body:      fd.Body,
		line:      fd.Line,
		result:    kindUnit,
		public:    fd.Public,
		lowerable: true,
	}
	for i := range fd.Params {
		p := &fd.Params[i]
		// A destructuring parameter is one value whose names the body's
		// prologue reads off it (irImplLower), as a file-level function's
		// are. Its kind is the annotation's, or the type its pattern head
		// names. An annotated one takes the annotation path below.
		if p.Destructure != nil && p.TypeAnnotation == nil {
			k := g.paramKind(*p)
			// kindInvalid: reports — returns the pattern's refusal for the caller to reject.
			if k == kindInvalid {
				return nil, implGap{why: "destructuring parameter", detail: patternText(p.Destructure), whole: true}
			}
			it.params = append(it.params, k)
			continue
		}
		switch {
		case p.TypeAnnotation == nil && p.Default == nil:
			return nil, implGap{why: "parameter without a declared type", detail: p.Name, whole: true}
		}
		// paramKind: a parameter written `limit = 10` has the type the
		// checker gave it from its default.
		k := g.paramKind(*p)
		// The TYPE's own reason, for the caller to reject — the same repoint funcDecl makes. See sigreason.go.
		// kindInvalid: reports — typeRefusal names the gap the annotation carries.
		if k == kindInvalid {
			construct, detail, _ := g.typeRefusal(p.TypeAnnotation)
			name := p.Name
			if p.Destructure != nil {
				name = patternText(p.Destructure)
			}
			return nil, implGap{why: construct, detail: name + ": " + detail, whole: true}
		}
		it.params = append(it.params, k)
	}
	if fd.ReturnTypeExpr != nil {
		it.result = g.typeOf(fd.ReturnTypeExpr)
		// kindInvalid: reports — returns the TYPE's own reason for the caller to reject.
		if it.result == kindInvalid {
			construct, detail, _ := g.typeRefusal(fd.ReturnTypeExpr)
			return nil, implGap{why: construct, detail: detail, whole: true}
		}
	}
	if d.iface != nil {
		if m := d.iface.methods[fd.Name]; m == nil && !g.stdIfaceOverride(d.iface, fd.Name) {
			return nil, implGap{why: "impl function not declared on the interface",
				detail: d.ifaceName + "." + fd.Name, whole: true}
		}
	}
	return it, implGap{}
}

// inheritDefaults gives the receiver one function for every interface
// default the block did not override: the default's body, lowered for this
// receiver in this file when the interface is declared here, and otherwise a
// call of the declaring file's instance at the receiver (irImplLower,
// ifacedefault.go), whose body that file lowers in its own scope.
//
// A default it CANNOT monomorphize is withheld from the block rather than
// refusing it. Every refusal below is keyed to one `m`, so the block's other
// functions — and, through `registerInstanceImpls`, the receiver TYPE — have no
// stake in it. `mayWithhold` is the safety condition and it is asked once for
// all three rows; see its comment for why the question is "can anything
// dispatch to this" and not "is this gap small".
func (g *gen) inheritDefaults(d *implDef) {
	if d.iface == nil || !d.lowerable {
		return
	}
	for _, m := range d.iface.order {
		if m.decl.Body == nil || d.items[m.name] != nil {
			continue
		}
		body, ok := m.decl.Body.(*ast.Block)
		if !ok {
			continue
		}
		gap := implGap{detail: d.ifaceName + "." + m.name}
		switch {
		case m.why != "" && !monomorphizedDefaultAnswers[m.why]:
			// The erased-receiver rows and the rows about `self` in the
			// signature do not apply to a monomorphized default: the body is
			// lowered once per implementing type with every self position,
			// bare or inside a composite, bound to that concrete type
			// (inheritedDefaultSig). Every other reason still refuses the
			// method, and not the block.
			gap.why = m.why
		}
		params, result, sigOK := g.inheritedDefaultSig(d, m)
		if !gap.named() && !sigOK {
			gap.why = m.why
			if gap.why == "" {
				gap.why = "non-scalar interface parameter type"
			}
		}
		if gap.named() {
			if !d.mayWithhold(m.name) {
				d.lowerable, d.why, d.whyDetail = false, gap.why, gap.detail
				return
			}
			d.noteGap(m.name, gap)
			continue
		}
		it := &implItem{
			impl:      d,
			instKey:   g.substLinkKey(),
			name:      m.name,
			params0:   m.decl.Params,
			body:      body,
			line:      m.decl.Line,
			result:    result,
			params:    params,
			inherited: true,
			lowerable: true,
			// params0 carries the interface's PARAMETER DEFAULTS as well as its
			// types, so a call site that omits one fills it from here, and
			// one an interface of another file declares is filled under
			// that file's names (ifaceslotdefault.go).
		}
		g.addImplItem(d, it)
	}
}

// monomorphizedDefaultAnswers are the ifaceMethod.why rows a default
// monomorphized per implementor answers by construction: no receiver to
// dispatch on, several self positions that may disagree, and `self` in the
// result or inside a parameter's type, which name the implementor here.
var monomorphizedDefaultAnswers = map[string]bool{
	"interface function without a self-typed parameter": true,
	gapMultiSelf:                          true,
	"interface function returning self":   true,
	gapNestedSelfResult:                   true,
	"non-scalar interface parameter type": true,
	"non-scalar interface return type":    true,
}

// inheritedDefaultSig is default m's parameter and result kinds for
// implementor d, with every `self` in its signature, bare or inside a
// composite (`(self, self)`, `List<self>`), read as d's receiver. ok is false
// when a kind is still outside the subset.
func (g *gen) inheritedDefaultSig(d *implDef, m *ifaceMethod) ([]kind, kind, bool) {
	prev := g.defaultSelf
	g.defaultSelf = &irDefaultSelf{iface: d.iface, recv: d.recv, typ: d.recvType}
	defer func() { g.defaultSelf = prev }()
	if d.iface.foreign != "" && d.iface.unit >= 0 {
		// The signature is written in the declaring file, so the checker's
		// answers about its type expressions (`Maybe<self>`) are that file's.
		defer g.lowerNamesFrom(d.iface.unit)()
	}
	ok := true
	params := make([]kind, len(m.params))
	for i, k := range m.params {
		switch {
		case m.shape.selfTyped(i):
			k = d.recv
		case typeExprHasSelf(m.decl.Params[i].TypeAnnotation):
			k = g.paramKind(m.decl.Params[i])
		}
		// kindInvalid: reports — the caller refuses the method under m.why.
		ok = ok && k != kindInvalid
		params[i] = k
	}
	result := m.result
	if ret := m.decl.ReturnTypeExpr; ret != nil && typeExprHasSelf(ret) {
		result = g.typeOf(ret)
		// kindInvalid: reports — as a parameter's does.
		ok = ok && result != kindInvalid
	}
	return params, result, ok
}

// --- runtime type identity ---------------------------------------------------

// hasTID reports whether a type has a runtime identity existential dispatch
// can key on.
//
// The five compiler-represented types always do. A MIRROR has one exactly
// when the file that declared it minted one: one Nomi type, one identity,
// whichever file reaches it. See existential.go for why the owner mints
// eagerly rather than on demand.
func (g *gen) hasTID(k kind) bool {
	if k.tag == tagAnonStruct {
		// A structural type has no declaring file, so every gen answers for
		// it.
		return k.comp != nil
	}
	switch k {
	case kindInt, kindFloat, kindString, kindBool, kindUnit:
		return true
	}
	if k.tag != tagNamed || k.def == nil {
		return false
	}
	if k.def.foreign != "" {
		if g.reg == nil {
			return false
		}
		o := g.reg.byDecl[k.def.decl]
		if o == nil || o.unit == g.fileUnit || o.unit >= len(g.reg.gens) {
			return false
		}
		src := g.reg.defOf(o)
		return src != nil && g.reg.gens[o.unit].tids[named(src)]
	}
	g.tids[k] = true
	return true
}

// mintTypeIDs gives every type this file DECLARES its runtime identity, in
// declaration order, before anything is lowered.
//
// Eager rather than on first use, and the reason is lowering ORDER rather than
// tidiness. A sibling file discovers it needs this file's identity while its
// own body is being lowered; modules are lowered in index order, so the demand
// may arrive after this module has already been walked, and demand-driven
// minting could not serve it.
//
// A mirror is skipped: its identity belongs to the file that declared it.
// A prelude instance is skipped too: no file declares it, and hasTID records
// it on demand.
func (g *gen) mintTypeIDs() {
	for _, d := range g.typeOrder {
		if d == nil || d.foreign != "" || d.preludeOf != nil || !d.lowerable {
			continue
		}
		k := named(d)
		g.tids[k] = true
	}
}

// qualifiedNomiName is a declared type's name qualified by this file's
// module, which irTypeSymName uses for a type this gen declares.
//
// Qualified and not short: two distinct types whose short names match,
// `shapes.Point` and `geometry.Point`, would otherwise have one name.
func (g *gen) qualifiedNomiName(nomi string) string {
	mod := strings.TrimSuffix(filepath.Base(g.nomiPath), ".nomi")
	if mod == "" {
		return nomi
	}
	return mod + "." + nomi
}

// --- lowering declarations ----------------------------------------------------

// ifaceDecl reports an interface declaration the builder cannot represent. It
// lowers nothing itself.
//
// A non-dispatchable function contributes no refusal here: an interface whose
// functions cannot be dispatched on an erased value is still perfectly usable
// through concrete qualifiers, and refusing the DECLARATION would make a file
// unlowerable for a gap it may never exercise. The refusal is recorded at a
// dispatch site instead, which is a position somebody wrote.
//
// A `useSiteOnly` interface is that rule applied to the WHOLE declaration
// rather than to one of its functions — see the field. It is not refused
// here, and `d.order` is empty for one: `resolveIfaces` skips a non-lowerable
// def, so there are no resolved methods.
//
// That emptiness is also the limit: the template's requirements are never
// resolved, so nothing may dispatch through the template itself, and
// `lowerable` staying false holds every erased route shut. A call on a
// concrete receiver resolves against the impl block's own instance instead
// (ifaceinst.go).
func (g *gen) ifaceDecl(id *ast.InterfaceDef) {
	g.at(id.Line)
	d := g.ifacesByDecl[id]
	if d == nil {
		// A second declaration of the name, which declareIfaces files
		// no def for.
		g.rejectWhole("interface declaration", id.Name, id)
		return
	}
	if !d.lowerable {
		if !d.useSiteOnly {
			g.rejectWhole(d.why, id.Name, id)
		}
		return
	}
}

// implDecl lowers one impl block's functions.
func (g *gen) implDecl(ib *ast.ImplBlock) {
	d := g.implFor(ib)
	if d == nil {
		g.rejectWhole("impl block", typeText(ib.Receiver), ib)
		return
	}
	if d.synth {
		// Already lowered (or already discarded) in settleImpls. Never refused
		// by name: nobody wrote it, and its position is fabricated.
		return
	}
	if d.owner != "" {
		// The block's owner functions, built once. A generic type's
		// instances build the rest (genericimpl.go). See ownerimpl.go.
		g.at(ib.Line)
		g.emitImpl(d)
		g.rejectWithheldItems(d, ib)
		g.queueWithheldAbstract(d, ib)
		return
	}
	if g.isGenericImplTemplate(ib) {
		// What its functions do for every type is the abstract body of the
		// template's instance at its own type parameters (abstract.go).
		if tpl, _, ok := g.receiverTemplate(ib); ok {
			g.queueAbstractBody(abstractBody{tpl: tpl})
		}
	}
	if g.isGenericImplTemplate(ib) || d.siblingTemplate != nil || d.stdRecvTemplate != nil {
		// A TEMPLATE, not an impl. Lowers nothing and REFUSES nothing, which is
		// funcDecl's rule for a monomorphized generic `fn` verbatim: a template
		// is not a function, it is a rule for making them. Every instance is
		// registered and lowered by genericimpl.go, and an instantiation this
		// builder cannot build refuses at the INSTANTIATION — the position that
		// has the type arguments. Refusing here as well would refuse a
		// declaration that lowers.
		return
	}
	g.at(ib.Line)
	if !d.lowerable {
		if g.typeOf(ib.Receiver).tag == tagIface {
			// `impl Release for Conn`: an impl for an interface type, whose
			// functions no program's dispatch reaches. What they do is
			// their abstract bodies' (abstract.go).
			g.queueAbstractBody(abstractBody{impl: d})
		}
		g.rejectWhole(d.why, d.whyDetail, ib)
		return
	}
	g.emitImpl(d)
	g.rejectWithheldItems(d, ib)
	g.queueWithheldAbstract(d, ib)
}

// queueWithheldAbstract queues d, the block ib, for its abstract bodies when
// it withholds a generic function of its own, which each call instantiates
// (withheldMember): what the function does for every type is its abstract
// body's (abstract.go). A block of owner functions (`impl Session<T>`'s
// `Session.new<T>`) withholds them as any block does.
func (g *gen) queueWithheldAbstract(d *implDef, ib *ast.ImplBlock) {
	for _, fd := range implFuncDecls(ib) {
		if _, withheld := d.withheldMember(fd.Name); withheld {
			g.queueAbstractBody(abstractBody{impl: d})
			return
		}
	}
}

// rejectWithheldItems names each function this block WROTE and could not
// supply, at that function's own position, and walks its body.
//
// # WHY THE DECLARATION AND NOT ONLY THE CALL
//
// A written function is a declaration somebody made, so it is reported the way
// `funcDecl` reports an unlowerable free `fn`: at the declaration, whether or
// not anything calls it. Dropping a written function silently because nothing
// happens to call it would be the builder deciding a program is smaller than
// the programmer wrote it.
//
// # And why the body walk matters
//
// `rejectWhole` probes the children so the report is not truncated by the
// outermost refusal. With the block lowerable and the item absent, `emitImpl`
// walks nothing, so without this walk a blocker inside the withheld method's
// BODY would vanish from the set.
//
// An INHERITED default is deliberately not here: it has no declaration in this
// block to report at, its body belongs to the interface, and its call sites are
// the only positions somebody wrote. A generic impl TEMPLATE is not here
// either — `implDecl` returns above for one, and every instantiation refuses at
// the instantiation, which is the position that has the type arguments.
func (g *gen) rejectWithheldItems(d *implDef, ib *ast.ImplBlock) {
	if len(d.gaps) == 0 {
		return
	}
	for _, item := range ib.Items {
		fd, isFn := item.(*ast.FuncDef)
		if !isFn {
			continue
		}
		gap, withheld := d.gaps[fd.Name]
		if !withheld {
			continue
		}
		if _, byCall := d.withheldMember(fd.Name); byCall && d.owner != "" {
			// An owner function with its own type parameters is a template
			// each call instantiates, as a generic `fn` is, so its
			// declaration refuses nothing. See ownerimpl.go.
			continue
		}
		g.at(fd.Line)
		g.rejectWhole(gap.why, gap.detail, fd)
	}
}

func (g *gen) implFor(ib *ast.ImplBlock) *implDef {
	for _, d := range g.implOrder {
		if d.decl == ib {
			return d
		}
	}
	return nil
}

// emitImpl lowers every item in the block, then checks that the receiver has
// a runtime identity (bindImpl).
//
// A generic interface instantiation pushes its frame here, because lowering is
// where a monomorphized default body resolves its type parameters and this runs
// long after resolveImplDef's frame was popped. `flushGenericImpls` does the
// same for a generic receiver's frame, and the two nest: they bind different
// names and genericSubstKind scans innermost-first.
func (g *gen) emitImpl(d *implDef) {
	defer g.enterTypeScope(d.typeScope)()
	if d.ifaceSubst != nil {
		g.pushIfaceSubst(substIfaceImpl, d, d.ifaceSubst, d.ifaceSubstTypes)
		defer g.popIfaceSubst()
	}
	for _, it := range d.order {
		g.implFunc(d, it)
	}
	g.bindImpl(d)
}

// implFunc lowers one impl function, or one interface default monomorphized for
// this receiver.
//
// The receiver is an ordinary parameter in whatever position the signature put
// it, so nothing here needs to know which one it is. Only dispatch does.
func (g *gen) implFunc(d *implDef, it *implItem) {
	// A synthesized body has no position a programmer wrote: derive synthesis
	// allocates from a band around 2^30, which `at` ignores. Attribute the
	// whole function to the receiver's declaration, which is where the
	// `derive` that asked for it sits.
	if d.synth && d.recv.def != nil {
		g.at(d.recv.def.line)
	}
	g.at(it.line)
	prevImpl := g.implEmitting
	g.implEmitting = d
	defer func() { g.implEmitting = prevImpl }()
	// A bare call in this body may name one of this block's own members:
	// `each_while(left, yield)` inside `impl Iter for Tree`.
	prevSelf := g.implBlock
	g.implBlock = d
	defer func() { g.implBlock = prevSelf }()
	// Another file's call that omits a defaulted parameter this file's
	// defaults alone can name calls an accessor for it (siblingdefault.go),
	// built outside the body's scope.
	defer g.irImplSlotDefaults(d, it)
	// A call on an interface value that leaves a defaulted parameter out
	// dispatches to this function's accessor for it (ifaceslotdispatch.go).
	defer g.irImplSlotDispatch(d, it)

	prev := g.result
	g.result = it.result
	g.irBodyObserve(irBodyImplFn, irImplSource(it) != nil)
	g.dc.take()
	produced, retained := g.irImplLower(d, it, it.params)
	if !retained {
		declined := g.dc.take()
		if irImplSource(it) == nil {
			declined = irBecause("an impl function with no source body")
		}
		g.unloweredBody(declined)
		produced = it.result
	}
	switch {
	case produced == kindInvalid:
		// Declines `impl return type mismatch`.
		g.suppress(it.body)
	case produced != it.result:
		g.reject("impl return type mismatch",
			fmt.Sprintf("%s.%s declares %s and returns %s",
				d.label(), it.name, it.result.nomi(), produced.nomi()), it.body)
	}
	g.result = prev
}

// bindImpl refuses an impl whose receiver has no runtime identity when the
// block supplies a function an erased receiver could reach. The dispatch
// entries themselves are recorded by irRecordImpls (irappboot.go).
// Asking hasTID also records a local named receiver's identity in g.tids,
// which another file's hasTID reads for its mirror of the type.
//
// Nothing is checked when the interface is not resolved: the impl's functions
// are still lowered and still reachable through a concrete qualifier.
func (g *gen) bindImpl(d *implDef) {
	if d.iface == nil {
		return
	}
	binds := make([]*ifaceMethod, 0, len(d.iface.order))
	for _, m := range d.iface.order {
		// hasTable, not dispatchable: a method no erased receiver can
		// select still counts here.
		if m.hasTable() && d.items[m.name] != nil && !g.debugDeferred(d.iface, m) {
			binds = append(binds, m)
		}
	}
	if len(binds) == 0 {
		return
	}
	if !g.hasTID(d.recv) {
		g.reject("existential identity for a type", d.recv.nomi(), d.decl)
		return
	}
}

// speculate runs f, and when f refused anything it restores what snapshot
// captured, the refusals among them. It reports whether f lowered cleanly.
//
// Used for exactly one thing: a front-end-synthesized impl block, whose
// position is fabricated and whose text nobody wrote. Everywhere else a refusal
// is the product, not a failure. See unsupported.go.
func (g *gen) speculate(f func()) bool {
	errs := len(g.errs)
	restore := g.snapshot()
	f()
	if len(g.errs) == errs {
		return true
	}
	restore()
	return false
}

// snapshot captures everything a lowering attempt writes, returning the undo.
//
// Extracted from speculate because other callers need the rollback under
// their own rule for when to take it: stdProgIsolated (stdprogiface.go)
// discards what a failed build left. Two hand-written rollbacks would be two
// places for a later field to be forgotten in one of them.
//
// Each line below is a thing a discarded run must not leave behind, and the
// reason it must not is on the line.
func (g *gen) snapshot() func() {
	errs := len(g.errs)
	nomiLine := g.nomiLine
	return func() {
		g.errs = g.errs[:errs]
		g.nomiLine = nomiLine
	}
}

// `embedsVariant` (types.go) answers WHICH VARIANT of an enum embeds a
// value, which is what `coerce` needs to construct one; `ir.Table.Embeds`
// records the edge and `ir.Type` carries no variant structure to name the
// arm with.

// --- erasure ------------------------------------------------------------------

// implements reports whether the program lowers an impl of d for k.
//
// The program and not this file, which is what makes a cross-file
// existential possible: `MyApp{logger: ProdLogger}` written in one file boxes
// a type declared in a second whose `impl Logger` is written in a third, and
// the dispatch finds that impl whichever file wrote it. See existential.go's
// implIndex: the key is a pair of declaration nodes, never a pair of names.
func (g *gen) implements(d *ifaceDef, k kind, kt analysis.Type) bool {
	return g.bindsImpl(d, k, kt)
}

// existential is the kind of values whose static type is the interface d.
func existential(d *ifaceDef) kind { return kind{tag: tagIface, iface: d} }

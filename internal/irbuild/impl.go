package irbuild

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Interfaces, impl blocks, and the three ways a Nomi call finds its callee.
//
// # Three call shapes, and only one of them needs a table
//
// The premise most easily got wrong is that interface dispatch means dynamic
// dispatch. It mostly does not.
//
//   - **Concrete qualifier.** `Dog.speak(rex)`, and also `Speech.speak(rex)`
//     when `rex` is statically a `Dog`: the qualifier plus the receiver's
//     static type name exactly one function. Lowered to a DIRECT call. This is
//     the common case in real Nomi and it is what makes existentials the
//     exception rather than the rule.
//   - **Existential.** An interface-typed parameter, field or element has lost
//     its concrete type, so the callee is a run-time fact. That is the only
//     shape that reaches rt.Method — see rt/dispatch.go for why identity is a
//     variable's ADDRESS and not a name.
//   - **Type-parameter qualifier.** `T.from_json(x)` needs a dictionary
//     carrying T's concrete type argument, and it has one: the bound picks the
//     table at compile time and an emitted `*rt.TypeID` picks the
//     implementation at run time. That pair is the only route that
//     serves a method with NO self-position parameter — which is why
//     `methodDispatchGap`'s two receiver rows do not stop a table from
//     existing. Scoped to a single bounded type parameter on a free function;
//     see dict.go for what that leaves refused, and by which name.
//
// # Go interfaces with generated methods are not an option
//
// Not awkward, impossible, three independent ways: an interface function may
// have no self-typed parameter at all (`Literal`, `FromJson`, `App`), Go cannot
// dispatch on a return type, and Nomi's orphan rule admits an impl for a
// foreign type where Go admits no method. So the encoding is a table plus
// ordinary functions, and `self` is an ordinary parameter in whatever position
// the interface declared it — `isSelfPositionParam`'s rule, not "argument
// zero".
//
// # A default is monomorphized, not shared
//
// `interface Identity { fn name(v: self): String; fn describe(v: self): String
// { "I am " + Identity.name(v) } }` gives every implementor a `describe`. The
// body
// is lowered ONCE PER IMPLEMENTING TYPE, with `self` bound to that concrete
// type — so `Identity.name(v)` inside it is itself a direct call, and the
// inherited default costs exactly what a hand-written one would. An impl block
// that supplies the function overrides it by simply already occupying the name.
//
// # Derives are not special-cased, deliberately
//
// `analysis.SynthesizeDerives` and `SynthesizeUniversalDebug` have already run
// by the time this package sees the AST, so `derive Equatable for Dog` arrives
// as an ordinary `impl Equatable for Dog { ... }` whose body is ordinary Nomi.
// It lowers through exactly the path a hand-written impl takes, so a derive
// has no separate implementation that could disagree with its Nomi body.
//
// The one accommodation they need is not semantic: SynthesizeUniversalDebug
// appends an `impl Debug for T` for EVERY declared type, at a fabricated
// position in the synth line band. Refusing one by name would blame a
// programmer for a construct nobody wrote and would make every struct
// declaration in the corpus unlowerable. So a SYNTHESIZED block is lowered
// speculatively: if its body lowers cleanly the functions are emitted, and if
// it does not, both the output and the refusals are discarded and the impl is
// marked unlowerable — which makes every site that would dispatch to it refuse
// at ITS OWN position, which is a position somebody wrote.

// ifaceDef is one `interface` declaration.
//
// A pointer to one of these IS the interface's identity, exactly as *typeDef is
// a struct's. It is what distinguishes `Greeter.greet` from `Farewell.greet` on
// a type implementing both — the two are different tables, and nothing in the
// path compares the method name alone.
type ifaceDef struct {
	nomi    string
	decl    *ast.InterfaceDef
	methods map[string]*ifaceMethod
	order   []*ifaceMethod
	// fields are the `field name: Type` requirements, each with its own getter
	// table. They are kept beside the methods rather than folded into them
	// because they are not functions: a requirement obliges every implementor
	// to DECLARE a field, so the table's entries are getters this builder
	// writes rather than bodies a programmer wrote. See fieldaccess.go.
	fields     map[string]*ifaceField
	fieldOrder []*ifaceField
	// lowerable is false for an interface this builder cannot represent at
	// all; why names the construct for the refusal.
	lowerable bool
	why       string
	whyDetail string
	// useSiteOnly says the reason in `why` is reported at a USE and never at
	// the declaration.
	//
	// `ifaceDecl`'s own header already states the rule for a non-dispatchable
	// FUNCTION: it "contributes no table and no refusal here", because "an
	// interface whose functions cannot be dispatched on an erased value is
	// still perfectly usable through concrete qualifiers, and refusing the
	// DECLARATION would make a file unlowerable for a gap it may never
	// exercise". A GENERIC interface is that case one level up — the whole
	// declaration rather than one of its functions — and this field is the rule
	// applied there rather than a new one.
	//
	// It is a SECOND FIELD instead of flipping `lowerable`, and the difference
	// is a wrong-answer boundary rather than bookkeeping. `lowerable` is read
	// by `typeOf`'s interface arms and `inferred.go`'s `solvedIface` arm to
	// decide whether a value may be ERASED to this interface, and a generic one
	// cannot: `existential(d)` takes its identity from the `*ifaceDef` POINTER,
	// so `Chooser<Int>` and `Chooser<String>` would be one existential kind and
	// one dispatch table. `registerImplDef` reads it to refuse an impl block of
	// an interface whose requirements this builder has not checked. Every one of
	// those answers must stay NO. The only thing that changes is whether the
	// DECLARATION spends a refusal on something the file may never reach, as
	// when a file declares one generic interface, implements it nowhere, and
	// calls it never.
	//
	// A call through a generic interface on a CONCRETE receiver
	// (`Chooser.left(Pair{a: 10, b: 20})`) does lower, but not through this
	// def: the impl block binds a per-instantiation def that `resolveIface`
	// resolves under the impl's substitution (ifaceinst.go), and the call
	// reaches that impl's item directly. This def is the TEMPLATE, and keeping
	// its `lowerable` false keeps the erased routes above shut.
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
	// foreign is the analyzer's file key of the DECLARING file when this def
	// is a MIRROR of a sibling file's interface, and pkg that file's generated
	// Go package. Two packages hold two *ifaceDefs for one Nomi interface, on
	// exactly the footing types.go's mirrors stand on: pointer equality is the
	// identity WITHIN a package, which is the only place a kind comparison
	// ever happens. A mirror's dispatch tables are the OWNER's variables,
	// qualified — one interface, one table, however many packages name it.
	// See existential.go.
	foreign string
	pkg     string
	// unit is the declaring module's index in the program, or -1 for a local
	// declaration. Read to reach the DECLARING file's analysis, which is what
	// decides whether one of its defaults means the same thing here.
	unit int
	// instOf is the generic interface DECLARATION this def is an instantiation
	// of, or nil for an ordinary def.
	//
	// Its one reader is resolveIface, which mints no dispatch table for an
	// instance. Two instances of one template would otherwise mint two tables
	// from one declaration and neither would ever be DECLARED — `ifaceDecl`
	// walks `g.ifaceOrder`, which holds the TEMPLATE — so a lowered call would
	// name a table that does not exist. See ifaceinst.go.
	instOf *ast.InterfaceDef
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
	// table is the generated name of this method's rt.Method variable, empty
	// when the method's SIGNATURE cannot be represented in one.
	//
	// Note what this is NOT: a table may exist for a method that cannot be
	// dispatched on an erased receiver. `Zero.zero(): self` has no receiver,
	// so `why` names that gap and every erased-receiver site refuses — but the
	// table is still the right home for `T.zero()`, which is keyed by the
	// concrete type argument instead. See methodSignatureGap.
	table bool
	// why names what stops an ERASED-RECEIVER dispatch, reported at a dispatch
	// site. It may be non-empty while table is non-empty.
	why string
}

// dispatchable reports whether an ERASED RECEIVER can select an implementation
// of this function. A dictionary-driven call asks a different question — see
// hasTable.
func (m *ifaceMethod) dispatchable() bool { return m.table && m.why == "" }

// hasTable reports whether this function has a dispatch table at all, which is
// a property of its SIGNATURE and not of how a call site finds its key.
func (m *ifaceMethod) hasTable() bool { return m.table }

// implDef is one `impl Iface for Type` or inherent `impl Type` block.
type implDef struct {
	// ifaceName is the interface as written, "" for an inherent block. It is
	// recorded even when the interface is NOT declared in this module — an
	// `impl Debug for Member` still lowers its function, and
	// `Debug.inspect(m)` in the same module still reaches it directly. Only
	// the existential path needs the declaration.
	ifaceName string
	iface     *ifaceDef
	recv      kind
	decl      *ast.ImplBlock
	// synth marks a block the front end produced from a `derive`, or the
	// universal Debug. Its refusals are never reported at its own position.
	synth bool
	// ifaceSubst is the generic interface instantiation this block implements,
	// or nil. Non-nil exactly when `iface.instOf != nil`.
	//
	// Carried on the def rather than re-derived at lowering, for the reason
	// `genericImplInst` carries its own frame: `emitImpl` runs long after the
	// registering frame was popped, and a monomorphized default BODY is exactly
	// where a type parameter still has to resolve. See ifaceinst.go.
	ifaceSubst map[string]kind
	// oper is the stdlib OPERATOR interface this block implements, or nil.
	// Non-nil marks the one family that is registered in `g.operImpls` instead
	// of in `g.implsByIface`, because a receiver may carry SEVERAL impls of one
	// operator interface distinguished by their right-hand type and that map's
	// key drops it. `iface` stays nil for these: there is no `*ifaceDef` to
	// instantiate and no table to bind. See operimpl.go, which owns the
	// mechanism and the argument for the second index.
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
	// `d.items[name]` — among them `bindImpl`, `interfaceCall`,
	// `typeQualifiedCall`, `foreignIfaceCall`, `equalCall`, `operimpl.go` and
	// `tail.go` — test only `!= nil` and would take a refused item, naming a
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
	// emitted, since the receiver's name is visible only there.
	typeScope *blockTypeDecls

	lowerable bool
	why       string
	whyDetail string
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
// # THE GRANULARITY RULE, AND ITS SAFETY CONDITION
//
// The unit this package decides lowerability about is the ITEM: `emitImpl`
// walks `d.order` one function at a time and `bindImpl` binds one table entry
// at a time. Two sites could answer at the whole BLOCK instead — a written
// item with its own type-parameter binder (`implItemSig`'s `generic impl
// function`) and an inherited default carrying the interface method's own
// `why` (`inheritDefaults`). Answering there would let one method's refusal
// delete every sibling method, and — because `registerInstanceImpls` turns an
// unlowerable impl into an unlowerable INSTANCE — the receiver TYPE with them:
// `fn prefer<K>`, a default nobody wrote, would refuse `Holder{item:
// "ignored"}`.
//
// # WHY THIS IS A PREDICATE AND NOT A LIST OF ROWS
//
// The condition for withholding is NOT "the gap is small". It is that NOTHING
// CAN DISPATCH TO THE METHOD. A withheld method whose dispatch table is live is
// a table this receiver never binds into, and a table dispatch on it would be
// a run-time trap on a program the checker accepted. `ifaceMethod.dispatchable`
// is exactly that question, so it is asked here rather than restated as a row
// list that a new gap row would silently join.
//
// An erased-receiver call declines on `!m.dispatchable()`
// (erasedInterfacePlan), and `bindsImpl` may answer yes for a block with a
// withheld method only because the method it lacks is an undispatchable one.
//
// An inherent block has no interface, no table and no dispatch, so a withheld
// member is reached only by a call that already handles an absent item.
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
			fields:    map[string]*ifaceField{},
			lowerable: true,
			unit:      -1,
		}
		switch {
		case len(id.TypeParams) > 0 || len(id.WhereClauses) > 0:
			// A generic interface needs the dictionary the type-parameter
			// call shape needs, and for the same reason. The template is not
			// erasable and not dispatchable (an impl block binds its own
			// instance, ifaceinst.go), and it is NOT refused at the
			// declaration, which is `ifaceDecl`'s own rule for a gap a file may
			// never exercise. See ifaceDef.useSiteOnly.
			d.lowerable, d.why, d.useSiteOnly = false, "generic interface", true
		}
		// A `variant` requirement contributes NOTHING here and refuses nothing:
		// Nomi has no operation that reaches a variant through the interface,
		// and the conformance obligation is discharged ENTIRELY by the analyzer
		// at the impl block, including for a non-enum receiver. See
		// ifacevariant.go.
		g.ifaces[id.Name] = d
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
	g.resolveIfaceFields(d)
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
			m.params = append(m.params, g.typeOf(im.Params[j].TypeAnnotation))
		}
		// A BARE `self` return is erased to `any` in the table, so it has no
		// kind here either. A NESTED one (`Maybe<self>`) is neither: typeOf
		// answers kindInvalid for it and methodSignatureGap names it, which is
		// why the model distinguishes the two rather than carrying a bool.
		if im.ReturnTypeExpr != nil && !m.shape.resultBareSelf() {
			m.result = g.typeOf(im.ReturnTypeExpr)
		}
		m.why = g.methodDispatchGap(d, im, m)
		// The TABLE follows the SIGNATURE, not the receiver. A method with no
		// self-position parameter, or one returning self, still gets one: an
		// erased receiver cannot select an implementation in it — which is what
		// `why` above records and what every erased-receiver site refuses
		// under — but a call keyed by a concrete type ARGUMENT can, which is
		// where a bounded `T.zero()` looks. See dict.go.
		//
		// An INSTANCE of a generic interface mints none, and that clause is not
		// a policy choice: `ifaceDecl` declares a table variable for the defs
		// in `g.ifaceOrder`, which holds the TEMPLATE, so an instance's table
		// would be named by a lowered call and declared nowhere. See ifaceinst.go.
		if gap, _ := g.methodSignatureGap(im, m); gap == "" && d.instOf == nil {
			m.table = true
		}
		d.methods[im.Name] = m
		d.order = append(d.order, m)
	}
}

// methodDispatchGap names what stops this function from being dispatched on an
// ERASED RECEIVER, or "" when nothing does.
//
// The interesting rows are the two the design doc names as making Go's own
// interfaces unusable: `FromJson.from_json` mentions self only inside its
// RETURN, and `App` declares no functions at all while `Literal` declares one
// with no self anywhere in its parameters. None of the three has a receiver to
// dispatch on — and that is a statement about the RECEIVER, not about the
// signature, which is why they do not stop a table from existing. A call keyed
// by a concrete type ARGUMENT needs no receiver, so those same two rows are
// exactly where the dictionary earns its place. See dict.go.
//
// Precedence: the rows that make a signature unrepresentable outright outrank
// the receiver rows, and the
// per-parameter rows come after them. `hard` is what says which is which, so
// the order lives here and the rows live in one place each.
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
// `Comparable.compare(a: self, b: self)` selects its implementation from ONE
// receiver and then needs every OTHER self position to hold that same concrete
// type — the table entry unwraps them all with a Go type assertion. Nothing
// makes that true: two `Comparable`-typed values may box two different types,
// and the checker accepts it.
//
// This is a refusal rather than a trap because the program is legal:
// `Comparable.compare` over a `shapes.Point` and a same-named `Point` from the
// entry file checks clean, and an answer would need the second operand's
// fields read structurally after dispatching on argument zero. The table entry
// cannot do that without dynamic field access, and its type assertion would
// fail instead. So the honest lowering is no lowering.
//
// It is an ERASED-RECEIVER row and only that. A concrete qualifier
// (`Comparable.compare(p, q)` on two `Point`s) is a direct call with both
// operands statically typed and is unaffected; so is a monomorphized interface
// default; and so is the DICTIONARY route, where the `*rt.TypeID` argument
// fixes one concrete type for every self position at once — which is why the
// table still exists. See dict.go and stdiface.go.
const gapMultiSelf = "interface function with more than one self-typed parameter"

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
		// Go cannot dispatch on a return type, and a table keyed on the
		// RECEIVER cannot produce one either. A table keyed on a type argument
		// can, because the key IS the identity the result needs.
		return "interface function returning self"
	case m.shape.multi():
		return gapMultiSelf
	}
	return ""
}

// gapNestedSelfResult is the row that says why a self inside a CONTAINER is
// not the same question as a self return.
//
// `FromJson.from_json(json: Json): Result<self, Json.ShapeError>`,
// `Discrete.next(v: self): Maybe<self>`, `Steppable.step_by(…): Maybe<self>` —
// the three in std. A BARE
// `self` return erases to `any` in the table and a dictionary-driven call
// re-boxes it under the TypeID that selected the implementation, which recovers
// exactly the identity that was lost. A NESTED one cannot: the identity would
// have to be re-attached INSIDE a container this builder would have to take
// apart and rebuild, and `Result<self, E>`'s `E` is not self.
//
// It is a SIGNATURE row and it is `hard`, which keeps the diagnostic accurate.
// Without it `Parse.parse(text: String): Maybe<self>` would refuse as
// `interface function without a self-typed parameter` — true of the erased
// route and irrelevant to the dictionary route, which exists precisely to serve
// a method with no receiver. Naming the receiver at a call whose problem is the
// RETURN sends a reader to fix the wrong thing.
const gapNestedSelfResult = "interface function returning a type that contains self"

// methodSignatureGap names what stops a dispatch TABLE from existing for this
// function at all, or "" when nothing does. hard marks the two rows that make
// the signature unrepresentable outright rather than merely unrepresentable in
// one position; methodDispatchGap uses it for precedence.
//
// Nothing about the RECEIVER appears here. That is the whole point of the
// split: a table is a property of the signature, and how a call site finds its
// key is a property of the call site.
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
		return "generic interface function", true
	case im.Extern:
		return "host-backed interface function", true
	case m.shape.resultNestedSelf():
		// Hard, so it outranks the receiver rows: a reader sent to the
		// receiver by a return-type obstacle fixes the wrong thing. See
		// gapNestedSelfResult.
		return gapNestedSelfResult, true
	}
	for j := range im.Params {
		p := &im.Params[j]
		switch {
		case p.Destructure != nil:
			return "destructuring interface parameter", false
		// kindInvalid: reports — returns the refusal name its caller rejects under.
		case !m.shape.selfTyped(j) && m.params[j] == kindInvalid:
			return "non-scalar interface parameter type", false
		}
	}
	// A parameter DEFAULT is deliberately not a gap here, and the distinction
	// is between dispatch and the call. The Go table entry's signature carries
	// every parameter whether or not it has a default, so nothing about a
	// default stops an erased receiver from being dispatched on. What a default
	// can stop is a call that OMITS it: which declaration supplies the value is
	// the selected implementation's business (an `impl` may override an
	// interface default's default, and the override wins, including through an
	// erased receiver), so a short call on an erased receiver has no
	// single default to fill, and such a call declines.
	//
	// A BARE self-typed return is not a gap here either: it erases to `any` in
	// the table, so there is a representable signature. What it stops is the
	// erased-receiver ROUTE, reported by methodDispatchGap above. A NESTED one
	// is a gap and is handled above, hard.
	// kindInvalid: reports — returns the refusal name its caller rejects under.
	if im.ReturnTypeExpr != nil && !m.shape.resultBareSelf() && m.result == kindInvalid {
		return "non-scalar interface return type", false
	}
	return "", false
}

// buildImpls registers every impl block, then lowers the SYNTHESIZED ones.
//
// Registration is complete before any body is emitted because an impl body may
// call any other impl in the module — a derived `Debug.inspect` on a struct
// with a struct field calls the field type's own inspect — and because a
// synthesized block's lowerability has to be settled before an ordinary
// function body that dispatches to it is emitted. Package-level Go
// declarations have no order requirement, so emitting these first costs
// nothing.
func (g *gen) buildImpls(nodes []ast.Node) {
	for _, n := range nodes {
		ib, ok := n.(*ast.ImplBlock)
		if !ok {
			continue
		}
		g.registerImpl(ib)
	}
	g.registerBlockLocalDebug(g.blockTypeOrder)
	g.emitSynthImpls(g.implOrder)
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
		return d.recv.nomi()
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
// registers it in the dispatch table.
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
		// A GENERIC interface, monomorphized at this block's type arguments.
		// Asked FIRST, and the order is the whole safety argument: the template
		// def below is deliberately unlowerable, so falling through would
		// refuse the very block this instantiation serves.
		//
		// The RECEIVER is resolved here rather than taken from below, because an
		// instance's identity is (block, receiver) — one block on a generic
		// receiver is registered once per INSTANTIATION, and keying on the block
		// alone would hand the second instantiation the first's bindings. Asking
		// `receiverKind` is the same question the arm below asks and it declines
		// identically; a generic receiver only ever arrives through
		// `registerImplAt`, which supplies `recv` directly. See ifaceinst.go.
		if tpl := g.ifaceTemplate(name); tpl != nil {
			instRecv := recv
			// kindInvalid: sentinel — the caller's "resolve it yourself" request, not an operand.
			if instRecv == kindInvalid {
				instRecv, _ = g.receiverKind(ib.Receiver)
			}
			inst, why, detail := g.ifaceInstanceFor(tpl, ib, instRecv)
			if why != "" {
				d.lowerable, d.why, d.whyDetail = false, why, detail
				return
			}
			d.iface, d.ifaceSubst = inst.def, inst.subst
			// Live for every item signature AND for inheritDefaults below: an
			// item's annotation may name `T` and an inherited default's
			// parameter kinds are the INSTANCE's, already substituted.
			g.pushIfaceSubst(inst.subst)
			defer g.popIfaceSubst()
		}
		// TYPE ARGUMENTS WRITTEN AGAINST AN INTERFACE WITH NO TEMPLATE HERE —
		// a STDLIB generic interface (`impl Add<Score, Score> for Score`) or a
		// sibling's. Nothing to instantiate, so the key stays.
		//
		// AN EXPLICIT ARM RATHER THAN A FALL-THROUGH: a fall-through and a
		// deliberate decline print the same refusal. `Add`/`Subtract`/
		// `Multiply`/`Divide` are excluded from `stdIfaceSpecs` because
		// `stdIfaceSpec.matches` returns false on `len(decl.TypeParams) > 0`,
		// so `stdIfaceNamed` answers nothing for them and there is no
		// declaration for this builder to instantiate.
		if d.iface == nil {
			// THE FOUR OPERATOR INTERFACES, which need no `*ifaceDef` and no
			// table: dispatch is from a statically-known left operand at all
			// three of their call shapes, each interface declares one method
			// with no default, and `bindImpl` returns immediately for a nil
			// iface. Claimed AHEAD of the decline below.
			//
			// It only decides ROUTE, and then falls through to the SHARED tail
			// below: the receiver resolution, the item signatures and the
			// default inheritance are the same questions for this family, and a
			// second copy of them is exactly the drift this function's own
			// comment exists to prevent. The divert is at the LAST step, the
			// registration — see the `d.oper != nil` arm there.
			g.admitOperImpl(d, ib, name)
			if _, explicit := ifaceHeaderArgs(ib.Interface); explicit && d.oper == nil {
				d.lowerable, d.why, d.whyDetail = false, "generic interface impl", typeText(ib.Interface)
				return
			}
		}
		if d.iface == nil {
			// A sibling file's interface resolves to a MIRROR, so an impl
			// written here binds into the OWNER's dispatch table rather than a
			// second one. See existential.go.
			d.iface, _ = g.ifaceNamed(name)
		}
		if d.iface == nil {
			// A STDLIB interface. Asked LAST, so a local declaration and a
			// sibling's mirror both win — the same precedence typeOf uses, and
			// the same the analyzer's own "project wins" shadowing rule uses.
			// An impl written here binds into rt's variable, which is the one
			// table for that interface in the whole artifact. See stdiface.go.
			d.iface, _ = g.stdIfaceNamed(name)
		}
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
		recv, ok = g.receiverKind(ib.Receiver)
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

	for _, item := range ib.Items {
		if _, isOnce := item.(*ast.OnceBinding); isOnce {
			// An owner-level `once` is no function of the block: declareOnces
			// files it and the module loop lowers it. See once.go.
			continue
		}
		fd, ok := implItemFunc(item)
		if !ok {
			d.lowerable, d.why, d.whyDetail = false, "host-backed impl function", constructName(item)
			return
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
		d.items[it.name] = it
		d.order = append(d.order, it)
	}
	g.inheritDefaults(d)

	// AN OPERATOR IMPL NEVER ENTERS `implsByIface`, and this return is the whole
	// of that claim: one line to check, rather than a property of eight
	// consultation sites. No lookup in that map ever sees an operator impl,
	// which is why the second index cannot reproduce `noEquatableImpl`'s
	// wrong-answer failure. See operimpl.go.
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
	byRecv := g.implsByIface[d.ifaceName]
	if byRecv == nil {
		byRecv = map[kind]*implDef{}
		g.implsByIface[d.ifaceName] = byRecv
	}
	if prior, dup := byRecv[d.recv]; dup {
		// Two impls of one interface for one type is a coherence error the
		// analyzer rejects. Refusing both rather than picking is the same rule
		// rt.Method.Bind holds at run time: never let ordering decide.
		prior.lowerable, prior.why, prior.whyDetail = false, "duplicate impl block", d.label()
		d.lowerable, d.why, d.whyDetail = false, "duplicate impl block", d.label()
		return
	}
	byRecv[d.recv] = d
}

// receiverKind resolves the type an impl block is for.
//
// A builtin scalar is admitted: `impl Numberish for Int` is legal under the
// orphan rule as long as the interface is local, and it is precisely the case a
// Go method could not express.
func (g *gen) receiverKind(te ast.TypeExpr) (kind, bool) {
	st, ok := te.(*ast.SimpleType)
	if !ok {
		return kindInvalid, false
	}
	k := g.typeOf(st)
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
	case fd.Body == nil && !implItemIsHost(fd):
		return nil, implGap{why: "impl function without a body", detail: fd.Name, whole: true}
	}
	it := &implItem{
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
		switch {
		case p.Destructure != nil:
			return nil, implGap{why: "destructuring parameter", detail: p.Name, whole: true}
		case p.TypeAnnotation == nil:
			// A DEFAULTED parameter is no exception: paramKind's rule, that
			// inferring a `fn` parameter's type from its default would be a
			// second inference path inside a backend, holds here too.
			return nil, implGap{why: "parameter without a declared type", detail: p.Name, whole: true}
		}
		k := g.typeOf(p.TypeAnnotation)
		// The TYPE's own reason, for the caller to reject — the same repoint funcDecl makes. See sigreason.go.
		// kindInvalid: reports — typeRefusal names the gap the annotation carries.
		if k == kindInvalid {
			construct, detail, _ := g.typeRefusal(p.TypeAnnotation)
			return nil, implGap{why: construct, detail: p.Name + ": " + detail, whole: true}
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
		if m := d.iface.methods[fd.Name]; m == nil {
			return nil, implGap{why: "impl function not declared on the interface",
				detail: d.ifaceName + "." + fd.Name, whole: true}
		}
	}
	return it, implGap{}
}

// inheritDefaults gives the receiver one monomorphized copy of every interface
// default the block did not override.
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
		case d.iface.foreign != "" && !g.portableDefault(d.iface, m):
			// A default's body is Nomi written in the DECLARING file and it is
			// monomorphized, not called — so lowering it here resolves its
			// names against THIS file's scope. When the two files resolve one
			// of those names differently that is a silent wrong answer rather
			// than a Go compile error, the hazard that has a field default or
			// a parameter default reached from another file lowered in its
			// declaring file's gen instead. portableDefault asks
			// whether any name actually differs, rather than assuming one
			// does; see existential.go.
			//
			// This row routinely stays WHOLE, which is `mayWithhold`'s
			// condition doing its job: a portable-looking default has an
			// ordinary signature, so its table is live and every other
			// implementor binds into it. Withholding it here would leave THIS
			// receiver with no entry in a table an erased call reaches, a
			// run-time trap in place of a refusal.
			gap.why = "sibling file interface default"
			gap.detail = d.iface.foreign + "." + d.ifaceName + "." + m.name
		case m.why != "" && m.why != "interface function without a self-typed parameter" && m.why != gapMultiSelf:
			// Two of the erased-receiver rows do not apply to a MONOMORPHIZED
			// default: the body is emitted once per implementing type with
			// every self position bound to that concrete type, so "no receiver
			// to dispatch on" and "two self positions that may disagree" are
			// both answered by construction here. Every other reason still
			// refuses the method, rather than the block.
			gap.why = m.why
		case m.shape.recvAt() < 0:
			// No receiver, so nothing to substitute self with and no way to
			// reach the default type-qualified either.
			gap.why = m.why
		}
		if gap.named() {
			if !d.mayWithhold(m.name) {
				d.lowerable, d.why, d.whyDetail = false, gap.why, gap.detail
				return
			}
			d.noteGap(m.name, gap)
			continue
		}
		if d.iface.foreign != "" {
			if g.declaredIn == nil {
				g.declaredIn = map[ast.Node]int{}
			}
			g.declaredIn[body] = d.iface.unit
			for _, p := range m.decl.Params {
				if p.Default != nil {
					g.declaredIn[p.Default] = d.iface.unit
				}
			}
		}
		it := &implItem{
			name:      m.name,
			params0:   m.decl.Params,
			body:      body,
			line:      m.decl.Line,
			result:    m.result,
			inherited: true,
			lowerable: true,
			// params0 carries the interface's PARAMETER DEFAULTS as well as its
			// types, so a call site that omits one fills it from here.
			// portableDefault has already agreed, name by name, that a mirror's
			// default expressions resolve identically from both files — which
			// is what makes emitting one in THIS module's scope the same
			// program the programmer wrote there.
		}
		for i, k := range m.params {
			if m.shape.selfTyped(i) {
				k = d.recv
			}
			it.params = append(it.params, k)
		}
		d.items[m.name] = it
		d.order = append(d.order, it)
	}
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
		// A STRUCTURAL type has no declaring file; rt interns its identity
		// process-wide. See rt/anontid.go for why tagList/tagTuple/tagMap stay
		// closed.
		if k.comp == nil {
			return false
		}
		g.anonTids[k] = true
		return true
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
		owner := g.reg.gens[o.unit]
		if owner == nil {
			return false
		}
		src := owner.types[o.nomi]
		return src != nil && owner.tids[named(src)]
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
// A PRELUDE instance is skipped too — `rt.Maybe[T]` is declared in rt and its
// identity is not this package's to mint — and keeps the demand-driven path.
func (g *gen) mintTypeIDs() {
	for _, d := range g.typeOrder {
		if d == nil || d.foreign != "" || d.preludeOf != nil || !d.lowerable {
			continue
		}
		k := named(d)
		g.tids[k] = true
	}
}

// qualifiedNomiName is the name a TypeID carries for diagnostics: module plus
// declared name.
//
// Qualified rather than short on purpose. Two distinct types whose short names
// match — `shapes.Point` and `geometry.Point` — would otherwise hold byte-identical
// contents, and two byte-identical package variables are a merge candidate for
// a sufficiently aggressive toolchain. Making the contents differ by
// construction closes that the way the field itself closes zero-size address
// sharing. It is also the better diagnostic.
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
	d := g.ifaces[id.Name]
	if d == nil || d.decl != id {
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

// implDecl lowers one impl block's functions and records its dispatch bindings.
func (g *gen) implDecl(ib *ast.ImplBlock) {
	d := g.implFor(ib)
	if d == nil {
		g.rejectWhole("impl block", typeText(ib.Receiver), ib)
		return
	}
	if d.synth {
		// Already lowered (or already discarded) in buildImpls. Never refused
		// by name: nobody wrote it, and its position is fabricated.
		return
	}
	if g.isGenericImplTemplate(ib) {
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
		g.rejectWhole(d.why, d.whyDetail, ib)
		return
	}
	g.emitImpl(d)
	g.rejectWithheldItems(d, ib)
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
		fd, isFn := implItemFunc(item)
		if !isFn {
			continue
		}
		gap, withheld := d.gaps[fd.Name]
		if !withheld {
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

// emitImpl lowers every item in the block and records its dispatch bindings.
//
// A GENERIC interface instantiation pushes its frame here, because lowering is
// where a monomorphized default BODY resolves its type parameters and this runs
// long after resolveImplDef's frame was popped. `flushGenericImpls` does the
// same for a generic RECEIVER's frame, and the two nest rather than conflict:
// they bind different names and genericSubstKind scans innermost-first.
func (g *gen) emitImpl(d *implDef) {
	defer g.enterTypeScope(d.typeScope)()
	if d.ifaceSubst != nil {
		g.pushIfaceSubst(d.ifaceSubst)
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
	// allocates from a band around 2^30, which `at` refuses and which is not a
	// legal `//line` argument. Attribute the whole function to the RECEIVER's
	// declaration, which is where the `derive` that asked for it sits.
	if d.synth && d.recv.def != nil {
		g.at(d.recv.def.line)
	}
	g.at(it.line)
	prevTail := g.tail
	g.tail = nil
	prevImpl := g.implEmitting
	g.implEmitting = d
	defer func() { g.tail, g.implEmitting = prevTail, prevImpl }()
	// A bare call in this body may name one of THIS block's own members —
	// `each_while(left, yield)` inside `impl Iter for Tree`. Set here, AFTER
	// the multi-member driver above has returned: that driver holds several
	// members' bodies and they need not all come from one block, so a bare
	// self-call inside one refuses rather than resolving against a block that
	// may not own it. See implselfcall.go.
	prevSelf := g.implBlock
	g.implBlock = d
	defer func() { g.implBlock = prevSelf }()

	g.pushScope()
	defer g.popScope()
	for i, p := range it.params0 {
		if !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: it.params[i]})
		}
	}

	prev := g.result
	g.result = it.result
	g.irBodyObserve(irBodyImplFn, irImplSource(it) != nil)
	irTakeDecline()
	produced, retained := g.irImplLower(d, it, nil, it.params)
	if !retained {
		declined := irTakeDecline()
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

// bindImpl registers this impl in each dispatchable interface function's table.
//
// Nothing is bound when the interface is not resolved: the impl's FUNCTIONS
// are still emitted and still reachable through a concrete qualifier.
func (g *gen) bindImpl(d *implDef) {
	if d.iface == nil {
		return
	}
	binds := make([]*ifaceMethod, 0, len(d.iface.order))
	for _, m := range d.iface.order {
		// hasTable, not dispatchable: an implementation of a method that no
		// erased receiver can select is still the answer a DICTIONARY-driven
		// call needs, and it is the only answer there is for `T.zero()`. An
		// unbound table would trap at run time on a program the checker
		// accepted. See dict.go.
		if m.hasTable() && d.items[m.name] != nil && !g.debugDeferred(d.iface, m) {
			binds = append(binds, m)
		}
	}
	// A `field` requirement counts here too, and it is the reason this is not
	// gated on the METHOD bindings alone: `interface Named { field name }` has
	// no methods, so `impl Named for Person` owes exactly one getter and
	// nothing else. Gating on methods alone would bind nothing for a
	// requirements-only interface, and every read through the box would trap.
	fields := g.bindsAnyIfaceField(d)
	if len(binds) == 0 && !fields {
		return
	}
	if !g.hasTID(d.recv) {
		g.reject("existential identity for a type", d.recv.nomi(), d.decl)
		return
	}
	// Binding into a MIRROR's table names the declaring file's package. It is
	// the same variable the declaring file's own impls bind into: one
	// interface, one table, whichever file wrote the impl.
	if fields {
		g.bindIfaceFields(d)
	}
}

// speculate runs f, discarding its output AND its refusals if it refused
// anything. It reports whether f lowered cleanly.
//
// Used for exactly one thing: a front-end-synthesized impl block, whose
// position is fabricated and whose text nobody wrote. Everywhere else a refusal
// is the product, not a failure — see unsupported.go.
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
// Extracted from speculate because a SECOND caller needs the rollback without
// the discard-on-refusal rule: stdiface.go's erased-receiver arm has to lower
// the arguments to learn the receiver's kind, and then hand the call back to
// the rest of implCall's chain when the kind is not the one it serves. Two
// hand-written rollbacks would be two places for a later field to be forgotten
// in one of them.
//
// Each line below is a thing a discarded run must not leave behind, and the
// reason it must not is on the line.
func (g *gen) snapshot() func() {
	errs := len(g.errs)
	nomiLine := g.nomiLine
	census := g.census
	census.boxes = census.boxes[:len(census.boxes):len(census.boxes)]
	// THE TEST TABLE. `testCase` appends its row to `g.tests` and derives the
	// case's name from `len(g.tests)` BEFORE it builds the body, so without
	// this a discarded run would leave a row naming a case whose body had been
	// rolled back. The only caller that speculates a test case is
	// stdtests.go's per-prompt loop, and a stdlib module whose prompt cases
	// lower only in part is where that row would survive.
	tests := len(g.tests)
	return func() {
		g.errs = g.errs[:errs]
		g.nomiLine = nomiLine
		// A discarded run's boxes and dispatches did not happen either. The
		// census is a claim about what was lowered, so leaving them in would
		// make the diagnostic count work that was thrown away.
		g.census = census
		// Discarded output must not leave its std package references behind
		// either.
		// The test table, for the reason recorded above the capture.
		g.tests = g.tests[:tests]
	}
}

// --- call sites ---------------------------------------------------------------

// qualifierKind is the kind a call qualifier names, or kindInvalid.
func (g *gen) qualifierKind(owner string) kind {
	// A TYPE PARAMETER of an enclosing generic TYPE, resolved through the
	// substitution frame. Asked FIRST, for the reason typeOf's *ast.SimpleType
	// arm asks genericSubstKind first and dictTypeParamCall is asked ahead of
	// every declaration lookup in implCall: a type parameter SHADOWS a
	// same-named type for the length of its activation.
	//
	// This is what makes `T.hello(value.value)` inside
	// `impl Greet for Wrapper<T>` an ordinary concrete call. The body is only
	// ever emitted inside an instantiation, so `T` HAS a concrete answer here —
	// and without this arm the same body would reach the end of implCall's
	// chain and report `qualified call | T.hello`. Declines when no frame is open or the
	// name is not one of its parameters, so nothing else pays for it.
	//
	// ONE insertion point rather than an arm per call form, which is the
	// property generictype.go claims for genericSubstKind's placement in typeOf:
	// every qualified-call shape funnels through here for its receiver kind.
	// See genericimpl.go.
	// kindInvalid: lookup — a miss tries the next form.
	if k, isParam := g.genericSubstKind(owner); isParam && k != kindInvalid {
		return k
	}
	switch owner {
	case "Int":
		return kindInt
	case "Float":
		return kindFloat
	case "String":
		return kindString
	case "Bool":
		return kindBool
	case "Unit":
		return kindUnit
	}
	if d, isBlockLocal := g.blockLocalNamed(owner); isBlockLocal {
		// A type declared in an enclosing block wins its name there.
		if d.lowerable {
			return named(d)
		}
		return kindInvalid
	}
	if d, found := g.types[owner]; found && d.lowerable {
		return named(d)
	}
	return kindInvalid
}

// The fit tier and the subtyping relation are `ir.Fit` (`FitNone`/
// `FitWidened`/`FitExact`), `ir.Table.Accepts` and `ir.Table.Widens`; the two
// callers that score, `operImplFor` and `foreignIfaceCall`, reach them through
// `irSelectAmong`. See foreignIfaceCall for why the tier exists.
//
// `embedsVariant` (types.go) is not the same question. It answers WHICH
// VARIANT of the enum embeds the value, which is what `coerce` needs to
// construct one; `ir.Table.Embeds` records the edge and `ir.Type` carries no
// variant structure to name the arm with.

// --- erasure ------------------------------------------------------------------

// implements reports whether the PROGRAM lowers an impl of d for k.
//
// The program rather than this file, and the difference is what makes a
// cross-file existential possible at all: `MyApp{logger: ProdLogger}` written
// in one file boxes a type declared in a second whose `impl Logger` is written
// in a third, and the box dispatches through a table any of them may have
// bound into. See existential.go's implIndex — the key is a pair of
// DECLARATION NODES, never a pair of names.
func (g *gen) implements(d *ifaceDef, k kind) bool {
	return g.bindsImpl(d, k)
}

// existential is the kind of values whose static type is the interface d.
func existential(d *ifaceDef) kind { return kind{tag: tagIface, iface: d} }

// handWrittenNominalEquatable reports whether k is an ENUM or a DISTINCT
// carrying a programmer-written `impl Equatable`.
//
// equatableDispatches excludes enums and distincts, but `==` on either
// nominal kind routes through a hand-written impl when one exists, so for
// those the exclusion is wrong and a comparison that reaches the structural
// comparator here is a silent wrong answer.
//
// Returning true does not lower anything. It puts the comparison back on the
// impl route, where equalCall may lower it and the noEquatableImpl fence
// refuses it under `equality on a named type` otherwise. A refusal is never a
// wrong answer; a DIFF always is.
//
// `synth` is the discriminator, and it must be: a DERIVED impl on either kind
// still compares structurally, so firing on one would stop `==`
// lowering for every `derive Equatable` enum — `Maybe` and `Result` included
// — and for `std/duration`'s and `std/instant`'s `pub opaque type … Int`
// (`05-calendar-and-time/durations_test.nomi`).
//
// Only LOCAL impls. A std enum or distinct carrying a hand-written Equatable
// is not covered: establishing provenance through `g.std.byIface` needs a
// separate look. std declares no hand-written `impl Equatable` on either kind;
// its Equatable receivers are primitives, containers, structs and one
// `host type`.
func (g *gen) handWrittenNominalEquatable(k kind) bool {
	if k.def == nil || !(k.def.isEnum || k.def.isDistinct) {
		return false
	}
	d := g.implsByIface["Equatable"][k]
	return d != nil && !d.synth
}

// noEquatableImpl reports whether this builder can ESTABLISH that no
// `impl Equatable` exists for k anywhere in the program.
//
// Only asked of a struct-shaped type, and only to decide whether the
// STRUCTURAL fallback is the right answer. It has to be positive rather than
// "the lookup for a callable impl missed", because that lookup misses for two
// opposite reasons: no impl at all, which is exactly when `==` is structural
// equality, and an impl this builder cannot lower, which is when `==` CALLS
// something and a structural answer would be a silent wrong answer.
// Both `stdlibImplOf` and `equalCall` collapse those two into one nil.
//
// Three sources. The FOREIGN arm leaves this gen and establishes the answer
// program-wide off the declaration node the mirror carries; the reasoning, including why the OWNER'S FILE is the wrong scope to
// ask, is in foreign.go's foreignNoEquatableImpl.
//
// The std probe reads `byIface` BEFORE the `why`/signature filtering
// stdlibImplOf applies, which is the whole point: a std impl that exists and
// was refused must refuse the comparison, not silently become structural.
//
// The lookups are keyed on the KIND — the `*typeDef` pointer — everywhere
// except the interface SPELLING, which is a std interface name and not a type
// identity. A bare name here could only ever make this answer FALSE for a type
// that has no impl, which over-refuses; it can never grant a structural
// lowering to a type that has one. The error direction is the safe one and
// that is deliberate.
func (g *gen) noEquatableImpl(k kind) bool {
	if k.def == nil {
		return false
	}
	if k.def.foreign != "" {
		// A MIRROR holds none of the owner's impl table, and the answer is not
		// in this gen at all. It is established program-wide, keyed on the
		// declaration node the mirror carries verbatim. See foreign.go.
		return g.foreignNoEquatableImpl(k.def)
	}
	if g.implsByIface["Equatable"][k] != nil {
		return false
	}
	if g.std != nil && len(g.std.byIface["Equatable.equal?"][k]) > 0 {
		return false
	}
	return true
}

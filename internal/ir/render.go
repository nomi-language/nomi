package ir

// The `render` class: turn a value into the String a human reads.
//
// THIS CLASS CONTRIBUTES ONE NODE AND NO NEW FIELD, and the reason it is one
// node rather than three is the reason the class is worth a node at all:
// Nomi has THREE renderings of one value and they DISAGREE, so a producer
// that names one of them is stating a fact a consumer cannot re-derive.
//
//	RenderRow      the assertion report's `values:` row   rt.RowText
//	RenderDisplay  `${…}`, `io.print`, `Display.to_string` the Display impl
//	RenderDebug    `Debug.inspect`, `dbg`, `io.inspect`    the Debug impl
//
// The disagreements are observable in the output, which is what makes the
// discriminator load-bearing rather than a taxonomy:
//
//	a Decimal        Row `1.50d`       Display `1.50`   Debug `1.50d`
//	a String         Row `"f.txt"`     Display `f.txt`  Debug `"f.txt"`
//	a struct         Row SORTED fields          Debug DECLARATION order
//	a host value     Row `<Handle>`             Debug `Handle`, with no
//	                                            written `impl Debug`
//	a hand impl      Row HONOURS a user's       Debug HONOURS it
//	                 (Module.ImplementRowDebug)
//	a derived impl   Row IGNORES it             Debug HONOURS it
//
// A row's leaves are Debug's: a Decimal and a Dynamic read as their written
// `impl Debug` renders them, at any depth (spec §17 *Failure reports*).
// Getting any of these wrong produces a silent wrong string: a passing
// assertion renders no operand, so only a deliberately failing fixture sees
// one.
//
// WHY NOT THREE NODES. They are one operation — read a value, answer a
// String — in three DISCIPLINES. The same argument gives one
// `ir.Match` over two positions and one `ir.Record` over two row lists.
//
// WHY NOT ZERO NODES, which is the live alternative because the renderer is
// the consumer's. The rendering is the one operation in this package whose
// WRONG ANSWER IS A STRING rather than a crash or a type error, and the
// discipline is chosen per call site by the producer. Nothing else asks the
// question out loud. A node asks it once.
//
// NO NEW FIELD, and the discipline is the only one. Every property a reader
// would reach for is derivable from the kind — whether a String is quoted,
// whether the rendering consults an `impl`, whether it sorts — so a field
// would be a vacuous field that repeats the kind.
// `Dispatches` below is a PREDICATE OVER THE KIND, the `Assert.Refuted`
// species and not the `Proj.Faults` species, and it is labelled as such.
//
// What is below this node is which function spells a discipline at a kind,
// and that function's body. The VM renders by walking its own values (rt.RowText,
// rt.DisplayShapeText, rt.DebugText). So the discipline is above the line and the
// renderer is below it, as iteration's push protocol and a call's calling
// convention are below theirs.

// RenderKind is which of Nomi's three renderings of a value this is.
type RenderKind uint8

const (
	// RenderRow is the assertion report's `values:` row: `rt.RowText`,
	// a STRUCTURAL walk whose leaves are Debug's. A user's `impl Display`
	// does not change a failure report, and a hand-written `impl Debug`
	// does, through the impls the module names (Module.ImplementRowDebug).
	//
	// The largest population.
	RenderRow RenderKind = iota + 1
	// RenderDisplay is `${…}`, `io.print` and `Display.to_string`: the
	// value's own `impl Display`, dispatched.
	//
	// The identity on a String. That is the one discipline whose answer for
	// some kind is the operand unchanged, which is why a consumer may not
	// assume a rendering wraps anything.
	RenderDisplay
	// RenderDebug is `Debug.inspect`, `dbg` and `io.inspect`: the value's
	// own `impl Debug`, dispatched, including the universal one the front
	// end synthesizes for every declared type.
	RenderDebug
)

func (k RenderKind) String() string {
	switch k {
	case RenderRow:
		return "row"
	case RenderDisplay:
		return "display"
	case RenderDebug:
		return "debug"
	}
	return "render?"
}

// Render answers the String one value reads as, under one discipline.
type Render struct {
	pos   Pos
	dst   Temp
	src   Temp
	kind  RenderKind
	impls []PartImpl
	// erased marks a Display rendering of an existential: its operand is any
	// value the program's Display impls cover, so the consumer selects the
	// value's own impl by its runtime type. See NewRenderDisplayErased.
	erased bool
}

// PartImpl names the implementation a Debug or Display rendering calls for
// values of one nominal type nested in its operand. Type is the type's
// identity, the name construction stamps on its values and its ValType's
// Sym names. Inst is the instance's ValType.InstanceKey for a generic type
// and empty otherwise: two instances of one generic type (`Box<Bool>`,
// `Box<String>`) share Type. The consumer selects the body by the static
// type of the part's position, read from the operand's ValType as the
// rendering descends (a value of an `embeds` enum is the embedded value at
// run time, and only its position says it is the enum's), and where the
// position states no declared type, by the value's descriptor, which
// carries both. A box names the static type it was made at, and a
// PartImpl of a box's name takes the value inside.
type PartImpl struct {
	Type string
	Inst string
	Fn   *Symbol
}

// NewRenderRow renders src the way an assertion report shows it.
func NewRenderRow(pos Pos, dst, src Temp) *Render {
	return newRender(pos, dst, src, RenderRow, "ir.NewRenderRow")
}

// NewRenderDisplay renders src by its shape: a scalar as its text, a
// tuple, an anonymous record, a collection and a declared type whose
// Display is derived as the literal that builds it, each part by its own
// Display.
func NewRenderDisplay(pos Pos, dst, src Temp) *Render {
	return newRender(pos, dst, src, RenderDisplay, "ir.NewRenderDisplay")
}

// NewRenderDisplayWith is NewRenderDisplay calling the named impls for the
// nominal values nested in src.
func NewRenderDisplayWith(pos Pos, dst, src Temp, impls []PartImpl) *Render {
	r := newRender(pos, dst, src, RenderDisplay, "ir.NewRenderDisplayWith")
	r.impls = append([]PartImpl(nil), impls...)
	return r
}

// NewRenderDisplayErased renders src, a `Display` existential, through the
// `impl Display` of the value it holds: a declared type's retained body
// (Module.ImplementDisplay), or a scalar's own text. A value of any other
// kind is refused by the consumer rather than rendered structurally, because
// the structural text is not what its Display impl answers.
func NewRenderDisplayErased(pos Pos, dst, src Temp) *Render {
	r := newRender(pos, dst, src, RenderDisplay, "ir.NewRenderDisplayErased")
	r.erased = true
	return r
}

// Erased reports whether this is a Display rendering of an existential.
func (r *Render) Erased() bool { return r.erased }

// NewRenderDebug renders src through its `impl Debug`.
func NewRenderDebug(pos Pos, dst, src Temp) *Render {
	return newRender(pos, dst, src, RenderDebug, "ir.NewRenderDebug")
}

// NewRenderDebugWith renders src through Debug, calling the named impls for
// the nominal values nested in it.
func NewRenderDebugWith(pos Pos, dst, src Temp, impls []PartImpl) *Render {
	r := newRender(pos, dst, src, RenderDebug, "ir.NewRenderDebugWith")
	r.impls = append([]PartImpl(nil), impls...)
	return r
}

// PartImpls are the implementations this rendering calls for the nominal
// values nested in its operand.
func (r *Render) PartImpls() []PartImpl { return r.impls }

func newRender(pos Pos, dst, src Temp, kind RenderKind, who string) *Render {
	requirePos(pos, who)
	if src == NoTemp {
		panic(who + ": a rendering reads a value and this one has none")
	}
	if dst == NoTemp {
		// A rendering with no destination is dead: it runs nothing and
		// answers a String nobody reads. `ir.newRef` refuses the same shape
		// for the same reason.
		panic(who + ": a rendering nobody reads is dead; do not build it")
	}
	return &Render{pos: pos, dst: dst, src: src, kind: kind}
}

// Kind is which rendering this is.
func (r *Render) Kind() RenderKind { return r.kind }

// Src is the temporary rendered.
func (r *Render) Src() Temp { return r.src }

// Dispatches reports whether this rendering consults the program's `impl`
// declarations, so its answer depends on what the program declared and not
// only on the value's type.
//
// A PREDICATE OVER THE KIND, and it is labelled that deliberately. It is the
// `Assert.Refuted` species — one field, and a question a consumer asks,
// derived rather than stored so no producer can write a state no source can
// mean. It is NOT the `Proj.Faults` species: nothing here is un-derivable.
//
// It is worth asking out loud because the two answers have different
// FAILURE MODES. A dispatched rendering that finds no implementation is a
// refusal, which can be reported. A structural rendering cannot
// refuse for that reason and cannot honour a hand-written impl either — so a
// consumer that substituted one for the other would print a wrong string with
// nothing to report. `internal/irbuild/debuginspect.go`'s header describes
// that mistake.
func (r *Render) Dispatches() bool { return r.kind != RenderRow }

func (r *Render) Pos() Pos                     { return r.pos }
func (r *Render) Dst() Temp                    { return r.dst }
func (r *Render) AppendUses(dst []Temp) []Temp { return append(dst, r.src) }
func (r *Render) String() string {
	if r.erased {
		return r.dst.String() + " = render display erased " + r.src.String()
	}
	return r.dst.String() + " = render " + r.kind.String() + " " + r.src.String()
}
func (r *Render) irNode()  {}
func (r *Render) irInstr() {}

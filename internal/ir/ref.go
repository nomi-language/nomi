package ir

// The `ref` class: read a named thing into a temporary.
//
// The builder has several lowering functions for this class (appFieldExpr,
// funcRef, onceRef, refSiblingOnce, stdFuncValue, stdOnceRef, typeWitnessArg,
// and the local-read path), and they are not that many operations. `onceRef`,
// `stdOnceRef` and `refSiblingOnce` build one shape and differ in WHICH cell
// they name, and `funcRef` and `stdFuncValue` likewise. The difference is an
// operand, so they collapse to the kinds below. Counting lowering functions
// answers "did a lowering enter the builder", not "how many opcodes does the
// IR need".
//
// A provider read is not a ref: `injected` is a copy of an already-computed
// temporary and `providerRead` is `ir.ProjField`, a projection with an
// operand. So there is no provider kind for a consumer's exhaustive switch to
// carry.
type RefKind uint8

const (
	// RefLocal reads a local binding or parameter.
	RefLocal RefKind = iota + 1
	// RefOnce reads a `once` cell, which must be FORCED: the initializer runs
	// at most once, on first read, and producer and consumer must agree about
	// when that is. Owners: onceRef, stdOnceRef, refSiblingOnce.
	RefOnce
	// RefAppField reads a field of the running app. It is not a plain read: a
	// an app-field write rebinds the field for the rest of a block, so the value
	// depends on the scope stack at this position. Owner: appFieldExpr.
	RefAppField
	// RefFunc takes a function as a value. Owners: funcRef, stdFuncValue.
	RefFunc
	// RefTypeWitness reads the witness for a type parameter — the dictionary
	// argument type-parameter dispatch is passed. Owner: typeWitnessArg.
	RefTypeWitness
	// RefContext reads the app's Context field, which answers the ACTIVE
	// execution context rather than a published value: a supervised task's
	// frame carries its context without the spawning call's deadline, and a
	// rebind replaces it for the rest of the activation. The symbol names the
	// field. Owner: appFieldExpr.
	RefContext
	// RefScope reads the running scope — the app fields, Context and deadline
	// in force at this point — into a handle a StoreScope restores. A block
	// that writes an app field reads it before its first write and restores
	// it at its exit, so the write holds for the block alone. The symbol
	// names the block's scope. Owner: scopedBlock.
	RefScope
)

func (k RefKind) String() string {
	switch k {
	case RefLocal:
		return "local"
	case RefOnce:
		return "once"
	case RefAppField:
		return "appfield"
	case RefFunc:
		return "func"
	case RefTypeWitness:
		return "witness"
	case RefContext:
		return "context"
	case RefScope:
		return "scope"
	}
	return "ref?"
}

// Ref reads one named declaration into a temporary.
type Ref struct {
	pos  Pos
	dst  Temp
	kind RefKind
	sym  *Symbol
}

// NewRefLocal reads a local binding or parameter.
func NewRefLocal(pos Pos, dst Temp, binding *Symbol) *Ref {
	return newRef(pos, dst, RefLocal, binding, "NewRefLocal")
}

// NewRefOnce reads a `once` cell. See RefOnce: this forces the cell.
func NewRefOnce(pos Pos, dst Temp, cell *Symbol) *Ref {
	return newRef(pos, dst, RefOnce, cell, "NewRefOnce")
}

// NewRefAppField reads an app field through the scope stack.
func NewRefAppField(pos Pos, dst Temp, field *Symbol) *Ref {
	return newRef(pos, dst, RefAppField, field, "NewRefAppField")
}

// NewRefFunc takes a function as a value.
func NewRefFunc(pos Pos, dst Temp, fn *Symbol) *Ref {
	return newRef(pos, dst, RefFunc, fn, "NewRefFunc")
}

// NewRefContext reads the active execution context through the field that
// names it.
func NewRefContext(pos Pos, dst Temp, field *Symbol) *Ref {
	return newRef(pos, dst, RefContext, field, "NewRefContext")
}

// NewRefScope reads the running scope for a later StoreScope.
func NewRefScope(pos Pos, dst Temp, scope *Symbol) *Ref {
	return newRef(pos, dst, RefScope, scope, "NewRefScope")
}

// NewRefTypeWitness reads a type parameter's witness.
func NewRefTypeWitness(pos Pos, dst Temp, typ *Symbol) *Ref {
	return newRef(pos, dst, RefTypeWitness, typ, "NewRefTypeWitness")
}

func newRef(pos Pos, dst Temp, kind RefKind, sym *Symbol, who string) *Ref {
	requirePos(pos, who)
	if dst == NoTemp {
		panic("ir: " + who + ": a read with no destination is dead; do not build it")
	}
	if sym == nil {
		panic("ir: " + who + ": a read needs the declaration it names")
	}
	return &Ref{pos: pos, dst: dst, kind: kind, sym: sym}
}

// Kind says what is being read.
func (r *Ref) Kind() RefKind { return r.kind }

// Sym is the declaration this reads. Its identity is the pointer.
func (r *Ref) Sym() *Symbol { return r.sym }

// Forces reports whether reading this may RUN something: a `once` cell's
// initializer fires on first read. A consumer may not reorder or duplicate a
// forcing read, and a consumer must force at the point the graph states or a
// program with an observable `once` initializer changes behaviour.
func (r *Ref) Forces() bool { return r.kind == RefOnce }

// Volatile reports whether two reads of this declaration at two positions may
// ANSWER DIFFERENTLY. It is a different question from Forces and neither
// implies the other.
//
// RefAppField is the member. An app-field write rebinds an app field for the rest
// of a block, so the value depends on the scope stack at the reading position
// and a consumer may not hoist the read out of, or into, a block that rebinds
// it. It is a predicate because a consumer deciding whether it may reuse a
// read has to ask something.
//
// RefOnce is NOT volatile. The first read runs the initializer and every later
// read is an atomic load of the same value, so the EFFECT is ordered and the
// ANSWER is stable — which is exactly why the two predicates are separate: a
// consumer may reuse a `once` read it has already performed and may not reuse
// an app-field read across a rebinding.
//
// RefContext is volatile for the same reason: a Context rebind replaces it.
func (r *Ref) Volatile() bool {
	return r.kind == RefAppField || r.kind == RefContext || r.kind == RefScope
}

// Stable reports whether this read is free to duplicate, reorder and reuse:
// it runs nothing and always answers the same value.
//
// It is `!Forces() && !Volatile()` and it exists so a consumer asks once
// instead of restating the conjunction. `internal/irbuild` computes `expr.pure`
// from it at all eight of its `ref` sites, so no site decides its own purity.
func (r *Ref) Stable() bool { return !r.Forces() && !r.Volatile() }

func (r *Ref) Pos() Pos  { return r.pos }
func (r *Ref) Dst() Temp { return r.dst }

// AppendUses appends nothing: a Ref names a declaration, not a temporary.
func (r *Ref) AppendUses(dst []Temp) []Temp { return dst }

func (r *Ref) String() string {
	return r.dst.String() + " = " + r.kind.String() + " " + r.sym.Name()
}

func (r *Ref) irNode()  {}
func (r *Ref) irInstr() {}

// --- Copy -------------------------------------------------------------------

// Copy moves one temporary into another.
//
// It is here for short-circuit `and` / `or` and for every other construct
// whose branch arms each write one destination. The builder's `gen.slot` /
// `gen.fixSlot` pair keeps the same shape on its side. In this package the
// type comes from the front end, so there is nothing to patch, but the
// multiple-writers shape stays. It is why this IR is not SSA.
type Copy struct {
	pos Pos
	dst Temp
	src Temp
}

// NewCopy moves src into dst.
func NewCopy(pos Pos, dst, src Temp) *Copy {
	requirePos(pos, "NewCopy")
	if dst == NoTemp || src == NoTemp {
		panic("ir: NewCopy: a copy needs both a source and a destination")
	}
	return &Copy{pos: pos, dst: dst, src: src}
}

// Src is the temporary read.
func (c *Copy) Src() Temp { return c.src }

func (c *Copy) Pos() Pos                     { return c.pos }
func (c *Copy) Dst() Temp                    { return c.dst }
func (c *Copy) AppendUses(dst []Temp) []Temp { return append(dst, c.src) }
func (c *Copy) String() string               { return c.dst.String() + " = " + c.src.String() }
func (c *Copy) irNode()                      {}
func (c *Copy) irInstr()                     {}

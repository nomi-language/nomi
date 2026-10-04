package ir

import "strconv"

// The `proj` class: read one component out of a composite value.
//
// A projection is the operation a construction is the inverse of, and the two
// classes agree about how a composite value is represented: every selector
// below names a component the matching `ir.Make` supplied an OPERAND for, by
// the same identity the construction used — a declaration plus a NAME for a
// nominal field, a name for a record's, an index for a tuple slot or a
// variant payload. Nothing in either class names a Go field, a tag number or
// a storage slot. For a nominal field this is why `NewMakeStruct` takes
// names: the declaration's identity plus an operand order is not enough to
// construct a value keyed by field name. See make.go's header.
//
// Where the representation would leak, and why it does not. A struct field, a
// tuple slot, an interface field requirement and a distinct's inner value can
// each be stored differently: four storage shapes behind one Nomi operation.
// If this node had to know which, the IR/consumer line would be in the wrong
// place.
//
// It does not, because the storage is a function of the KIND and the kind is a
// function of WHAT IS BEING READ rather than of how the consumer stores it.
// `ProjIfaceField` is the sharp case: a consumer may reach it through a table
// keyed on a run-time type identity, having erased the value, or by reading a
// field off a dynamically typed value. How an existential is represented is a
// consumer property, and this class does not decide it.
//
// Element access at a computed position is not in this class. `Vector.at(v, i)` and
// `Map.get(m, k)` are CALLS — `vectorCall` and `mapCall`, in the `call` class
// — and they are calls in the consumer too. What is here is exactly the set of
// reads whose location is fixed by the subject's STATIC TYPE plus a
// COMPILE-TIME CONSTANT, so every one of them has a selector or a constant
// index and none has an index OPERAND.
//
// The constant index matters: a list pattern's head reads element i, where i
// is the pattern's arity and therefore a constant, and that is the same shape
// as a tuple slot. `ProjElem` and `ProjSuffix` below are those reads.
//
// This class needs no mobility predicate, which is the opposite of `ref`.
//
// `ir.Ref` needs `Forces` and `Volatile` because its kinds differ: reading a
// `once` cell runs an initializer, and reading an app field under a `use`
// binding can answer differently at two positions.
//
// A projection runs nothing and its answer is its subject's, at all nine
// kinds. There is no `once` FIELD in Nomi — a `once` is a module binding and
// is `RefOnce` — and there is no field a `use` binding rebinds, because a
// `use` binding rebinds an app field, which is also a `Ref`. So both of
// `Ref`'s predicates are constantly false here and their conjunction is
// constantly true, and a consumer gets the answer by asking the SUBJECT rather
// than by asking this node. `internal/irbuild` does exactly that.
//
// The one predicate that is not constant is Faults, and it is about delivery
// rather than mobility: see ProjEnumField.
//
// Two kinds are valid only after a test, and they are not a new case.
// `ProjElem` and `ProjSuffix` read past the front of a list, which is in
// bounds only because an `ir.Match` list-length test already passed;
// `ProjPayload` has the same property, where the tag is
// established by a pattern match, a `try` or an attach. The IR expresses the
// dependency by ORDER, which is what a linear representation is for.
type ProjKind uint8

const (
	// ProjField reads a declared field of a struct. `Sym` is the FIELD's
	// declaration identity, not the struct's: two same-named fields of two
	// structs are two declarations, and this class exists to read one of them.
	// Owners: fieldAccess, providerRead.
	ProjField = ProjKind(iota + 1)
	// ProjRecordField reads a field of an anonymous struct BY NAME. A record
	// has no declaration to carry an identity, so the name is the identity —
	// which is the same fact MakeRecord records from the construction side.
	// Owner: anonFieldAccess.
	ProjRecordField
	// ProjSlot reads one component of a tuple by INDEX. Owner: tupleIndex.
	ProjSlot
	// ProjPayload reads one payload of a known enum variant by index. `Sym` is
	// the enum and `Text` the variant: the subject's tag is already known,
	// because a pattern match or a `try` has established it. Owner:
	// payloadValue.
	ProjPayload
	// ProjEnumField reads a field NAME off an enum value WITHOUT knowing the
	// tag. `Sym` is the enum and `Text` the field name.
	//
	// THE ONE PROJECTION WHOSE LOWERING IS NOT A SELECTOR, and the clearest
	// illustration in this class of where the line falls. Nomi says "read
	// field f"; a consumer reads it off whatever the variant's payload is, or
	// expands a switch over the tag with one arm per variant. Same operation,
	// and the expansion is entirely the consumer's.
	//
	// It is also the only projection that can FAULT, which is what Faults
	// reports. Owner: enumFieldRead.
	ProjEnumField
	// ProjIfaceField reads an interface `field` requirement off a value whose
	// concrete type is erased. `Sym` is the INTERFACE and `Text` the
	// requirement's name. Total by construction: the checker obliges every
	// implementor to declare the field. Owner: ifaceFieldRead.
	ProjIfaceField
	// ProjInner reads the value a distinct type wraps — `Int(m)` for a
	// `type M Int`. `Sym` is the distinct's declaration.
	//
	// It is a projection and not a conversion: Nomi has no conversions, and
	// `Int(m)` answers the wrapped value and rejects anything that is not a
	// distinct. Owner: scalarUnwrap.
	ProjInner
	// ProjElem reads the element at a CONSTANT position of a List. Structural:
	// it names no declaration, and `Index` is the position.
	//
	// The position is a constant because the only thing in Nomi that reads a
	// list element at a fixed position is a LIST PATTERN, whose heads are
	// counted by the pattern itself. An element read at a computed position is
	// `List.at`, a call. Owner: listArm.
	ProjElem
	// ProjSuffix reads the List of everything from a CONSTANT position on —
	// the `..rest` of `[a, b, ..rest]`, with `Index` the number of heads
	// before it.
	//
	// A SUFFIX AND NOT A COPY: the consumer reads the `Index`'th tail of the
	// list, so `[h, ..t]` allocates nothing. That is a property of the
	// operation rather than of one representation, and it is why this is a
	// projection and not `MakeList` over a drop.
	// Owner: listArm.
	ProjSuffix
)

func (k ProjKind) String() string {
	switch k {
	case ProjField:
		return "field"
	case ProjRecordField:
		return "recordfield"
	case ProjSlot:
		return "slot"
	case ProjPayload:
		return "payload"
	case ProjEnumField:
		return "enumfield"
	case ProjIfaceField:
		return "ifacefield"
	case ProjInner:
		return "inner"
	case ProjElem:
		return "elem"
	case ProjSuffix:
		return "suffix"
	}
	return "proj?"
}

// Proj reads one component out of a composite value.
type Proj struct {
	pos    Pos
	dst    Temp
	subj   Temp
	kind   ProjKind
	sym    *Symbol
	text   string
	idx    int
	faults bool
	shape  ValShape
	// field names a struct-shaped variant's payload; empty for a positional
	// payload.
	field string
	// embeds is the embedded type of an `embeds` variant's payload; nil
	// otherwise.
	embeds *Symbol
}

// NewProjField reads a declared struct field. field is the FIELD's identity.
func NewProjField(pos Pos, dst, subj Temp, field *Symbol, name string, shape ValShape) *Proj {
	p := newProj(pos, dst, subj, ProjField, shape, "NewProjField")
	p.sym = requireProjSym(field, "NewProjField")
	p.text = name
	return p
}

// NewProjRecordField reads an anonymous struct's field by name.
func NewProjRecordField(pos Pos, dst, subj Temp, name string, shape ValShape) *Proj {
	if name == "" {
		panic("ir.NewProjRecordField: a record's field name IS its identity and cannot be empty")
	}
	p := newProj(pos, dst, subj, ProjRecordField, shape, "NewProjRecordField")
	p.text = name
	return p
}

// NewProjSlot reads a tuple component by index.
func NewProjSlot(pos Pos, dst, subj Temp, i int, shape ValShape) *Proj {
	if i < 0 {
		panic("ir.NewProjSlot: negative component index " + strconv.Itoa(i))
	}
	p := newProj(pos, dst, subj, ProjSlot, shape, "NewProjSlot")
	p.idx = i
	return p
}

// NewProjPayload reads payload i of a known variant.
func NewProjPayload(pos Pos, dst, subj Temp, enum *Symbol, variant string, i int, shape ValShape) *Proj {
	if variant == "" {
		panic("ir.NewProjPayload: a payload with no variant names nothing")
	}
	if i < 0 {
		panic("ir.NewProjPayload: negative payload index " + strconv.Itoa(i))
	}
	p := newProj(pos, dst, subj, ProjPayload, shape, "NewProjPayload")
	p.sym = requireProjSym(enum, "NewProjPayload")
	p.text = variant
	p.idx = i
	return p
}

// NewProjPayloadField reads the named payload i of a struct-shaped variant.
func NewProjPayloadField(pos Pos, dst, subj Temp, enum *Symbol, variant, field string, i int, shape ValShape) *Proj {
	if field == "" {
		panic("ir.NewProjPayloadField: a struct-shaped payload read names its field")
	}
	p := NewProjPayload(pos, dst, subj, enum, variant, i, shape)
	p.field = field
	return p
}

// NewProjPayloadEmbed reads the embedded value of an `embeds` variant. The VM
// holds the embedded value itself, which is then the payload.
func NewProjPayloadEmbed(pos Pos, dst, subj Temp, enum *Symbol, variant string, embedded *Symbol, shape ValShape) *Proj {
	p := NewProjPayload(pos, dst, subj, enum, variant, 0, shape)
	p.embeds = requireProjSym(embedded, "NewProjPayloadEmbed")
	return p
}

// PayloadEmbeds is the embedded type an `embeds` payload read names, or nil.
func (p *Proj) PayloadEmbeds() *Symbol { return p.embeds }

// PayloadField is the field a struct-shaped variant's payload read names, or
// empty for a positional payload.
func (p *Proj) PayloadField() string { return p.field }

// NewProjEnumField reads a field name off an enum value whose tag is not
// known. faults says whether some variant of this enum does NOT supply the
// name, which is a fact about the enum's declaration and not about this read.
func NewProjEnumField(pos Pos, dst, subj Temp, enum *Symbol, field string, faults bool, shape ValShape) *Proj {
	if field == "" {
		panic("ir.NewProjEnumField: a field read with no name reads nothing")
	}
	p := newProj(pos, dst, subj, ProjEnumField, shape, "NewProjEnumField")
	p.sym = requireProjSym(enum, "NewProjEnumField")
	p.text = field
	p.faults = faults
	return p
}

// NewProjIfaceField reads an interface `field` requirement off an erased value.
func NewProjIfaceField(pos Pos, dst, subj Temp, iface *Symbol, field string, shape ValShape) *Proj {
	if field == "" {
		panic("ir.NewProjIfaceField: a requirement with no name names nothing")
	}
	p := newProj(pos, dst, subj, ProjIfaceField, shape, "NewProjIfaceField")
	p.sym = requireProjSym(iface, "NewProjIfaceField")
	p.text = field
	return p
}

// NewProjInner reads the value a distinct type wraps.
func NewProjInner(pos Pos, dst, subj Temp, typ *Symbol, shape ValShape) *Proj {
	p := newProj(pos, dst, subj, ProjInner, shape, "NewProjInner")
	p.sym = requireProjSym(typ, "NewProjInner")
	return p
}

// NewProjElem reads the element at constant position i of a List.
func NewProjElem(pos Pos, dst, subj Temp, i int, shape ValShape) *Proj {
	if i < 0 {
		panic("ir.NewProjElem: negative element position " + strconv.Itoa(i))
	}
	p := newProj(pos, dst, subj, ProjElem, shape, "NewProjElem")
	p.idx = i
	return p
}

// NewProjSuffix reads the List of everything from constant position i on.
//
// i may be zero: `[..rest]` binds the whole list, and that is a real pattern
// rather than a degenerate one — unlike `ir.MatchListMin(0)`, whose question
// every list answers and which is therefore refused at construction.
//
// THE ONE FACTORY WHOSE SHAPE IS CHECKED RATHER THAN TAKEN ON TRUST, and the
// reason is that this is the one kind whose result shape is a function of the
// KIND alone: a suffix of a List is a List, whatever the element type is.
// Every other kind's result is the component's, which nothing here can see.
// `ValUnknown` is still admitted, because a producer that declines to state a
// shape is the rule `ir.Param` establishes and not a hole.
func NewProjSuffix(pos Pos, dst, subj Temp, i int, shape ValShape) *Proj {
	if i < 0 {
		panic("ir.NewProjSuffix: negative suffix position " + strconv.Itoa(i))
	}
	if shape != ValUnknown && shape != ValContainer {
		panic("ir.NewProjSuffix: a List's suffix is a List, not " + shape.String())
	}
	p := newProj(pos, dst, subj, ProjSuffix, shape, "NewProjSuffix")
	p.idx = i
	return p
}

func newProj(pos Pos, dst, subj Temp, kind ProjKind, shape ValShape, who string) *Proj {
	requirePos(pos, who)
	if dst == NoTemp {
		panic("ir." + who + ": a projection with no destination reads nothing")
	}
	if subj == NoTemp {
		panic("ir." + who + ": a projection with no subject has nothing to read from")
	}
	return &Proj{pos: pos, dst: dst, subj: subj, kind: kind, shape: shape}
}

func requireProjSym(s *Symbol, who string) *Symbol {
	if s == nil {
		panic("ir." + who + ": this projection names a declaration and needs its identity")
	}
	return s
}

// Kind says which projection this is.
func (p *Proj) Kind() ProjKind { return p.kind }

// Subject is the composite value being read.
func (p *Proj) Subject() Temp { return p.subj }

// Sym is the declaration this read names: a field, an enum, an interface or a
// distinct type, per the kind. Nil for ProjRecordField, ProjSlot, ProjElem and
// ProjSuffix, which are structural and name no declaration.
func (p *Proj) Sym() *Symbol { return p.sym }

// Name is the field or requirement name, or the variant a payload belongs to.
// Empty for ProjSlot, ProjInner, ProjElem and ProjSuffix.
func (p *Proj) Name() string { return p.text }

// Index is the tuple component, the payload position, a list element's
// position or a suffix's start. Zero for the named kinds, which select by
// name.
func (p *Proj) Index() int { return p.idx }

// Shape is the value shape this read ANSWERS WITH, or `ValUnknown` where the
// producer declined to state one.
//
// # One of the two stored facts in shape.go
//
// The subject's shape is derivable and the result's is not, and that
// asymmetry is the whole reason this field exists. `RuleOperandShape` already
// checks the SUBJECT — `shapeOfTemp` reads whatever `ir.Make` or parameter
// declaration wrote it — and the field below is about the DESTINATION: what a
// consumer holds after the read. A struct's field type, a tuple slot's, a
// payload's, a distinct's inner type; none of them is anywhere in this graph.
// `Sym()` is the FIELD's identity and "a name plus an identity and NOTHING
// ELSE", so a reader cannot get a type out of it.
//
// Deriving it from the subject's own `ir.Make` plus the selector would need
// no field, but it would answer almost none of the positions: in the
// retained population the subject is almost always a PARAMETER or another
// projection, so there is no construction in the function to read the
// component off. Recording it answers every
// projection result.
//
// `ValUnknown` is an answer and not a hole, exactly as it is on `ir.Param`. A
// producer that cannot say states it and `RuleOperandShape` is then silent at
// that position. A WRONG shape is the failure mode, and it is reported rather
// than believed: a recorded shape meets the position's own demand in
// `shapeOfTemp`, so the two either agree or contradict.
func (p *Proj) Shape() ValShape { return p.shape }

// Faults reports whether this read can fail at run time.
//
// TRUE ONLY FOR ProjEnumField, and only when some variant of the enum does not
// supply the name — which is a property of the DECLARATION, so a producer
// states it and this node does not derive it. Every other projection's
// location is fixed by the subject's static type and is therefore total.
//
// It is not a mobility predicate and does not pair with `ir.Ref.Forces`. A
// forcing read RUNS an initializer whose effect a consumer must order; a
// faulting read runs nothing and either answers or aborts. What a consumer
// does with it is deliver the fault in the arms that cannot answer.
func (p *Proj) Faults() bool { return p.faults }

func (p *Proj) Pos() Pos  { return p.pos }
func (p *Proj) Dst() Temp { return p.dst }

// AppendUses appends the subject, which is the one temporary a projection
// reads. An index is part of the operation and not an operand: Nomi has no
// projection at a computed position.
func (p *Proj) AppendUses(dst []Temp) []Temp { return append(dst, p.subj) }

func (p *Proj) String() string {
	s := p.dst.String() + " = " + p.kind.String() + " " + p.subj.String()
	switch p.kind {
	case ProjSlot, ProjElem, ProjSuffix:
		s += "." + strconv.Itoa(p.idx)
	case ProjPayload:
		s += " " + p.sym.Name() + "." + p.text + "[" + strconv.Itoa(p.idx) + "]"
		if p.embeds != nil {
			s += " embeds " + p.embeds.Name()
		}
	case ProjInner:
		s += " " + p.sym.Name()
	default:
		s += "." + p.text
	}
	if p.faults {
		s += " faults"
	}
	return s
}

func (p *Proj) irNode()  {}
func (p *Proj) irInstr() {}

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
// tuple slot and a distinct's inner value can each be stored differently:
// three storage shapes behind one Nomi operation.
// If this node had to know which, the IR/consumer line would be in the wrong
// place.
//
// It does not, because the storage is a function of the KIND and the kind is a
// function of WHAT IS BEING READ rather than of how the consumer stores it.
//
// Element access at a computed position is not in this class. `Vector.get(v, i)` and
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
// established by a pattern match, a `?` or an attach. The IR expresses the
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
	// because a pattern match or a `?` has established it. Owner:
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
	// ProjInner reads the value a distinct type wraps — `n` in `M(n) = m`
	// for a `type M Int`. `Sym` is the distinct's declaration.
	//
	// It is a projection and not a conversion: Nomi has no conversions.
	// Owner: distinctProjection.
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
	// before it. A negative `Index` counts from the end: the List of the
	// last -Index elements, which a list pattern's tails are read from
	// (`[first, .., last]`). That suffix is found by one walk, since every
	// cell knows its length.
	//
	// A SUFFIX AND NOT A COPY: the consumer reads the `Index`'th tail of the
	// list, so `[h, ..t]` allocates nothing. That is a property of the
	// operation rather than of one representation, and it is why this is a
	// projection and not `MakeList` over a drop.
	// Owner: listArm.
	ProjSuffix
	// ProjStrSuffix reads the String of a String's bytes from a CONSTANT byte
	// offset on: the `rest` of a string prefix pattern `"/users/" + rest`,
	// with `Index` the prefix literal's length in bytes. It is in bounds only
	// after the MatchStrPrefix test of that literal passed, so the offset
	// falls on the boundary between two whole UTF-8 sequences and the result
	// is a valid String. Owner: strPrefixTest.
	ProjStrSuffix
	// ProjVectorElem reads the element of a Vector at a CONSTANT position:
	// `Index` counts from the front when it is zero or more, and from the end
	// when it is negative, -1 being the last element. A vector pattern's
	// heads read from the front and its tails, after a `..`, from the end
	// (`#[first, .., last]`). It is in bounds only after a MatchVectorLen or
	// MatchVectorMin test passed. Owner: vectorCaseTest.
	ProjVectorElem
	// ProjVectorSlice reads the Vector of a Vector's elements from position
	// `Index` up to `Drop` elements before its end: the `..rest` of
	// `#[a, ..rest, z]`, with `Index` the heads and `Drop` the tails.
	//
	// A WINDOW AND NOT A COPY: the result shares the subject's storage, so
	// a recursive `#[x, ..rest]` walk is linear (rt.VectorSlice). Owner:
	// vectorCaseTest.
	ProjVectorSlice
	// ProjListSlice reads the List of a List's elements from position
	// `Index` up to `Drop` elements before its end: the `..mid` of
	// `[a, ..mid, z]`, with `Index` the heads and `Drop` the tails.
	//
	// A COPY, unlike ProjSuffix: the elements before the last `Drop` cells
	// are new cells, made in one walk (rt.ListCellSlice). It is in bounds
	// only after a MatchListMin test of `Index`+`Drop` passed. Owner:
	// listCaseTest.
	ProjListSlice
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
	case ProjInner:
		return "inner"
	case ProjElem:
		return "elem"
	case ProjSuffix:
		return "suffix"
	case ProjStrSuffix:
		return "strsuffix"
	case ProjVectorElem:
		return "vectorelem"
	case ProjVectorSlice:
		return "vectorslice"
	case ProjListSlice:
		return "listslice"
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
	// drop is how many elements a ProjVectorSlice or a ProjListSlice leaves
	// off the end; zero for every other kind.
	drop int
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

// NewProjSuffix reads the List of everything from constant position i on,
// or, for a negative i, the List of the last -i elements.
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
	if shape != ValUnknown && shape != ValContainer {
		panic("ir.NewProjSuffix: a List's suffix is a List, not " + shape.String())
	}
	p := newProj(pos, dst, subj, ProjSuffix, shape, "NewProjSuffix")
	p.idx = i
	return p
}

// NewProjVectorElem reads the element of a Vector at constant position i,
// counted from the end when i is negative (-1 is the last element).
func NewProjVectorElem(pos Pos, dst, subj Temp, i int, shape ValShape) *Proj {
	p := newProj(pos, dst, subj, ProjVectorElem, shape, "NewProjVectorElem")
	p.idx = i
	return p
}

// NewProjVectorSlice reads the Vector of subj's elements from position from
// up to drop elements before its end. Its result is a Vector, so, as for
// NewProjSuffix, the shape is checked.
func NewProjVectorSlice(pos Pos, dst, subj Temp, from, drop int, shape ValShape) *Proj {
	if from < 0 || drop < 0 {
		panic("ir.NewProjVectorSlice: negative bound " + strconv.Itoa(from) + ", " + strconv.Itoa(drop))
	}
	if shape != ValUnknown && shape != ValContainer {
		panic("ir.NewProjVectorSlice: a Vector's slice is a Vector, not " + shape.String())
	}
	p := newProj(pos, dst, subj, ProjVectorSlice, shape, "NewProjVectorSlice")
	p.idx = from
	p.drop = drop
	return p
}

// NewProjListSlice reads the List of subj's elements from position from up
// to drop elements before its end, copied. Its result is a List, so, as for
// NewProjSuffix, the shape is checked.
func NewProjListSlice(pos Pos, dst, subj Temp, from, drop int, shape ValShape) *Proj {
	if from < 0 || drop < 0 {
		panic("ir.NewProjListSlice: negative bound " + strconv.Itoa(from) + ", " + strconv.Itoa(drop))
	}
	if shape != ValUnknown && shape != ValContainer {
		panic("ir.NewProjListSlice: a List's slice is a List, not " + shape.String())
	}
	p := newProj(pos, dst, subj, ProjListSlice, shape, "NewProjListSlice")
	p.idx = from
	p.drop = drop
	return p
}

// Drop is how many elements a ProjVectorSlice or a ProjListSlice leaves off
// the end of its subject.
func (p *Proj) Drop() int { return p.drop }

// NewProjStrSuffix reads the String of subj's bytes from byte offset i on.
// Its result is a String, so the shape is not taken from the producer.
func NewProjStrSuffix(pos Pos, dst, subj Temp, i int) *Proj {
	if i < 0 {
		panic("ir.NewProjStrSuffix: negative byte offset " + strconv.Itoa(i))
	}
	p := newProj(pos, dst, subj, ProjStrSuffix, ValString, "NewProjStrSuffix")
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
// distinct type, per the kind. Nil for ProjRecordField, ProjSlot, ProjElem,
// ProjSuffix and ProjStrSuffix, which are structural and name no declaration.
func (p *Proj) Sym() *Symbol { return p.sym }

// Name is the field or requirement name, or the variant a payload belongs to.
// Empty for ProjSlot, ProjInner, ProjElem, ProjSuffix and ProjStrSuffix.
func (p *Proj) Name() string { return p.text }

// Index is the tuple component, the payload position, a list element's
// position, a suffix's start (from the end when negative), a slice's start,
// or a string suffix's starting byte. Zero for the named kinds, which select by
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
	case ProjSlot, ProjElem, ProjSuffix, ProjStrSuffix, ProjVectorElem:
		s += "." + strconv.Itoa(p.idx)
	case ProjVectorSlice, ProjListSlice:
		s += "." + strconv.Itoa(p.idx) + ".." + strconv.Itoa(p.drop)
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

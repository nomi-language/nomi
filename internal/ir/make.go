package ir

import "strconv"

// The `make` class: build a composite value out of operands.
//
// What a construction is here. The package's general line is: the operation,
// its operand domain and its position are in the IR; how the consumer
// represents the value is not. For a constant that cut falls between the
// value and its spelling. For a construction it falls between what is being
// built and from what, and how the built value is laid out:
//
//	moves into the IR                      stays in the consumer
//	------------------------------------   -----------------------------------
//	which construction this is (MakeKind)  `NomiT_Point{F_x: …, F_y: …}`
//	the identity of the declaration a      a variant's TAG NUMBER and which
//	  struct, enum or distinct names         SLOT each payload occupies
//	which variant of that enum             a boxed field's `&bx1` indirection
//	the OPERANDS, and their ORDER          `rt.Cons` nesting against a
//	a struct's and a record's field           variadic `rt.ListOf`
//	  NAMES                                `rt.SetOf`'s `hash, eq` pair
//	whether a range's end is inclusive     `(*rt.List[T])(nil)` for an empty
//	the position                             tail
//
// The test of the line: two consumers could disagree about the right-hand
// column and could not disagree about the left.
//
// A struct's field names are on the node. `proj.go`'s header says the two
// classes are inverses: every selector names a component the matching
// `ir.Make` supplied an operand for, by the same identity the construction
// used. `NewProjField` takes a field Symbol and the field's Nomi name, and
// the read side selects by name, so the write side must supply the name too.
// A consumer that keys a struct's fields by the Nomi field name, holding only
// the declaration's identity and the operand order, could not build the
// value at all.
//
// So the names are on the node, exactly as a record's are, and the two kinds
// carry one identity. A field name is not qualified and does not need to
// be: fields are keyed by the bare declared name, and the module qualification
// lives on the type name one level up.
//
// AN ENUM LITERAL IS THE CASE THAT PROVES IT. `Shape.Rect{w: 4.0}` could be
// laid out as a tagged struct — one integer tag field plus one slot per
// payload, with zero-sized payloads given no slot at all and same-typed
// payloads of different variants SHARING one — or as a record carrying a
// variant name and its payload, with no tag, no slots and no sharing. Neither
// arrangement is in this node. What is in it is the enum's identity, the
// variant's name and the payload values, which is everything a consumer needs
// and nothing it invents.
//
// A CONTAINER LITERAL IS THE SECOND CASE. `[a, b]` can be a right-folded chain
// of cons cells or a list built over a slice. Same operation, same operands,
// two representations, and the choice between them is argued in the consumer
// that makes it.
//
// The element type of a container is not on this node. A consumer derives it
// from the operands. `ir.Const`'s three empty containers have no operands to
// derive it from and answer with nil plus `Undischarged`, because for them the
// absent fact is the field.
//
// There is no mobility predicate on this node. `ir.Ref` needs two — `Forces` and `Volatile` — because
// reading a `once` cell RUNS something and reading an app field under a `use`
// binding can ANSWER DIFFERENTLY. A construction does neither: it runs
// nothing, and its answer is a function of its operands alone, for every kind
// below. So the predicate a consumer would ask is constantly true, and a
// constant field carries no information.
//
// What the builder's own `expr.pure` flag carries at a construction site is a
// different question and it stays there: `internal/irbuild`'s `consChain`,
// `setLit`, `vectorLit`, `mapLit` and `rangeLit` all say in as many words that
// re-evaluating a container "builds a second trie" or "copies a second backing
// slice" and that "the value compares equal, so nothing observes the
// difference". That is a COST, not an observation, and a cost is the
// consumer's. See internal/irbuild/irmake.go, which records the two places the
// producer's own sites disagree about it.
type MakeKind uint8

const (
	// MakeStruct builds a value of a declared struct type. `Typ` is the
	// struct's declaration and the operands are its fields IN DECLARATION
	// ORDER, which is the order defaults are evaluated in.
	// Owner: structValue.
	MakeStruct MakeKind = iota + 1
	// MakeRecord builds an anonymous struct. It has no declaration, so its
	// field NAMES are part of the type and are carried here; `Names` is
	// parallel to the operands. Owner: anonStructLit.
	MakeRecord
	// MakeVariant builds a value of one variant of a declared enum. `Typ` is
	// the enum and `Text` the variant's name within it.
	//
	// THE OPERANDS ARE THE PAYLOAD VALUES THE PRODUCER HAS, which is not
	// always one per declared payload: a payload whose type has no run-time
	// representation contributes no value to read, and `AppendUses` must be
	// truthful about what is read. Owners: variantValue, variantConstruct.
	MakeVariant
	// MakeDistinct wraps a value in a distinct type. `Typ` is the distinct's
	// declaration and there is exactly one operand, its inner value.
	// Owners: distinctCallNamed, variantConstruct.
	MakeDistinct
	// MakeTuple builds a tuple from its components in order. Structural: it
	// names no declaration. Owners: tupleLit, tupleFlatArg.
	MakeTuple
	// MakeList builds a List from its elements in order, on `Tail` — which is
	// NoTemp for a list literal and the spread operand for `[a, ..rest]`.
	// Owner: consChain.
	MakeList
	// MakeSet builds a Set from its elements in order. Owner: setLit.
	MakeSet
	// MakeVector builds a Vector from its elements in order. Owner: vectorLit.
	MakeVector
	// MakeMap builds a Map. THE OPERANDS ALTERNATE KEY, VALUE, in source
	// order, which is the order they are evaluated in. Owner: mapLit.
	MakeMap
	// MakeRange builds a Range from a start and an end, either of which may be
	// NoTemp — `Range.naturals()` has neither and `Range.from(n)` has only a
	// start. `Inclusive` is the `..=` form and is canonically false when there
	// is no end. Owners: rangeLit, rangeFromCall.
	MakeRange
	// MakeUpdate copies a struct or record and replaces named fields: `{..base,
	// f: v}`. Operand 0 is the base and `Names` is parallel to the remaining
	// operands, in source order. Owner: structSpreadLit.
	MakeUpdate
)

func (k MakeKind) String() string {
	switch k {
	case MakeStruct:
		return "struct"
	case MakeRecord:
		return "record"
	case MakeVariant:
		return "variant"
	case MakeDistinct:
		return "distinct"
	case MakeTuple:
		return "tuple"
	case MakeList:
		return "list"
	case MakeSet:
		return "set"
	case MakeVector:
		return "vector"
	case MakeMap:
		return "map"
	case MakeRange:
		return "range"
	case MakeUpdate:
		return "update"
	}
	return "make?"
}

// Make builds one composite value.
type Make struct {
	pos   Pos
	dst   Temp
	kind  MakeKind
	typ   *Symbol
	text  string
	names []string
	ops   []Temp
	tail  Temp
	incl  bool
	// embeds is the embedded type an `embeds` widening names; nil otherwise.
	embeds *Symbol
}

// NewMakeStruct builds a declared struct from its fields in DECLARATION
// order. names is the Nomi field name each operand is the value of, parallel
// to vals.
//
// The names are required, and the check is not defensive. `NewProjField`
// names the field it reads, so a construction that supplied by position alone
// would give the two classes two identities for one component, and a consumer
// keying fields by name could not build the value. See this file's header.
func NewMakeStruct(pos Pos, dst Temp, typ *Symbol, names []string, vals []Temp) *Make {
	if len(names) != len(vals) {
		panic("ir.NewMakeStruct: " + strconv.Itoa(len(names)) + " name(s) for " +
			strconv.Itoa(len(vals)) + " value(s); a field's name is how a projection " +
			"of it selects, so the two lists are one list")
	}
	m := newMake(pos, dst, MakeStruct, vals, "NewMakeStruct")
	m.typ = requireSym(typ, "NewMakeStruct")
	m.names = append([]string(nil), names...)
	return m
}

// NewMakeRecord builds an anonymous struct. names is parallel to vals and is
// part of the type rather than a spelling: two records differing only in a
// field name are two types.
func NewMakeRecord(pos Pos, dst Temp, names []string, vals []Temp) *Make {
	if len(names) != len(vals) {
		panic("ir.NewMakeRecord: " + strconv.Itoa(len(names)) + " name(s) for " +
			strconv.Itoa(len(vals)) + " value(s); a record's names are its type")
	}
	m := newMake(pos, dst, MakeRecord, vals, "NewMakeRecord")
	m.names = append([]string(nil), names...)
	return m
}

// NewMakeVariant builds one variant of an enum. payloads are the values the
// producer has; see MakeVariant.
func NewMakeVariant(pos Pos, dst Temp, enum *Symbol, variant string, payloads []Temp) *Make {
	if variant == "" {
		panic("ir.NewMakeVariant: a variant with no name names nothing")
	}
	m := newMake(pos, dst, MakeVariant, payloads, "NewMakeVariant")
	m.typ = requireSym(enum, "NewMakeVariant")
	m.text = variant
	return m
}

// NewMakeVariantFields builds a struct-shaped variant. names are the payload
// field names, parallel to payloads and in declaration order; a consumer that
// keeps payloads by name reads them from Names.
func NewMakeVariantFields(pos Pos, dst Temp, enum *Symbol, variant string, names []string, payloads []Temp) *Make {
	if len(names) == 0 || len(names) != len(payloads) {
		panic("ir.NewMakeVariantFields: a struct-shaped variant names each of its payloads")
	}
	m := NewMakeVariant(pos, dst, enum, variant, payloads)
	m.names = append([]string(nil), names...)
	return m
}

// NewMakeVariantEmbed widens a value of the embedded type into the enum's
// `embeds` variant. payload is the embedded value, or NoTemp for a zero-sized
// embedded type, which has no storage to read. The VM answers the embedded
// value itself: a widening does nothing to the value.
func NewMakeVariantEmbed(pos Pos, dst Temp, enum *Symbol, variant string, embedded *Symbol, payload Temp) *Make {
	var payloads []Temp
	if payload != NoTemp {
		payloads = []Temp{payload}
	}
	m := NewMakeVariant(pos, dst, enum, variant, payloads)
	m.embeds = requireSym(embedded, "NewMakeVariantEmbed")
	return m
}

// NewMakeUpdate copies base and replaces the named fields with vals, in
// source order. names is parallel to vals.
func NewMakeUpdate(pos Pos, dst Temp, base Temp, names []string, vals []Temp) *Make {
	if base == NoTemp || len(names) == 0 || len(names) != len(vals) {
		panic("ir.NewMakeUpdate: an update reads a base and names each replaced field")
	}
	m := newMake(pos, dst, MakeUpdate, append([]Temp{base}, vals...), "NewMakeUpdate")
	m.names = append([]string(nil), names...)
	return m
}

// NewMakeDistinct wraps inner in a distinct type.
func NewMakeDistinct(pos Pos, dst Temp, typ *Symbol, inner Temp) *Make {
	m := newMake(pos, dst, MakeDistinct, []Temp{inner}, "NewMakeDistinct")
	m.typ = requireSym(typ, "NewMakeDistinct")
	return m
}

// NewMakeTuple builds a tuple from its components in order.
func NewMakeTuple(pos Pos, dst Temp, parts []Temp) *Make {
	if len(parts) < 2 {
		panic("ir.NewMakeTuple: " + strconv.Itoa(len(parts)) +
			" component(s); a tuple has at least two")
	}
	return newMake(pos, dst, MakeTuple, parts, "NewMakeTuple")
}

// NewMakeList builds a List from elems, consed onto tail. tail is NoTemp for a
// plain literal and the spread operand for `[a, ..rest]`.
func NewMakeList(pos Pos, dst Temp, elems []Temp, tail Temp) *Make {
	m := newMake(pos, dst, MakeList, elems, "NewMakeList")
	m.tail = tail
	return m
}

// NewMakeSet builds a Set from its elements in order.
func NewMakeSet(pos Pos, dst Temp, elems []Temp) *Make {
	return newMake(pos, dst, MakeSet, elems, "NewMakeSet")
}

// NewMakeVector builds a Vector from its elements in order.
func NewMakeVector(pos Pos, dst Temp, elems []Temp) *Make {
	return newMake(pos, dst, MakeVector, elems, "NewMakeVector")
}

// NewMakeMap builds a Map. kvs alternates key, value in source order.
func NewMakeMap(pos Pos, dst Temp, kvs []Temp) *Make {
	if len(kvs)%2 != 0 {
		panic("ir.NewMakeMap: " + strconv.Itoa(len(kvs)) +
			" operand(s); a map literal's operands alternate key, value")
	}
	return newMake(pos, dst, MakeMap, kvs, "NewMakeMap")
}

// NewMakeRange builds a Range. Either endpoint may be NoTemp; inclusive is
// canonically false when there is no end, which is ast.RangeLit's own rule and
// is enforced here so no consumer has to re-establish it.
func NewMakeRange(pos Pos, dst Temp, start, end Temp, inclusive bool) *Make {
	if end == NoTemp && inclusive {
		panic("ir.NewMakeRange: an unbounded range cannot be inclusive")
	}
	m := newMake(pos, dst, MakeRange, nil, "NewMakeRange")
	m.ops = []Temp{start, end}
	m.incl = inclusive
	return m
}

func newMake(pos Pos, dst Temp, kind MakeKind, ops []Temp, who string) *Make {
	requirePos(pos, who)
	if dst == NoTemp {
		panic("ir." + who + ": a construction with no destination builds nothing")
	}
	return &Make{pos: pos, dst: dst, kind: kind, ops: append([]Temp(nil), ops...)}
}

func requireSym(s *Symbol, who string) *Symbol {
	if s == nil {
		panic("ir." + who + ": a construction of a declared type needs that declaration's identity")
	}
	return s
}

// Kind says which construction this is.
func (m *Make) Kind() MakeKind { return m.kind }

// Typ is the declaration this builds a value of: a struct, an enum or a
// distinct type. Nil for the structural kinds, which name no declaration.
func (m *Make) Typ() *Symbol { return m.typ }

// Variant is the name of the enum variant a MakeVariant builds. Empty
// otherwise.
func (m *Make) Variant() string { return m.text }

// Embeds is the embedded type a MakeVariant widens from, or nil when it
// builds the variant from its payloads.
func (m *Make) Embeds() *Symbol { return m.embeds }

// Names are a MakeStruct's or MakeRecord's field names, parallel to its
// operands. Nil for every other kind, whose components are identified by
// position.
//
// A STRUCT'S AND A RECORD'S NAMES ARE THE SAME FACT FOR DIFFERENT REASONS,
// and both are needed. A record HAS no declaration, so its names are its
// type. A struct has one, and its names are still here because a projection
// of a field selects BY NAME (`NewProjField`) and a consumer reading the
// graph has no way to ask the declaration for them. See this file's header.
func (m *Make) Names() []string { return m.names }

// Arity is how many operands this construction reads, NOT counting a list's
// tail.
func (m *Make) Arity() int { return len(m.ops) }

// Operand is the i'th operand. For MakeMap the even indices are keys and the
// odd ones values; for MakeRange index 0 is the start and 1 the end, either of
// which may be NoTemp.
func (m *Make) Operand(i int) Temp { return m.ops[i] }

// Tail is the list a MakeList is consed onto, or NoTemp.
func (m *Make) Tail() Temp { return m.tail }

// Inclusive reports whether a MakeRange includes its end.
func (m *Make) Inclusive() bool { return m.incl }

func (m *Make) Pos() Pos  { return m.pos }
func (m *Make) Dst() Temp { return m.dst }

// AppendUses appends every operand and a list's tail, skipping NoTemp — which
// a range's absent endpoint is, and which names no value.
func (m *Make) AppendUses(dst []Temp) []Temp {
	for _, t := range m.ops {
		if t != NoTemp {
			dst = append(dst, t)
		}
	}
	if m.tail != NoTemp {
		dst = append(dst, m.tail)
	}
	return dst
}

func (m *Make) String() string {
	s := m.dst.String() + " = make " + m.kind.String()
	if m.typ != nil {
		s += " " + m.typ.Name()
	}
	if m.text != "" {
		s += "." + m.text
	}
	for i, t := range m.ops {
		if i == 0 {
			s += " "
		} else {
			s += ", "
		}
		if i < len(m.names) {
			s += m.names[i] + ": "
		}
		s += t.String()
	}
	if m.tail != NoTemp {
		s += " .." + m.tail.String()
	}
	if m.incl {
		s += " inclusive"
	}
	if m.embeds != nil {
		s += " embeds " + m.embeds.Name()
	}
	return s
}

func (m *Make) irNode()  {}
func (m *Make) irInstr() {}

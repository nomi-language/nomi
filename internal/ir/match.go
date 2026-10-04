package ir

import "strconv"

// The `match` class: one REFUTABLE question about one temporary.
//
// What a pattern is here: a pattern is not a node. Nomi's patterns cover a variant with a payload, a
// literal, a struct with named fields, a tuple, a list with a `[h, ..t]` tail,
// a map, a wildcard and a binding that names what it matched — and a pattern
// nests, so a node carrying a whole pattern would be a TREE. This IR is
// linear with explicit temporaries (ir.go, SHAPE), so a pattern lowers to a
// STRAIGHT-LINE SEQUENCE and not to one node:
//
//	`Shape.Circle{radius: 3.0}`
//	    match variant t1 Shape.Circle     <- this class
//	    t2 = payload t1 Shape.Circle[0]   <- proj
//	    t3 = const 3.0                    <- const
//	    match lit t2 t3                   <- this class
//
// The tests are here, the navigation is `proj`, and the names are
// `destructure.go`'s `Bind`. A binding never has to refer to a position
// inside the pattern, because by the time a name is bound the navigation has
// already produced a TEMPORARY for that position. A `Bind` names an operand,
// which is the same answer `ir.Proj` gives for mobility.
//
// WHAT MOVES AND WHAT STAYS:
//
//	moves into the IR                      stays in the consumer
//	------------------------------------   -----------------------------------
//	which question this is (MatchKind)     `(x).Tag == 1` against a name
//	the enum's identity and the VARIANT      comparison on a variant record
//	  name                                 a variant's TAG NUMBER and the
//	the literal, as an OPERAND               name of the tag FIELD
//	a list pattern's element COUNT, and    `==` against `rt.EqDecimal`
//	  whether the count is exact or a      `x != nil && x.Len == 2`
//	  minimum
//	the position                           WHERE THE FALSE ANSWER GOES
//
// The enum case is the one that tests the line. `case s {
// Shape.Circle(r) -> … }` can be an integer comparison against a tag constant
// or a comparison of the variant's NAME. Neither the tag nor the name
// comparison is in this node; what is in it is the enum's identity and the
// variant's name. That is exactly the split make.go records for `make` —
// "the identity of the struct, enum or distinct" moves and "a variant's TAG
// NUMBER" stays — and a match on an enum inherits it unchanged.
//
// A Match writes at most one temporary. Literal, variant and list-length tests
// optionally write a Boolean answer that an explicit Branch can consume. The VM evaluates
// that answer. Non-answering tests leave control delivery to the surrounding lowering.
// In either form, the kind and operands describe the question, while the
// consumer owns its spelling and the surrounding graph owns the false edge.
// An irrefutable pattern needs only projections and bindings.
//
// EXHAUSTIVENESS IS NOT HERE AND CANNOT BE. The checker decides a `case` is
// exhaustive and rejects it otherwise, and whether a lowering emitted a
// default arm is a fact about that lowering. Nothing below carries an arm
// count, an arm index or a "has default" bit.
//
// This class needs no predicate, for the reason proj.go gives for `proj`:
// naming the operand answers the question a predicate would have
// carried.
//
//   - Does the test RUN anything a consumer must order or must not duplicate?
//     No, at all four kinds. A literal comparison NEVER DISPATCHES — a literal pattern is an Int,
//     Float, Decimal or String, so its subject is always a scalar and there is no `impl Equatable`
//     to reach. An equality that may dispatch is not a shape the language
//     has here.
//   - Can the test ITSELF fail, the way `ir.Proj.Faults` reports? No. The
//     literal is an OPERAND, so its fault and its effects are its own node's.
//   - Can this test ever be UNCONDITIONALLY true? Then no node is built. A
//     tuple pattern's arity is fixed by its type, a record pattern's field set
//     is fixed by its type, a distinct pattern names the only type its subject
//     can have, and a single-variant enum has no other variant — so those four
//     irrefutable patterns produce navigation and names without a `Match`. A predicate
//     for it would be constantly false, and a constant field carries no
//     information.
type MatchKind uint8

const (
	// MatchLit asks whether the subject equals a literal value. `Arg` is the
	// literal, as an operand: a literal is its own instruction writing a
	// temporary, which is ir.go's rule for every operand shape.
	//
	// ONE KIND FOR FOUR LITERAL TYPES. Int, Float, String and Decimal differ
	// in the comparison their consumer runs — `==` for the first three and
	// `rt.EqDecimal` for the last, which compares scale-insensitively — and
	// that is a function of the operand's TYPE, which the consumer already
	// has. Owners: literalArm, decimalArm.
	MatchLit MatchKind = iota + 1
	// MatchVariant asks whether the subject is one named variant of one enum.
	// `Sym` is the ENUM's declaration and `Variant` the variant's name within
	// it.
	//
	// The prelude's `Bool` reaches this kind like any other enum. It is the
	// one enum held as a plain boolean, so its consumer tests the value
	// itself rather than a tag, and it reads that from the SUBJECT's type
	// rather than from a flag here.
	// Owners: enumArm, structArm.
	MatchVariant
	// MatchListLen asks whether the subject is a list of EXACTLY `Arity`
	// elements. This is the no-spread form: `[a, b]` does not match a
	// three-element list. Owner: listArm.
	MatchListLen
	// MatchListMin asks whether the subject is a list of AT LEAST `Arity`
	// elements. This is the spread form `[a, b, ..rest]`, whose length is a
	// minimum.
	//
	// `Arity` is never zero here: `[..rest]` matches every list including the
	// empty one, so the producer builds no node. Owner: listArm.
	MatchListMin
)

func (k MatchKind) String() string {
	switch k {
	case MatchLit:
		return "lit"
	case MatchVariant:
		return "variant"
	case MatchListLen:
		return "listlen"
	case MatchListMin:
		return "listmin"
	}
	return "match?"
}

// Match is one refutable question about one temporary.
type Match struct {
	pos  Pos
	dst  Temp
	subj Temp
	arg  Temp
	kind MatchKind
	sym  *Symbol
	text string
	idx  int
	// embeds is the embedded type of an `embeds` variant a MatchVariant asks
	// for; nil otherwise.
	embeds *Symbol
}

// NewMatchLit asks whether subj equals the literal in lit.
func NewMatchLit(pos Pos, subj, lit Temp) *Match {
	m := newMatch(pos, subj, MatchLit, "NewMatchLit")
	if lit == NoTemp {
		panic("ir.NewMatchLit: a literal test with no literal compares against nothing")
	}
	m.arg = lit
	return m
}

// NewMatchLitInto asks whether subj equals the literal in lit and writes its
// Boolean answer to dst. An explicit Branch can consume that temporary; the
// structured Go reader can fold it into the condition it emits inline.
func NewMatchLitInto(pos Pos, dst, subj, lit Temp) *Match {
	m := NewMatchLit(pos, subj, lit)
	if dst == NoTemp {
		panic("ir.NewMatchLitInto: an answer with no destination is NewMatchLit")
	}
	m.dst = dst
	return m
}

// NewMatchVariant asks whether subj is variant of enum.
func NewMatchVariant(pos Pos, subj Temp, enum *Symbol, variant string) *Match {
	if enum == nil {
		panic("ir.NewMatchVariant: a variant test names an enum and needs its identity")
	}
	if variant == "" {
		panic("ir.NewMatchVariant: a variant test with no variant name asks nothing")
	}
	m := newMatch(pos, subj, MatchVariant, "NewMatchVariant")
	m.sym = enum
	m.text = variant
	return m
}

// NewMatchVariantInto asks the same question as NewMatchVariant and writes its
// Boolean answer to dst for a value-branching consumer.
func NewMatchVariantInto(pos Pos, dst, subj Temp, enum *Symbol, variant string) *Match {
	m := NewMatchVariant(pos, subj, enum, variant)
	if dst == NoTemp {
		panic("ir.NewMatchVariantInto: an answer with no destination is NewMatchVariant")
	}
	m.dst = dst
	return m
}

// NewMatchVariantEmbedInto asks whether subj is the `embeds` variant of enum
// that embeds the type embedded, writing its Boolean answer to dst. A value
// widened into the enum is the embedded value itself in the VM; that value is
// this variant.
func NewMatchVariantEmbedInto(pos Pos, dst, subj Temp, enum *Symbol, variant string, embedded *Symbol) *Match {
	if embedded == nil {
		panic("ir.NewMatchVariantEmbedInto: an embeds test names the embedded type and needs its identity")
	}
	m := NewMatchVariantInto(pos, dst, subj, enum, variant)
	m.embeds = embedded
	return m
}

// NewMatchListLen asks whether subj is a list of exactly n elements.
func NewMatchListLen(pos Pos, subj Temp, n int) *Match {
	if n < 0 {
		panic("ir.NewMatchListLen: negative element count " + strconv.Itoa(n))
	}
	m := newMatch(pos, subj, MatchListLen, "NewMatchListLen")
	m.idx = n
	return m
}

// NewMatchListMin asks whether subj is a list of at least n elements.
//
// n must be positive: a minimum of zero is satisfied by every list, so the
// question is not one and the producer must not build it. See MatchListMin.
func NewMatchListMin(pos Pos, subj Temp, n int) *Match {
	if n < 1 {
		panic("ir.NewMatchListMin: a minimum of " + strconv.Itoa(n) +
			" elements is satisfied by every list; do not build the test")
	}
	m := newMatch(pos, subj, MatchListMin, "NewMatchListMin")
	m.idx = n
	return m
}

// NewMatchListLenInto writes whether subj has exactly n elements.
func NewMatchListLenInto(pos Pos, dst, subj Temp, n int) *Match {
	m := NewMatchListLen(pos, subj, n)
	if dst == NoTemp {
		panic("ir.NewMatchListLenInto: an answer needs a destination")
	}
	m.dst = dst
	return m
}

// NewMatchListMinInto writes whether subj has at least n elements.
func NewMatchListMinInto(pos Pos, dst, subj Temp, n int) *Match {
	m := NewMatchListMin(pos, subj, n)
	if dst == NoTemp {
		panic("ir.NewMatchListMinInto: an answer needs a destination")
	}
	m.dst = dst
	return m
}

func newMatch(pos Pos, subj Temp, kind MatchKind, who string) *Match {
	requirePos(pos, who)
	if subj == NoTemp {
		panic("ir." + who + ": a test with no subject asks nothing")
	}
	return &Match{pos: pos, subj: subj, kind: kind}
}

// Kind says which question this is.
func (m *Match) Kind() MatchKind { return m.kind }

// Subject is the temporary being tested.
func (m *Match) Subject() Temp { return m.subj }

// Arg is the second operand: the literal a MatchLit compares against. NoTemp
// for the three kinds whose question is answered by the subject alone.
func (m *Match) Arg() Temp { return m.arg }

// Sym is the enum a MatchVariant names. Nil for every other kind, none of
// which names a declaration.
func (m *Match) Sym() *Symbol { return m.sym }

// Embeds is the embedded type of the `embeds` variant a MatchVariant asks
// for, or nil.
func (m *Match) Embeds() *Symbol { return m.embeds }

// Variant is the variant name a MatchVariant asks for. Empty for every other
// kind.
func (m *Match) Variant() string { return m.text }

// Arity is the element count the two list kinds ask about — exact for
// MatchListLen, a minimum for MatchListMin. Zero for every other kind.
func (m *Match) Arity() int { return m.idx }

func (m *Match) Pos() Pos { return m.pos }

// Dst is the Boolean answer of an answering test, and NoTemp otherwise.
func (m *Match) Dst() Temp { return m.dst }

// Answers reports whether this test writes its Boolean answer to Dst.
func (m *Match) Answers() bool { return m.dst != NoTemp }

// AppendUses appends the subject and, where there is one, the second operand.
// An arity is part of the question and not an operand: Nomi has no list
// pattern whose length is computed.
func (m *Match) AppendUses(dst []Temp) []Temp {
	dst = append(dst, m.subj)
	if m.arg != NoTemp {
		dst = append(dst, m.arg)
	}
	return dst
}

func (m *Match) String() string {
	s := "match " + m.kind.String() + " " + m.subj.String()
	switch m.kind {
	case MatchLit:
		s += " " + m.arg.String()
	case MatchVariant:
		s += " " + m.sym.Name() + "." + m.text
		if m.embeds != nil {
			s += " embeds " + m.embeds.Name()
		}
	case MatchListLen, MatchListMin:
		s += " " + strconv.Itoa(m.idx)
	}
	if m.Answers() {
		s = m.dst.String() + " = " + s
	}
	return s
}

func (m *Match) irNode()  {}
func (m *Match) irInstr() {}

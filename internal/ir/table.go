package ir

// THE TYPE AND DECLARATION TABLE.
//
// WHAT IT IS FOR, argued from its dependents. Two things need it, and they
// need different halves:
//
//   - `Add.add`. `Score(2) + Score(3)` is an impl call, and WHICH impl is the
//     whole content of the answer: two `impl Add` blocks for one receiver
//     differ only in their right-hand type. Selecting among them is a rule
//     that scores candidate declarations against operand TYPES, so a
//     representation that carries the operator and not the types cannot hold
//     the answer. This is the half built here: Decl, its Signature, and
//     SelectOverload.
//   - The 31-arm `implCall` chain. The front end has already resolved every
//     qualified callee to its declaration with an instantiated signature
//     (`g.fa.References[calleeRefPos(t.Func)]`), and the builder reads exactly
//     that channel in one place for one field. Replacing 31 ordered arms with
//     a resolved callee needs the table to be POPULATED FROM THE FRONT END'S
//     RESOLUTION and keyed so a call site can ask for its own callee. That is
//     a different requirement from selection and it is NOT built here; see
//     Table.Declare's comment for exactly what is missing.
//
// IDENTITY IS THE POINTER, AND THE TABLE IS WHAT MAKES THAT AFFORDABLE. A
// Symbol (ir.go) is a name plus a pointer identity and nothing else, which is
// enough to say two same-named declarations are two declarations and not
// enough to choose between them. A Decl adds the signature, so the choosing
// rule is expressible over the representation instead of over the producer's
// own tables.
//
// WHY THIS IS AN INTERNING ARENA AND NOT ANOTHER SIDE TABLE. A
// `map[*Symbol]*implItem` would leave every fact outside the representation and
// make the token a receipt for work done elsewhere. The maps below go the
// opposite direction, from a PRODUCER IDENTITY to an IR entity, which is what
// interning is, and which `internal/irbuild`'s own `g.comps` already is one
// layer up. The difference is behavioural: the builder's `argsFitLevel` is not
// on the operator path at all, because the scoring rule lives here. A receipt
// design could not have moved it.
//
// What the producer supplies and what the table decides: the fault/delivery
// division of ir.go's package header, applied to types. The producer supplies FACTS: this type
// is concrete, this one is an erased interface, this enum's variants embed
// these types, this type has an impl of that interface. The table decides the
// RULE: Widens is the three-case subtyping relation and SelectOverload is the
// tiered scoring, and neither is a fact any producer states. That is the same
// split as `ir.Arith` carrying "Int arithmetic, faulting" while the builder
// answers "the rt function is rt.AddInt".
//
// Population is lazy, at the site that asks, and that is deliberate. Whether
// `Score` implements `Display` is answered in the builder by `bindsImpl`, a
// query over a registry built during lowering; mirroring it eagerly at
// declaration time would be a second implementation of a question that
// already has one, and the two would have to agree. Supplying it as a fact at
// the site that needs it cannot disagree. The cost is that the table is not a
// complete program-wide index, which dependent 2 above would require.

import "strconv"

// TypeForm is how a Type participates in the subtyping relation.
//
// Two members, not a mirror of the builder's tag set. `internal/irbuild`'s `tag`
// has nineteen members because it also answers "which Go type renders this",
// which is a consumer question. The only thing the relation below needs to
// know is whether a value of this type carries its own concrete type or has
// had it erased.
type TypeForm uint8

const (
	// FormConcrete is a type whose values carry it: a scalar, a struct, an
	// enum, a distinct, a structural container, a bound-free type parameter.
	FormConcrete TypeForm = iota
	// FormExistential is an interface in value position — a type whose
	// concrete type is erased and whose operations go through a dispatch
	// table.
	FormExistential
)

func (f TypeForm) String() string {
	if f == FormExistential {
		return "existential"
	}
	return "concrete"
}

// Type is one Nomi type in a Table. Its identity is the pointer.
//
// Comparing types by printed name is a defect this repository has already had:
// two same-named `Error` declarations compared equal and produced the
// uninterpretable diagnostic `expected Error, got Error`. Name is for display.
type Type struct {
	owner *Table
	name  string
	form  TypeForm
	// embeds are the types this enum's variants embed. An `embeds` variant
	// makes its payload type a SUBTYPE of the enum, which is one of the three
	// cases in Widens.
	embeds []*Type
	// conforms are the existentials this concrete type has a recorded impl
	// for. A recorded fact, supplied by the producer; see the file header on
	// why the relation is not re-derived here.
	conforms map[*Type]bool
	// val is the value type this table type stands for, stated once by the
	// producer when the type is minted. It is what a Slot and a Cell of this
	// type hold; see valtype.go.
	val *ValType
}

// Val is the value type of this table type, or nil when the producer stated
// none.
func (t *Type) Val() *ValType {
	if t == nil {
		return nil
	}
	return t.val
}

// SetVal states the value type of this table type. Once: an interned type is
// one Nomi type, and two answers for it would be a producer bug.
func (t *Type) SetVal(v *ValType) {
	if v == nil {
		panic("ir: Type.SetVal: a nil value type states nothing")
	}
	if t.val != nil && !t.val.Identical(v) {
		panic("ir: Type.SetVal: " + t.name + " is already " + t.val.String() + ", not " + v.String())
	}
	t.val = v
}

// Name is the type's printed name. It is not its identity.
func (t *Type) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}

// Form is how this type participates in the subtyping relation.
func (t *Type) Form() TypeForm { return t.form }

// Existential reports whether this type's concrete type is erased.
func (t *Type) Existential() bool { return t != nil && t.form == FormExistential }

func (t *Type) String() string { return t.Name() }

// Signature is a declaration's parameter types and its result type.
//
// Parameters only, and their COUNT is part of the signature: an arity mismatch
// is the first thing the scoring rule rejects, and a signature that could not
// state its arity would make that a producer's check rather than the rule's.
type Signature struct {
	params []*Type
	result *Type
}

// Params are the declared parameter types in order.
func (s *Signature) Params() []*Type { return s.params }

// Arity is the number of declared parameters.
func (s *Signature) Arity() int { return len(s.params) }

// Result is the declared result type.
func (s *Signature) Result() *Type { return s.result }

// Decl is one resolved declaration in a Table: a function, an impl method, a
// `once` cell. Its identity is the pointer, for Type's reason.
type Decl struct {
	owner *Table
	name  string
	sig   *Signature
}

// Name is the declaration's printed name. It is not its identity.
func (d *Decl) Name() string {
	if d == nil {
		return ""
	}
	return d.name
}

// Sig is the declaration's signature.
func (d *Decl) Sig() *Signature {
	if d == nil {
		return nil
	}
	return d.sig
}

func (d *Decl) String() string { return d.Name() }

// Table owns every Type, Decl and Symbol of one lowering.
//
// ONE PER LOWERING, NOT PROCESS-WIDE, and that is a correctness requirement
// rather than a scoping preference. The producer's identity tokens include
// pointers the builder shares process-wide under a mutex (`hostTypeDefs`,
// `opaqueDefs`, `stdEnumDefs`, `sharedGenStructDefs`), so a process-wide table
// would be a map two lowerings mutate. A per-lowering table needs no lock and
// cannot be the reason two lowerings interfere.
type Table struct {
	types map[any]*Type
	decls map[any]*Decl
	syms  map[any]*Symbol
}

// NewTable is an empty table.
func NewTable() *Table {
	return &Table{types: map[any]*Type{}, decls: map[any]*Decl{}, syms: map[any]*Symbol{}}
}

// Types is how many types this table holds.
func (t *Table) Types() int { return len(t.types) }

// Decls is how many declarations this table holds.
func (t *Table) Decls() int { return len(t.decls) }

// Symbols is how many symbols this table holds.
func (t *Table) Symbols() int { return len(t.syms) }

// Symbol interns the declaration the producer identifies by token as a Symbol:
// a name plus an identity and nothing else.
//
// WHY THIS EXISTS. A `Symbol`'s identity is the POINTER and its name is
// display only, and `NewSymbol` MINTS. Minting alone holds that rule in one
// direction and fails it in the other: two same-named declarations are
// correctly two symbols, but two reads of ONE declaration would also be two
// symbols, which is the receipt-not-identity failure `Table.Declare`'s comment
// names for `Decl`.
//
// A REPEAT CALL RETURNS THE FIRST ENTRY AND IGNORES THE NAME OFFERED, exactly
// as Declare does, so one declaration has one symbol for the life of the
// table. `NewSymbol` stays, for a producer naming something that genuinely has
// no prior identity — a synthesized binding, a test's subject.
func (t *Table) Symbol(token any, name string) *Symbol {
	if token == nil {
		panic("ir: Table.Symbol: a declaration with no producer identity cannot be " +
			"interned; identity is the whole reason this table exists")
	}
	if s := t.syms[token]; s != nil {
		return s
	}
	s := &Symbol{name: name}
	t.syms[token] = s
	return s
}

// Concrete interns the concrete type the producer identifies by token, and
// reports whether this call MINTED it.
//
// `minted` is the producer's cue to declare this type's subtyping edges, and
// it is returned rather than left implicit because declaring them twice is a
// silent duplicate and declaring them never is a silent wrong answer. It also
// terminates the recursion a mutually-embedding pair of enums would otherwise
// be: the entry exists before its edges are read.
//
// The token must be a comparable value the producer uses as an identity. It is
// held opaquely: nothing in this package inspects it, so a second consumer
// supplies its own without this file knowing what either looks like.
func (t *Table) Concrete(token any, name string) (ty *Type, minted bool) {
	return t.intern(token, name, FormConcrete)
}

// Existential interns the erased interface type the producer identifies by
// token, and reports whether this call minted it.
func (t *Table) Existential(token any, name string) (ty *Type, minted bool) {
	return t.intern(token, name, FormExistential)
}

func (t *Table) intern(token any, name string, form TypeForm) (*Type, bool) {
	if token == nil {
		panic("ir: Table.intern: a type with no producer identity cannot be interned; " +
			"identity is the whole reason this table exists")
	}
	if ty := t.types[token]; ty != nil {
		if ty.form != form {
			panic("ir: Table.intern: " + name + " was interned as " + ty.form.String() +
				" and is now " + form.String() + "; one identity cannot be two types")
		}
		return ty, false
	}
	ty := &Type{owner: t, name: name, form: form}
	t.types[token] = ty
	return ty, true
}

// Embeds records that one of enum's variants embeds sub, making sub a subtype
// of enum.
func (t *Table) Embeds(enum, sub *Type) {
	t.require(enum, "Embeds: enum")
	t.require(sub, "Embeds: sub")
	enum.embeds = append(enum.embeds, sub)
}

// Conforms records that concrete has an implementation of the existential
// iface.
func (t *Table) Conforms(concrete, iface *Type) {
	t.require(concrete, "Conforms: concrete")
	t.require(iface, "Conforms: iface")
	if !iface.Existential() {
		panic("ir: Table.Conforms: " + iface.Name() + " is concrete; only an " +
			"existential has implementors")
	}
	if concrete.conforms == nil {
		concrete.conforms = map[*Type]bool{}
	}
	concrete.conforms[iface] = true
}

// Declare interns the declaration the producer identifies by token, with this
// signature.
//
// A REPEAT CALL RETURNS THE FIRST ENTRY AND IGNORES THE SIGNATURE OFFERED, so
// one declaration has one signature for the life of the table. That is what
// makes the entry an identity rather than a receipt: two candidate lists
// naming the same impl function get the same pointer, which is the property
// SelectOverload's ambiguity answer rests on.
//
// WHAT DEPENDENT 2 NEEDS AND THIS DOES NOT HAVE. `implCall`'s 31 arms would be
// replaced by asking the table for the declaration the FRONT END resolved a
// callee to. That needs the token to be something a call site can compute
// without already knowing the answer — the analyzer's resolved symbol, or its
// declaration node — and it needs every declaration in the program to be
// present rather than only those some site has already asked about. Both are
// population properties, not shape properties, so work on them would extend
// the shape here rather than replace it.
func (t *Table) Declare(token any, name string, params []*Type, result *Type) *Decl {
	if token == nil {
		panic("ir: Table.Declare: a declaration with no producer identity cannot be " +
			"interned; comparing declarations by printed name is the defect this avoids")
	}
	if d := t.decls[token]; d != nil {
		return d
	}
	sig := &Signature{result: result}
	if len(params) > 0 {
		sig.params = append(make([]*Type, 0, len(params)), params...)
	}
	d := &Decl{owner: t, name: name, sig: sig}
	t.decls[token] = d
	return d
}

// Widens reports whether a value of type have is accepted where want is
// declared.
//
// THREE CASES AND NO MORE, and the reason it is three is Nomi's, not this
// package's. A value is accepted at a declared type when it IS that type; when
// the declared type is an interface it implements, which erases it; or when the
// declared type is an enum one of whose variants embeds it, which widens it
// into the enum. `embeds` is Nomi's subtyping and erasure is its existential
// representation, and there is no third source of subsumption in the language.
//
// AN EXISTENTIAL IS NEVER RE-ERASED. `have` being existential fails the second
// case even where the recorded conformance would allow it, because a value
// already erased into one interface is not a concrete value to erase into
// another. That is the builder's `have.tag != tagIface` guard, and it is a rule
// rather than an implementation detail: erasing twice would put a dispatch
// table inside a dispatch table with nothing to recover the concrete type.
func (t *Table) Widens(want, have *Type) bool {
	if want == nil || have == nil {
		return false
	}
	t.require(want, "Widens: want")
	t.require(have, "Widens: have")
	if want == have {
		return true
	}
	if want.Existential() {
		return !have.Existential() && have.conforms[want]
	}
	for _, sub := range want.embeds {
		if sub == have {
			return true
		}
	}
	return false
}

// Fit is how well a declaration's parameters accept an argument list.
//
// Ordered, so two candidates compare with `<` and `>` rather than through a
// precedence table written out by hand. The one bit FitWidened adds over
// FitExact is what separates an impl whose parameter is the argument's own type
// from one whose parameter merely accepts it.
type Fit uint8

const (
	// FitNone: the arity is wrong, or at least one argument is not accepted at
	// all. Not a candidate.
	FitNone Fit = iota
	// FitWidened: every argument is accepted, at least one of them only
	// through Widens.
	FitWidened
	// FitExact: every argument's type IS its declared parameter, so the
	// candidate needs no widening.
	FitExact
)

func (f Fit) String() string {
	switch f {
	case FitWidened:
		return "widened"
	case FitExact:
		return "exact"
	}
	return "none"
}

// Accepts scores one declaration against an argument list.
//
// A nil argument type is FitNone rather than a panic. That is the shape a
// producer whose own type inference failed for one operand arrives in, and a
// candidate filter is the wrong place to turn it into a crash: the producer has
// already recorded that refusal and the right answer here is "not a candidate".
//
// ONE LOOP, not an exact pass followed by a widening pass. Two passes would be
// two definitions of "accepted" and the second would drift from the first.
func (t *Table) Accepts(d *Decl, args []*Type) Fit {
	if d == nil || d.sig == nil || len(args) != len(d.sig.params) {
		return FitNone
	}
	out := FitExact
	for i, a := range args {
		p := d.sig.params[i]
		if a != nil && a == p {
			continue
		}
		if !t.Widens(p, a) {
			return FitNone
		}
		out = FitWidened
	}
	return out
}

// SelectOverload picks the candidate whose parameters best accept args, and
// reports AMBIGUITY rather than picking between equals.
//
// The tier is the whole rule: a strictly better fit discards every rival found
// so far, and two candidates at one tier is an ambiguity the producer must
// refuse. A refusal is never a wrong answer and a silent pick between equals
// is, which is why this returns the ambiguity instead of the first or the last.
//
// ORDER-INDEPENDENT, which is worth stating because the loop reads as though it
// depended on the order: the answer is the highest tier reached and whether
// more than one candidate reached it, and neither is a function of the order
// the candidates arrive in.
func (t *Table) SelectOverload(cands []*Decl, args []*Type) (chosen *Decl, ambiguous bool) {
	best := FitNone
	for _, d := range cands {
		if d == nil {
			continue
		}
		t.requireDecl(d)
		fit := t.Accepts(d, args)
		switch {
		case fit == FitNone || fit < best:
			continue
		case fit > best:
			best, chosen, ambiguous = fit, d, false
		default:
			ambiguous = true
		}
	}
	if ambiguous {
		return nil, true
	}
	return chosen, false
}

// require rejects a type this table does not own.
//
// A Type carries its table because the alternative is a wrong answer that
// nothing reports: two tables' types are two pointer sets, so a cross-table
// comparison silently answers "different type" for one declaration and would
// make a selection decline where it should have chosen.
func (t *Table) require(ty *Type, who string) {
	if ty == nil {
		panic("ir: Table." + who + ": a nil type has no identity")
	}
	if ty.owner != t {
		panic("ir: Table." + who + ": " + ty.Name() + " belongs to another table; " +
			"types from two tables are two pointer sets and compare unequal")
	}
}

func (t *Table) requireDecl(d *Decl) {
	if d.owner != t {
		panic("ir: Table.SelectOverload: " + d.Name() + " belongs to another table")
	}
}

// TableSummary is a table's size, for diagnostics and for a test that wants to
// say the table was populated rather than that a lookup happened to answer.
func (t *Table) TableSummary() string {
	return "table[" + strconv.Itoa(len(t.types)) + " types, " +
		strconv.Itoa(len(t.decls)) + " decls, " +
		strconv.Itoa(len(t.syms)) + " symbols]"
}

package ir

// THE TYPE TABLE.
//
// A Table interns the producer's identities as IR entities: each Nomi type
// the builder names (Type, with the value type it is held as) and each
// declaration a call or a reference names (Symbol). One identity is one
// entity for the life of the table, so two same-named declarations are two
// entities and one declaration asked for twice is one.
//
// IDENTITY IS THE POINTER, AND THE TABLE IS WHAT MAKES THAT AFFORDABLE. A
// Symbol (ir.go) is a name plus a pointer identity and nothing else, which is
// enough to say two same-named declarations are two declarations.
//
// WHY THIS IS AN INTERNING ARENA AND NOT ANOTHER SIDE TABLE. A
// `map[*Symbol]*implItem` would leave every fact outside the representation and
// make the token a receipt for work done elsewhere. The maps below go the
// opposite direction, from a PRODUCER IDENTITY to an IR entity, which is what
// interning is, and which `internal/irbuild`'s own `g.comps` already is one
// layer up.
//
// The producer supplies FACTS about a type: whether it is concrete or an
// erased interface, the enum variants that embed it, the interfaces it has an
// impl of. Which function a call or an operator reaches is the checker's
// record, which the builder reads; no rule here chooses among candidates.
// The embeds and conformance facts travel with the type into an encoded
// image (encode.go); no reader of the IR asks for either.

import "strconv"

// TypeForm is whether a Type's values carry their own concrete type.
//
// Two members, not a mirror of the builder's tag set. `internal/irbuild`'s `tag`
// has many more, because it tells apart the shapes the builder lowers
// differently.
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
	// embeds are the types this enum's variants embed: an `embeds` variant
	// makes its payload type a subtype of the enum.
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

// Form is whether this type's values carry their own concrete type.
func (t *Type) Form() TypeForm { return t.form }

// Existential reports whether this type's concrete type is erased.
func (t *Type) Existential() bool { return t != nil && t.form == FormExistential }

func (t *Type) String() string { return t.Name() }

// Table owns every Type and Symbol of one lowering.
//
// ONE PER LOWERING, NOT PROCESS-WIDE, and that is a correctness requirement
// rather than a scoping preference. The producer's identity tokens include
// pointers the builder shares process-wide under a mutex (`hostTypeDefs`,
// `opaqueDefs`, `stdEnumDefs`, `sharedGenStructDefs`), so a process-wide table
// would be a map two lowerings mutate. A per-lowering table needs no lock and
// cannot be the reason two lowerings interfere.
type Table struct {
	types map[any]*Type
	syms  map[any]*Symbol
	// linker names a token's linkage name, or "" for a symbol only its own
	// module names; imports answers the symbol another lowering already
	// made under a linkage name, or nil. Both nil unless SetLinkage ran.
	linker  func(token any, name string) string
	imports func(link string) *Symbol
}

// SetLinkage makes the symbols t interns carry the linkage name linker
// answers for their token, and makes a token whose linkage name imports
// answers resolve to that symbol rather than to one t mints: a declaration
// lowered once for the process (an object linked into this lowering) keeps
// its one symbol. Either may be nil.
func (t *Table) SetLinkage(linker func(token any, name string) string, imports func(link string) *Symbol) {
	t.linker, t.imports = linker, imports
}

// NewTable is an empty table.
func NewTable() *Table {
	return &Table{types: map[any]*Type{}, syms: map[any]*Symbol{}}
}

// Types is how many types this table holds.
func (t *Table) Types() int { return len(t.types) }

// Symbols is how many symbols this table holds.
func (t *Table) Symbols() int { return len(t.syms) }

// Symbol interns the declaration the producer identifies by token as a Symbol:
// a name plus an identity and nothing else.
//
// WHY THIS EXISTS. A `Symbol`'s identity is the POINTER and its name is
// display only, and `NewSymbol` MINTS. Minting alone holds that rule in one
// direction and fails it in the other: two same-named declarations are
// correctly two symbols, but two reads of ONE declaration would also be two
// symbols, a receipt for a declaration rather than its identity.
//
// A REPEAT CALL RETURNS THE FIRST ENTRY AND IGNORES THE NAME OFFERED, so one
// declaration has one symbol for the life of the table. `NewSymbol` stays, for a producer naming something that genuinely has
// no prior identity — a synthesized binding, a test's subject.
func (t *Table) Symbol(token any, name string) *Symbol {
	return t.internSymbol(token, name, nil)
}

// ImplSymbol interns the function of an impl block the producer identifies
// by token, named by f (ImplFunc.Name) and carrying it. A function of an
// impl block is interned only here, so its name is the same whichever call
// site asks first, and a token this table already holds as a plain Symbol
// panics. A symbol linked from another module (the table's imports) keeps
// the ImplFunc that module wrote.
func (t *Table) ImplSymbol(token any, f ImplFunc) *Symbol {
	if s := t.syms[token]; s != nil && s.impl == nil {
		panic("ir: Table.ImplSymbol: " + s.name + " was interned as a plain symbol and is now " +
			f.Name() + "; one declaration has one name")
	}
	return t.internSymbol(token, f.Name(), &f)
}

func (t *Table) internSymbol(token any, name string, impl *ImplFunc) *Symbol {
	if token == nil {
		panic("ir: Table.Symbol: a declaration with no producer identity cannot be " +
			"interned; identity is the whole reason this table exists")
	}
	if s := t.syms[token]; s != nil {
		return s
	}
	var link string
	if t.linker != nil {
		link = t.linker(token, name)
	}
	if link != "" && t.imports != nil {
		if s := t.imports(link); s != nil {
			t.syms[token] = s
			return s
		}
	}
	s := &Symbol{name: name, link: link, impl: impl}
	t.syms[token] = s
	return s
}

// Concrete interns the concrete type the producer identifies by token, and
// reports whether this call MINTED it.
//
// `minted` is the producer's cue to state this type's value type and its
// embeds edges, and it is returned rather than left implicit because stating
// them twice is a silent duplicate and stating them never is a silent wrong
// answer. It also terminates the recursion a mutually-embedding pair of enums
// would otherwise be: the entry exists before its edges are read.
//
// The token must be a comparable value the producer uses as an identity. It is
// held opaquely: nothing in this package inspects it. `internal/irbuild`
// passes its whole `kind` value (internal/irbuild/irtable.go).
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

// require rejects a type this table does not own.
//
// A Type carries its table because the alternative is a wrong answer that
// nothing reports: two tables' types are two pointer sets, so a fact
// recorded across two tables relates types no reader of either holds.
func (t *Table) require(ty *Type, who string) {
	if ty == nil {
		panic("ir: Table." + who + ": a nil type has no identity")
	}
	if ty.owner != t {
		panic("ir: Table." + who + ": " + ty.Name() + " belongs to another table; " +
			"types from two tables are two pointer sets and compare unequal")
	}
}

// TableSummary is a table's size, for diagnostics and for a test that wants to
// say the table was populated rather than that a lookup happened to answer.
func (t *Table) TableSummary() string {
	return "table[" + strconv.Itoa(len(t.types)) + " types, " +
		strconv.Itoa(len(t.syms)) + " symbols]"
}

package irbuild

// The builder's half of the IR's type and declaration table.
//
// This file is the producer side of `internal/ir`'s Table. It turns this
// package's `kind` into an `*ir.Type` and this package's `*implItem` into an
// `*ir.Decl` with a signature, and then lets `internal/ir` do the choosing:
// overload scoring for the operator family and for `foreignIfaceCall` is
// `ir.Table.SelectOverload` over `ir.Table.Widens`. No subtyping-and-fit rule
// is implemented in this package. `foreignIfaceCall` and `irSelectOperImpl`
// collect their own candidate sets, which is the part of the answer this
// package knows, and ask `irSelectAmong` to choose.
//
// The IR says which declaration the arguments select. Which impl body
// realizes that declaration, what its parameter defaults are, and how an
// argument is coerced to a declared parameter are this package's answers, so
// the `*implItem` travels beside the node rather than being recoverable from
// it. There is no Decl-to-implItem map in this package, and none is needed:
// every consumer of a selection is reached from the function that performed
// it.
//
// # Lazy population
//
// A type's conformance to an interface is answered by `g.bindsImpl`, a query
// over a registry that is built while lowering runs. Mirroring that eagerly
// into the table at declaration time would be a second implementation of a
// question that already has one. Supplying the answer as a fact at the site
// that needs it cannot disagree with the site's own answer. The cost is that
// the table is not a program-wide index; see ir/table.go's Declare comment.
//
// # The failure direction is loud
//
// A type token here is the whole `kind` value, so two IR types are identical
// exactly when two kinds are `==`. The `embeds` edge is registered against
// `kind{tag: tagNamed, def: e}`. If some named kind carried a non-nil `comp`,
// `iface` or `tp` beside its `def`, the edge would miss and the table would
// reject the candidate, which makes the call decline with a named reason. It
// cannot silently accept a candidate it should reject.
//
// # Kind equality is IR type identity
//
// `irTypeOf` interns every tag, including a `tagNamed` with no `*typeDef`, a
// `tagIface` with no `*ifaceDef` and a `tagTypeParam` with no
// `*typeParamDef`, under a placeholder display name, because `ir.Type` says
// its identity is the pointer and the name is for reading. Only `tagInvalid`
// answers nil, and that nil is semantic: it is the shape an operand whose own
// lowering was refused arrives in, and `ir.Table.Accepts` reads a nil
// argument type as "not a candidate". Totality matters because a rule that
// compared kinds before asking about widening would score two equal
// malformed kinds as an exact fit, while a nil type scores them as no fit;
// interning every tag makes the two agree by construction.
// TestIRTable_TypeIdentityIsKindEquality is the pin.

import "github.com/nomi-language/nomi/internal/ir"

// irTypes is this compilation unit's IR type and declaration table, minted on
// first use.
//
// Lazily rather than in a constructor because `gen` is built at several sites
// and a table nobody asked for is a map nobody reads.
func (g *gen) irTypes() *ir.Table {
	if g.irTable == nil {
		g.irTable = ir.NewTable()
	}
	return g.irTable
}

// irKindName is a kind's Nomi name for display, without `kind.nomi()`'s
// preconditions.
//
// `nomi()` dereferences `def`, `iface` or `tp` for its three declaration tags,
// so a kind carrying one of those tags with a nil pointer panics reading it.
// That shape is a bug in this package rather than a program somebody wrote, and
// a panic is the wrong report for it HERE specifically: this name is display
// only — `ir.Type` and `ir.Decl` both state their identity is the pointer — so
// a placeholder costs nothing an answer depends on, while a panic would turn a
// malformed kind into a crash at a candidate filter.
//
// The `comp` short-circuit is `nomi()`'s own first line, kept so the two agree
// for every kind either of them can name.
func irKindName(k kind) string {
	if k.comp != nil {
		return k.comp.nomi
	}
	switch k.tag {
	case tagNamed:
		if k.def == nil {
			return "<named>"
		}
	case tagIface:
		if k.iface == nil {
			return "<interface>"
		}
	case tagTypeParam:
		if k.tp == nil {
			return "<type parameter>"
		}
	}
	return k.nomi()
}

// irTypeOf is a kind as an IR type, or nil for `kindInvalid`.
//
// NIL ONLY FOR tagInvalid, and that nil is the semantics rather than a hole:
// it is the shape an operand whose own lowering was refused arrives in, the
// refusal has already been recorded, and `ir.Table.Accepts` reads a nil
// argument type as "not a candidate".
//
// THE TOKEN IS THE KIND VALUE. `kind` is comparable — a tag plus four
// declaration pointers — and its equality IS this package's type identity, so
// interning on it makes IR type identity and kind equality the same relation by
// construction rather than by a mapping somebody has to keep faithful. Every
// tag reaches an entry, malformed or not, which is what makes that sentence
// true for every kind rather than for the well-formed ones; see this file's
// header.
func (g *gen) irTypeOf(k kind) *ir.Type {
	if k.tag == tagInvalid {
		return nil
	}
	if k.tag == tagIface {
		// An existential is the one form whose values do not carry their own
		// concrete type, which is the distinction `ir.Table.Widens` needs and
		// the only thing `ir.TypeForm` records.
		ty, minted := g.irTypes().Existential(k, irKindName(k))
		if v := g.irValType(k); minted && v != nil {
			ty.SetVal(v)
		}
		return ty
	}
	ty, minted := g.irTypes().Concrete(k, irKindName(k))
	if !minted {
		return ty
	}
	if v := g.irValType(k); v != nil {
		ty.SetVal(v)
	}
	// The `embeds` edges, declared exactly once per interned type. `minted` is
	// what makes "exactly once" checkable rather than remembered, and it is
	// also what terminates the recursion two mutually-embedding enums would
	// otherwise be: the entry exists before its edges are read.
	if k.tag == tagNamed && k.def != nil && k.def.isEnum {
		for i := range k.def.variants {
			e := k.def.variants[i].embeds
			if e == nil {
				continue
			}
			// `kind{tag: tagNamed, def: e}` and not the variant's own kind: the
			// subtype's identity in the embeds relation is its declaration and
			// nothing else.
			if sub := g.irTypeOf(kind{tag: tagNamed, def: e}); sub != nil {
				g.irTypes().Embeds(ty, sub)
			}
		}
	}
	return ty
}

// irImplDecl is one impl function as an IR declaration with its signature.
//
// The token is the `*implItem` POINTER, which is this package's identity for
// one impl function: two `impl Add` blocks on one receiver build two items, so
// two calls here return two `*ir.Decl` and a repeat call for one item returns
// the first. Declarations are comparable by identity rather than by printed
// name, by construction.
//
// The NAME is `Receiver.method`, a Nomi spelling. It is display only: `ir.Decl`
// says its identity is the pointer, and the builder's Go-spelled name is
// deliberately absent because it is not a fact about the program.
func (g *gen) irImplDecl(recv kind, it *implItem) *ir.Decl {
	params := make([]*ir.Type, len(it.params))
	for i := range it.params {
		params[i] = g.irTypeOf(it.params[i])
	}
	return g.irTypes().Declare(it, irKindName(recv)+"."+it.name, params, g.irTypeOf(it.result))
}

// irNoteConformance records, for every existential parameter of one candidate,
// which of these arguments has an implementation of that interface.
//
// This is the one fact `ir.Table.Widens` cannot derive and this package can:
// `g.implements` is `g.bindsImpl`, a query over the impl registry. The rule
// stays in the table — an existential accepts a concrete implementor and never
// re-erases an already-erased value — and only the edges come from here.
//
// An argument that is ITSELF existential is skipped rather than recorded,
// matching the table's own guard: a value already erased into one interface is
// not a concrete value to erase into another.
func (g *gen) irNoteConformance(it *implItem, args []kind) {
	for i := range it.params {
		p := it.params[i]
		if p.tag != tagIface || p.iface == nil {
			continue
		}
		pt := g.irTypeOf(p)
		if pt == nil {
			continue
		}
		for _, a := range args {
			if a.tag == tagInvalid || a.tag == tagIface {
				continue
			}
			at := g.irTypeOf(a)
			if at == nil {
				continue
			}
			if g.implements(p.iface, a) {
				g.irTypes().Conforms(at, pt)
			}
		}
	}
}

// irCandidate is one impl function a call site is choosing among: the item, and
// the receiver kind its display name is built from.
//
// The receiver travels with the item because a candidate SET may span
// receivers. `foreignIfaceCall` scores `impl Debug for Worker` against
// `impl Debug for Node` at one call, so the two candidates have different
// receivers and asking the caller for a single one would be wrong there even
// though it is right on the operator path.
type irCandidate struct {
	recv kind
	it   *implItem
}

// irSelectAmong is candidate scoring, performed by the IR.
//
// The caller supplies the candidate SET, which is this package's knowledge —
// which impl blocks are impls of this interface for a reachable receiver, which
// are lowerable, which declare this method. The IR supplies the RULE: how well
// each candidate's declared parameters accept these argument types, which
// candidate wins, and whether two of them tie.
//
// THE PAIRING IS A LOCAL VARIABLE, not a table on the gen, and that is the
// shape the constraint requires. The chosen `*ir.Decl` and the `*implItem` that
// realizes it are returned TOGETHER, so nothing downstream has to recover one
// from the other and no persistent map exists to do it with.
//
// A CANDIDATE'S CONFORMANCE EDGES ARE RECORDED AS IT IS COLLECTED, and every
// candidate is collected before anything is scored, because `Widens` reads
// those edges: a rule that scored a rival before a later candidate's edge was
// recorded would make the answer depend on the arrival order, and
// `SelectOverload` is order-independent.
func (g *gen) irSelectAmong(cands []irCandidate, args []kind) (*implItem, *ir.Decl, bool) {
	argTypes := make([]*ir.Type, len(args))
	for i, a := range args {
		argTypes[i] = g.irTypeOf(a)
	}
	decls := make([]*ir.Decl, 0, len(cands))
	for _, c := range cands {
		g.irNoteConformance(c.it, args)
		decls = append(decls, g.irImplDecl(c.recv, c.it))
	}
	chosen, ambiguous := g.irTypes().SelectOverload(decls, argTypes)
	if ambiguous || chosen == nil {
		return nil, nil, ambiguous
	}
	for i := range decls {
		if decls[i] == chosen {
			return cands[i].it, chosen, false
		}
	}
	// Unreachable: SelectOverload returns one of the candidates it was given.
	// Stated as a refusal-shaped answer rather than a panic because a miss here
	// would be this file's bug and a declining call lands on a named refusal,
	// which is a diagnosable outcome and not a wrong one.
	return nil, nil, false
}

// irSelectOperImpl is `operImplFor`'s selection, performed by the IR.
//
// It collects the candidate set — the same filter `operImplFor` applied, which
// is this package's knowledge of which impl blocks are operator impls of one
// interface for one receiver — and hands it to irSelectAmong.
func (g *gen) irSelectOperImpl(iface string, recv kind, method string, args []kind) (*implItem, *ir.Decl, bool) {
	var cands []irCandidate
	for _, d := range g.operOrder {
		if d.ifaceName != iface || d.recv != recv || !d.lowerable {
			continue
		}
		it := d.items[method]
		if it == nil {
			continue
		}
		cands = append(cands, irCandidate{recv: recv, it: it})
	}
	return g.irSelectAmong(cands, args)
}

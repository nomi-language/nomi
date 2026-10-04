package irbuild

import (
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// Process-wide identity for the structural types whose components are the
// same in every module.
//
// # Why a process-wide table
//
// composite.go gives a structural type a declaration-shaped identity by
// interning it: one `*compKind` per distinct type, so pointer equality is
// structural equality. It interns in `g.comps`, which is PER GEN. That is
// wrong at the stdlib boundary, because a stdFunc's parameter and result kinds
// are built once in stdCandidateFor and compared BY POINTER against a call
// site's argument kinds in a different gen. Two gens' `List<String>` would be
// two unequal `kind` values, and no std function naming `List<...>` in its
// signature could match a call.
//
// # Which kinds may be shared
//
// A structural kind is shareable exactly when every COMPONENT is
// package-neutral (kind.packageNeutral): a scalar, a process-wide def or an
// anchored interface. `List<Point>` over a user's `Point` names a type one
// module declares, so it keeps the per-gen path. Nominal identity is
// `(declaring file, name)`, so two files may each declare a `Point`, and a
// shared table is where the two would otherwise collapse.
// TestStructuralInterningDoesNotCollapseNominallyDistinctTypes asserts
// `List<Point>` over each stays two kinds.
//
// # The key
//
// composite.go's key: the Nomi spelling, INCLUDING the type arguments, plus
// the component kinds. `List<String>` and `List<Int>` are two keys; a key on
// the base name would be a silent overwrite that hands one instantiation
// another's element type. TestStructuralKindIdentityIsProcessWide pins it.
//
// # Why the shared table is safe
//
// A shared entry is IMMUTABLE after creation. `compKind`'s three fields are
// written once, in the constructor below, and nothing assigns to them
// afterwards. An entry holds no pointer that belongs to any gen: its
// components are what packageNeutral certifies. So concurrent gens sharing one
// entry observe only the value it was constructed with.

var (
	sharedCompMu sync.Mutex
	// sharedComps groups entries by the FULL Nomi spelling, arguments
	// included, and separates them within a group by their component kinds.
	sharedComps = map[string][]*compKind{}
)

// shareableParts reports whether a composite built from these components may
// take process-wide identity.
//
// A parts-less intern stays per-gen, and that is a rule rather than an
// omission: every structural constructor passes at least one component (a
// list's element, a function type's result), so "no components" is not a
// structural type at all — it is a direct probe — and an empty quantifier must
// not be what earns a kind process-wide identity.
func shareableParts(parts []kind) bool {
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if !p.packageNeutral() {
			return false
		}
	}
	return true
}

// internComp is the ONE router for a structural type's identity: the
// process-wide table when every component is package-neutral, this gen's own
// otherwise.
//
// g may be nil, which is the stdlib signature boundary — it has no gen, and a
// neutral kind needs none. A nil gen with a non-neutral component is a caller
// bug and says so, rather than producing a kind interned nowhere: such a kind
// would be one no call site in any gen could ever match, which is the failure
// this whole file exists to remove.
func internComp(g *gen, nomi string, parts ...kind) *compKind {
	return internCompNamed(g, nomi, nil, parts)
}

// internCompNamed is internComp for a structural type whose components are
// addressed by NAME rather than by position — an anonymous struct — and is
// where both routers actually live. The names ride with the entry and take
// part in its identity; see compKind.names.
func internCompNamed(g *gen, nomi string, names []string, parts []kind) *compKind {
	if shareableParts(parts) {
		return internSharedComp(nomi, names, parts)
	}
	if g == nil {
		panic("irbuild: no gen to intern the non-neutral structural type " + nomi + " in")
	}
	return g.internLocal(nomi, names, parts)
}

// internSharedComp is the shared table's one insertion point.
//
// Entries are grouped by Nomi spelling and separated by their components and
// field names, exactly as composite.go's per-gen table is.
func internSharedComp(nomi string, names []string, parts []kind) *compKind {
	sharedCompMu.Lock()
	defer sharedCompMu.Unlock()
	for _, c := range sharedComps[nomi] {
		if samePartsSlice(c.parts, parts) && sameNameSlice(c.names, names) {
			return c
		}
	}
	// The slices are copied because the caller's may be reused or appended to,
	// and an entry the whole process shares must not alias a caller's storage.
	// funcKind builds its parts with append, which is exactly such a slice.
	own := make([]kind, len(parts))
	copy(own, parts)
	var ownNames []string
	if names != nil {
		ownNames = make([]string, len(names))
		copy(ownNames, names)
	}
	c := &compKind{nomi: nomi, parts: own, names: ownNames}
	sharedComps[nomi] = append(sharedComps[nomi], c)
	return c
}

func samePartsSlice(a, b []kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameNameSlice compares two field-name lists. nil and empty are the same
// answer: a structural type with no names is one whose components are
// positional, and there is exactly one such list.
func sameNameSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- the constructors that answer without a gen ----------------------------

// listKindIn is `List<elem>` interned in g, or process-wide when elem is
// package-neutral. g may be nil; see internComp.
func listKindIn(g *gen, elem kind) kind {
	// kindInvalid: propagates — a kind CONSTRUCTOR; the element's own decline is counted in items().
	if elem == kindInvalid {
		return kindInvalid
	}
	return kind{tag: tagList, comp: internComp(g, "List<"+elem.nomi()+">", elem)}
}

// sharedListKind is `List<elem>` for a caller that has no gen — the stdlib
// signature boundary — and reports whether the element could be shared at all.
//
// A non-neutral element DECLINES rather than falling back, because the fallback
// would be a per-gen kind and there is no gen: see stdTypeKind, which turns the
// decline into the refusal it already had.
func sharedListKind(elem kind) (kind, bool) {
	// A non-neutral element declines here; the element's own refusal is what
	// listSigKind's caller reports, at the element's own position.
	// kindInvalid: propagates — a kind CONSTRUCTOR; the element's decline is reported by the caller.
	if elem == kindInvalid || !elem.packageNeutral() {
		return kindInvalid, false
	}
	return listKindIn(nil, elem), true
}

// --- the stdlib signature side ---------------------------------------------

// listSigKind is the kind a stdlib signature's `List<…>` annotation names, and
// reports whether the annotation named a List at all.
//
// Resolved by NAME, unlike the prelude arm beside it, and the asymmetry is
// deliberate rather than an oversight. `Maybe` and `Result` are ordinary `enum`
// DECLARATIONS in std/prelude.nomi, so a stdlib module declaring its own would
// shadow them and the anchor is what makes the builder agree with the analyzer's
// (Origin, Name) rule about which one was meant. `List` is not a declaration
// anywhere — not in std, not in a user file; the front end builds it — so there
// is no declaration to anchor to and nothing that could shadow it. The builder
// already resolves it by name for user modules, in collections.go's
// structuralTypeOf (`t.Name != "List"`), and a second rule here would be a
// second convention for one question.
//
// The element is resolved by the same restricted stdTypeKind the outer position
// uses, so `List<Fragment<String>>` refuses on the `Fragment` rather than
// admitting a kind this boundary cannot share, and `List<List<String>>` works
// without a second rule.
//
// Reported as handled even when it refuses, for the reason preludeSigKind is: an
// unrepresentable element must be the answer rather than falling through to a
// lookup that would return the same kindInvalid for a different reason.
func listSigKind(gt *ast.GenericType, anchors stdAnchors) (kind, bool) {
	if gt.Name != "List" {
		return kindInvalid, false
	}
	if len(gt.Params) != 1 {
		// The checker rejects the program first; refusing rather than
		// instantiating at the wrong arity keeps this from emitting Go against a
		// type nobody wrote. Same rule preludeSigKind applies to its arity.
		return kindInvalid, true
	}
	elem := stdTypeKind(gt.Params[0], anchors)
	// kindInvalid: propagates — the element's own refusal is the answer.
	if elem == kindInvalid {
		return kindInvalid, true
	}
	k, shared := sharedListKind(elem)
	if !shared {
		// Unreachable while stdTypeKind admits only neutral kinds, and treated
		// as a refusal rather than trusted: a *compKind interned in no gen at
		// all would be a kind no call site could ever match, which is the
		// failure this file removes and must not reintroduce silently.
		return kindInvalid, true
	}
	return k, true
}

// sharedTupleKind is `(parts...)` for a caller that has no gen — the stdlib
// signature boundary — and reports whether every part could be shared at all.
//
// sharedListKind's shape and its reason: a non-neutral part DECLINES rather than
// falling back, because the fallback would be a per-gen kind and there is no
// gen. `tupleKind` itself is already nil-safe (its only gen access is
// `g.intern`, which routes to internComp), so the neutrality check here is what
// turns internComp's panic into the refusal the caller already had.
func sharedTupleKind(parts []kind) (kind, bool) {
	for _, p := range parts {
		// kindInvalid: propagates — a kind CONSTRUCTOR; the part's decline is reported by the caller.
		if p == kindInvalid || !p.packageNeutral() {
			return kindInvalid, false
		}
	}
	return (*gen)(nil).tupleKind(parts), true
}

// tupleSigKind is the kind a stdlib signature's TUPLE annotation names, and
// reports whether the annotation named a tuple at all.
//
// # Why a tuple needs its own arm
//
// `stdTypeKind`'s families are all NOMINAL plus a `*ast.GenericType` block for
// the structural ones (`List`, `Map`, a prelude instance, a generic std struct).
// A tuple is spelled neither way: the parser gives `(Int, Int)` as an
// `*ast.FuncType` with a NIL Return, reusing one node for `(Int, Int)` and
// `(Int) -> String` with the arrow as the only difference (collections.go's
// structuralTypeOf says so). Without this arm a tuple annotation such as
// `random.below_state(state: Int, n: Int): (Int, Int)`'s would match nothing
// and refuse as `stdlib function outside the scalar subset`, though tuples are
// an interned `tagTuple` over flat `F0`/`F1` fields.
//
// It is NOT the FFI projection. A Go multiple return projects to a Nomi tuple
// element-by-element on the adapter side; this is the SIGNATURE side of the
// same type, asked by a different question, and the two are independent.
//
// Admitted on exactly the List and Map arms' terms and no wider: a structural
// type over package-neutral components has a process-wide `*compKind` and
// renders to the same Go text in every module's package, so the boundary the
// scalar rule protects is not crossed. Parts resolve through the same restricted
// `stdTypeKind`, so `(Fragment<String>, Int)` refuses on the `Fragment`.
//
// ONE-WAY BY CONSTRUCTION, and the asymmetry is not this function's to fix. A
// tuple in a stdlib PARAMETER position is admitted here as a kind, and it is a
// perfectly good Go struct to bind — unlike the FFI direction, where a tuple
// parameter is rejected because Go has no tuple type to receive it. So this arm
// serves both positions while the FFI projection serves one.
func tupleSigKind(te ast.TypeExpr, anchors stdAnchors) (kind, bool) {
	ft, isFunc := te.(*ast.FuncType)
	if !isFunc || ft.Return != nil {
		// An arrow makes it a FUNCTION type, which is a separate question with
		// no stdlib customer: no std signature names one, and admitting
		// it here would be a projection with no reachable site. A `run` FIELD
		// on `std/random.Generator<T>` is a function type, but a field is
		// stdGenStructSpecs' business and answers through its own `kindOf`.
		return kindInvalid, false
	}
	if len(ft.Params) < 2 {
		// Nomi has no 1-tuple: `(T)` is a grouped type, which the parser also
		// spells this way. Declining rather than refusing is right — the inner
		// type is what was meant, and the caller's other arms should see it.
		return kindInvalid, false
	}
	parts := make([]kind, 0, len(ft.Params))
	for _, p := range ft.Params {
		k := stdTypeKind(p, anchors)
		// kindInvalid: propagates — the part's own refusal is the answer.
		if k == kindInvalid {
			return kindInvalid, true
		}
		parts = append(parts, k)
	}
	k, shared := sharedTupleKind(parts)
	if !shared {
		// Unreachable while stdTypeKind admits only neutral kinds, and treated
		// as a refusal rather than trusted — listSigKind's clause, for its
		// reason.
		return kindInvalid, true
	}
	return k, true
}

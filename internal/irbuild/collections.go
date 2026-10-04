package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Lists and tuples: two structural types, two very different representations.
//
// # `List<T>` is `*rt.List[T]` — a cons cell
//
// The reasoning lives at rt/list.go, because the choice is a runtime-library
// choice: a Go slice would invert Nomi's costs (O(1) prepend becomes O(n),
// because two immutable values may not share a backing array either of them
// appends into), and the accumulate-by-prepend recursion that `[h, ..t]` exists
// to serve would go quadratic. Every answer would be right and a program would
// stop finishing on real input.
//
// What that buys the builder is that a list literal is a chain of `rt.Cons`
// calls with the tail shared, `[a, ..t]` is one `rt.Cons` onto `t` rather than a
// copy of it, and a list pattern's tail is a pointer read rather than a slice
// header. What it costs is one allocation per element and O(n) index — the costs
// Nomi's List already has.
//
// # A tuple is an ANONYMOUS Go struct
//
// `(Int, String)` becomes `struct { F0 int64; F1 string }`. Fixed-arity and
// heterogeneous IS a struct, so the alternative was a uniform box — a slice of
// `any`, or a generic `Tuple2[A, B]` — and both are worse for the same reason:
// they throw the element types away at the boundary and pay an interface word or
// a slice header to get them back.
//
// ANONYMOUS rather than a generated named type per shape. A named type needs a name derived injectively from the shape,
// which needs a sanitizer, a collision guard, and a declaration written exactly
// once per shape — three mechanisms whose only job is to reconstruct something
// Go already has. Go's struct type identity is STRUCTURAL: two spellings of
// `struct { F0 int64; F1 string }` are the same type, so the interning key (see
// composite.go) is the type itself with nothing of ours in between. Field names are exported so identity survives a package
// boundary, since Go treats unexported field names from different packages as
// different.
//
// The cost is legibility of the Go spelling: a nested tuple's type is spelled out
// in full at every mention, so `(Int, (String, Bool))` reads as
// `struct { F0 int64; F1 struct { F0 string; F1 bool } }`. That is a
// NOMI_IRBUILD_DUMP readability cost and nothing else.
//
// # `==` is NOT Go's `==`, except where it provably is
//
// Two independent reasons, and each one is a silent wrong answer:
//
// Go's `==` on a struct compares fields with Go's `==`, and for a
// `*rt.List[T]` field that compares ADDRESSES. `(1, [2, 3]) == (1, [2, 3])` is
// True in Nomi, whose `==` is structural and recursive, and would be false in
// Go, because two cons chains are two allocations. The failure is quiet
// precisely because pointers ARE comparable in Go.
//
// And Nomi's `==` on Float is REFLEXIVE for NaN where Go's is IEEE:
// `nan == nan` is True in Nomi and false in Go. That is by design: it is what
// makes a NaN map key retrievable, and rt.EqFloat implements it.
//
// So equality is ELEMENTWISE and recursive (see comparator), and Go's `==`
// is spelled only where goEqualIsNomiEqual says the two are the same operation.
// That predicate's recursion IS the check; it is not a list of shapes believed
// safe, and Float is excluded by name.

// kindEmptyList is `[]` — a list literal with no elements, and therefore no
// element type.
//
// It is a separate tag rather than a `List<Unit>` in disguise because it is a
// genuinely different thing: a value the CHECKER types as a fresh type variable,
// which downstream usage resolves. This builder has no inference, so it carries
// the ambiguity in the kind and discharges it at the one place a wanted type is
// known — coerce, which widens it to any `List<T>` by spelling a typed nil.
// Every position that matters reaches coerce already: an annotated binding, an
// argument, a `return`, and a spread tail.
//
// Its Go type is `*rt.List[rt.Unit]`, which is the only type an element-less
// list can be given, and it is never wrong because there is no element to
// observe: the list is empty, so `[]` renders as "[]" from its type alone and
// two of them are equal without either being read.
var kindEmptyList = kind{tag: tagEmptyList}

// listKind is the kind of `List<elem>`.
//
// The rendering and the intern routing both live in sharedcomp.go, because a
// `List<T>` over a package-neutral T is a type the stdlib boundary names too
// and there must be exactly one renderer of the intern key.
func (g *gen) listKind(elem kind) kind { return listKindIn(g, elem) }

// tupleKind is the kind of `(parts...)`.
func (g *gen) tupleKind(parts []kind) kind {
	if len(parts) < 2 {
		// Nomi has no 1-tuple: `(x)` is a grouped expression and `(T)` a
		// grouped type. Reaching here with fewer than two parts is a caller
		// bug, so it must not silently produce a one-field struct.
		return kindInvalid
	}
	var nomi strings.Builder
	nomi.WriteByte('(')
	for i, p := range parts {
		// kindInvalid: propagates — a kind CONSTRUCTOR; the part`s own decline is counted in items().
		if p == kindInvalid {
			return kindInvalid
		}
		if i > 0 {
			nomi.WriteString(", ")
		}
		nomi.WriteString(p.nomi())
	}
	nomi.WriteByte(')')
	return kind{tag: tagTuple, comp: g.intern(nomi.String(), parts...)}
}

// structuralTypeOf reads a type annotation this file owns, or kindInvalid.
//
// A tuple type is spelled `*ast.FuncType` with a nil Return — the parser reuses
// one node for `(Int, String)` and `(Int) -> String`, and the arrow is the only
// difference. typeOf handles the arrow form; this handles the other.
func (g *gen) structuralTypeOf(te ast.TypeExpr) kind {
	switch t := te.(type) {
	case *ast.GenericType:
		// `Map<K, V>` is the other structural generic this builder reads. It
		// declines rather than refusing when the name is neither, so an
		// unrepresentable `Foo<Bar>` keeps its own refusal key. See maps.go.
		if k, isMap := g.mapTypeOf(t); isMap {
			return k
		}
		if t.Name == "Iter" && len(t.Params) == 1 {
			// A declared `Iter<T>` is the lowered sequence a pipeline
			// produces. A source of another kind entering it is viewed as one
			// (coerceEmpty, seqView), so every value of this kind is a real
			// `rt.Seq`.
			elem := g.typeOf(t.Params[0])
			// kindInvalid: propagates — a refused element type ARGUMENT already reported.
			if elem == kindInvalid {
				return kindInvalid
			}
			return seqKindIn(g, elem)
		}
		if t.Name != "List" || len(t.Params) != 1 {
			return kindInvalid
		}
		return g.listKind(g.typeOf(t.Params[0]))
	case *ast.FuncType:
		if t.Return != nil {
			return kindInvalid
		}
		parts := make([]kind, len(t.Params))
		for i, p := range t.Params {
			parts[i] = g.typeOf(p)
		}
		return g.tupleKind(parts)
	}
	return kindInvalid
}

// --- declared components ----------------------------------------------------

// note records the declared types k mentions, on the two graphs that need them.
//
// They are two graphs and not one, and conflating them is a bug in each
// direction:
//
//   - mentions drives LOWERABILITY. `struct Holder { items: List<Bad> }` cannot
//     be lowered if `Bad` was refused, because its kind would name a type
//     that was never declared — so this graph follows every composite, list and
//     tuple alike.
//   - components drives SIZE reachability, which is what decides boxing. A list
//     field is a POINTER, so it cannot make its container infinite and must NOT
//     be followed: following it would box a field that is already indirect. A
//     tuple field is INLINE and must be, or `struct Node { pair: (Int, Node) }`
//     spells a Go type of infinite size.
func (d *typeDef) note(k kind) {
	d.mentions = appendMentionedDefs(d.mentions, k)
	d.components = appendInlineDefs(d.components, k)
}

func appendMentionedDefs(out []*typeDef, k kind) []*typeDef {
	if k.def != nil {
		return append(out, k.def)
	}
	if k.comp != nil {
		for _, p := range k.comp.parts {
			out = appendMentionedDefs(out, p)
		}
	}
	return out
}

func appendInlineDefs(out []*typeDef, k kind) []*typeDef {
	switch k.tag {
	case tagNamed:
		return append(out, k.def)
	case tagTuple, tagAnonStruct:
		// Both are a flat Go struct, so a component of either is stored INLINE
		// and can make its container's size infinite:
		// `struct Node { p: {next: Node} }` needs the same boxed edge
		// `struct Node { pair: (Int, Node) }` does.
		for _, p := range k.comp.parts {
			out = appendInlineDefs(out, p)
		}
	}
	return out
}

// --- literals ---------------------------------------------------------------

// elementCandidate selects without emitting; each consumer then checks and
// discharges every operand against the selected element type.
func (g *gen) elementCandidate(items []kind) kind {
	for _, cand := range items {
		if !untypedLiteral(cand) && g.widensAll(items, cand) {
			return cand
		}
	}
	return kindEmptyList
}

// widensAll reports whether every element is already `want`, is an untyped
// literal, or widens into `want`.
//
// Asked through embedsVariant rather than by calling coerce, because coerce may
// emit — a boxed payload takes the address of a temporary — and this question is
// asked once per candidate. That is also why an untyped literal is waved through
// rather than test-discharged here: dischargeNone needs a gen and emits, and
// elementKind's coerce loop performs the real discharge one line later.
func (g *gen) widensAll(items []kind, want kind) bool {
	for _, k := range items {
		if k == want || untypedLiteral(k) {
			continue
		}
		if _, _, widens := embedsVariant(want, k); !widens {
			return false
		}
	}
	return true
}

// --- rendering --------------------------------------------------------------

// --- equality ---------------------------------------------------------------

// --- patterns ---------------------------------------------------------------

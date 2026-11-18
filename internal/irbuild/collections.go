package irbuild

import (
	"strings"
)

// Lists and tuples: the two structural kinds this file builds.
//
// # `List<T>`
//
// A list kind is tagList over an interned *compKind whose one part is the
// element kind (listKind). At run time every list is a `*rt.List[any]`, a
// cons list (rt/list.go): a list literal is a chain of cells, `[a, ..t]` is
// one cell onto `t` and shares it, and a list pattern's tail is the rest of
// the chain. Prepend is O(1) and index is O(n).
//
// # A tuple
//
// `(Int, String)` is tagTuple over an interned *compKind whose parts are the
// item kinds in order (tupleKind). The interning key is the Nomi spelling
// plus the parts (composite.go), so every spelling of one tuple type is one
// kind and `kind`'s `==` compares tuple types. The value is a VM tuple built
// by `ir.NewMakeTuple` (irtuple.go).
//
// # `==`
//
// Nomi's `==` is structural and recursive: `(1, [2, 3]) == (1, [2, 3])` is
// True though the two lists are two allocations. On Float it is reflexive
// for NaN, so `nan == nan` is True and a NaN map key can be found again
// (rt.EqFloat). The builder lowers `==` on a tuple part by part and on a
// record as one `ir.Compare` (structuralEqual and tupleRecordEquality,
// irequality.go).

// listKind is the kind of `List<elem>`.
//
// The intern routing lives in sharedcomp.go, because a `List<T>` over a
// package-neutral T is a kind the stdlib boundary names too, and one function
// builds the intern key for both.
func (g *gen) listKind(elem kind) kind { return listKindIn(g, elem) }

// tupleKind is the kind of `(parts...)`.
func (g *gen) tupleKind(parts []kind) kind {
	if len(parts) < 2 {
		// Nomi has no 1-tuple: `(x)` is a grouped expression and `(T)` a
		// grouped type. Reaching here with fewer than two parts is a caller
		// bug, so it must not produce a one-part tuple kind.
		return kindInvalid
	}
	var nomi strings.Builder
	nomi.WriteString("(")
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

// --- declared components ----------------------------------------------------

// note records the declared types k mentions, on the two graphs that need them.
//
// The two graphs differ in what they follow:
//
//   - mentions drives lowerability (settleLowerable, types.go).
//     `struct Holder { items: List<Bad> }` cannot be lowered if `Bad` was
//     refused, because its kind would name a type that was never declared.
//     This graph follows every composite, a list and a tuple alike.
//   - components is the graph boxFields walks (types.go) to mark as boxed
//     each field whose type reaches its own container. A named type and the
//     parts of a tuple or record are followed, so
//     `struct Node { pair: (Int, Node) }` has a boxed field. A list's element
//     is not followed.
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
		// The parts of a tuple or a record are followed:
		// `struct Node { p: {next: Node} }` gets the same boxed field
		// `struct Node { pair: (Int, Node) }` does.
		for _, p := range k.comp.parts {
			out = appendInlineDefs(out, p)
		}
	}
	return out
}

// --- literals ---------------------------------------------------------------

// elementCandidate picks a list literal's element kind from its items' kinds:
// the first item's kind that every other item already is or widens into,
// or kindInvalid when there is none. It appends no IR; the caller
// then converts every item to the chosen kind (convertTo).
func (g *gen) elementCandidate(items []kind) kind {
	for _, cand := range items {
		if g.widensAll(items, cand) {
			return cand
		}
	}
	// kindInvalid: sentinel — no item kind takes the others; the caller declines.
	return kindInvalid
}

// widensAll reports whether every element is already `want` or widens into
// `want`.
//
// It asks embedsVariant and not convertTo, because convertTo appends IR
// and this question is asked once per candidate. The caller's convertTo
// loop converts each element to the chosen kind.
func (g *gen) widensAll(items []kind, want kind) bool {
	for _, k := range items {
		if k == want {
			continue
		}
		if _, _, widens := embedsVariant(want, k); !widens {
			return false
		}
	}
	return true
}

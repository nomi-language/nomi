package rt

// Nomi's `List<T>`, and the operations a list literal needs.
//
// # A cons cell, not a slice
//
// The obvious Go representation is `[]T`, and the default is to converge on Go
// absent a specifically-Nomi reason. There is one here, and it is asymptotic
// rather than aesthetic.
//
// Nomi's List is a persistent singly-linked list: O(1)
// prepend, O(1) tail, structural sharing between a list and its own tail, O(n)
// index. Its whole surface is written against those costs — `[h, ..t]` is the
// destructuring form the language gives you, `List.head`/`List.tail` are
// documented as "the cons-list idiom", and the recursive shape
// `case xs { [] -> …; [h, ..t] -> f(h) + go(t) }` is how a Nomi program walks
// one. A slice inverts exactly those two costs: `..t` stays O(1) (a subslice),
// but prepend becomes O(n), because two immutable values may not share a
// backing array that either of them appends into. So the idiomatic Nomi
// accumulate-by-prepend loop would go from linear to quadratic — the program
// would produce the right answers and stop finishing on real input.
//
// So: a cons cell, `*List[T]`, with nil as the empty list. Costs, stated
// rather than implied: one heap allocation per element, no cache locality
// across a walk, and O(n) index. Those are the costs Nomi's List is documented
// to have.
//
// # Len is cached
//
// Eight bytes per cell buys O(1) length, which is not a micro-optimization
// here: `Iter.known_count` on a List is documented as O(1), and an
// `Iter.count` over a Len-less cell would be asymptotically slower than that
// contract. It also makes an exact-length list
// pattern (`[a, b]`) one integer comparison instead of a pointer walk.
//
// # Tuples have nothing here, deliberately
//
// A Nomi tuple is fixed-arity, heterogeneous and structural, which is exactly a
// Go anonymous struct — `struct { F0 int64; F1 string }`. The builder's kind
// for a tuple is one per shape, Go's own structural type identity makes two spellings of one
// shape the same type, and there is no runtime support to write: construction
// is a composite literal, `pair.0` is `.F0`, and equality is field-wise `==`.
// A uniform `Tuple` box would have cost an interface per element and thrown the
// element types away.

// List is one cons cell. A nil *List[T] is the empty list.
type List[T any] ListCell[T, List[T]]

// Cons prepends head to tail — O(1), and tail is shared, not copied.
func Cons[T any](head T, tail *List[T]) *List[T] {
	return ConsCell[T, List[T]](head, tail)
}

// Eq is `==` on a Go-comparable element, as a function value.
//
// It exists so ListCellsEqual can be `T any` and still bottom out somewhere: a list
// of lists needs a comparator for its elements, so the comparator has to be a
// parameter, and then the leaf case needs a name. Go instantiates and inlines
// this per element type, and a reference to it allocates nothing (a top-level
// func's value is static).
//
// Not for Float; see EqFloat. `comparable` admits float64, so this function
// would compile and answer wrong for exactly one value.
func Eq[T comparable](a, b T) bool { return a == b }

// EqFloat is `==` on a Float, which is reflexive for NaN.
//
// Nomi's Float equality is not IEEE's: `nan == nan` is True. That is a design
// decision rather than an oversight — Equal is what decides map-key identity,
// so an IEEE `==` would make a NaN key unretrievable from the map it was
// inserted into. `0.0 == -0.0` agrees with Go and needs nothing.
//
// `a != a` is the NaN test with no import and no function call. A caller must
// route every Float comparison here rather than through Eq, which would
// silently answer false for the one value the two definitions disagree on.
func EqFloat(a, b float64) bool { return a == b || (a != a && b != b) }

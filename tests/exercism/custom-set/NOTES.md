# Custom Set

Subject: Collections & Algorithms

Build a set type of your own: membership, subsets, disjointness, equality, adding, intersection, difference and union.

Adapted from Exercism problem-specifications (MIT), exercises/custom-set.

## Choices

- The exercise asks for a set built by hand, so `CustomSet<T>` does not wrap std's `Set`. It is an opaque struct over one `List<T>` kept in ascending order with no value twice. Every set of the same values then holds the same list, so `derive Equatable` is set equality, and the canonical "constructed from an array with duplicates" case needs no code of its own.
- Order and uniqueness are kept in one place, the private `insert`: a `case` on the list, then `Comparable.compare` with the head, whose three arms put the value before it, keep the list as it is, or carry on down the tail. `from` and `union` are `Iter.reduce` over `add`, so they keep the order too.
- `intersection` and `difference` filter the first set's list, which is already in order, so they build the struct directly with the pipe inside its field (style §1).
- The functions are in `impl CustomSet<T>`, generic over the block's `T`. The ones that only test membership ask for `T: Equatable`; the ones that build a set ask for `T: Comparable`.
- A lookup is a linear scan of the list, so `subset?` and `intersection` are quadratic. A balanced tree would fix that; the canonical sets have at most four values, and the sorted list keeps equality structural.
- The tests use `Int` values. Each canonical group is a test named by its description, holding one `subtests` table. The tables are annotated, since most open with a row of empty lists that give the element type nothing.

## Friction

None.

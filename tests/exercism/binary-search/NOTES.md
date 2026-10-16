# Binary Search

Find a value's index in a sorted array by repeatedly halving the range it could be in.

Adapted from Exercism problem-specifications (MIT), exercises/binary-search.

## Choices

- The input is a `Vector<T>`, not a `List<T>`, since binary search needs indexed access and `List` has none.
- The function is generic over `T: Comparable` and branches on `Comparable.compare` returning `Ordering`, so all three outcomes are one exhaustive `case`.
- A missing value is `Err("value not in array")`, the canonical message. `Maybe<Int>` would have been as natural for "not found".
- Recursion over a half-open range `low..high`; each recursive call is in tail position.
- Two `subtests` tables: found values, and missing values, which assert a different shape.

## Friction

- [verbose] `Vector.at` returns `Maybe<T>`, so reading the middle element, whose index is in range by construction, still needs `Some(item) = Vector.at(items, middle) else { return Err("value not in array") }`, an arm that cannot run. Rust writes `items[middle]` and Go `items[middle]`, both of which trap on a bad index. A trapping `Vector.get` (or similar) beside the total `at` would fit this.

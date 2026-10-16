# Binary Search

Find a value's index in a sorted array by repeatedly halving the range it could be in.

Adapted from Exercism problem-specifications (MIT), exercises/binary-search.

## Choices

- The input is a `Vector<T>`, not a `List<T>`, since binary search needs indexed access and `List` has none.
- The function is generic over `T: Comparable` and branches on `Comparable.compare` returning `Ordering`, so all three outcomes are one exhaustive `case`.
- A missing value is `Err("value not in array")`, the canonical message. `Maybe<Int>` would have been as natural for "not found".
- Recursion over a half-open range `low..high`; each recursive call is in tail position.
- The middle element is read with `Vector.at(items, middle)`, which returns the element and traps on an index outside the vector. `middle` is in `low..high` by construction, so there is no miss to handle.
- Two `subtests` tables: found values, and missing values, which assert a different shape.

## Friction

None.

## Indexing

One indexed read, `Vector.at(items, middle)` in `search`. Its index is in range by construction, so the read returns the element itself and the function has no arm for a miss. `Vector.at` was added as a result of this exercise: before it, the only read was the `Maybe`-returning `Vector.get`, and `search` needed an `else` that could not run.

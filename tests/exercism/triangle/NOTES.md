# Triangle

Classify a triangle by its three side lengths as equilateral, isosceles or scalene.

Adapted from Exercism problem-specifications (MIT), exercises/triangle.

## Choices

- Sides are a `(Float, Float, Float)` tuple, since the canonical data includes fractional sides and Nomi does not mix `Int` and `Float`. Whole sides are written `2.0`.
- `kind` answers `Maybe<Kind>` over a `Kind` enum, `None` for a non-triangle; the three canonical predicates are thin wrappers over it.
- The kind comes from the size of the set of sides (1, 2 or 3 distinct lengths) rather than pairwise comparisons.
- Each canonical group is a `group` holding one `subtests` table.
- `isosceles?` matches `Some(.Equilateral or .Isosceles)`: the `or` sits inside the payload, so one arm says both kinds count.

## Friction

None.

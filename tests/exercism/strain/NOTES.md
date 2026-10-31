# Strain

Subject: Collections & Algorithms

Implement `keep` and `discard`, which split a collection by a predicate, without the standard library's filter.

Adapted from Exercism problem-specifications (MIT), exercises/strain.

## Choices

- `keep` is a `case` over `[]` and `[head, ..tail]`, with a `when` guard on the arm that keeps the head. Each kept item is consed onto what the rest keeps, so the order is the input's with no reverse.
- `discard` is `keep` with the predicate negated, `&(not predicate(&1))`, so the walk is written once.
- Both are generic over the item type, which the canonical data asks for with integers, strings and lists of integers. The integer cases are two `subtests` tables, one per function, whose predicate column is a function value, `(Int) -> Bool`; the string and list cases are one test each, since a table's columns have one type.
- The canonical predicates are written as captures (`&(&1 % 2 == 1)`, `&String.starts_with(&1, "z")`, `&List.contains(&1, 5)`), and the constant ones as `fn _x -> True`.

## Friction

None.

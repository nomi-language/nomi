# Satellite

Subject: Collections & Algorithms

Rebuild a binary tree from its pre-order and in-order traversals, rejecting traversals that describe no tree.

Adapted from Exercism problem-specifications (MIT), exercises/satellite.

## Choices

- `Tree<T>` is a generic enum, `Leaf` or a `Node` with named `left`, `value` and `right` fields, as binary-search-tree has it. The canonical data's empty object `{}` is `.Leaf`.
- `tree_from_traversals` is generic over any `T: Hashable and Equatable` and answers `Result<Tree<T>, Error>`. The three canonical errors are an `Error` enum, checked in the canonical data's order: the lengths, then the two sets of items, then whether the set is as large as the list, which finds a repeated item.
- `build` takes the traversals apart with a list pattern: the head of the pre-order is the root, `Iter.take_while` reads the in-order up to it as the left subtree, and `Iter.take` and `Iter.drop` cut the rest of both lists at that size. The validation has already run, so `build` cannot meet a root missing from the in-order and has no error path.
- The tests use `String` items, as the canonical data does. `leaf` keeps the expected trees short. The canonical cases are two `subtests` tables, the trees and the rejections, since they assert different shapes; the table of trees is annotated so `.Leaf` in its first row has a type.

## Friction

None.

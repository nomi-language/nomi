# Zipper

Subject: Collections & Algorithms

Navigate a binary tree with a zipper: move the focus left, right and up, read and change the focused node, and get the whole tree back.

Adapted from Exercism problem-specifications (MIT), exercises/zipper.

## Choices

- `Tree<T>` is a struct of a value and two `Maybe<Tree<T>>` subtrees, so the canonical data's `null` subtree is `None` and every tree has a root node to focus on. `Zipper.from_tree` therefore cannot fail.
- Nomi values cannot change, so the zipper is the persistent structure zippers were invented for: an opaque struct of the focus subtree and the path back to the root, nearest parent first. Each step of the path is a private `Crumb`, `FromLeft` or `FromRight`, holding the parent's value and its other subtree. A move or a change answers a new zipper and rebuilds only the nodes on the path; `to_tree` walks `up` until there is no parent.
- `left`, `right` and `up` answer `Maybe<Zipper<T>>`, with `None` where there is no node to move to, which is the canonical "dead end" and "traversing up from top". `left` and `right` read the subtree with `?` inside the new zipper's literal (`focus: left?`).
 `set_value`, `set_left` and `set_right` cannot fail and answer a `Zipper<T>`, each one spread update of the focus (`#{..zipper, focus: #{.., value}}`).
- `derive Equatable` on `Zipper` and `Crumb` makes the "different paths to same zipper" case one `==` between two zippers.
- The canonical cases are a starting tree and a list of operations, all on the same tree. The test file writes each operation as an `Operation` variant and `follow` applies them with one `Iter.reduce` over a `Maybe<Zipper<Int>>`, as `apply(zipper?, operation)`: the `?` exits the lambda, so the first move that finds no node leaves `None` for the rest of the fold. The cases are three `subtests` tables by what they expect (a tree, a value, no zipper) and one test for the two paths. `leaf` and `initial_tree` keep the expected trees short.
- The canonical data's `left`, `right` and `value` operations at the end of a case are what the table asserts: a case ending in `value` is a row of the value table, and `to_tree` is applied by the tree table.

## Friction

None.

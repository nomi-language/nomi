# Binary Search Tree

Subject: Collections & Algorithms

Insert values into a binary search tree, check the shape it builds, and read its values back in sorted order.

Adapted from Exercism problem-specifications (MIT), exercises/binary-search-tree.

## Choices

- The tree is a generic enum, `Tree<T>`: a `Leaf`, or a `Node` with named `left`, `value` and `right` fields. It is a value, so `insert` answers a new tree that shares the subtrees it did not change.
- `insert` is a `case` with a guard: an empty tree becomes a node, a value at most the node's goes left (`when value <= here`), and anything else goes right. That puts an equal value on the left, as the canonical "same number at left node" case asks.
- `from` builds a tree by `Iter.reduce` over the values from an empty tree, and `sorted` reads it in order, left subtree, value, right subtree.
- The functions are generic over any `T: Comparable`. The canonical data writes the values as strings (`"4"`), to stay out of each language's number types; the tests use `Int`s, which is what the strings stand for.
- `derive Equatable for Tree<T>` lets the shape cases compare the built tree with the expected one, written as nested `#.Node{...}` literals.
- The functions are in `impl Tree<T>`, called `Tree.from(values)` and `Tree.sorted(tree)` after `import binary_search_tree.Tree`. Each names the block's `T`, so a call takes it from its tree or its values, and `where T: Comparable` narrows the two that compare.
- An empty tree is `.Leaf` wherever a `Tree<T>` is expected: in `insert`'s new node and in every expected tree of the tests. `from` starts its `Iter.reduce` from `Tree.Leaf`, qualified, since nothing there says what type the starting value has.
- The two canonical properties are two `subtests` tables. The shape table names the enum once, in its first row's `#Tree.Node{...}`; the bare `#.Node{...}` and `.Leaf` in the later rows and inside each node take `Tree<Int>` from it.

## Friction

None.

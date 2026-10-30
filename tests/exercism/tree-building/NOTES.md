# Tree Building

Subject: Collections & Algorithms

Rebuild a forum's tree of posts from unordered records of an id and a parent id, rejecting records that do not form a tree.

Adapted from Exercism problem-specifications (MIT), exercises/tree-building.

## Choices

- The exercise is a refactoring of slow code that each track supplies; there is no slow version here, only the tree built directly.
- `Record` and `Node` are structs. `Node`'s `children` defaults to `[]`, so a leaf is `#{id}`, and `derive Equatable` lets the tests compare whole trees.
- `build_tree` returns `Result<Maybe<Node>, Error>`: no records is `Ok(None)`, the canonical empty answer, not an error. `Error` has two variants for the canonical two messages, given by its `Display`.
- The records are sorted by id once. They are valid ids exactly when the sorted record at each index has that index as its id, which `Iter.with_index` and `Iter.all` check in one pass; that catches a missing root, a duplicate and a gap alike.
- The canonical data splits the remaining rules oddly, and the solution follows it: a root whose parent is not 0 is the "parent_id should be smaller" error, but any other record whose parent is not lower than its id, a cycle included, is "record id is invalid or out of order". These are three arms of one `case` on the sorted list, two with `when` guards.
- Once every parent is lower than its child, the records cannot form a cycle, so the tree is built by `Iter.group_by` on the parent, which keeps each parent's children in id order, and a recursive `node` from the root.
- The valid and invalid cases are two `subtests` tables. The invalid table asserts each error's message, which is what the canonical data gives. `leaf` keeps the expected trees short.

## Friction

None.

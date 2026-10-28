# Relative Distance

Subject: Collections & Algorithms

Find the degree of separation between two people in a family tree, where a step joins a parent and a child or two siblings.

Adapted from Exercism problem-specifications (MIT), exercises/relative-distance.

## Choices

- The family tree is the canonical data's shape, a `Map<String, List<String>>` from each parent to their children, and the answer is `Maybe<Int>`, with `None` for two people no steps join (the canonical `null`).
- `relatives` turns the tree into each person's set of people one step away: every parent and child both ways, and every pair of children of one parent. Each link is a `#(person, relative)` pair; `Iter.group_by` on the person and `Map.map_values` to a set of relatives finish it. Siblings are linked directly, as the instructions ask, so a sibling is one step, not two through the parent.
- The search is breadth first, a tail-recursive function over the frontier, the set of people seen and the distance, as in two-bucket. The first frontier holding the target is reached in the fewest steps, and a frontier that adds nobody new means no steps join them.
- The seven canonical cases are one `subtests` table. The three complex cases use two large trees, written once each as a `once` above the test: the first two cases share one tree, and the last uses a variant of it with one branch cut off.

## Friction

None.

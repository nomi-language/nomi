# Knapsack

Subject: Collections & Algorithms

Choose items, each at most once, to carry the greatest total value without going over a weight limit.

Adapted from Exercism problem-specifications (MIT), exercises/knapsack.

## Choices

- An item is a struct, `Item`, with the canonical data's `weight` and `value` fields, and the test rows write each as `#{weight: 2, value: 5}` under an annotated table.
- The answer is the 0/1 knapsack's dynamic programming: a `Vector` of the best value for every capacity from 0 to the limit, rebuilt once per item by `consider`. The capacity is the index, so `Iter.with_index` reads it beside the value. Trying every subset would be 2^15 subsets for the largest case; the table is 15 rows of 751 entries.
- Each row is a new `Vector` built from the one before it, read through `best`, so no entry is overwritten before a later capacity reads it, the trap an in-place version must avoid by running the capacities downwards.
- The fold names its step, `Iter.reduce(items, consider, from: nothing)` (style §27), and the empty row is `Iter.repeat(0)` cut to the limit plus one.
- The seven current canonical cases are one `subtests` table named by the description column. The first "no items" case, whose items are a JSON object, is left out: the second, with an empty list, reimplements it.

## Indexing

Two indexed reads.

| Read | Where | Result | Real use for a miss? |
|---|---|---|---|
| `Vector.at(best, capacity - item.weight)` | `consider` | the best value without the item's weight | No: the branch runs only when `capacity >= item.weight`, and the row has an entry for every capacity below `capacity` |
| `Vector.at(best, maximum_weight)` | `maximum_value` | the answer | No: every row has `maximum_weight + 1` entries |

## Friction

None.

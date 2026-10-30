# Change

Subject: Collections & Algorithms

Find the fewest coins, from an unlimited supply of given values, that add up to an amount of change.

Adapted from Exercism problem-specifications (MIT), exercises/change.

## Choices

- The answer is dynamic programming over the amounts from 0 to the target. `Iter.reduce` builds a `Vector` whose entry at each amount is the fewest coins that make it, or `None` when none do; `best` reads the entries for `amount - coin` for each coin that fits, adds the coin, and keeps the shortest with `Iter.min_by(Iter.count)`. A greedy pass is wrong for the canonical case `[1, 10, 11]` making 20, and a search over combinations grows with the target; the table is `target × coins` steps, about 7,000 for the largest case.
- Each entry is the coin list itself, not a count with a back pointer, so the answer needs no reconstruction pass. The lists share their tails, since each is a coin consed onto an earlier entry.
- The answer is sorted at the end with `Iter.sort`, smallest coin first, as the canonical data lists it.
- The two canonical errors are an `Error` enum, `NegativeTarget` and `CannotMakeTarget`, rather than the canonical message strings. A negative target returns early, before the table, since `1..=target` would be empty and answer the 0 entry.
- The 13 canonical cases are one `subtests` table named by the description column, annotated so the `Ok` and `Err` rows share a type.

## Indexing

Two indexed reads, both `Vector.get(...)!`.

| Read | Where | Result | Real use for a miss? |
|---|---|---|---|
| `Vector.get(table, amount - coin)!` | `best` | the fewest coins for the rest, a `Maybe` | No: the filter keeps `coin <= amount`, and the table holds every amount below `amount` |
| `Vector.get(fewest, target)!` | `find_fewest_coins` | the answer, a `Maybe` | No: the table holds every amount from 0 to `target`, and a negative target has returned |

## Friction

None.

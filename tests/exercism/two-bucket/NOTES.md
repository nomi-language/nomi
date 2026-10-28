# Two Bucket

Subject: Games & Simulation

Find the fewest actions that measure an exact number of liters with two buckets, starting by filling a given one, and never leaving the starting bucket empty with the other full.

Adapted from Exercism problem-specifications (MIT), exercises/two-bucket.

## Choices

- `measure` returns `Result<Outcome, Error>`. `Outcome` is a struct of the three canonical answers, the number of actions, the bucket that holds the goal and the liters in the other, and `Error.Impossible` is the canonical error.
- A state is the liters in each bucket, a `#(Int, Int)` named `State` with a `typealias`. The buckets are positions in the tuple, so filling, emptying and pouring are tuple arithmetic in `actions`.
- `search` is a breadth-first search, written as a tail-recursive function over three values: the frontier of states the current number of actions reaches first, the set of states seen so far, and the number of actions. Breadth first, the first frontier with the goal in either bucket is reached in the fewest actions.
- The forbidden state, the starting bucket empty and the other full, goes into the seen set before the search starts, so no action ever leads to it, with no check of its own.
- A frontier that adds no new state means the goal cannot be reached, which is `Error.Impossible`; that covers a goal larger than both buckets with no case of its own.
- The answer reads the bucket from the state that reaches the goal, with a guard, `Some(#(one, two)) when one == goal`, and a second arm for bucket two.
- `Iter.loop` would not do here: a loop breaks with a value of its state's type, and the search ends with a `Result`.
- The 11 canonical cases are one `subtests` table, annotated so each expected `Ok(#{...})` is an `Outcome`; the bucket sizes are one `#(one, two)` column, taken apart in the row pattern.

## Friction

None.

# Sum of Multiples

Sum the distinct multiples, below a limit, of any of a list of factors.

Adapted from Exercism problem-specifications (MIT), exercises/sum-of-multiples.

## Choices

- The solution follows the instructions' wording: the multiples of each factor (`Range.step_by(factor..limit, factor)`), combined into a `Set` to drop duplicates, then summed with `Iter.sum`. Filtering `1..limit` by "divisible by any factor" was the other natural form; the set form says what the problem says.
- A factor of 0 is filtered out before stepping, since it has no positive multiples and `step_by` by 0 would not end.
- One `subtests` table for all 16 cases.

## Friction

None.

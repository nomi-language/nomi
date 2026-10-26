# Difference of Squares

Subject: Numbers & Math

Find the difference between the square of the sum and the sum of the squares of the first N natural numbers.

Adapted from Exercism problem-specifications (MIT), exercises/difference-of-squares.

## Choices

- Both sums iterate the range `1..=n` with `Iter.sum`, rather than using the closed forms `n(n+1)/2` and `n(n+1)(2n+1)/6`. The ranges say what is summed, and the inputs are small.
- `square_of_sum` binds the sum and multiplies it by itself, since there is no integer power.
- The squares are `fn k -> k * k`, a lambda rather than a capture, because the body uses its parameter twice (style §31).
- Each canonical group is a `group` holding one `subtests` table.

## Friction

None.

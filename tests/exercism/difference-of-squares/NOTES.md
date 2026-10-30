# Difference of Squares

Subject: Numbers & Math

Find the difference between the square of the sum and the sum of the squares of the first N natural numbers.

Adapted from Exercism problem-specifications (MIT), exercises/difference-of-squares.

## Choices

- Both sums iterate the range `1..=n` with `Iter.sum`, rather than using the closed forms `n(n+1)/2` and `n(n+1)(2n+1)/6`. The ranges say what is summed, and the inputs are small.
- A square is `Int.pow` with 2: `square_of_sum` pipes the sum into `Int.pow(2)`, and `sum_of_squares` maps each number with the capture `&Int.pow(&1, 2)`.
- Each canonical group is a test named by the group's description, holding one `subtests` table; a `group` block is for shared `clock`, `boot` or `setup`, which none needs.

## Friction

None.

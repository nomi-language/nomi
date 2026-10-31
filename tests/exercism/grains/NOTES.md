# Grains

Subject: Numbers & Math

Count the grains of wheat on a chessboard square, doubling from one grain on the first, and on the whole board.

Adapted from Exercism problem-specifications (MIT), exercises/grains.

## Choices

- The counts are `Int`s. The 64th square holds 2^63 grains and the board 2^64 - 1, past 64 bits, and an `Int` is exact at any size.
- A square's grains are `Int.pow(2, square - 1)` (style §26), which says what the count is. `1 <<< (square - 1)` is the same number.
- `total` sums every square rather than using `2^64 - 1`, so it says what the board holds.
- A square off the board is `Err` with the canonical message, a `String`, as collatz-conjecture does. There is one error, so an `Error` enum would add a type for one variant.
- The seven counts are one `subtests` table and the three bad squares another, since they assert different shapes; both sit in the canonical group.

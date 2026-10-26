# Grains

Subject: Numbers & Math

Count the grains of wheat on a chessboard square, doubling from one grain on the first, and on the whole board.

Adapted from Exercism problem-specifications (MIT), exercises/grains.

## Choices

- The counts are `Decimal`s. The 64th square holds 2^63 grains and the board 2^64 - 1, and neither fits in a 64-bit `Int`. `Decimal`'s `+` and `*` are exact at any size.
- A square's grains are a fold over the squares before it, `Iter.reduce(2..=square, fn grains = 1d, _ -> grains * 2d)`. `Int.shift_left(1, square - 1)` would be the obvious form if the result fit in an `Int`.
- `total` sums every square rather than using `2^64 - 1`, so it says what the board holds.
- A square off the board is `Err` with the canonical message, a `String`, as collatz-conjecture does. There is one error, so an `Error` enum would add a type for one variant.
- The seven counts are one `subtests` table and the three bad squares another, since they assert different shapes; both sit in the canonical group.

## Friction

- [feature] There is no integer type wider than 64 bits, and no unsigned one, so the grain counts are `Decimal` and the tests write them as `9223372036854775808d`. A count of grains is a whole number, and `Decimal` also allows `0.5d`. Rust has `u64` and `u128`, Go `uint64` and `math/big`, and Elixir and Python integers are unbounded.

# Armstrong Numbers

Subject: Numbers & Math

Decide whether a number equals the sum of its digits, each raised to the power of the digit count.

Adapted from Exercism problem-specifications (MIT), exercises/armstrong-numbers.

## Choices

- The digits come from `Int.to_digits`, rather than from `"${number}"` and `String.to_int` per grapheme. The arithmetic stays in `Int` and needs no `Maybe` to unwrap, and `Int.to_digits(0)` is `[0]`, so zero needs no early return.
- Each digit is raised with `Int.pow` in a capture, `&Int.pow(&1, width)`.
- The sum is bound as `total` and compared on its own line, rather than piped into a `then &(&1 == number)` stage.
- The nine cases that fit in `Int` are one `subtests` table. The canonical "Armstrong number containing seven zeroes" (33 digits) and "The largest and last Armstrong number" (39 digits) are left out, since neither fits in a 64-bit `Int`; see Friction.

## Friction

- [feature] There is no integer type wider than 64 bits, so two canonical cases cannot be written as an `Int`. `Decimal` is arbitrary-precision and could hold them, but an `armstrong?` that takes a `Decimal` would accept `1.5d`. Python's `int` and Elixir's integers are unbounded, and Rust and Go have `u128` and `math/big`.

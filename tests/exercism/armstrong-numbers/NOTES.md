# Armstrong Numbers

Subject: Numbers & Math

Decide whether a number equals the sum of its digits, each raised to the power of the digit count.

Adapted from Exercism problem-specifications (MIT), exercises/armstrong-numbers.

## Choices

- The digits come from arithmetic, `Iter.iterate(number, &(&1 / 10))` cut at zero and taken `% 10`, rather than from `"${number}"` and `String.to_int` per grapheme. The arithmetic stays in `Int` and needs no `Maybe` to unwrap.
- Zero is its own early return, `[0]`: the iterate chain gives no digits for it.
- The digits come out least significant first. The sum does not care about order, so they are not reversed.
- The sum is bound as `total` and compared on its own line, rather than piped into a `then &(&1 == number)` stage.
- The nine cases that fit in `Int` are one `subtests` table. The canonical "Armstrong number containing seven zeroes" (33 digits) and "The largest and last Armstrong number" (39 digits) are left out, since neither fits in a 64-bit `Int`; see Friction.

## Friction

- [feature] There is no integer type wider than 64 bits, so two canonical cases cannot be written as an `Int`. `Decimal` is arbitrary-precision and could hold them, but an `armstrong?` that takes a `Decimal` would accept `1.5d`. Python's `int` and Elixir's integers are unbounded, and Rust and Go have `u128` and `math/big`.
- [stdlib] There is no integer power. `power` is written by hand as `Iter.repeat(base) |> Iter.take(exponent) |> Iter.reduce(fn product = 1, n -> product * n)`. Rust has `i64::pow`, Elixir `Integer.pow/2`, Gleam `int.power` (over floats).
- [stdlib] There is no function for a number's digits. `digits_of` takes five lines. Elixir has `Integer.digits/2`.

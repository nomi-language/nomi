# Luhn

Decide whether a string of digits and spaces passes the Luhn checksum.

Adapted from Exercism problem-specifications (MIT), exercises/luhn.

## Choices

- Each grapheme goes through `String.to_int`, and `Maybe.collect` turns "every one is a digit" into one `Maybe<List<Int>>`, so a stray character and the digit conversion are one step.
- The length rule is a `when` guard on the `Some` arm rather than a separate `if`.
- `checksum` reverses the digits and doubles odd indexes from `Iter.with_index`, rather than tracking a parity flag in a fold.
- All 22 cases are one `subtests` table whose first column is the canonical description.

## Friction

- [stdlib] No `Iter.sum`; the checksum ends in `Iter.reduce(|sum = 0, d| sum + d)`. Rust has `Iterator::sum`, Elixir `Enum.sum/1`, Gleam `int.sum`.

# Armstrong Numbers

Subject: Numbers & Math

Decide whether a number equals the sum of its digits, each raised to the power of the digit count.

Adapted from Exercism problem-specifications (MIT), exercises/armstrong-numbers.

## Choices

- The digits come from `Int.to_digits`, rather than from `"${number}"` and `String.to_int` per grapheme. The arithmetic stays in `Int` and needs no `Maybe` to unwrap, and `Int.to_digits(0)` is `[0]`, so zero needs no early return.
- Each digit is raised with `Int.pow` in a capture, `&Int.pow(&1, width)`.
- The sum is bound as `total` and compared on its own line, rather than piped into a `then &(&1 == number)` stage.
- The eleven canonical cases are one `subtests` table, the 33-digit and 39-digit Armstrong numbers included: an `Int` is exact at any size, so `is_armstrong` takes them as it takes 153.

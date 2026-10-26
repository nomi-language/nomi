# Largest Series Product

Subject: Numbers & Math

Find the largest product of a given number of adjacent digits in a string of digits.

Adapted from Exercism problem-specifications (MIT), exercises/largest-series-product.

## Choices

- Errors are an `Error` enum, one variant per canonical message. The checks run in the order the canonical errors imply: a negative span, then a character that is not a digit, then a span longer than the digits.
- The digits become a `List<Int>` in one pipe: `String.to_codepoints`, `Codepoint.to_digit` on each and `Maybe.collect`, whose `None` is the not-a-digit error through a binding `else`.
- The span check computes `last_start`, the last position a series can start at, and refuses a negative one, so the range of starts `0..=last_start` that follows is never empty.
- The largest product is an `Iter.reduce` from 0 rather than `Iter.max`: every product is at least 0 and there is always one series, so 0 is a safe floor, and `Iter.max`'s `Maybe` would need a default for a case that cannot happen.
- A span of 0 needs no case of its own: each start takes no digits, and the empty product is 1.
- The canonical cases are two `subtests` tables, the products and the rejections, since the rows of each share an assertion.

## Friction

- [stdlib] There is no sliding window over a list, so each series is `numbers |> Iter.drop(start) |> Iter.take(span)` for each start. Rust has `slice.windows(n)`, Gleam `list.window(xs, n)` and Elixir `Enum.chunk_every(xs, n, 1, :discard)`.
- [stdlib] There is no product of an `Iter`, the counterpart of `Iter.sum`, so `product` is `Iter.reduce(numbers, fn total = 1, n -> total * n)`. Rust has `Iterator::product`, Elixir `Enum.product/1` and Gleam `int.product`.

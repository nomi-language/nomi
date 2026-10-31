# Largest Series Product

Subject: Numbers & Math

Find the largest product of a given number of adjacent digits in a string of digits.

Adapted from Exercism problem-specifications (MIT), exercises/largest-series-product.

## Choices

- Errors are an `Error` enum, one variant per canonical message. The checks run in the order the canonical errors imply: a negative span, then a character that is not a digit, then a span longer than the digits.
- The digits become a `List<Int>` in one pipe: `String.to_codepoints`, `Codepoint.to_digit` on each and `Maybe.collect`, whose `None` is the not-a-digit error through a binding `else`.
- The span check compares the span with the digit count, before any series is built, since `Iter.windows` yields no series for a span longer than the digits and the error must be told apart from a product.
- Each series is a window from `Iter.windows(span)`, and its product is `Iter.product`, passed to `Iter.map` as a function value.
- The largest product is `Iter.max()!`. The span is checked against the digit count first, so there is always one series and the `None` would be a bug (style §28). An `Iter.reduce` from 0 also works, since no product is negative, but it spells out what `Iter.max` names.
- A span of 0 returns 1, the empty product, before the windows: `Iter.windows(0)` yields no window, as a non-positive size does in `Iter.chunks`, where the exercise wants one empty series.
- The canonical cases are two `subtests` tables, the products and the rejections, since the rows of each share an assertion.

## Friction

None.

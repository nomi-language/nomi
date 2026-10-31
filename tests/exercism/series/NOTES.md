# Series

Subject: Strings & Text

List every run of adjacent digits of a given length in a string of digits, in order.

Adapted from Exercism problem-specifications (MIT), exercises/series.

## Choices

- Errors are an `Error` enum with one variant per canonical message. The checks run in the order the canonical cases need: an empty series is `EmptySeries` even when the length is also too large.
- The validation is a `case` with no subject, since each condition compares different values.

- The runs are `Iter.windows(series, length)` over the string's graphemes, each joined back into a string. The function works on graphemes, not only digits.
- The test file splits the canonical cases into two `subtests` tables, one for `Ok` results and one for errors, so each table makes one claim. The test binds the input as `digits`, since `series` is the module's name there.

## Friction

None.

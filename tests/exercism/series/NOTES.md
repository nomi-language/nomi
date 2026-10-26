# Series

Subject: Strings & Text

List every run of adjacent digits of a given length in a string of digits, in order.

Adapted from Exercism problem-specifications (MIT), exercises/series.

## Choices

- Errors are an `Error` enum with one variant per canonical message. The checks run in the order the canonical cases need: an empty series is `EmptySeries` even when the length is also too large.
- The validation is an `if`/`else if` chain, since each condition compares different values; a `case` on `length` would need a guard on every arm.
- Each run is `String.slice(series, start, start + length)` for every start in `0..=size - length`. The function works on graphemes, not only digits.
- The test file splits the canonical cases into two `subtests` tables, one for `Ok` results and one for errors, so each table makes one claim. The test binds the input as `digits`, since `series` is the module's name there.

## Friction

- [stdlib] There is no sliding window over an `Iter` or a list, so the runs come from a range of start indices and `String.slice`. Rust has `slice::windows(n)`, Gleam `list.window(xs, n)`, and Elixir `Enum.chunk_every(xs, n, 1, :discard)`. The proverb exercise pairs neighbours with `Iter.zip(xs, Iter.drop(xs, 1))` for the same reason.

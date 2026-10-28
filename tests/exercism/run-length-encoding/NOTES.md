# Run-Length Encoding

Subject: Encoding & Ciphers

Encode runs of repeated characters as a count and the character, and decode them back.

Adapted from Exercism problem-specifications (MIT), exercises/run-length-encoding.

## Choices

- `encode` is `Iter.chunk_by` on the string's graphemes, then each run written out by `encode_run`, then `String.join`.
- `decode` reads the runs with ``String.captures_all(Regex`(\d*)(\D)`)``: an optional count, then one character. `decode_run` repeats the character `count` times, or once when the count group matched empty. The other way is an `Iter.reduce` over graphemes that threads `#(count, text)` and builds the count digit by digit; the pattern is clearer because it states the encoded format in one line, where the reducer spreads it over a tuple accumulator and an `if count == 0` fallback. The cost is that `\D` matches one code point, not one grapheme, so a run of a character written with a combining mark would decode wrongly; the canonical cases are all single code points.
- Each canonical group is a test named by the group's description, holding one `subtests` table; the round-trip case is a plain test.
- The arms the types require but the code never reaches are `unreachable` with the invariant as the reason (style §28): `[] -> unreachable "chunk_by never yields an empty run"` in `encode_run`, and the `_` arm of `decode_run`, which the pattern's two always-participating groups rule out. A made-up `""` there would hide a bug in either.

## Friction

- [verbose] `Iter.chunk_by` returns each run as a `List<T>`, which is never empty, but `case run` must still handle `[]` to be exhaustive. Its arm is an `unreachable`, which says what is true, but it is still an arm to write. Rust's `chunk_by` yields slices with the same problem; Gleam's `list.chunk` too. Elixir's `Enum.chunk_by` is untyped. A non-empty list type, or `chunk_by` yielding `#(first, rest)`, would remove the arm.
- [verbose] `String.captures_all` returns each match's groups as a `List<Maybe<String>>`, so `decode_run` must handle shapes the pattern `(\d*)(\D)` cannot produce, with an `unreachable` arm: the type says neither that there are two groups nor that both always take part. Rust's `captures_iter` and Go's `FindAllStringSubmatch` have the same gap; a typed capture (a tuple per pattern) would remove the arm.

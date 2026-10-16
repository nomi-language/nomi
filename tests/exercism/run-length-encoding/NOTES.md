# Run-Length Encoding

Encode runs of repeated characters as a count and the character, and decode them back.

Adapted from Exercism problem-specifications (MIT), exercises/run-length-encoding.

## Choices

- `encode` is `Iter.chunk_by` on the string's graphemes, then each run written out by `encode_run`, then `String.join`.
- `decode` is one `Iter.reduce` over graphemes threading `(count, text)`: a digit (`String.to_int` of the grapheme) extends the count, and any other character is repeated `count` times, or once when no count was written.
- The canonical groups are `group` blocks, each holding one `subtests` table; the round-trip case is a plain test in its own group.

## Friction

- [verbose] `Iter.chunk_by` returns each run as a `List<T>`, which is never empty, but `case run` must still handle `[]` to be exhaustive (`[] -> ""` in `encode_run`). Rust's `chunk_by` yields slices with the same problem; Gleam's `list.chunk` too. Elixir's `Enum.chunk_by` is untyped. A non-empty list type, or `chunk_by` yielding `(first, rest)`, would remove the arm.
- [stdlib] There is no regular-expression capture, so `decode` cannot split `12W` into `12` and `W` with a pattern; `Regex` offers `find` and `replace_all` only. Go has `FindAllStringSubmatch`, Rust's `regex` has `captures_iter`, Elixir `Regex.scan`. The hand-written digit accumulator is short, so this cost little here.

# Hamming

Count the positions at which two DNA strands differ; strands of different
lengths are an error.

Adapted from Exercism problem-specifications (MIT), exercises/hamming.

## Choices

- The result is `Result<Int, String>` with the canonical message as the
  error, rather than a one-variant error enum. There is one failure and its
  message is the useful part; an enum with a single variant would be a name
  for the same string.
- The length check is an early `return Err(...)`, and the count is a
  `zip`, `filter`, `count` pipeline ending in `|> Ok()`. Strings iterate by
  grapheme, so `Iter.zip(strand1, strand2)` pairs the letters directly.
- Every reimplemented error case uses its newest version, all with the
  message "strands must be of equal length". That leaves nine cases: five
  distances and four errors.

## Friction

None.

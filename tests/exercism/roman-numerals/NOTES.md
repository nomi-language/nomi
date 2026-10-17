# Roman Numerals

Write a number from 1 to 3999 in Roman numerals.

Adapted from Exercism problem-specifications (MIT), exercises/roman-numerals.

## Choices

- The numerals, subtractive pairs included, are a `once` table of `#(value, symbol)` tuples, largest first.
- One `Iter.reduce` over the table threads `#(left, text)`: each symbol is repeated `left / value` times and `left` becomes `left % value`. The seed and element are both tuple patterns in the lambda header.
- No validation of the range, since the canonical data has no error cases.
- One `subtests` table for all 27 cases.

## Friction

None.

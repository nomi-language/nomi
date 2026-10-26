# Roman Numerals

Subject: Encoding & Ciphers

Write a number from 1 to 3999 in Roman numerals.

Adapted from Exercism problem-specifications (MIT), exercises/roman-numerals.

## Choices

- The numerals, subtractive pairs included, are a `once` table of `#(value, symbol)` tuples, largest first.
- One `Iter.map_reduce` over the table: each numeral's output is its symbol repeated `left / value` times, and the state carried to the next is `left % value`. The parts are joined with `String.join`. An `Iter.reduce` threading `#(left, text)` and concatenating was the other form; style §27 picks `map_reduce` when each element has its own output and the state only carries what the next one needs.
- No validation of the range, since the canonical data has no error cases.
- One `subtests` table for all 27 cases.

## Friction

None.

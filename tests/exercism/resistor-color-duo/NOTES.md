# Resistor Color Duo

Subject: Encoding & Ciphers

Read a resistor's first two color bands as a two-digit number, ignoring any band after them.

Adapted from Exercism problem-specifications (MIT), exercises/resistor-color-duo.

## Choices

- `value` takes the bands as a `List<Color>`, since one canonical case passes three, and matches `[first, second, ..]`.
- Fewer than two bands answer `None`, so `value` returns `Maybe<Int>`. The canonical data never gives fewer; the test file adds one such case, marked as not canonical. A band list is data the caller may have read from anywhere, so a missing band is an answer rather than a trap (style §28).
- The `Color` enum and its `digit` table are repeated from resistor-color: each exercise stands alone.
- The table qualifies only the first row's first color (`[Color.Brown, .Black]`); every later bare variant, in its own row's list, takes that enum.

## Friction

None.

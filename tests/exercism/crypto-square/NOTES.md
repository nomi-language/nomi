# Crypto Square

Subject: Encoding & Ciphers

Encode a message by writing its letters and digits in the rows of a near-square rectangle and reading it down the columns.

Adapted from Exercism problem-specifications (MIT), exercises/crypto-square.

## Choices

- The rectangle is never a grid of cells. The normalized text is padded with spaces to fill the rectangle (`String.pad_end`), cut into rows with `Iter.chunks`, and turned into columns with `Iter.transpose`; each column is joined into a word and the words are joined with spaces. The padding is where the canonical trailing spaces come from.
- The width is the integer square root rounded up, `Int.sqrt` plus one when the length is not a square, and the height is the length divided by the width, rounded up. That pair is the smallest rectangle with the width at most one more than the height.
- Normalizing works on codepoints: `String.to_lower`, then keep each `Codepoint.letter?` or `Codepoint.digit?`.
- Empty text returns `""` before the width is worked out, so nothing divides by a width of zero.
- The canonical cases are one `subtests` table. The canonical data replaces its "54 character plaintext results in 7 chunks" case with one that says 8 chunks and expects the same text (`reimplements`); the table holds only the replacement.

## Friction

None.

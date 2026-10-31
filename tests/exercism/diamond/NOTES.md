# Diamond

Subject: Strings & Text

Draw a diamond of letters from `A` out to a given letter and back.

Adapted from Exercism problem-specifications (MIT), exercises/diamond.

## Choices

- `rows` takes the widest letter as a `Codepoint`, so `'A'..=widest` is the list of letters with no conversion. The canonical input is a one-letter string; `'C'` says the same and cannot be two letters.
- The top half is built row by row from `Iter.with_index`, and the bottom half is the top reversed without its last row, joined as `[..top, ..bottom]`.
- `row` pads with `String.repeat`. The first row is its own branch, since it holds one letter rather than two.
- The five cases are plain `test` blocks rather than a `subtests` table. Each expected value is a list of up to 51 rows, which reads better under its own test name than as a table row.

## Friction

None.

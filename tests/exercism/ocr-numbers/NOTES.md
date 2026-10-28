# OCR Numbers

Subject: Parsing & Validation

Read the digits a scanner drew with pipes and underscores, three columns and four rows per digit, and join several lines of them with commas.

Adapted from Exercism problem-specifications (MIT), exercises/ocr-numbers.

## Choices

- `convert` returns `Result<String, Error>`. The two canonical errors, a row count that is not a multiple of four and a row width that is not a multiple of three, are the `Error` variants `LinesNotMultipleOfFour` and `ColumnsNotMultipleOfThree`. A drawing that is no digit is not an error: it reads as `?`, as the canonical data asks.
- The digits are not a hand-written table of twelve-character keys. `font` is the ten digits drawn the way the input draws them, and `digits` reads the font with the same `glyphs` function the input goes through, zipped with `"0123456789"` into a map. The font and the reader cannot disagree.
- `glyphs` cuts each row into three-column cells (`Iter.chunks(3)` over the row's graphemes, each cell joined back into a `String`), then `Iter.transpose` turns four rows of cells into the cells of each digit, top to bottom, and each digit's cells are joined into one key.
- The lines are `Iter.chunks(4)` over the rows, each read with `read_line` and joined with commas. A cell drawing missing from `digits` is the `?` the canonical data expects, so `Maybe.with_default("?")` is the rule, not a guess.
- `String.join` is passed as a function, `Iter.map(String.join)`; its separator defaults to `""`.
- The 17 canonical cases are one `subtests` table. Each row's drawing is a list written one string per line, so the rows read as the picture they are.

## Friction

None.

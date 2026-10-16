# Minesweeper

Write the count of adjacent mines into each empty square of a minesweeper board.

Adapted from Exercism problem-specifications (MIT), exercises/minesweeper.

## Choices

- The board stays `List<String>` in and out. Mines are collected once into a `Set<(Int, Int)>` of coordinates, so counting neighbours is eight set lookups and there is no grid indexing or bounds check.
- Rows are iterated as strings of graphemes (`Iter` over `String`), comparing squares with `"*"`, not as codepoints; style §19 keeps text in strings.
- The eight offsets are a `once` literal rather than generated from `-1..=1` twice with the centre removed.
- Each canonical case is its own `test`, with the board bound to `minefield` before the assertion.

## Friction

- [verbose] `nomi fmt` joins a list written one item per line whenever it fits in 100 columns, so a board loses its grid shape. The boards were written one row per line; after formatting, "large minefield" has its input on one line and its expected output stacked, because only the second overflowed:

  ```nomi
  minefield = [" *  * ", "  *   ", "    * ", "   * *", " *  * ", "      "]

  assert minesweeper.annotate(minefield) == [
      "1*22*1",
      ...
  ```

  gofmt keeps a composite literal's line breaks as written. The only ways to keep the rows stacked are a comment inside the list or a `"""` string split with `String.lines`, whose rows end in significant spaces.

# Minesweeper

Write the count of adjacent mines into each empty square of a minesweeper board.

Adapted from Exercism problem-specifications (MIT), exercises/minesweeper.

## Choices

- The board stays `List<String>` in and out. Mines are collected once into a `Set<(Int, Int)>` of coordinates, so counting neighbours is eight set lookups and there is no grid indexing or bounds check.
- Rows are iterated as strings of graphemes (`Iter` over `String`), comparing squares with `"*"`, not as codepoints; style §19 keeps text in strings.
- The eight offsets are a `once` literal rather than generated from `-1..=1` twice with the centre removed.
- Each canonical case is its own `test`, with the board bound to `minefield` before the assertion.
- Every board of more than one row, input and expected output, is written one row per line. `nomi fmt` keeps a list broken after its `[` that way even when it would fit on one line.

## Friction

None.

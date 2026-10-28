# Killer Sudoku Helper

Subject: Numbers & Math

List every way a killer sudoku cage can be filled: the sets of different digits 1 to 9, of the cage's size, that add up to its sum, leaving out digits already used.

Adapted from Exercism problem-specifications (MIT), exercises/killer-sudoku-helper.

## Choices

- `combinations(sum, size, exclude:)` takes the excluded digits as a parameter that defaults to `[]`, since most canonical cases exclude none. It filters them out of `1..=9`, takes every choice of `size` of the rest with `Iter.combinations`, and keeps those that add up to `sum`.
- `Iter.combinations` keeps the source's order and walks the choices in lexicographic order of position, so from increasing digits each combination is increasing and the list is in lexicographic order, the order the canonical data writes, with no sort. Nine digits give at most 126 choices, so checking each sum costs nothing worth pruning.
- The nine one-digit cages are one `subtests` table named by the digit, as the canonical data names them; the four other cases are plain tests.

## Friction

None.

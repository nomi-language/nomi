# Piecing It Together

Subject: Numbers & Math

Fill in the details of a jigsaw puzzle from some of them (its pieces, border and inside pieces, rows, columns, aspect ratio and format), or say that the details given are too few or contradict each other.

Adapted from Exercism problem-specifications (MIT), exercises/piecing-it-together.

## Choices

- What is known is a `Clues` struct whose seven fields are `Maybe`s that default to `None`, so a case writes only what it knows: `#{rows: Some(32), format: Some(.Square)}`. The answer is a `Jigsaw` with every field filled, or `Error.InsufficientData` or `Error.ContradictoryData`.
- A puzzle is settled by its rows and columns, so `jigsaw_data` searches them rather than solving each combination of clues by its own formula. `row_candidates` gives the known row count, or every count up to a bound some known part sets (the pieces, the border, the inside pieces plus two, or the columns with a ratio or a format that keeps the rows at most the columns). `column_rule` answers a function from a row count to the column counts worth trying, from the first known part that settles the columns. `puzzle` builds the whole puzzle from rows and columns, and `fits` keeps those that agree with every clue. A clue agrees when it is `None` or matches, which is `Iter.all` over the `Maybe`, an `Iter` of none or one value.
- The search stops at the second fitting puzzle with `Iter.take(2)`: none is a contradiction, one is the answer, two are too many. When nothing bounds the rows or settles the columns, endlessly many puzzles fit and the answer is `InsufficientData` before any search.
- The aspect ratio is compared within 1e-9, since it is a `Float` computed as columns over rows and the canonical data writes 5/3 as `1.6666666666666667`.
- The five complete puzzles are one `subtests` table named by the description column; the two errors are plain tests.

## Friction

None.

# Word Search

Subject: Grids & Matrices

Find where each of a list of words lies in a square of letters, reading in any of the eight directions, by its first and last letter.

Adapted from Exercism problem-specifications (MIT), exercises/word-search.

## Choices

- The answer is a `Map` from each word to `Maybe<Span>`, as the canonical data maps each word to a location or `null`. A `Span` holds two `Position`s, each a struct with the canonical `column` and `row`, counted from 1.
- The grid is a `Vector` of rows, each a `Vector` of the row's letters as one-grapheme strings (`Iter.to_vector` over a `String`), so a letter is read by its row and column.
- `find` is one lazy pipeline: every cell, then every one of the eight directions from it, then `Iter.find` for the first pair from which the word reads, with a `?` after it that answers `None` when no pair does. The directions are a `once` list of row and column steps, so the diagonals and the reversed words need no code of their own.
- `reads` walks the word's letters with their index and compares each with the letter that many steps away. `letter_at` answers `None` off the grid, so a word that would run past an edge, or wrap to the next line, simply does not read; that is what the four "should not" cases check.
- The canonical cases are one `subtests` table named by the description column. Ten of them search the same ten-line puzzle, which is a `once` in the test file; the grids that differ from it are written in their rows.

## Indexing

Two indexed reads, both in `letter_at`; no indexed update.

| Read | Where | Result | Real use for a miss? |
|---|---|---|---|
| `letters \|> Vector.get(row)?` | `letter_at` | compared `== Some(letter)` in `reads` | Yes: a step can go above the first row or below the last |
| `\|> Vector.get(column)` | `letter_at` |
 same | Yes: a step can go left of the first column or right of the last |

## Friction

None.

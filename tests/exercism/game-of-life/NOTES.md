# Game of Life

Compute the next generation of a Conway's Game of Life board.

Adapted from Exercism problem-specifications (MIT), exercises/game-of-life.

## Choices

- The board is `Vector<Vector<Int>>` (`typealias Board`), with cells as the canonical `0` and `1`. A `Bool` or a `Cell` enum would read better in the solution, but the test grids of `#[1, 0, 1]` read as the board; `next_state` is the only place that knows `1` means alive.
- Neighbours are counted by indexed reads (`cell_at`), with an off-board neighbour read as `None` and counted as dead by `Maybe.with_default(0)`. A `Set<(Int, Int)>` of live cells, as minesweeper uses, was the other natural form; I chose the vector because the input already is one and `Vector.get`'s `None` is exactly "off the board".
- The next generation is built by mapping over rows and cells with `Iter.with_index`, not by updating cells of a copy, so no indexed update is needed. This was the natural form, not a retreat from `Vector.set`.
- The eight offsets are a `once` literal, as in minesweeper.
- `next_state` is one `case` on `(cell, live_neighbours)` with the or-pattern `(1, 2) or (_, 3)`.
- `Iter.sum` totals the neighbours (style §26).
- Plain `test` blocks rather than a `subtests` table: see Friction.

## Friction

- [verbose] A `subtests` table of grids is unreadable after `nomi fmt`. A list of `(description, board, expected)` tuples holding `#[#[...]]` grids is three compound levels deep, and the formatter breaks any literal that deep one element per line regardless of width, and pushes the break into every nested literal (`maxCompoundDepth`, `internal/format/emit.go:2092`). So `("live cells with zero live neighbors die", #[#[0, 0, 0], #[0, 1, 0], #[0, 0, 0]], ...)` becomes 36 lines with one `0` per line, and the eight-case table took about 400 lines. Even `[[[1, 2]]]` becomes nine lines. The test file uses one `test` per case instead, with the board bound to `board` (two levels deep, so rows stay on one line). Breaking only the outer levels and keeping a short innermost row on one line would let a table of grids stay a table.

## Indexing

Two indexed reads, both in `cell_at` (`game_of_life.nomi:28`); no indexed update.

| Read | Result | Real use for `None`? |
|---|---|---|
| `try Vector.get(board, row)` | `cell_at`'s result, then `Maybe.with_default(0)` in `live_neighbours` | Yes: `row + dr` is -1 or the height at an edge |
| `Vector.get(cells, column)` | same | Yes: `column + dc` is off the row at an edge |

`cell_at` is 4 lines, plus the `|> Maybe.with_default(0)` on the line that calls it. Both reads keep `Vector.get`, because their `None` is the edge of the board.

The trapping `Vector.at` was added as a result of the indexing exercises, and it does not fit here: with it, the bounds checks `Vector.get` does would be written by hand, 6 lines instead of 4.

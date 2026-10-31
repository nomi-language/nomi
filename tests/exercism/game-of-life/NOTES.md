# Game of Life

Subject: Grids & Matrices

Compute the next generation of a Conway's Game of Life board.

Adapted from Exercism problem-specifications (MIT), exercises/game-of-life.

## Choices

- The board is `Vector<Vector<Int>>`, with cells as the canonical `0` and `1`. A `Bool` or a `Cell` enum would read better in the solution, but the test grids of `#[1, 0, 1]` read as the board; `next_state` is the only place that knows `1` means alive.
- Neighbours are counted by indexed reads (`cell_at`), with an off-board neighbour read as `None`. A `Maybe` is an `Iter` of zero or one elements, so `Iter.flat_map` over the offsets drops the off-board ones before `Iter.sum`. A `Set<#(Int, Int)>` of live cells, as flower-field uses, was the other natural form; I chose the vector because the input already is one and `Vector.get`'s `None` is exactly "off the board".
- The next generation is built by mapping over rows and cells with `Iter.with_index`, not by updating cells of a copy, so no indexed update is needed. This was the natural form, not a retreat from `Vector.set`.
- The eight offsets are a `once` literal, as in flower-field.
- `next_state` is one `case` on `#(cell, live_neighbours)` with the or-pattern `#(1, 2) or #(_, 3)`.
- `Iter.sum` totals the neighbours (style §26).
- The cases are one `subtests` table of `#(description, board, expected)` rows. The table is three compound levels deep, so `nomi fmt` breaks it one row per line; each row is too wide for one line and breaks into its three parts, and a 3x3 grid fits on one line inside it. The 8x8 grids are written one board row per line, which `nomi fmt` keeps because each breaks after its `#[`.

## Friction

None.

## Indexing

Two indexed reads, both in `cell_at` (`game_of_life.nomi:30`); no indexed update.

| Read | Result | Real use for `None`? |
|---|---|---|
| `Vector.get(board, row)?` | `cell_at`'s result, which `Iter.flat_map` in `live_neighbours` drops when it is `None` | Yes: `row + dr` is -1 or the height at an edge |
| `Vector.get(cells, column)` | same | Yes: `column + dc` is off the row at an edge |

`cell_at` is 4 lines. Both reads keep `Vector.get`, because their `None` is the edge of the board.

A trapping `Vector.get(...)!` read does not fit here: with it, the bounds checks `Vector.get` does would be written by hand, 6 lines instead of 4.

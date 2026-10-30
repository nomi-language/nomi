# Connect

Subject: Grids & Matrices

Find the winner of a game of Hex: `X` wins by joining the left edge to the right, `O` by joining the top edge to the bottom.

Adapted from Exercism problem-specifications (MIT), exercises/connect.

## Choices

- `winner` answers `Maybe<Player>`, a `Player` enum of `X` and `O`, where the canonical data writes `"X"`, `"O"` and `""` for no winner.
- The board is read as the canonical rows: each is trimmed of its indentation and split on spaces, so cell `#(row, column)` is the column-th symbol of the row. The indentation is only the picture of the shift, which the neighbour offsets carry: a cell touches its two sides, the cell above and the one above and right, and the cell below and the one below and left.
- A player's stones are collected once into a `Set<Cell>`, so the search asks set membership and never indexes the grid or checks bounds; a cell off the board holds no stone.
- `connects` sets the start and goal edges per player as a pair of predicates, and `reaches` is a depth-first search from every stone on the start edge, with a frontier list and a set of cells seen. It stops at the first cell on the goal edge.
- Both players are tried with `Iter.find`. A legal board has at most one winner, so the order does not matter.
- `Cell` is a `typealias` for `#(Int, Int)`, so the search's signature says what its tuples are.
- The twelve canonical cases are one `subtests` table named by the description column, each board one row per line as the canonical data draws it.

## Friction

None.

# Spiral Matrix

Fill a square grid with 1 to n² in a clockwise spiral from the top left corner.

Adapted from Exercism problem-specifications (MIT), exercises/spiral-matrix.

## Choices

- The grid is `Vector<Vector<Int>>` (`typealias Grid`), filled by walking the spiral and writing one cell at a time. This is the algorithm the problem describes, and it needs indexed reads (is the next square free?) and indexed updates. A closed form per cell (which ring, which side) or a `Map<(Int, Int), Int>` of the walk turned into rows were the alternatives; I did not take them, since the brief asked for the natural form and the walk is it.
- `0` marks an unwritten square, so "free" is `cell_at(grid, position) == Some(0)`, which is `False` both off the grid and on a written square. One comparison does both checks.
- The walk is a tail-recursive `fn fill` with the position and heading as tuples; turning clockwise is `(dr, dc) -> (dc, -dr)`. It stops after writing `size * size`, so size 0 needs no special case.
- `set_cell` returns the new `Grid`. It reads the row with `Vector.at` and writes with `Vector.set`, both of which trap on an index outside the vector; `fill` only steps onto a square `empty?` accepted, so the position is always on the grid.
- Plain `test` blocks rather than a `subtests` table, because `nomi fmt` breaks a table of grids one number per line (see game-of-life's NOTES).

## Friction

None.

## Indexing

Two indexed reads in `cell_at`, and one read and two updates in `set_cell`.

| Operation | Where | Result | Real use for a miss? |
|---|---|---|---|
| `try Vector.get(grid, row)` | `cell_at`, line 44 | `== Some(0)` in `empty?` | Yes: the square ahead is off the grid at an edge |
| `Vector.get(cells, column)` | `cell_at`, line 45 | same | Yes: same |
| `Vector.at(grid, row)` | `set_cell`, line 51 | the row | No: `fill` only steps onto a square `empty?` accepted |
| `Vector.set(column, value)` | `set_cell`, line 51 | the new row | No |
| `Vector.set(grid, row, cells)` | `set_cell`, line 52 | the new grid | No |

The reads in `cell_at` keep `Vector.get`, since their `None` is the edge test. `set_cell` is 4 lines and returns a `Grid`, so `fill` writes a cell with `grid = set_cell(grid, position, value)` and `spiral_matrix` returns `fill`'s grid with no default.

A trapping `Vector.at` and a trapping `Vector.set` were added as a result of this exercise. Before them `Vector.set` returned `Maybe<Vector<T>>`, and the three misses that cannot happen travelled up through a `try` in `fill` to a `Maybe.with_default` in `spiral_matrix` that could not be reached.

An indexed nested update (hypothetical syntax, `grid = #[..grid, row => #[.., column => value]]`) would remove `set_cell` altogether; with trapping writes it no longer has to carry a `Maybe`.

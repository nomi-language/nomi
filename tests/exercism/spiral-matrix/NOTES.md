# Spiral Matrix

Subject: Grids & Matrices

Fill a square grid with 1 to n² in a clockwise spiral from the top left corner.

Adapted from Exercism problem-specifications (MIT), exercises/spiral-matrix.

## Choices

- The grid is `Vector<Vector<Int>>`, filled by walking the spiral and writing one cell at a time. This is the algorithm the problem describes, and it needs indexed reads (is the next square free?) and indexed updates. A closed form per cell (which ring, which side) or a `Map<#(Int, Int), Int>` of the walk turned into rows were the alternatives; the walk is the more direct form of the problem's description.

- `0` marks an unwritten square, so "free" is `cell_at(grid, position) == Some(0)`, which is `False` both off the grid and on a written square. One comparison does both checks.
- The walk is a tail-recursive `fn fill` with the position and heading as tuples; turning clockwise is `#(dr, dc) -> #(dc, -dr)`. It stops after writing `size * size`, so size 0 needs no special case.
- `set_cell` returns the new grid. It reads the row with `Vector.get(...)!` and writes with `Vector.set`, both of which trap on an index outside the vector; `fill` only steps onto a square `is_empty` accepted, so the position is always on the grid.
- The cases are one `subtests` table of `#(description, size, expected)` rows. The 4x4 and 5x5 grids are written one grid row per line, which `nomi fmt` keeps because each breaks after its `#[`.

## Friction

None.

## Indexing

Two indexed reads in `cell_at`, and one read and two updates in `set_cell`.

| Operation | Where | Result | Real use for a miss? |
|---|---|---|---|
| `Vector.get(grid, row)?` | `cell_at`, line 49 | `== Some(0)` in `is_empty` | Yes: the square ahead is off the grid at an edge |
| `Vector.get(cells, column)` | `cell_at`, line 50 | same | Yes: same |
| `Vector.get(grid, row)!` | `set_cell`, line 60 | the row | No: `fill` only steps onto a square `is_empty` accepted |
| `Vector.set(column, value)` | `set_cell`, line 60 | the new row | No |
| `Vector.set(grid, row, cells)` | `set_cell`, line 61 | the new grid | No |

The reads in `cell_at` keep `Vector.get`, since their `None` is the edge test. `set_cell` is 2 lines and returns the new grid, so `fill` writes a cell with `grid = set_cell(grid, position, value)` and `spiral_matrix` returns `fill`'s grid with no default.

A trapping `Vector.set` was added as a result of this exercise. Before it `Vector.set` returned `Maybe<Vector<T>>`, and the three misses that cannot happen travelled up through a `?` in `fill` to a `Maybe.with_default` in `spiral_matrix` that could not be reached.

An indexed nested update (hypothetical syntax, `grid = #[..grid, row => #[.., column => value]]`) would remove `set_cell` altogether; with trapping writes it no longer has to carry a `Maybe`.

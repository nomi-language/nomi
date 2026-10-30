# Saddle Points

Subject: Grids & Matrices

Find the cells of a grid that are the largest in their row and the smallest in their column.

Adapted from Exercism problem-specifications (MIT), exercises/saddle-points.

## Choices

- The matrix is `Vector<Vector<Int>>`, as the canonical data's grid of rows reads. A row is in hand while iterating, so its maximum is `Iter.max(heights)` with no indexing.
- The columns come from `Iter.transpose(matrix)`, and their minima are computed once into a `Vector<Int>` and looked up by column index, rather than scanning the column again for every cell. `Iter.map(Iter.min)` passes the function itself; `Maybe.values()` drops the `None` an empty column would give, and no column is empty.
- A point is a `pub struct Position { row, column }`, counted from 1 as the canonical data does (style §18: a struct once it travels). Tests write `#{row: 2, column: 1}`, which the comparison types as `Position`.
- Points come out row by row and left to right. The canonical "non square matrix" case lists (1, 3) before (1, 1); its comment allows reordering, so the test expects (1, 1) first.
- `Some(height) == tallest` compares against `Iter.max`'s `Maybe` directly instead of unwrapping it; an empty row has no cells to compare.
- The one indexed read uses `Vector.get(...)!`, which traps on an index outside the vector. The input is a matrix, so every row is as wide as the first and `minima` has a value for every column. A ragged matrix (rows of different widths) is outside the problem: `Iter.transpose` gives a short row nothing past its end, so a later row's column could have no minimum and the read would stop the program rather than answer.
- The cases are one `subtests` table of `#(description, matrix, expected)` rows. Its annotation names `List<Position>` so the `#{row, column}` literals in the rows are typed; each row breaks into its three parts and each grid stays on one line.

## Friction

None.

## Indexing

One indexed read.

| Read | Where | Result | Real use for a miss? |
|---|---|---|---|
| `Vector.get(minima, column)!` | the filter in `saddle_points`, line 20 | `== height` | No: `column` comes from `Iter.with_index` over a row of the same width |

The filter compares `Vector.get(minima, column)! == height`, which reads as what it means. `Iter.min`'s `Maybe` remains, so `column_minima` drops it with `Maybe.values()`.

`Iter.transpose` was added as a result of this exercise. Before it, each column was read by index, `matrix |> Iter.map(&Vector.get(&1, column)!)`, once per column.

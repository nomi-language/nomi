# Saddle Points

Find the cells of a grid that are the largest in their row and the smallest in their column.

Adapted from Exercism problem-specifications (MIT), exercises/saddle-points.

## Choices

- The matrix is `Vector<Vector<Int>>`, since the column check reads by index. A row is in hand while iterating, so its maximum is `Iter.max(heights)` with no indexing; only columns need it.
- Column minima are computed once into a `Vector<Int>` and looked up by column index, rather than scanning the column again for every cell.
- A point is a `pub struct Position { row, column }`, counted from 1 as the canonical data does (style §18: a struct once it travels). Tests write `#{row: 2, column: 1}`, which the comparison types as `Position`.
- Points come out row by row and left to right. The canonical "non square matrix" case lists (1, 3) before (1, 1); its comment allows reordering, so the test expects (1, 1) first.
- `Some(height) == tallest` compares against `Iter.max`'s `Maybe` directly instead of unwrapping it; an empty row has no cells to compare.
- Both indexed reads use `Vector.at`, which traps on an index outside the vector. The input is a matrix, so every row is as wide as the first. A ragged matrix (rows of different widths) is outside the problem, and a read past a short row stops the program rather than answering.
- The cases are one `subtests` table of `#(description, matrix, expected)` rows. Its annotation names `List<Position>` so the `#{row, column}` literals in the rows are typed; each row breaks into its three parts and each grid stays on one line.

## Friction

- [stdlib] No transpose or column access for nested sequences. The column is `matrix |> Iter.map(fn cells -> Vector.at(cells, column))`, read by index for each column. Gleam has `list.transpose`, Elixir `Enum.zip/1` over the rows, Python `zip(*rows)`. An `Iter.transpose` (or `zip_all`) over `Iter<Iter<T>>` would give the columns with no index.

## Indexing

Two indexed reads.

| Read | Where | Result | Real use for a miss? |
|---|---|---|---|
| `Vector.at(cells, column)` | `column_minimum`, line 40 | the height, into `Iter.min()` | No: `column` is below the first row's width, and every row is that wide |
| `Vector.at(minima, column)` | the filter in `saddle_points`, line 20 | `== height` | No: `column` comes from `Iter.with_index` over a row of the same width |

`column_minimum` is 5 lines and the filter compares `Vector.at(minima, column) == height`, which reads as what it means. `Iter.min`'s `Maybe` remains, so `column_minima` still collects and defaults it.

`Vector.at` was added as a result of this exercise. Before it, the only read was the `Maybe`-returning `Vector.get`: `column_minimum` collected the reads' `Maybe`s with `try Maybe.collect()` (8 lines), and the filter compared with `Some(height)`.

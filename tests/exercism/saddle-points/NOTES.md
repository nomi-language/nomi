# Saddle Points

Find the cells of a grid that are the largest in their row and the smallest in their column.

Adapted from Exercism problem-specifications (MIT), exercises/saddle-points.

## Choices

- The matrix is `Vector<Vector<Int>>`, since the column check reads by index. A row is in hand while iterating, so its maximum is `Iter.max(heights)` with no indexing; only columns need it.
- Column minima are computed once into a `Vector<Int>` and looked up by column index, rather than scanning the column again for every cell.
- A point is a `pub struct Position { row, column }`, counted from 1 as the canonical data does (style §18: a struct once it travels). Tests write `{row: 2, column: 1}`, which the comparison types as `Position`.
- Points come out row by row and left to right. The canonical "non square matrix" case lists (1, 3) before (1, 1); its comment allows reordering, so the test expects (1, 1) first.
- `Some(height) == tallest` compares against `Iter.max`'s `Maybe` directly instead of unwrapping it; an empty row has no cells to compare.
- A ragged matrix (rows of different widths, outside the problem) gets no column minima and so no saddle points, rather than a wrong answer.
- Plain `test` blocks rather than a `subtests` table, because `nomi fmt` breaks a table of grids one number per line (see game-of-life's NOTES).

## Friction

- [stdlib] No transpose or column access for nested sequences. The column is `matrix |> Iter.map(|cells| Vector.at(cells, column)) |> Maybe.collect()`, inside its own `Maybe`-returning helper. Gleam has `list.transpose`, Elixir `Enum.zip/1` over the rows, Python `zip(*rows)`. An `Iter.transpose` (or `zip_all`) over `Iter<Iter<T>>` would give the columns with no index and no `Maybe`.
- [verbose] The column minima take two functions and 17 lines, because each indexed read and `Iter.min` adds a `Maybe` that cannot be `None` for a rectangular matrix. See Indexing. In Rust: `(0..width).map(|c| m.iter().map(|r| r[c]).min().unwrap()).collect()`, one expression.

## Indexing

Two indexed reads.

| Read | Where | Leftover `Maybe` ends up | Real use for `None`? |
|---|---|---|---|
| `Vector.at(cells, column)` | `column_minimum`, line 42 | `try` after `Maybe.collect`, then `Maybe.collect()` and `Maybe.with_default([])` in `column_minima` | No for the problem's input: `column` is below the first row's width. Only a ragged matrix reaches it |
| `Vector.at(minima, column)` | the filter in `saddle_points`, line 20 | `== Some(height)` | No: `column` comes from `Iter.with_index` over a row of the same width |

The second read is written inline as a comparison rather than through a `try` helper, since comparing with `Some(height)` absorbs the `None` in the same expression; a helper would be four more lines.

Today: `column_minimum` is 8 lines and `column_minima` 9; the filter is 3.

(a) With a trapping `Vector.get` (hypothetical), `column_minimum` is 5 lines, and the filter's comparison reads as what it means:

```nomi
fn column_minimum(matrix: Vector<Vector<Int>>, column: Int): Maybe<Int> {
    matrix
    |> Iter.map(|cells| Vector.get(cells, column))
    |> Iter.min()
}

// in the filter
Some(height) == tallest and Vector.get(minima, column) == height
```

`Iter.min`'s `Maybe` remains, so `column_minima` still collects and defaults it (9 lines). Saving: 3 lines.

(b) An indexed nested update does not apply: nothing is updated.

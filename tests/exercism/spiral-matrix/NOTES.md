# Spiral Matrix

Fill a square grid with 1 to n² in a clockwise spiral from the top left corner.

Adapted from Exercism problem-specifications (MIT), exercises/spiral-matrix.

## Choices

- The grid is `Vector<Vector<Int>>` (`typealias Grid`), filled by walking the spiral and writing one cell at a time. This is the algorithm the problem describes, and it needs indexed reads (is the next square free?) and indexed updates. A closed form per cell (which ring, which side) or a `Map<(Int, Int), Int>` of the walk turned into rows were the alternatives; I did not take them, since the brief asked for the natural form and the walk is it.
- `0` marks an unwritten square, so "free" is `cell_at(grid, position) == Some(0)`, which is `False` both off the grid and on a written square. One comparison does both checks.
- The walk is a tail-recursive `fn fill` with the position and heading as tuples; turning clockwise is `(dr, dc) -> (dc, -dr)`. It stops after writing `size * size`, so size 0 needs no special case.
- `set_cell` returns `Maybe<Grid>`, `None` for a position off the grid. `fill` passes that on with `try`, and `spiral_matrix` ends in `Maybe.with_default(grid)`. The walk never steps off the grid, so the default is never used.
- The cases are one `subtests` table of `(description, size, expected)` rows. The 4x4 and 5x5 grids are written one grid row per line, which `nomi fmt` keeps because each breaks after its `#[`.

## Friction

- [verbose] Writing one cell of a grid takes a 5-line helper (`set_cell`) with two `try`s, and its `Maybe` travels up through `fill` to a default in `spiral_matrix` that cannot be reached, for what Rust writes as `grid[row][column] = value`. See Indexing.

## Indexing

Two indexed reads in `cell_at`, and one read and two updates in `set_cell`.

| Operation | Where | Leftover `Maybe` ends up | Real use for `None`? |
|---|---|---|---|
| `try Vector.at(grid, row)` | `cell_at`, line 45 | `== Some(0)` in `empty?` | Yes: the square ahead is off the grid at an edge |
| `Vector.at(cells, column)` | `cell_at`, line 46 | same | Yes: same |
| `try Vector.at(grid, row)` | `set_cell`, line 52 | `try set_cell` in `fill` (line 20), then `Maybe.with_default(grid)` in `spiral_matrix` (line 10) | No: `fill` only steps onto a square `empty?` accepted |
| `try Vector.set(cells, column, value)` | `set_cell`, line 53 | same | No |
| `Vector.set(grid, row, cells)` | `set_cell`, line 54 | same | No |

The three `None`s that cannot happen travel up two functions to a default that cannot be reached. That costs a `try` in `fill`, a `Maybe<Grid>` result on `fill`, and the `Maybe.with_default` in `spiral_matrix`.

(a) With a trapping read and a trapping write (named `get` and `put` here; hypothetical), `set_cell` is 4 lines and returns a `Grid`, and `fill` and `spiral_matrix` lose their `Maybe`:

```nomi
fn set_cell(grid: Grid, (row, column): (Int, Int), value: Int): Grid {
    cells = Vector.get(grid, row) |> Vector.put(column, value)
    Vector.put(grid, row, cells)
}
```

A trapping read alone, with `Vector.set` still total, saves nothing: `set_cell` is still 5 lines and still returns a `Maybe` for its two `set`s.

(b) With an indexed nested update (hypothetical syntax), the helper goes away and `fill` writes the cell in 1 line:

```nomi
grid = #[..grid, row => #[.., column => value]]
```

That form only helps if an out-of-range index traps; if it returned a `Maybe`, `fill` would still need its `try` and `spiral_matrix` its default. So (b) assumes (a)'s semantics for writes.

The reads in `cell_at` would not change under either: their `None` is the edge test.

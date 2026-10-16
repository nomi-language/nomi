# Spiral Matrix

Fill a square grid with 1 to n² in a clockwise spiral from the top left corner.

Adapted from Exercism problem-specifications (MIT), exercises/spiral-matrix.

## Choices

- The grid is `Vector<Vector<Int>>` (`typealias Grid`), filled by walking the spiral and writing one cell at a time. This is the algorithm the problem describes, and it needs indexed reads (is the next square free?) and indexed updates. A closed form per cell (which ring, which side) or a `Map<(Int, Int), Int>` of the walk turned into rows were the alternatives; I did not take them, since the brief asked for the natural form and the walk is it.
- `0` marks an unwritten square, so "free" is `cell_at(grid, position) == Some(0)`, which is `False` both off the grid and on a written square. One comparison does both checks.
- The walk is a tail-recursive `fn fill` with the position and heading as tuples; turning clockwise is `(dr, dc) -> (dc, -dr)`. It stops after writing `size * size`, so size 0 needs no special case.
- `set_cell` returns a `Grid`, not a `Maybe<Grid>`, and quietly leaves the grid alone on a bad position, because of the `[bug]` below. The version I meant to write is in Indexing.
- Plain `test` blocks rather than a `subtests` table, because `nomi fmt` breaks a table of grids one number per line (see game-of-life's NOTES).

## Friction

- [bug] A function whose result is `Maybe<Vector<Vector<Int>>>` cannot be called: the IR builder declines it, so `nomi check` and `nomi test` report the caller as not supported yet. The same holds for `Result<Vector<Vector<Int>>, String>` and `Maybe<Vector<List<Int>>>`; `Maybe<Vector<Int>>`, `Maybe<List<List<Int>>>` and `Maybe<List<Vector<Int>>>` work. Reproducer:

  ```nomi
  import std/io

  fn f(): Maybe<Vector<Vector<Int>>> {
      Some(#[#[1]])
  }

  fn main() {
      io.inspect(f())
  }
  ```

  `error: this call to `f` is not supported yet, so `fn main` cannot run`, and with `NOMI_DEBUG_LOWERING=1` the reason `a direct callee's result kind outside the domain: Maybe<Vector<Vector<Int>>>`. The same kind declines in two more places: `Some(written) = Vector.set(grid, 0, #[1]) else { return }` (`a nested pattern outside the retained tests: a EnumPattern over Maybe<Vector<Vector<Int>>>`), and `Maybe.flat_map(row, |cells| Vector.set(grid, 0, cells))` (`a generic stdlib instance's parameter outside the domain: maybe.Maybe.flat_map at Vector<Int>, Vector<Vector<Int>>`). The front end accepts all three. `irRetainedValueKind` (`internal/irbuild/irstruct.go:594`) admits a `Vector` of any callable kind, but not a `Maybe` or `Result` of such a vector. `Maybe.with_default` over the same value does lower, which is the workaround in `set_cell`.
- [verbose] Writing one cell of a grid takes two helpers and 11 lines (`set_cell` and `row_with`), for what Rust writes as `grid[row][column] = value`. See Indexing.

## Indexing

Two indexed reads in `cell_at` and one read and two updates on the write path (`set_cell` and `row_with`).

| Operation | Where | Leftover `Maybe` ends up | Real use for `None`? |
|---|---|---|---|
| `try Vector.at(grid, row)` | `cell_at`, line 44 | `== Some(0)` in `empty?` | Yes: the square ahead is off the grid at an edge |
| `Vector.at(cells, column)` | `cell_at`, line 45 | same | Yes: same |
| `try Vector.at(grid, row)` | `row_with`, line 60 | `else { return grid }` in `set_cell` | No: `fill` only steps onto a square `empty?` accepted |
| `Vector.set(cells, column, value)` | `row_with`, line 61 | same | No |
| `Vector.set(grid, row, cells)` | `set_cell`, line 55 | `Maybe.with_default(grid)` in `set_cell` | No |

What I meant to write, blocked by the `[bug]`: `set_cell` returning `Maybe<Grid>` with three `try`s (5 lines), `fill` returning `Maybe<Grid>` with `grid = try set_cell(...)`, and `spiral_matrix` ending in `|> Maybe.with_default(grid)`. The `None` would have travelled up two functions to a default that cannot be reached. What is there now absorbs it inside `set_cell`, in 11 lines over two helpers, and a bad position silently writes nothing.

(a) With a trapping read and a trapping write (named `get` and `put` here; hypothetical), `set_cell` is 4 lines and one helper, and returns a `Grid`:

```nomi
fn set_cell(grid: Grid, (row, column): (Int, Int), value: Int): Grid {
    cells = Vector.get(grid, row) |> Vector.put(column, value)
    Vector.put(grid, row, cells)
}
```

A trapping read alone, with `Vector.set` still total, saves little: the two `set`s still need their `Maybe` handled (7 lines, one helper).

(b) With an indexed nested update (hypothetical syntax), the helpers go away and `fill` writes the cell in 1 line:

```nomi
grid = #[..grid, row => #[.., column => value]]
```

That form only helps if an out-of-range index traps; if it returned a `Maybe` it would be the same 11 lines. So (b) assumes (a)'s semantics for writes.

The reads in `cell_at` would not change under either: their `None` is the edge test.

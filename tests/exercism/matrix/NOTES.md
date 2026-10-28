# Matrix

Subject: Grids & Matrices

Read a matrix of numbers from text, one row per line, and answer any row or column.

Adapted from Exercism problem-specifications (MIT), exercises/matrix.

## Choices

- `Matrix.parse` reads the text once into an opaque struct of a `Vector<Vector<Int>>`, so `row` and `column` index it and never parse again. Each line is split with `String.words`, which ignores the different widths of the numbers.
- A word that is not a number is `Err(Error.NotANumber(word))`, through `Maybe.to_result` per word and `Result.collect` per row and over the rows. The canonical data has no such case; the test file adds one, marked not canonical.
- `row` and `column` count from 1, as the canonical data does, and answer `Maybe<List<Int>>`: an index past the last row or column is `None`, which the test file also checks outside the canonical cases. `column` is `Vector.get` on every row, gathered with `Maybe.collect`, so it is `None` if any row is too short.
- The row and column groups are one `subtests` table each, named by the description column. A row's body takes the matrix apart with `assert Ok(matrix) = Matrix.parse(text)`, since `try` cannot leave a `subtests` row.

## Friction

None.

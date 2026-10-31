# Pascal's Triangle

Subject: Numbers & Math

Build the first rows of Pascal's triangle, where each value is the sum of the two above it.

Adapted from Exercism problem-specifications (MIT), exercises/pascals-triangle.

## Choices

- `rows` is `Iter.iterate([1], next_row)` under `Iter.take(count)`: the triangle is an unbounded sequence of rows, each from the last, and the count only says how many to keep.
- `next_row` zips the row shifted right by a leading zero (`[0, ..row]`) with the row followed by a zero (`[..row, 0]`) and adds the pairs.
- The eight canonical cases are one `subtests` table.

## Friction

None.

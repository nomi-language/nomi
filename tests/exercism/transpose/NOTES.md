# Transpose

Transpose lines of text, padding short lines on the left of a column with spaces and never on the right.

Adapted from Exercism problem-specifications (MIT), exercises/transpose.

## Choices

- Each line is padded on the right to the width of the longest line from it to the end. After that, the rows that reach column `i` are always a prefix of the rows, so output line `i` is every row's character at `i` where it has one, and no row needs trailing-space bookkeeping.
- The padding widths are a running maximum from the bottom up, written as one `Iter.reduce` over the reversed lines with a `(width, rows)` accumulator that prepends each row.
- Rows are `Vector<String>` of graphemes (`Iter.to_vector` over a `String`), since output line `i` reads column `i` of each row by index. The input and output stay `List<String>`.
- A row too short for a column gives `Vector.get`'s `None`, and `Maybe.values` drops it. That `None` is the rule "don't pad on the right", not an error.
- One `subtests` table: the cases share one shape, and a table of string lists is shallow enough for `nomi fmt` to keep.

## Friction

- [stdlib] No `Iter.scan` for a running maximum. The padding widths needed `Iter.reduce` threading a `(width, rows)` tuple and prepending to a list, over `Iter.reverse(lines)`. Rust has `Iterator::scan`, Elixir `Enum.scan/2`, Gleam `list.scan`. With one, it is `lines |> Iter.reverse() |> Iter.scan(0, |width, line| Int.max(width, String.length(line)))`, zipped back with the lines.

## Indexing

One indexed read.

| Read | Where | Result | Real use for `None`? |
|---|---|---|---|
| `Vector.get(row, column)` | `transpose`, line 12 | `Maybe.values()` in the same pipeline | Yes: the row ends before this column |

There is no `try` helper: the `None` is consumed one stage later by `Maybe.values`, which is the point of the read.

The trapping `Vector.at` was added as a result of the indexing exercises, and it does not fit here: the rows that reach the column would have to be selected first (`Iter.take_while(|row| Vector.length(row) > column)`), one more stage, and 5 lines would become 6.

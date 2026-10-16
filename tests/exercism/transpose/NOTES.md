# Transpose

Transpose lines of text, padding short lines on the left of a column with spaces and never on the right.

Adapted from Exercism problem-specifications (MIT), exercises/transpose.

## Choices

- Each line is padded on the right to the width of the longest line from it to the end. After that, the rows that reach column `i` are always a prefix of the rows, so output line `i` is every row's character at `i` where it has one, and no row needs trailing-space bookkeeping.
- The padding widths are a running maximum from the bottom up: one `Iter.map_reduce` over the reversed lines, whose state is the width so far and whose output is each padded row. The rows come out bottom first, so one `Iter.reverse` puts them back in order. `Iter.scan` over the widths, reversed and zipped back with the lines, is the other spelling; it needs a reverse and a zip where `map_reduce` needs one reverse.
- Rows are `Vector<String>` of graphemes (`Iter.to_vector` over a `String`), since output line `i` reads column `i` of each row by index. The input and output stay `List<String>`.
- A row too short for a column gives `Vector.at`'s `None`, and `Maybe.values` drops it. That `None` is the rule "don't pad on the right", not an error.
- One `subtests` table: the cases share one shape, and a table of string lists is shallow enough for `nomi fmt` to keep.

## Friction

None.

## Indexing

One indexed read.

| Read | Where | Leftover `Maybe` ends up | Real use for `None`? |
|---|---|---|---|
| `Vector.at(row, column)` | `transpose`, line 12 | `Maybe.values()` in the same pipeline | Yes: the row ends before this column |

There is no `try` helper: the `None` is consumed one stage later by `Maybe.values`, which is the point of the read.

(a) A trapping accessor makes it longer: the rows that reach the column would have to be selected first (`Iter.take_while(|row| Vector.length(row) > column)`), one more stage, before `Vector.get`. 5 lines become 6.

(b) An indexed nested update does not apply: nothing is updated.

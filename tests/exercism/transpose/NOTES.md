# Transpose

Subject: Grids & Matrices

Transpose lines of text, padding short lines on the left of a column with spaces and never on the right.

Adapted from Exercism problem-specifications (MIT), exercises/transpose.

## Choices

- Each line is padded on the right to the width of the longest line from it to the end. After that, the rows that reach column `i` are always a prefix of the rows, so output line `i` is every row's character at `i` where it has one, which is what `Iter.transpose` gives: a short row adds nothing past its end. No row needs trailing-space bookkeeping.
- The padding widths are a running maximum from the bottom up: one `Iter.map_reduce` over the reversed lines, whose state is the width so far and whose output is each padded line. The rows come out bottom first, so one `Iter.reverse` puts them back in order. `Iter.scan` over the widths, reversed and zipped back with the lines, is the other spelling; it needs a reverse and a zip where `map_reduce` needs one reverse.
- The padded lines go to `Iter.transpose` as they are: a `String` is an `Iter` of its graphemes, so each column comes back as a `List<String>` of characters, which `String.join` puts back together. Nothing is indexed.
- One `subtests` table: the cases share one shape, and a table of string lists is shallow enough for `nomi fmt` to keep.

## Friction

None.

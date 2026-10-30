# Rectangles

Subject: Grids & Matrices

Count the rectangles in an ASCII diagram: `+` corners joined by `-` sides along the rows and `|` sides down the columns, where a `+` may also stand in a side.

Adapted from Exercism problem-specifications (MIT), exercises/rectangles.

## Choices

- The diagram is a `Vector` of rows, each a `Vector` of one-grapheme strings, and `symbol_at` reads a cell as a `Maybe`, so a line shorter than the others, as in "two rectangles without shared parts", reads as no symbol rather than a trap.
- `count` lists every `+` once, pairs every corner with every other, and counts the pairs that are the top left and bottom right of a rectangle. `is_rectangle` asks that the second is below and right of the first, that the other two corners are `+`, and that the two rows and two columns between them are whole sides, each an `Iter.all` over the corners or the sides.
- `is_line_across` and `is_line_down` walk one side and accept `-` or `+` along a row and `|` or `+` down a column, one `case` arm joined by `or` (`Some("-" or "+")`). A `+` in the middle of a side is where another line crosses it, which "rectangles must have four sides" and the large input rely on.
- The 14 canonical cases are one `subtests` table named by the description column, each diagram written one line per row so it reads as the picture.

## Friction

None.

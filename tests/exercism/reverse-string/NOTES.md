# Reverse String

Reverse a string by its characters as a reader sees them, so combining marks
stay on their letters.

Adapted from Exercism problem-specifications (MIT), exercises/reverse-string.

## Choices

- The solution is `String.reverse`, which already reverses by grapheme, so
  the two grapheme-cluster cases pass with no extra work. A hand-written
  `Iter.reverse(text) |> String.join()` does the same thing and would only
  restate the stdlib.

## Friction

- [bug] `nomi fmt` measures line width in bytes, not characters, so a line of
  non-ASCII text breaks well short of 100 columns. The Thai row of the test
  table is 68 characters wide (128 bytes) and `fmt` breaks it across five
  lines. Reproducer: format

  ```nomi
  fn main() {
      pair = ("ééééééééééééééééééééééééé", "ééééééééééééééééééééééééé")
      dbg pair
  }
  ```

  The `pair` line is 68 characters and 118 bytes; `fmt` breaks the tuple one
  element per line. The cause is `fitsWalk` in `internal/format/layout.go`,
  which subtracts `len(v.s)` for each text piece. `rustfmt` counts display
  width instead.

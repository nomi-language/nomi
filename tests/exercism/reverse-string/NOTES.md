# Reverse String

Subject: Strings & Text

Reverse a string by its characters as a reader sees them, so combining marks
stay on their letters.

Adapted from Exercism problem-specifications (MIT), exercises/reverse-string.

## Choices

- The solution is `String.reverse`, which already reverses by grapheme, so
  the two grapheme-cluster cases pass with no extra work. A hand-written
  `Iter.reverse(text) |> String.join()` does the same thing and would only
  restate the stdlib.

## Friction

None.

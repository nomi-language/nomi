# Acronym

Build an acronym from the first letter of each word of a phrase, treating
hyphens as word separators and ignoring other punctuation.

Adapted from Exercism problem-specifications (MIT), exercises/acronym.

## Choices

- Words are found with one regex, `[A-Za-z][A-Za-z']*`, through
  `String.find_all`, rather than splitting on spaces and hyphens and then
  stripping punctuation. The regex makes "Halley's" one word and "_Not_" the
  word "Not" without special cases.
- The first letter of a word is `&String.slice(&1, 0, 1)`, a capture rather than `fn word -> ...` (style §31: one call that uses its parameter once).
  `Iter.first(word)` also works but returns a `Maybe`, and every match has at
  least one letter.
- The whole acronym is upper-cased once at the end, not letter by letter.

## Friction

None.

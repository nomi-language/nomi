# Scrabble Score

Subject: Strings & Text

Score a word by summing the Scrabble values of its letters.

Adapted from Exercism problem-specifications (MIT), exercises/scrabble-score.

## Choices

- The letter table is one `case` over one-letter strings, each value one arm with its letters joined by `or` (style §25). A `Map` built from the table was the other option; the `case` reads as the problem's table and needs no lookup that may miss.
- The word is case-folded once with `String.fold_case`, then iterated as graphemes, so the table lists lowercase letters only.
- The total is `Iter.sum` (style §26).
- Anything not in the table scores 0, which the canonical data does not test.

## Friction

None.

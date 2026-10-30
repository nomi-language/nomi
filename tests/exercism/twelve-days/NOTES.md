# Twelve Days

Subject: Strings & Text

Recite the verses of "The Twelve Days of Christmas", each adding a gift to the one before.

Adapted from Exercism problem-specifications (MIT), exercises/twelve-days.

## Choices

- The ordinals are a `Vector` read with `Vector.get(...)!`: the day comes from `recite`'s range, so an index out of bounds is a caller's bug, and a trap reports it (style §28).
- The first day's gift is a `once` of its own, apart from the later gifts, because it is the only one that changes: it gains "and" once another gift comes before it. `gifts` returns it alone on day one, and otherwise takes the later gifts, reverses them and joins them before ", and a Partridge in a Pear Tree".
- `String.join_list` with an English locale was the other way to write the "and", but CLDR's English list puts no comma before "and" in a list of two, and the song does.
- The test file holds each canonical verse once, in a `once verses` table. The single-verse cases are a `subtests` over it, and the three multi-verse cases take their expected verses from it with `Iter.drop` and `Iter.take`, since each canonical multi-verse case is exactly those verses in order. Writing the whole song out again for "recites the whole song" would add 40 lines of the same text.

## Friction

None.

# Bottle Song

Subject: Strings & Text

Recite the counting song "Ten Green Bottles", from a given number of bottles down, for a given number of verses.

Adapted from Exercism problem-specifications (MIT), exercises/bottle-song.

## Choices

- The numbers are a `Vector` of their words from "no" to "ten", read by the bottle count, and `green_bottles` adds "green bottle" or "green bottles". A verse's first two lines start with that phrase capitalized, and its last line uses the phrase for one bottle fewer, so "no green bottles" and "one green bottle" come out of the same two functions as every other count.
- The counts sung are `Iter.iterate` down from the start, taken `take_down` times.
- `recite` answers lines, as the canonical data does, with an empty line between two verses: `Iter.intersperse([""])` puts a verse of one empty line between each two verses, and `Iter.flatten` joins them.
- The canonical groups, single verses and multiple verses, are one test each, holding one `subtests` table of the canonical lines written out.

## Friction

None.

## Indexing

One indexed read.

| Read | Where | Result | Real use for a miss? |
|---|---|---|---|
| `Vector.get(numbers, bottles)!` | `green_bottles`, line 44 | the count's word | No: the song counts from at most ten down to none, and `numbers` has a word for each |

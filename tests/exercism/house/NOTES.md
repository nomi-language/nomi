# House

Subject: Strings & Text

Recite "This is the House that Jack Built", where each verse embeds every phrase of the verse before.

Adapted from Exercism problem-specifications (MIT), exercises/house.

## Choices

- The rhyme is one list of phrases, each ending with what its subject did to the next one down ("the rat that ate"). A verse is the first `n` phrases, reversed and joined with spaces, so no phrase needs a special case: the house's phrase is the only one without a verb, and it always comes last.
- The alternative, a list of `#(subject, verb)` pairs joined with " that ", splits each line of the rhyme in two for no gain in any verse.
- `recite` maps a range of verse numbers, like twelve-days.
- The test file holds each canonical verse once, in a `once verses` table; the two multi-verse cases take their expected verses from it, since each is exactly those verses in order.

## Friction

None.

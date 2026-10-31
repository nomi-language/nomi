# Food Chain

Subject: Strings & Text

Recite the cumulative song about the old lady who swallowed a fly, where each verse swallows a bigger animal to catch the one before.

Adapted from Exercism problem-specifications (MIT), exercises/food-chain.

## Choices

- The song is data: a list of `Animal` structs with a name, the line sung when she swallows it, and a `detail` that follows its name when it is the one caught. Only the spider has a detail ("that wriggled and jiggled and tickled inside her"), so the field defaults to `""`, and the fly, about which nothing is sung, leaves its `reaction` at its default, `None`.
- A verse takes the animals swallowed so far, newest first. The chain of "She swallowed the X to catch the Y" lines zips that list with itself shifted by one, so each line pairs a predator with its prey and no line is built by index.
- A list literal's spread takes any `Iter` of its elements, and a `Maybe` is an `Iter` of zero or one, so `..newest.reaction` splices the reaction's line, or no line for the fly, and `..chain` splices the lazy chain of lines with no `Iter.to_list`.
- The horse is not an `Animal`: its verse is the opening and "She's dead, of course!", with no chain and no closing line, so it is a fixed `horse_verse` that `verse` answers for any number past the cow.
- The newest animal is taken with a list pattern, `[newest, ..] = swallowed`, whose `else` is `unreachable`, since every verse up to the cow swallows the fly at least (style §28).
- `recite` answers lines with an empty line between two verses, as bottle-song does.
- The ten canonical cases are one `subtests` table of the canonical lines written out.

## Friction

None.

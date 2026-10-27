# Space Age

Subject: Numbers & Math

Given an age in seconds, say how old someone would be on each planet of the Solar System.

Adapted from Exercism problem-specifications (MIT), exercises/space-age.

## Choices

- The planets are a `Planet` enum, and `age` takes one, so an age on a planet that does not exist cannot be asked for. The canonical "not a planet" case becomes `Planet.parse("Sun") == None`; the test file adds one non-canonical case that `parse` finds a planet.
- `Planet.parse` finds the variant whose `derive Display` name matches, over `derive Variants`' list, instead of a `case` with eight string arms that repeat the variant names.
- `Planet.parse` returns a `Maybe`, not a `Result`: a name either names a planet or it does not, and there is only one way to fail.
- The orbital periods are a `case` in `Planet.orbital_period`, so adding a planet without its period is a missing arm.
- The canonical ages are given to two decimal places, so the tests round the computed age with `Float.round(age, 2)` and compare exactly. The rounded Float is the one nearest the two-decimal value, which is the Float the expected literal reads as.
- The table's first row names `Planet.Earth`; later rows write bare variants, which take the enum of the first row's column.

## Friction

None.

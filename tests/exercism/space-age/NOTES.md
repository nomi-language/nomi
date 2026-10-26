# Space Age

Subject: Numbers & Math

Given an age in seconds, say how old someone would be on each planet of the Solar System.

Adapted from Exercism problem-specifications (MIT), exercises/space-age.

## Choices

- The planets are a `Planet` enum, and `age` takes one, so an age on a planet that does not exist cannot be asked for. The canonical "not a planet" case becomes `Planet.parse("Sun") == None`; the test file adds one non-canonical case that `parse` finds a planet.
- `Planet.parse` finds the variant whose `derive Display` name matches, over `derive Variants`' list, instead of a `case` with eight string arms that repeat the variant names.
- `Planet.parse` returns a `Maybe`, not a `Result`: a name either names a planet or it does not, and there is only one way to fail.
- The orbital periods are a `case` in `Planet.orbital_period`, so adding a planet without its period is a missing arm.
- The canonical ages are given to two decimal places, so the tests round the computed age to hundredths with a `to_hundredths` helper and compare exactly.
- The table's first row names `Planet.Earth`; later rows write bare variants, which take the enum of the first row's column.

## Friction

- [stdlib] There is no rounding to a number of decimal places, so the tests write `Float.round(years * 100.0) / 100.0`. Elixir has `Float.round(x, 2)` and Gleam `float.to_precision(x, 2)`; Rust and Go have the same gap as Nomi.

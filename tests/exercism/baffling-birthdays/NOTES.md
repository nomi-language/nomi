# Baffling Birthdays

Subject: Dates & Time

Find whether two people in a group share a birthday, draw random groups of birthdates, and estimate how likely a shared birthday is by simulation.

Adapted from Exercism problem-specifications (MIT), exercises/baffling-birthdays.

## Choices

- Birthdates are `std/calendar` `Date`s, written in the tests as `` Date`2000-01-01` `` literals, which the compiler checks. `shared_birthday?` maps each date to its `#(month, day)` and compares the size of the set with the size of the group.
- Randomness is `std/random`'s. `random_birthdates` and `estimated_probability_of_shared_birthday` answer `Generator`s, and the tests run them against one fixed `Seed`, so every run checks the same draws.
- A birthdate is a uniformly random day, 0 to 364 days after 1 January, of a year drawn from `common_years`, the years 1900 to 2023 that `Date.leap_year?` says have no 29 February. Drawing the year first and the day second keeps every day of the year equally likely, with no 29 February to correct for.
- The estimate draws `trials` groups (10,000 by default), checks each with `shared_birthday?`, and answers the percentage that share. The canonical expectations are the exact probabilities; the tests accept an estimate within one percentage point, which the canonical comments leave to the track.
- The canonical "generate requested number of birthdates" case names no size; the test draws groups of 1 to 20. The other three random cases share one group of 100, drawn in the group's `setup`.
- The shared-birthday and estimate cases are `subtests` tables named by the description column; the random-birthdates cases are a `group` of that name, since each checks something different of the same draw.

## Friction

None.

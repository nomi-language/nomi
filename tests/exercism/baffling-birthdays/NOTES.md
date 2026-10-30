# Baffling Birthdays

Subject: Dates & Time
Run: manual

Find whether two people in a group share a birthday, draw random groups of birthdates, and estimate how likely a shared birthday is by simulation.

Adapted from Exercism problem-specifications (MIT), exercises/baffling-birthdays.

## Choices

- Birthdates are `std/calendar` `Date`s, written in the tests as `` Date`2000-01-01` `` literals, which the compiler checks. `has_shared_birthday` walks the group with a set of the `#(month, day)` pairs it has seen and stops at the first repeat, so a large group is seldom read to the end.
- Randomness is `std/random`'s. `random_birthdates` and `estimated_probability_of_shared_birthday` answer `Generator`s, and the tests run them against one fixed `Seed`, so every run checks the same draws.
- A birthdate is a uniformly random day of a year from 1900 to 2023 that `Date.is_leap_year` says has no 29 February. With no 29 February, every year has 365 days, so one `Generator.int` draw `n` below 365 times the number of years picks both: the year's 1 January is `new_years_days`'s entry `n / 365`, and the day is `n % 365` days after it. Every day of the year is equally likely, and each date costs one draw.
- The estimate draws `trials` groups (10,000 by default), checks each with `has_shared_birthday`, and answers the percentage that share. The four estimate cases draw over a million dates, which takes seconds in a browser, so the page shows the recorded report and runs the example only when you press Run. The canonical expectations are the exact probabilities; the tests accept an estimate within one percentage point, which the canonical comments leave to the track.
- The canonical "generate requested number of birthdates" case names no size; the test draws groups of 1 to 20. The other three random cases share one group of 100, drawn in the group's `setup`.
- The shared-birthday and estimate cases are `subtests` tables named by the description column; the random-birthdates cases are a `group` of that name, since each checks something different of the same draw.

## Friction

None.

# Meetup

Subject: Dates & Time

Find the date of a monthly meetup described as "the first Monday", "the last Thursday" or "the teenth Saturday" of a month.

Adapted from Exercism problem-specifications (MIT), exercises/meetup.

## Choices

- The week is an enum, `Week`, with the canonical data's six descriptors, and the day is std/calendar's `Weekday`. The canonical strings (`"teenth"`, `"Monday"`) are the test table's data, written as variants: the first row qualifies them (`Week.Teenth`, `Weekday.Monday`) and the rows after it write `.First`, `.Tuesday`.
- `days_on` lists the month's days on the weekday by trying `Date.new` for each of the 1st to the 31st. `Date.new` answers `Err` for a day the month does not have, so the calendar decides the month's length, with no table of month lengths or leap-year rule here. `Result.to_maybe` and `Maybe.values` drop the misses. `Date.days_in_month` would give the length first, but it answers a `Result` too, and each `Date.new` after it still would, so it adds a step and removes none.
- The first four weeks and the last are taken apart with vector patterns, `#[first, second, third, fourth, ..]` and `#[.., last]`. A weekday falls four or five times in every month, so neither pattern can fail, and each `else` is an `unreachable` that says why (style §28). A vector pattern needs at least as many elements as it names, so the last day, which is the fourth in a month with four, has its own pattern.
- The teenth day is `Iter.find` over the days for one from the 13th to the 19th. Those are seven days in a row, so one of them is on every weekday, and the `None` that `Iter.find` can answer is an `unreachable` that says so.
- The expected dates are `Date` literals, ``Date`2013-05-13` ``, so the table compares dates, not strings.
- The 95 canonical cases are one `subtests` table named by the description column.

## Friction

None.

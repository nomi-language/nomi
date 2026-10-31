# Meetup

Subject: Dates & Time

Find the date of a monthly meetup described as "the first Monday", "the last Thursday" or "the teenth Saturday" of a month.

Adapted from Exercism problem-specifications (MIT), exercises/meetup.

## Choices

- The week is an enum, `Week`, with the canonical data's six descriptors, and the day is std/calendar's `Weekday`. The canonical strings (`"teenth"`, `"Monday"`) are the test table's data, written as variants: the first row qualifies them (`Week.Teenth`, `Weekday.Monday`) and the rows after it write `.First`, `.Tuesday`.
- `days_on` lists the month's days on the weekday: `Date.days_in_month(year, month)!` days, each `Date.new(year, month, day)!`, filtered by weekday. The calendar decides the month's length, with no table of month lengths or leap-year rule here. Both `!`s trap only for a month that is not 1 to 12, which the caller chose, and the trap names the call (style §28).
- The first four weeks are taken apart with a vector pattern, `#[first, second, third, fourth, ..]`. A weekday falls four or five times in every month, so the pattern cannot fail, and its `else` is an `unreachable` that says why (style §28). The last day is `Iter.last(days)!`: the pattern above matched, so `days` is not empty. A vector pattern needs at least as many elements as it names, and the last day is the fourth in a month with four, so it cannot be a fifth name in the same pattern.
- The teenth day is `Iter.find` over the days for one from the 13th to the 19th. Those are seven days in a row, so one of them is on every weekday, and the `None` that `Iter.find` can answer is an `unreachable` that says so.
- The expected dates are `Date` literals, ``Date`2013-05-13` ``, so the table compares dates, not strings.
- The 95 canonical cases are one `subtests` table named by the description column.

## Friction

None.

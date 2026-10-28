# Swift Scheduling

Subject: Dates & Time

Turn a delivery promise made at a meeting, such as "ASAP", "EOW", "11M" or "Q3", into the date and time the delivery is due.

Adapted from Exercism problem-specifications (MIT), exercises/swift-scheduling.

## Choices

- The meeting start and the answer are std/calendar `NaiveDateTime`s, the canonical data's dates with no zone, and the expected values are ``NaiveDateTime`...` `` literals, so the table compares datetimes, not strings.
- The description is parsed once into a private enum, `Description`, with the three fixed words and the two numbered forms, `Month(n)` and `Quarter(n)`; `delivery_date` is then one `case` over it, with a `when` guard for ASAP before 13:00. A number out of range or any other text is `Error.UnknownDescription`, which the canonical data never gives; the test file adds six such cases.
- `numbered` reads both numbered forms at once, `strip_suffix` "M" and `strip_prefix` "Q" each then `String.to_int`, and one `case` over the pair with range guards picks the form.
- Every date step is calendar arithmetic, `Days`, `Hours` and `Months` added to a `Date` or `NaiveDateTime`, so month lengths and leap days (the canonical "EOW translates to leap day") are the calendar's business. A quarter's last day is the first of its last month, plus a month, less a day.
- The first or last workday is `Iter.find` over seven days in a row from the month's first day or the quarter's last, and the end of the week is the next Friday or Sunday found the same way. Seven days in a row hold every weekday, so each `None` is an `unreachable` that says so.
- The canonical data gives meetings from Monday to Friday only; a Saturday or Sunday meeting's EOW is that week's Sunday at 20:00, the later rule's answer.

## Friction

None.

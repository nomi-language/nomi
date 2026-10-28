# Gigasecond

Subject: Dates & Time

Find the moment one gigasecond, a thousand million seconds, after a given moment.

Adapted from Exercism problem-specifications (MIT), exercises/gigasecond.

## Choices

- The moment is std/calendar's `NaiveDateTime`. The canonical moments carry no zone or offset, and the answer is the wall-clock reading a gigasecond later, so a zoned `DateTime` would add a zone the data does not have (style §12 keeps `NaiveDateTime` for such readings).
- The gigasecond is `once gigasecond = Seconds(1_000_000_000)`, and `add` is `moment + gigasecond`: std/calendar's `Add<Seconds, NaiveDateTime>` does the carrying into minutes, hours, days, months and years.
- Three canonical moments are dates alone. The test table holds them as ``Date`2011-04-25` `` literals and the test turns each into midnight with `NaiveDateTime.at_midnight`, so the data says what the canonical data says. The expected moments are ``NaiveDateTime`...` `` literals, compared as values.
- The canonical "does not mutate the input" case is a test that adds a gigasecond and asserts the moment is still what it was. Nomi values cannot change, so it can only pass; it is kept so every canonical case has a test.

## Friction

None.

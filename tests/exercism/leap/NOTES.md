# Leap

Subject: Dates & Time

Decide whether a year is a leap year in the Gregorian calendar.

Adapted from Exercism problem-specifications (MIT), exercises/leap.

## Choices

- The answer is std/calendar's `Date.is_leap_year`, which holds the rule
  (`year % 4 == 0 and (year % 100 != 0 or year % 400 == 0)`), so the
  solution does not write it again. Python's solution can call
  `calendar.isleap` the same way.
- The function is `is_leap_year`, a `?` predicate, since it answers a yes/no
  question about its argument.
- The cases are one `subtests` table. Each row's first column is the
  canonical description, which names the row (`subtests cases, #(description,
  year, expected), description`), so a failed row's report
  shows which case it was.

## Friction

None.

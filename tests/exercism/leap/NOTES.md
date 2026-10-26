# Leap

Subject: Dates & Time

Decide whether a year is a leap year in the Gregorian calendar.

Adapted from Exercism problem-specifications (MIT), exercises/leap.

## Choices

- One boolean expression with `%`, not a helper lambda. The lambda version
  needs its parameter annotated (`fn n: Int ->`), and the plain expression is
  the form most languages' solutions use.
- The function is `leap_year?`, a `?` predicate, since it answers a yes/no
  question about its argument.
- The cases are one `subtests` table. Each row's first column is the
  canonical description, which names the row (`subtests cases, #(description,
  year, expected), description`), so a failed row's report
  shows which case it was.

## Friction

None.

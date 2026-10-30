# Line Up

Subject: Strings & Text

Write the ticket message a deli gives its customers, with the customer's place in line as an English ordinal: 1st, 2nd, 3rd, 4th, 11th, 21st.

Adapted from Exercism problem-specifications (MIT), exercises/line-up.

## Choices

- `format` is one interpolated string, and `suffix` one `case` on the pair `#(number % 100, number % 10)`. The first arm catches 11, 12 and 13 in every hundred, which take "th" whatever their last digit; the next three read the last digit, and everything else is "th". The tuple keeps the exception and the rule in one match instead of an `if` in front of a `case`.
- std/plural's ``PluralCategory.ordinal(number, Locale`en`)`` answers the same four classes from CLDR's rules (`One`, `Two`, `Few`, `Other`), and a `case` on it could pick the suffix. The arithmetic is kept: it is the rule the exercise asks for, and it is as short.
- The 25 canonical cases are one `subtests` table. Each row holds the name, the number and the ordinal the message should carry, and the body builds the expected sentence from them, so the table shows only what differs from row to row.

## Friction

None.

# Leap

Decide whether a year is a leap year in the Gregorian calendar.

Adapted from Exercism problem-specifications (MIT), exercises/leap.

## Choices

- One boolean expression with `%`, not a helper lambda. The lambda version
  needs its parameter annotated (see Friction), and the plain expression is
  the form most languages' solutions use.
- The function is `leap_year?`, a `?` predicate, since it answers a yes/no
  question about its argument.
- The cases are one `subtests` table. Each row's first column is the
  canonical description, bound to `_description`, so a failed row's report
  shows which case it was.

## Friction

- [feature] A lambda bound to a name does not infer its parameter from the
  body. I first wrote:

  ```nomi
  divisible? = |n| year % n == 0
  divisible?(4) and (!divisible?(100) or divisible?(400))
  ```

  and got `error: cannot infer type for parameter n` at `|n|`. `year` is an
  `Int`, so `n` can only be an `Int`, and Gleam infers it from the same body
  (`let divisible = fn(n) { year % n == 0 }`). The spec says this is deliberate ("A later use
  never types a parameter"), and `|n: Int|` fixes it, but the error has no
  hint suggesting that annotation.
- [feature] A `subtests` row has no name of its own, so a table that wants
  the canonical description in its failure report carries it as a discarded
  first column. A form such as `subtests cases, name: .description, (...)`, or
  rendering a row's first String as its label, would read better than
  `(_description, year, expected)` repeated through every exercise here.

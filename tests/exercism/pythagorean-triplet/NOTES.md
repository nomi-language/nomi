# Pythagorean Triplet

Subject: Numbers & Math

Find every Pythagorean triplet `a < b < c` with `a² + b² = c²` whose sides sum to a given number.

Adapted from Exercism problem-specifications (MIT), exercises/pythagorean-triplet.

## Choices

- A triplet is a tuple, `#(a, b, c)`, and the answer a `List` of them, smallest `a` first, as the canonical data lists them. The three sides are read together and never apart, so a struct would only name them.
- The search runs over `a` alone. With `c = n - a - b`, the equation solves to `b = (n² - 2na) / (2(n - a))`, so each `a` has at most one triplet, and it exists when that division is exact and `a < b`. That is one pass of `n / 3` steps, which keeps the largest case (`n = 30000`) to 10,000 divisions instead of a nested loop over `a` and `b`.
- `triplet` answers a `Maybe` per `a`, and `Maybe.values` keeps the ones that exist. The derivation sits in `triplet`'s doc comment, so the arithmetic is explained where it is written.
- The seven canonical cases are one `subtests` table named by the description column.

## Friction

None.

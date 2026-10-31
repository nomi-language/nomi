# Prime Factors

Subject: Numbers & Math

Break a natural number into its prime factors, smallest first.

Adapted from Exercism problem-specifications (MIT), exercises/prime-factors.

## Choices

- Trial division by recursion: `factors_from(n, divisor)` either conses a factor onto the rest (`[divisor, ..factors_from(n / divisor, divisor)]`) or moves to the next divisor in tail position. The non-tail call happens once per factor, so the depth is at most 63.
- It stops as soon as `divisor * divisor > n`, which keeps the large-prime case (93819012551) to about 9,500 steps.
- A `case` with no subject picks among the four conditions in order.
- One `subtests` table for all 12 cases.

## Friction

None.

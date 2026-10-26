# Collatz Conjecture

Subject: Numbers & Math

Count the Collatz steps that take a positive integer to 1.

Adapted from Exercism problem-specifications (MIT), exercises/collatz-conjecture.

## Choices

- The sequence is `Iter.iterate(number, next)`, cut at the first 1 with `Iter.take_while(&(&1 != 1))` and counted. That states the problem directly; a tail-recursive counter was the other option. The cut is one comparison, so it is written as the capture `&(&1 != 1)` rather than as `fn n -> n != 1`.
- A non-positive input returns `Err` with the canonical message, through an early `return` before the pipeline.
- The four step counts are a `subtests` table; the two error cases are plain tests, since they assert a different shape.

## Friction

None.

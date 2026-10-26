# Perfect Numbers

Subject: Numbers & Math

Classify a positive integer as perfect, abundant or deficient by comparing it with the sum of its other divisors.

Adapted from Exercism problem-specifications (MIT), exercises/perfect-numbers.

## Choices

- The answer is a `Classification` enum, and `classify` returns `Result<Classification, Error>`, where `Error` has the one variant `NotPositive` for zero and negative numbers.
- The classification is a `case` over `Int.compare(aliquot_sum(number), number)`, whose three `Ordering`s are the three classes.
- `aliquot_sum` finds divisors only up to the square root and adds each with its pair (`divisor_pair`), so the cases near 33.5 million take about 5,800 steps rather than 33 million.
- The divisor search is a lazy pipe from `Iter.from(1)` under `Iter.take_while`, rather than a range to a computed integer square root.
- Each canonical group is a `group` with one `subtests` table, since every row in a group expects the same answer.

## Friction

None.

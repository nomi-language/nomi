# Nth Prime

Subject: Numbers & Math

Find the nth prime number.

Adapted from Exercism problem-specifications (MIT), exercises/nth-prime.

## Choices

- `prime` returns `Maybe<Int>`: there is no zeroth prime, and `None` says so without an error type for the one way to fail.
- The primes are a lazy pipe, `Iter.from(2) |> Iter.filter(prime?) |> Iter.get(n - 1)`, rather than a sieve: the sieve needs a limit chosen in advance, and the 10,001st prime takes well under a second by trial division.
- `prime?` tries divisors up to the square root with `Iter.take_while` and `Iter.all?`, which stops at the first divisor.
- The five canonical cases are one `subtests` table.

## Friction

None.

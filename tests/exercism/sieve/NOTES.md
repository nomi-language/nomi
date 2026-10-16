# Sieve

List the primes up to a limit with the Sieve of Eratosthenes.

Adapted from Exercism problem-specifications (MIT), exercises/sieve.

## Choices

- Marking is a fold over `2..=limit` into a `Set<Int>` of composites. An unmarked number is prime and adds its multiples from `n * n` with `Range.step_by`, joined with `+` (set union). A `Vector<Bool>` with `Vector.set` was the other option; it returns `Maybe` per write, which the set avoids.
- The primes are the numbers the fold left unmarked, filtered in a second pass.
- The step pipeline is bound to `multiples` before `marked + multiples`, per the style guide's rule for pipes feeding an operator.
- Five plain tests, since the up-to-1000 case is a long literal on its own.

## Friction

- [verbose] Fixed: `nomi fmt` put each element of a long `List<Int>` literal on its own line, so the 168 expected primes took 170 lines. It now fills number literals across the width, and they take 11.

# All Your Base

Subject: Numbers & Math

Convert a number written as digits in one base into its digits in another.

Adapted from Exercism problem-specifications (MIT), exercises/all-your-base.

## Choices

- Errors are an `Error` enum with one variant per canonical message. The checks run in the canonical order (input base, digits, output base), so "both bases are negative" reports the input base.
- Each check is an early `return`, which keeps the conversion unindented.
- The digits become a number with `Iter.reduce` (`total * input_base + d`) after the checks, rather than with `Int.from_digits`. `Int.from_digits` answers `None` for a digit outside the base without saying which, and the canonical errors name the digit; keeping the check and the fold reports `InvalidDigit` with the digit.
- The number becomes digits with `Int.to_digits(number, output_base)`, which gives `[0]` for zero, so zero and an empty list need no early return.
- The 21 cases are one `subtests` table. The first error row writes `Err(Error.InvalidInputBase)` and the later ones `Err(.InvalidDigit)`: a qualified variant in an earlier row gives the enum to the bare ones after it, so the table needs no type annotation.

## Friction

None.

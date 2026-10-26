# All Your Base

Subject: Numbers & Math

Convert a number written as digits in one base into its digits in another.

Adapted from Exercism problem-specifications (MIT), exercises/all-your-base.

## Choices

- Errors are an `Error` enum with one variant per canonical message. The checks run in the canonical order (input base, digits, output base), so "both bases are negative" reports the input base.
- Each check is an early `return`, which keeps the conversion unindented.
- The digits become a number with `Iter.reduce` (`total * input_base + d`), and the number becomes digits with `Iter.iterate` divided down to zero, each step taken `% base`, then `Iter.reverse`. Zero and an empty list both come out `[0]` through one early return.
- The 21 cases are one `subtests` table. The first error row writes `Err(Error.InvalidInputBase)` and the later ones `Err(.InvalidDigit)`: a qualified variant in an earlier row gives the enum to the bare ones after it, so the table needs no type annotation.

## Friction

None.

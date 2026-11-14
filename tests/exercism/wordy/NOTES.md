# Wordy

Subject: Parsing & Validation

Parse and evaluate simple arithmetic word problems, left to right.

Adapted from Exercism problem-specifications (MIT), exercises/wordy.

## Choices

- The evaluator walks the word list with list patterns, including two-word operators (`["multiplied", "by", ..rest]`), instead of tokenizing first.
- Operators are a private `Operation` enum applied by `apply`, rather than lambdas passed around.
- Errors are an `Error` enum. A number where an operator belongs is a syntax error and any other word is an unknown operation, which is the distinction the canonical cases draw.
- Division by zero is `Error.DivisionByZero`, through `NonZeroInt.try_from` and `Int.divide`, instead of the trap `/` raises. The canonical data has no such case; the test file adds one, marked as not canonical.
- Division is its own `divide` function. A binding `else` on `NonZeroInt.try_from(b)` answers `Error.DivisionByZero`, and `Int.divide(a, divisor)` divides by the `NonZeroInt`, an Int that cannot be zero, so the division cannot trap. Keeping it out of `apply` keeps each arm there on one line.
- Guards use binding `else` (`[first, ..rest] = String.words(problem) else { ... }`) so the success path stays unindented. The style guide uses a binding `else`, not `Maybe.to_result` with `?`, when the value is bound for the lines that follow, as every guard here is.
- The 27 canonical cases are one `subtests` table.
- The trailing `?` comes off through `Maybe.flat_map(&String.strip_suffix(&1, "?"))`, a capture rather than `fn rest -> ...`.

## Friction

None.

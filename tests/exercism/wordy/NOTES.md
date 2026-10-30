# Wordy

Subject: Parsing & Validation

Parse and evaluate simple arithmetic word problems, left to right.

Adapted from Exercism problem-specifications (MIT), exercises/wordy.

## Choices

- The evaluator walks the word list with list patterns, including two-word operators (`["multiplied", "by", ..rest]`), instead of tokenizing first.
- Operators are a private `Operation` enum applied by `apply`, rather than lambdas passed around.
- Errors are an `Error` enum. A number where an operator belongs is a syntax error and any other word is an unknown operation, which is the distinction the canonical cases draw.
- Division by zero is `Error.DivisionByZero`, through `Int.to_non_zero` and `Int.divide`, instead of the trap `/` raises. The canonical data has no such case; the test file adds one, marked as not canonical.
- Division is its own `divide` function, one pipe: `Int.to_non_zero(b) |> Maybe.map(&Int.divide(a, &1)) |> Maybe.to_result(Error.DivisionByZero)`. The missing divisor is never bound for later lines, so `Maybe.to_result` as a pipe stage fits rather than a binding `else` (style §16). The capture's open operand takes `NonZeroInt` from the pipe, which picks `Int`'s `Divide<NonZeroInt, Int>` impl. Keeping it out of `apply` keeps each arm there on one line.
- Guards use binding `else` (`[first, ..rest] = String.words(problem) else { ... }`) so the success path stays unindented. The style guide uses a binding `else`, not `Maybe.to_result` with `?`, when the value is bound for the lines that follow, as every guard here is.
- The 27 canonical cases are one `subtests` table.
- The trailing `?` comes off through `Maybe.flat_map(&String.strip_suffix(&1, "?"))`, a capture rather than `fn rest -> ...`.

## Friction

None.

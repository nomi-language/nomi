# Wordy

Parse and evaluate simple arithmetic word problems, left to right.

Adapted from Exercism problem-specifications (MIT), exercises/wordy.

## Choices

- The evaluator walks the word list with list patterns, including two-word operators (`["multiplied", "by", ..rest]`), instead of tokenizing first.
- Operators are a private `Operation` enum applied by `apply`, rather than lambdas passed around.
- Errors are an `Error` enum. A number where an operator belongs is a syntax error and any other word is an unknown operation, which is the distinction the canonical cases draw.
- Division by zero is `Error.DivisionByZero`, through `Int.to_non_zero` and `Int.divide`, instead of the trap `/` raises. The canonical data has no such case; the test file adds one, marked as not canonical.
- Guards use binding `else` (`[first, ..rest] = String.words(problem) else { ... }`) so the success path stays unindented.
- The 27 canonical cases are one `subtests` table.

## Friction

- [verbose] `nomi fmt` puts a binding `else` on one line only within 50 columns, and sets the broken form apart with blank lines, so each guard here takes four lines:

  ```nomi
  Some(total) = String.to_int(first) else {
      return Err(.SyntaxError)
  }

  ```

  The three guards in `answer` take 16 lines with their blank lines. rustfmt has the same 50-column default for `let ... else`, but adds no blank lines around it.
- [idiom] A `None` that becomes an error has two forms: the binding `else` above (four lines after `fmt`) and `total = String.to_int(first) |> Maybe.to_result(Error.SyntaxError) |> try` (one line). Style §16 reads as preferring the `else` ("returns a different error"), but says nothing about `Maybe.to_result` with `try`. The solution keeps the `else`.
- [error-message] Writing that one-liner with the dot form, `String.to_int(first) |> Maybe.to_result(.SyntaxError) |> try`, fails with `.SyntaxError resolves only to enum variants, but the expected type at this position is E`. `E` is `Maybe.to_result`'s unsolved type parameter, a name from inside std; the message would help more by saying no enum is known here and suggesting `Error.SyntaxError`, as the unannotated-binding message does.

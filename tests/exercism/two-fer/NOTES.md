# Two-fer

Say who gets one of two cookies: "One for Alice, one for me.", or "you" when
no name is given.

Adapted from Exercism problem-specifications (MIT), exercises/two-fer.

## Choices

- The missing name is a parameter default, `name: String = "you"`, rather than
  a `Maybe<String>`. The canonical `null` means "called without a name", and a
  default says that at the call site: `two_fer()`.
- Three plain `test` blocks rather than a `subtests` table: the first case
  calls with no argument, so the rows do not share a shape.

## Friction

None.

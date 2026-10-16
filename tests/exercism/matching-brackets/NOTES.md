# Matching Brackets

Check that every bracket, brace and parenthesis in a string is closed by its own kind, in order.

Adapted from Exercism problem-specifications (MIT), exercises/matching-brackets.

## Choices

- The string is scanned as `String.to_codepoints`, so each bracket is a codepoint literal in a pattern (style guide §19).
- The stack holds the closer each open bracket waits for, so a closing bracket only has to equal the stack's head.
- One `case` over the pair `(rest, awaited)` with list patterns. A first version matched `rest` alone and nested a `case awaited` in the closer arm; `fmt` then broke every arm of the outer `case` onto two lines, which hid the shape, so the pair form replaced it.
- One `subtests` table for all 20 cases.

## Friction

- [feature] There are no or-patterns. The natural closer arm, `[')' | ']' | '}' as closer, ..rest] -> ...`, is the parse error `expected ',' or ']' in list pattern`, so the solution adds a `closer?` helper with a four-arm `case` and a guard. Rust and OCaml accept the or-pattern nested in the list pattern; Gleam accepts alternatives at the arm level (`")" | "]" | "}" -> ...`).

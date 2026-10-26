# Matching Brackets

Subject: Parsing & Validation

Check that every bracket, brace and parenthesis in a string is closed by its own kind, in order.

Adapted from Exercism problem-specifications (MIT), exercises/matching-brackets.

## Choices

- The string is scanned as `String.to_codepoints`, so each bracket is a codepoint literal in a pattern (style guide §19).
- The stack holds the closer each open bracket waits for, so a closing bracket only has to equal the stack's head.
- One `case` over the pair `#(rest, awaited)` with list patterns. A first version matched `rest` alone and nested a `case awaited` in the closer arm; `fmt` then broke every arm of the outer `case` onto two lines, which hid the shape, so the pair form replaced it.
- One `subtests` table for all 20 cases.
- A closer that does not match the stack's head ends the scan: `#([')' or ']' or '}', .._], _) -> False`, an `or` pattern nested in the list pattern. A closer that does match is any codepoint equal to the head, since only closers are pushed. Matching the closer by name there too (`[')' or ']' or '}' as closer, ..rest]`) made the arm too long for one line, and `fmt` then broke every arm onto two lines.

## Friction

None.

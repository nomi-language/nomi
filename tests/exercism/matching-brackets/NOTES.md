# Matching Brackets

Subject: Parsing & Validation

Check that every bracket, brace and parenthesis in a string is closed by its own kind, in order.

Adapted from Exercism problem-specifications (MIT), exercises/matching-brackets.

## Choices

- The string is scanned as `String.to_codepoints`, so each bracket is a codepoint literal in a pattern (style guide §20).
- The stack holds the closer each open bracket waits for, so a closing bracket only has to equal the stack's head.
- One `case` over the pair `(rest, awaited)` with list patterns. Matching the pair puts every outcome in one table of arms, where a `case awaited` nested in the closer arm would split them across two levels.
- One `subtests` table for all 20 cases.
- A closer that does not match the stack's head ends the scan: `([')' or ']' or '}', ..], _) -> False`, an `or` pattern nested in the list pattern. A closer that does match is any codepoint equal to the head, since only closers are pushed. Matching the closer by name there too (`[')' or ']' or '}' as closer, ..rest]`) would make the arm too long for one line.

## Friction

None.

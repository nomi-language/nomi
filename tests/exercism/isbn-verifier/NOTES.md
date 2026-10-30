# ISBN Verifier

Subject: Parsing & Validation

Decide whether a string is a valid ISBN-10, with or without dashes.

Adapted from Exercism problem-specifications (MIT), exercises/isbn-verifier.

## Choices

- Dashes are removed first, then each grapheme becomes a value with its position from `Iter.with_index`, and `Maybe.collect` makes one `Maybe<List<Int>>`. A character that is neither a digit nor a check-position `X` makes the whole ISBN invalid in that one step.
- `value` takes the position as a named Bool, `value(character, is_check: i == 9)`, so the `case` arm reads `"X" when is_check -> Some(10)`.
- The length rule is a `when` guard on the `Some` arm, as in luhn, rather than a separate `if` before the conversion. The weights come from the index too, `v * (10 - i)`.
- All 21 cases are one `subtests` table.

## Friction

None.

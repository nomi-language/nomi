# Phone Number

Subject: Parsing & Validation

Clean a NANP phone number down to its ten digits, or say why it is not one.

Adapted from Exercism problem-specifications (MIT), exercises/phone-number.

## Choices

- Errors are an `Error` enum, one variant per canonical error message; tests compare against variants, not message text.
- The checks run in the order the canonical errors imply: letters, then punctuation, then digit count, then the area and exchange codes.
- Digits become a `List<Int>`, so the area and exchange rules are list patterns (`[_, _, _, 0, ..]`) rather than string slicing.
- `national` uses a `case` with `when` guards on the count, so the eleven-digit `[1, ..rest]` pattern can sit among them.
- The work is per codepoint: `Codepoint.is_letter` finds letters, `Codepoint.is_digit` and `is_separator` find punctuation, and `Codepoint.to_digit` with `Maybe.values` keeps the digits as `Int`s in one stage. `is_letter` is Unicode, so `é` is a letter here, not punctuation; `is_digit` is ASCII only, so a full-width `１` is punctuation.
- `is_separator` is one `or` pattern of the six separators and a `_` arm.

## Friction

None.

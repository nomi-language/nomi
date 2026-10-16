# Phone Number

Clean a NANP phone number down to its ten digits, or say why it is not one.

Adapted from Exercism problem-specifications (MIT), exercises/phone-number.

## Choices

- Errors are an `Error` enum, one variant per canonical error message; tests compare against variants, not message text.
- The checks run in the order the canonical errors imply: letters, then punctuation, then digit count, then the area and exchange codes.
- Digits become a `List<Int>`, so the area and exchange rules are list patterns (`[_, _, _, 0, .._]`) rather than string slicing.
- `national` uses a `case` with `when` guards on the count, so the eleven-digit `[1, ..rest]` pattern can sit among them.
- Character classes are `Codepoint` comparisons (`cp >= 'a' and cp <= 'z'`), since the work is per codepoint.

## Friction

- [stdlib] No codepoint classification or digit value. The solution writes `letter?`, `digit?` and `Codepoint.to_int(cp) - Codepoint.to_int('0')` by hand. Rust has `char::is_ascii_alphabetic`, `is_ascii_digit` and `to_digit`; Go has `unicode.IsLetter` and `unicode.IsDigit`; Elixir matches `c in ?0..?9` in a guard.
- [feature] No `or` between patterns. `separator?` is six arms that each answer `True`, where one arm `' ' or '(' or ')' or '-' or '.' or '+' -> True` would do.

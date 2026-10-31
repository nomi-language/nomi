# Resistor Color Tolerance

Subject: Numbers & Math

Read the label of a resistor with one, four or five color bands: its resistance in ohms, kiloohms or megaohms, and the tolerance its last band marks.

Adapted from Exercism problem-specifications (MIT), exercises/resistor-color-tolerance.

## Choices

- The colors are an enum, `Color`, with the ten digit colors in value order and the two tolerance-only colors, gold and silver. The canonical strings are the test table's data, written as variants.
- `label` returns `Maybe<Label>`, a struct of the value as a `Decimal`, the unit and the tolerance as a `Maybe<Decimal>` percent, `None` for the one-band resistor. The canonical answer is those three fields, so the table compares structs; `Label`'s `Display` writes the description's text, `33 ohms ±2%`, which a second table checks.
- The bands are taken apart by one `case` over their count: a lone black band, two digits, or three digits, each then a multiplier and a tolerance. Any other count, or a color that cannot stand where it is (gold as a digit, orange as a tolerance), is `None`, which `make` passes up with `?` at each band; `Int.from_digits(...)!` cannot fail there, since every digit is below ten. The canonical data has no such case, and the test file adds five, in a table of their own.
- The value is a `Decimal`, since the canonical "2.54 megaohms" and "6.89 kiloohms" are exact decimals. The ohms are a whole number, divided by the largest unit they reach with `Decimal.divide_exact` and normalized, so `680` stays `680` and `2540000` reads `2.54`. Dividing by a power of ten is always exact, which the `unreachable` on `divide_exact`'s `None` says.
- The units are an enum named `Magnitude`, as in resistor-color-trio: `Unit` is the prelude's unit type and cannot be redeclared.
- The canonical "Green, brownm orange and grey" case is reimplemented with a corrected tolerance; the table holds only the replacement, under the canonical description with its typo.

## Friction

None.

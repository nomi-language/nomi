# Resistor Color

Subject: Encoding & Ciphers

Look up the digit a resistor band's color stands for, and list the colors in value order.

Adapted from Exercism problem-specifications (MIT), exercises/resistor-color.

## Choices

- The colors are an enum, `Color`, rather than the canonical strings; the tests write `Color.Black` where the data says `"black"`.
- `color_code` spells each value out in a `case` rather than taking the color's position in `Color.variants()`. The position would need a search that answers a `Maybe`, and the table is what the problem states.
- `colors` is `Color.variants()` from `derive Variants`, which lists the variants in declaration order, the value order.
- The expected list in the `Colors` test qualifies only its first element (`[Color.Black, .Brown, ...]`); the later bare variants take its enum.

## Friction

None.

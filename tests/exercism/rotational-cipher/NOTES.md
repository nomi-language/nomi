# Rotational Cipher

Subject: Encoding & Ciphers

Shift every Latin letter of a text a number of places along the alphabet, keeping its case and everything else.

Adapted from Exercism problem-specifications (MIT), exercises/rotational-cipher.

## Choices

- The cipher is a `Map<Codepoint, Codepoint>` built once per call from the ranges `'a'..='z'` and `'A'..='Z'`: each range zipped with itself cycled and dropped by the key. A character that is not a key, such as a space or a digit, maps to itself through `Maybe.with_default`.
- The map avoids codepoint arithmetic. `Int.to_codepoint` and `Codepoint.step_by` answer a `Maybe`, and every shifted letter is a valid codepoint, so each call would need an arm that cannot run.
- `Int.modulo(key, 26)` keeps the key in one turn of the alphabet, so a negative key would rotate backwards. The canonical keys are all from 0 to 26.
- The text is read with `String.to_codepoints`, since the alphabet ranges are codepoints, and written back with `String.from_codepoints`. That takes a `List`, as `to_codepoints` gives one, so the lazy map ends in `Iter.to_list()`.

## Friction

None.

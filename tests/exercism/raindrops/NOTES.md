# Raindrops

Subject: Numbers & Math

Turn a number into raindrop sounds: "Pling" for a factor of 3, "Plang" for 5,
"Plong" for 7, or the number itself when it has none of them.

Adapted from Exercism problem-specifications (MIT), exercises/raindrops.

## Choices

- The factors and sounds are a `once` table of tuples, and the sounds come
  from a filter, map and `String.join` pipeline. Three `if`s appending to a
  string would repeat the same test three times, and Nomi has no mutable
  accumulator to append to.
- The fallback is a one-line `if drops == "" { ... } else { ... }`. A `case`
  on `""` reads the same; I picked the `if` because there is one condition.
- The number becomes text by interpolation, `"${number}"`, rather than
  `Int.to_string(number)`; both are the same `Display` call.
- The table is tuples, not structs: for a three-row local table a tuple is
  the right size. The filter and the map take each row apart in the lambda
  (`fn #(factor, _sound) ->`, `fn #(_factor, sound) ->`) rather than reading
  `.0` and `.1`, since the table is a `once` away from them (style §21).

## Friction

None.

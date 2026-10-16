# Raindrops

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
- The lambdas destructure the tuples and name the unused half
  (`|(factor, _sound)|`). Two field accessors would need a struct; for a
  three-row local table a tuple is the right size.

## Friction

None.

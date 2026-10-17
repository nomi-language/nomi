# ETL

Turn a Scrabble score table keyed by score into one keyed by lower-case letter.

Adapted from Exercism problem-specifications (MIT), exercises/etl.

## Choices

- The input is `Map<Int, List<String>>` and the output `Map<String, Int>`, the canonical shapes with the score as an `Int` rather than the JSON's string key.
- One pipeline: `Iter.flat_map` over the map's `#(score, letters)` entries, then `Iter.to_map`. A `Map` iterates as key-value tuples, so the lambda destructures the entry in its parameter.
- Letters stay `String`s, not `Codepoint`s, since they are text in the table.
- Four plain tests, since the cases differ mostly in the size of their maps and read better as separate literals.

## Friction

None.

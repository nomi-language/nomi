# Proverb

Subject: Strings & Text

Build the "For want of a nail" proverb from any chain of things, one line per link and a closing line for the first.

Adapted from Exercism problem-specifications (MIT), exercises/proverb.

## Choices

- `recite` returns the lines as a `List<String>`, as the canonical data does, rather than one string with newlines.
- Neighbouring pieces are paired with `Iter.zip(pieces, Iter.drop(pieces, 1))`, which stops at the shorter side, so the last piece pairs with nothing. `Iter.windows(pieces, 2)` gives the same pairs as two-element Lists, but a List pattern is refutable, so the lambda could not take each pair apart in its parameter as it takes apart the tuple `zip` yields.
- The closing line is added with `Iter.concat` as one more pipe stage, so the function is one guard and one pipeline.
- The empty list is a binding `else` on `[first, ..]`, which also binds the first piece the closing line names.
- Each case is a plain `test`. A `subtests` table fits the shape, but the expected lists run to seven lines each, and a row per case read worse than a test per case.

## Friction

None.

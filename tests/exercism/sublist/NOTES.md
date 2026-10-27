# Sublist

Subject: Collections & Algorithms

Say whether one list equals another, sits inside it, contains it, or none of these.

Adapted from Exercism problem-specifications (MIT), exercises/sublist.

## Choices

- The answer is a `Relation` enum of the four outcomes, not a string.
- `sublist` is generic over any `Equatable` element, though the canonical cases are all `Int`s.
- The three checks are an `if` / `else if` chain in the order the outcomes are tested: equal first, since equal lists also contain each other.
- `contains_run?` asks whether any of the longer list's `Iter.windows` of the run's length equals the run, after answering the empty run itself, since a window of size 0 does not exist. The other way is to walk the longer list with a list pattern and ask `List.starts_with?(rest, run)` at each position, which needs no lengths.
- The 18 canonical cases are one `subtests` table. The first row's `[]` and `[]` take their element type from the later rows.

## Friction

None.

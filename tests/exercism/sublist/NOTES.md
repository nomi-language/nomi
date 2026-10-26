# Sublist

Subject: Collections & Algorithms

Say whether one list equals another, sits inside it, contains it, or none of these.

Adapted from Exercism problem-specifications (MIT), exercises/sublist.

## Choices

- The answer is a `Relation` enum of the four outcomes, not a string.
- `sublist` is generic over any `Equatable` element, though the canonical cases are all `Int`s.
- The three checks are an `if` / `else if` chain in the order the outcomes are tested: equal first, since equal lists also contain each other.
- `contains_run?` walks the longer list with a list pattern and asks at each position whether the run starts there. The other way is to count the positions and `Iter.drop` and `Iter.take` at each; the recursion needs no lengths.
- The 18 canonical cases are one `subtests` table. The first row's `[]` and `[]` take their element type from the later rows.

## Friction

- [stdlib] There is no `List.starts_with?` and no sliding window, so `starts_with?` is written by hand as `Iter.take(list, Iter.count(prefix)) |> Iter.to_list() == prefix`. Elixir has `List.starts_with?/2`, Rust has `slice.starts_with` and `slice.windows(n)` (the whole check is `a.windows(b.len()).any(|w| w == b)` for a non-empty `b`), and Gleam has `list.window`.

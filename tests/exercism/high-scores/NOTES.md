# High Scores

Subject: Collections & Algorithms

Read a player's list of game scores: all of them, the latest, the best, and the top three.

Adapted from Exercism problem-specifications (MIT), exercises/high-scores.

## Choices

- The scores are a plain `List<Int>`, oldest first, and each property is a file-level function over it. A `HighScores` struct wrapping one list would add a constructor and a field read and nothing else. `scores` answers the list it is given, kept because the canonical data names the property.
- `latest` and `personal_best` answer `Maybe<Int>`, since a player who has not played has neither; they are `Iter.last` and `Iter.max`. The canonical lists are never empty, so the tests assert `Some(...)`.
- `personal_top_three` is `Iter.sort(.Descending)`, `Iter.take(3)` and `Iter.to_list`, which keeps ties and answers fewer than three when there are fewer.
- The four "after" cases check that reading the top three or the best leaves the scores as they were. Values are immutable, so they hold by construction; the tests still call the reader first and then check, as the canonical cases describe.
- The canonical "Top 3 scores" group is a `group` of that name. Its five `personalTopThree` rows are one `subtests` table, and the four "after" cases are tests of their own, since each checks a different reader.

## Friction

None.

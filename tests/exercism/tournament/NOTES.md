# Tournament

Subject: Collections & Algorithms

Tally the results of a small football competition into a league table, ordered by points and then by name.

Adapted from Exercism problem-specifications (MIT), exercises/tournament.

## Choices

- `tally` takes the rows as a `List<String>` and returns the table's lines, as the canonical data gives them.
- `tally` returns a `Result`, with `Error.InvalidRow` carrying a row that is not `home;away;outcome` or whose outcome is not `win`, `draw` or `loss`. The canonical data has no malformed rows; the test file adds a case, marked as not canonical.
- `parse_row` turns each match into its outcome for both teams, `#(home, outcome)` and `#(away, Outcome.reverse(outcome))`, so the rest of the tally never looks at which team was at home.
- The standings are `Iter.group_by` on the team, then a `Standing` per team with its wins, draws and losses counted. Matches played and points are functions of those three, not fields.
- A row of the table and the header are the same `line` function over the team's name and five cells, so the column widths are written once.
- Each canonical case is its own `test` with `rows` and `table` bindings: the tables are several lines each, and a `subtests` row holding two lists of strings would hide which list is which.

## Friction

- [verbose] Sorting by points, most first, and then by name takes two stable sorts, since a tuple is not `Comparable` and `Ordering` has no way to chain a second comparison:

  ```nomi
  |> Iter.sort_by(.team)
  |> Iter.sort_by(Direction.Descending, Standing.points)
  ```

  The reader must know the sort is stable and read the keys bottom up, and `Direction` needs an import from `std/comparable`. Elixir writes `Enum.sort_by(standings, &{-&1.points, &1.team})` and Rust `a.points.cmp(&b.points).reverse().then_with(|| a.team.cmp(&b.team))`.

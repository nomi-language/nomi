# Yacht

Subject: Games & Simulation

Score a throw of five dice in one of the twelve categories of the dice game Yacht.

Adapted from Exercism problem-specifications (MIT), exercises/yacht.

## Choices

- The categories are an enum, `Category`, and `score` is one `case` over it, so a category without a score is a missing arm. The canonical strings (`"full house"`) are the test table's data, written as variants: the first row qualifies `Category.Yacht` and the rows after it are bare.
- The dice are a `List<Int>`, as the canonical data gives them.
- Several categories ask for a shape, not particular faces. `group_sizes` answers how many dice show each face that comes up, sorted: `Iter.frequencies`, then the map's values, then `Iter.sort`. A full house is `[2, 3]` and a yacht `[5]`, whatever the faces are.
- The straights compare the sorted dice with the five faces counting up from 1 or from 2, built as a range, `Iter.to_list(from..from + 5)`; `from` is a named argument at the call, `straight(dice, from: 1)`.
- Four of a kind finds the face that four or more dice show with `Iter.find` over the frequencies, so a yacht scores as four of a kind too. No such face is a real answer, scored 0, so `Maybe.with_default(0)` is the rule, not a guess.
- Each category that is made or not is its own function, so every arm of `score` is one call and stays on one line; an `if` in an arm breaks past the formatter's one-line width and takes every arm with it.
- The 29 canonical cases are one `subtests` table named by the description column.

## Friction

None.

# Poker

Subject: Games & Simulation

Pick the best hand, or the hands that tie for best, from a list of five-card poker hands.

Adapted from Exercism problem-specifications (MIT), exercises/poker.

## Choices

- Each hand gets a `Score`: its `Category`, an enum of the nine hands from high card to straight flush, and the ranks that break a tie, in the order they are compared. Both derive `Comparable`, so the enum's declaration order ranks the categories and the struct compares category first, then the rank lists lexicographically. The best hands are those whose score equals `Iter.max` of the scores, kept in input order.
- The tie-break ranks come from one sort: each rank with how many cards show it, sorted descending by `#(count, rank)`. A full house `5 5 5 9 9` gives `[5, 9]`, two pairs `J Q J Q 8` give `[12, 11, 8]`, and a high-card hand its five ranks from the top, so every category compares its ranks the same way.
- The category is one `case` over the group counts, the straight and the flush together: `#([3, 2], _, _)` is a full house, `#(_, Some(high), True)` a straight flush. `straight` answers the straight's ranks, `[5, 4, 3, 2, 1]` for the wheel `A 2 3 4 5`, so a 5-high straight ranks below a 6-high one without a case of its own.
- The hands arrive as text, so a card that is not a card is data from outside: `best_hands` returns `Err(.InvalidHand(hand))` for it, or for a hand that is not five cards, rather than trapping. The canonical data has no such hand; the test file adds one test of three, marked as not canonical.
- The 37 current canonical cases are one `subtests` table named by the description column. Two cases that newer ones reimplement are left out: the first "two players can have same three of a kind" case and the first "both hands have a flush" case, whose newer versions use different hands.

## Friction

None.

# Camicia

Subject: Games & Simulation

Play out a game of Camicia, a beggar-my-neighbour card game, from two starting decks, and report whether it finishes or loops, how many cards were played and how many tricks were taken.

Adapted from Exercism problem-specifications (MIT), exercises/camicia.

## Choices

- `simulate_game` answers a `Game`, a struct of a `Status` enum (`Finished` or `Loop`) and the two counts, where the canonical data writes `"finished"` and `"loop"`.
- Cards stay the canonical strings. `penalty` is the one place that knows a card: `J`, `Q`, `K` and `A` make the other player pay 1 to 4 cards, and every other card is a number card worth 0.
- A trick is `play`, a tail-recursive function over whose turn it is, the pile so far (newest card first), how many cards the player on turn still owes, and who played the payment card they owe for. A payment card hands the debt to the other player; the last card of a debt ends the trick for that card's player; a player with no card to play ends it for the other.
- The hands are a `Hands` struct of the two decks, with a private `Side` enum and `hand` and `with_hand` to read and replace one side. The collector puts the pile under their deck in the order it was played, which is `Iter.reverse` of the pile as `play` built it.
- `rounds` plays tricks until the player who did not collect has no cards, or the hands take a shape they had before. The shape is both decks with every number card written `-`, since the rules decide nothing by a number card's value; the shapes seen so far, the starting one included, are a `Set` of tuples.
- The 28 canonical cases are one `subtests` table named by the description column. A deck is written as its cards separated by spaces and read with `String.words` (the test file's `deck`), so a 26-card deck fits on a line, and the two decks of "the story is starting to get a bit complicated" are `"""` strings over six lines each. As a list of strings, `nomi fmt` puts each card on its own line, about 360 lines for that case alone.
- The long games, Kleber 1999 to Nessler 2022, play up to 8,344 cards each. Putting a pile under a deck is a `List.concat`, linear in the deck, which is at most 52 cards, so lists serve and no queue is needed.

## Friction

None.

# State of Tic-Tac-Toe

Subject: Games & Simulation

Decide whether a tic-tac-toe board is a win, a draw or a game still going, or explain why no game played by the rules could have left it.

Adapted from Exercism problem-specifications (MIT), exercises/state-of-tic-tac-toe.

## Choices

- `gamestate` returns `Result<State, Error>`. `State` is the three canonical answers and `Error` the three ways a board breaks the rules; `Error`'s `Display` gives the canonical messages, so the variants carry no text.
- The board is read into one `Vector<Codepoint>` of nine cells, row by row, and the eight winning lines are a `once` list of position vectors. `has_won` is `Iter.any` over the lines of `Iter.all` over their cells.
- The rules are counts and wins: O never has more marks than X, X never more than one more than O, and the game stops at a win, so an X win leaves X one mark ahead and an O win leaves the counts equal. A win either way round, or both players winning, is the canonical "game should have ended" error.
- The checks run in the canonical order of precedence: turn order first, then play after a win, then the state. A board with every cell full and no line is a draw.
- The cases are four `subtests` tables by expected outcome. The invalid-board table asserts each error's message, which is what the canonical data gives. The two cases that newer ones reimplement (a middle-row win that broke the turn order, and an older error message) are left out.

## Friction

None.

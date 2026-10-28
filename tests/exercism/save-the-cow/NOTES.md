# Save the Cow

Subject: Games & Simulation

Play a word-guessing game: each guessed letter reveals every place it stands in the secret word, and ten failures lose the game.

Adapted from Exercism problem-specifications (MIT), exercises/save-the-cow.

## Choices

- Adapted: the canonical comments suggest an object that changes with each guess, or a functional reactive design. The Nomi version is a value: `Game.guess` answers the game after the guess, and a sequence of guesses is a fold.
- `Game` is an opaque struct of the word's letters, the set of revealed letters, the failures remaining and the `State`. `guess` is one `case` on the state, with `when` guards for the ongoing game: a letter in the word and not yet revealed reveals it, any other guess (a wrong letter, or a right one guessed again) is a failure, and a failure with none remaining loses. The canonical counts fix that rule: 9 failures remain at the start, 0 after nine and still 0 after the tenth, which loses.
- Guessing after the game is over is an `Err`, one `Error` variant per outcome, whose `Display` gives the canonical messages.
- A guess is a `Codepoint`, since the game works letter by letter (style §19), and `masked_word` maps each letter of the word to itself or `_`.
- The test file's `play` folds the guesses over `Result.flat_map`, so the first refused guess is the answer. The cases are two `subtests` tables, one for the games that end in a state and one for the refused guesses.

## Friction

None.

# Bowling

Subject: Games & Simulation

Score a game of ten-pin bowling roll by roll, refusing rolls the rules do not allow and a score before the game is over.

Adapted from Exercism problem-specifications (MIT), exercises/bowling.

## Choices

- A game is a value, `Game`, holding its rolls. `Game.roll` answers the game after a roll, or an `Err`, and `Game.score` the total of a finished game, so the canonical `roll` and `score` operations are owner functions in `impl Game` that return a new game instead of changing one.
- The game keeps only the rolls. Whether a roll is allowed, and whether the game is over, both come from one walk over them, `pins_standing`: it skips whole frames with list patterns (`[10, ..rest]` for a strike, `[_, _, ..rest]` for two rolls) and answers the pins standing for the next roll, or `None` once the game is over. A frame counter or a "fill balls left" field kept beside the rolls would be a second record that could disagree with them.
- The tenth frame has its own function, `last_frame_standing`, one `case` over the frame's rolls: `[10, second]` leaves `10 - second` pins for the last fill ball, and `[] or [10] or [10, 10]` a full rack.
- `total` scores frames the same way, with a strike's two bonus rolls and a spare's one read by `bonus` from the rolls after the frame. Its last arm is `unreachable`, since `score` calls it only for a game `pins_standing` says is over, which has two rolls in every frame that is not a strike.
- The four canonical error messages are an `Error` enum. A roll above the pins standing is `TooManyPins` whether it is over 10 or over what the frame leaves, as the canonical data gives both one message.
- The canonical data passes every previous roll to `roll`, expecting each to succeed. The test file's `play` folds them with `Iter.try_reduce(rolls, Game.new(), Game.roll)`, which stops at the first refusal and answers it, and each row asserts `Ok(game) = play(rolls)`, so a refused earlier roll fails the row rather than being mistaken for the expected error.
- The canonical cases are two `subtests` tables, one per property, named by the description column: 21 `score` rows, whose expected value is the score or an error, and 10 `roll` rows, all errors.

## Friction

None.

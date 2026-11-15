# Square Root

Subject: Numbers & Math

Find the square root of a perfect square of a positive integer without a built-in square root function.

Adapted from Exercism problem-specifications (MIT), exercises/square-root.

## Choices

- std has `Int.sqrt`, which is the answer the exercise forbids, so `square_root` uses Newton's method on integers instead. The file says so in its `//#` comment.
- `newton` starts from the radicand itself, which is at or above the root for any positive integer, and steps to `(guess + radicand / guess) / 2`. Integer division rounds each step down, so the guesses fall to the root and the first step that does not fall stops the walk. There is no tolerance to choose, as there would be with floats.
- The radicand is promised to be a perfect square, so there is no error case and no `Result`. `square_root` answers 0 for 0 and below, and `newton` takes its guess as a `PositiveInt`, the divisor `/` needs. A step from above the root lands at or above it, so the next guess is at least 1, which `PositiveInt.try_from(next)!` states (style §29).
- The six canonical cases are one `subtests` table named by the description column.

## Friction

None.

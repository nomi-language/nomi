# Robot Simulator

Move a robot around a grid by a string of turn and advance instructions.

Adapted from Exercism problem-specifications (MIT), exercises/robot-simulator.

## Choices

- `Direction` is an enum with `Direction.right` and `Direction.left` as explicit four-arm `case`s, rather than an index into `variants()` modulo 4.
- `Position` and `Robot` are plain structs; the canonical "create" cases are struct literals, so there is no constructor function.
- `Robot.move` answers `Result<Robot, Error>` and stops at an unknown letter. The canonical data has no such case; the alternative was to ignore unknown letters.
- `move` is an `Iter.reduce` over the instruction string whose accumulator is the `Result`; `try result` inside the lambda passes an `Err` along unchanged.
- Updates use spreads (`{..robot, direction: ...}`) and target-typed struct literals (`{x, y: y + 1}` as a `Position`).
- Test rows carry positions as `(Int, Int)` tuples and build each `Robot` in the body, because a table of nested struct literals is unreadable after `nomi fmt` (below).

## Friction

- [verbose] `nomi fmt` breaks any literal three compounds deep onto one field per line, even when it fits, and the break reaches every nested literal (`maxCompoundDepth` in `internal/format/emit.go`). A first version of the table wrote rows as `("changes north to east", {position: {x: 0, y: 0}, direction: .North}, "R", {position: {x: 0, y: 0}, direction: .East})`; each became about 20 lines. The smallest case is `[R{p: P{x: 0, y: 0}, n: 1}]`, which becomes 9 lines. Prettier keeps a short nested object literal on one line when it fits.

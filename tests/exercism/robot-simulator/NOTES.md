# Robot Simulator

Subject: Games & Simulation

Move a robot around a grid by a string of turn and advance instructions.

Adapted from Exercism problem-specifications (MIT), exercises/robot-simulator.

## Choices

- `Direction` is an enum with `Direction.right` and `Direction.left` as explicit four-arm `case`s, rather than an index into `variants()` modulo 4.
- `Position` and `Robot` are plain structs; the canonical "create" cases are struct literals, so there is no constructor function.
- `Robot.move` answers `Result<Robot, Error>` and stops at an unknown letter. The canonical data has no such case; the alternative was to ignore unknown letters.
- `move` is an `Iter.reduce` over the instruction string whose accumulator is the `Result`; `result?` inside the lambda passes an `Err` along unchanged.
- Updates use spreads (`#{..robot, direction: ...}`) and target-typed struct literals (`#{x, y: y + 1}` as a `Position`).
- Test rows are `#(description, robot, instructions, expected)` with each `Robot` a target-typed struct literal, so the body is one assertion. A row is too wide for one line, so `nomi fmt` writes its four parts one per line, each robot on one line.

## Friction

None.

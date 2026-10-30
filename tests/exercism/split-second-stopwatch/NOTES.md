# Split-Second Stopwatch

Subject: Dates & Time

Build a stopwatch that starts, stops, laps and resets, keeping the current lap's time and every finished lap's, and refusing a command its state does not allow.

Adapted from Exercism problem-specifications (MIT), exercises/split-second-stopwatch.

## Choices

- Adapted: the exercise asks for a stopwatch object whose commands change it in place while a clock runs. Nomi values do not change, so `Stopwatch` is a value and each command answers the stopwatch after it, and time passes only when `Stopwatch.advance` says so, which is what the canonical data's `advanceTime` command stands for. No clock is read, so every case is deterministic.
- `Stopwatch` is an opaque struct of its `State`, the current lap's `Duration` and the finished laps. `start`, `stop`, `lap` and `reset` return `Result<Stopwatch, Error>`, each one `case` over the state; `Error` has a variant per refusal and its `Display` gives the canonical messages. `advance` adds time only while running, and `total` is `Iter.sum` of the laps plus the current one.
- Times are `Duration`s in the API and in the test file, which writes the canonical `HH:MM:SS` times as `Duration` literals: `00:01:28` is ``Duration`1m28s` ``, and a reading compares as a `Duration`.
- Each canonical case is a list of commands and readings, so the test file writes them as `Step` variants, as circular-buffer does: the four commands, `Refused(command, message)` for one the state refuses, `Advance(time)`, and the four readings with their expected values. `replay` runs the steps with `Iter.map_reduce` from a new stopwatch and answers what happened in the same shape, so each case is one `replay(steps) == steps`, and a failure shows the first step that went differently. `Refused` holds the command it tried, so the enum refers to itself.
- The 29 canonical cases are one `subtests` table named by the description column.

## Friction

None.

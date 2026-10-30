# Error Handling

Subject: Parsing & Validation

Report failure in the ways the language offers, and release a resource whichever way the work using it ends.

Adapted from Exercism problem-specifications (MIT), exercises/error-handling.

## Choices

- Adapted: the exercise has no canonical data; each track sets its own tests, and most ask for an exception to be thrown and caught, a "none" result, a success-or-failure result, and a file-like object closed on success and on an exception. Nomi has no exceptions, and a trap is never caught inside the program. This port keeps the tests' shape, after the Python track's five: a missing answer is `None` (`number`), a failure the caller handles is an `Err` (`parse`), a broken promise traps (`parse_trusted`, seen from outside as a spawned task's `Failed` outcome), and a resource is released when the work succeeds, fails or traps.
- The resource is a real one, a `std/io` `Reader` over text, so the tests need no fake that records being closed: reading from a closed reader is refused with `io.Error.Closed`, which `is_closed` checks.
- `sum_lines` and `sum_trusted_lines` register `defer Reader.close_quietly(reader)` first, so the reader is closed on every way out of the function: the sum, an `Err` from `total`, and a trap. Each answers `total(...)` directly; a call made while a `defer` is pending is an ordinary call, so the reader is closed after `total` has read it.
- The trap is a postfix `!`, `number(text)!`, which answers the number and stops the program with ``line N: `number(text)` found nothing`` when the caller's text is not one. A test spawns the trapping call in a `concurrent` block and checks `Task.outcome` is `.Failed(_)`, the one place a program can see a trap.
- `Error` has its own variant for a line that is not a number and one that wraps the reader's `io.Error`, so `total` passes `Error.Io` to `Result.map_err`.

## Friction

None.

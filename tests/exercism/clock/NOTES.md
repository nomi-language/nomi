# Clock

A time of day without a date that wraps when minutes are added or taken away.

Adapted from Exercism problem-specifications (MIT), exercises/clock.

## Choices

- `Clock` is a `pub opaque type Clock Int` holding minutes past midnight, always in `0..1440`. Two clocks that show the same time hold the same Int, so the built-in `==` is the canonical equality.
- `Clock.add` and `Clock.subtract` are owner functions taking an `Int` of minutes. An `impl Add<Int, Clock> for Clock` (`clock + 3`) was the other option; named functions match the exercise and keep `Int` minutes from looking like a duration type.
- Rendering is an `impl Display`, and tests compare `"${clock}"` with the canonical `HH:MM` text.
- Parameters destructure the distinct directly (`fn add(Clock(time), minutes: Int)`).

## Friction

- [stdlib] No floored or Euclidean modulo: `%` takes the dividend's sign and `Int.modulo` is the same operation over a `NonZeroInt`. `wrap` writes `(minutes % day + day) % day`. Rust has `i64::rem_euclid`, Elixir `Integer.mod/2`, Gleam `int.modulo`.

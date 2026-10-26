# Resistor Color Trio

Subject: Encoding & Ciphers

Read a resistor's first three color bands as a resistance, written in the largest metric unit that keeps the value whole.

Adapted from Exercism problem-specifications (MIT), exercises/resistor-color-trio.

## Choices

- The answer is a `Resistance` struct of a value and a `Magnitude` enum (`Ohms`, `Kiloohms`, `Megaohms`, `Gigaohms`), matching the canonical `{value, unit}`, rather than the label text. The enum is `Magnitude`, not `Unit`: `Unit` names Nomi's empty value and is reserved, and `Magnitude` says what the variants are.
- `label` takes a `List<Color>` and answers `Maybe<Resistance>`, `None` for fewer than three bands, for the reason resistor-color-duo gives. The binding `else` keeps the success path unindented.
- `in_largest_unit` folds over the larger units with `Iter.reduce`, dividing by 1000 while the value stays whole and is not zero. It needs no arm for running out of units, which a recursive walk down a list of units would.
- The ten canonical cases are one `subtests` table whose rows hold the expected value and unit as two columns, rebuilt into a `Resistance` in the assertion.

## Friction

- [stdlib] There is no integer power, so `power_of_ten` is `Iter.reduce(1..=exponent, fn n = 1, _ -> n * 10)`. Rust has `i64::pow`, Elixir `Integer.pow/2`, Gleam `int.power` (over floats); Go has only `math.Pow` over floats.

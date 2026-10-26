# RNA Transcription

Subject: Encoding & Ciphers

Turn a DNA strand into its RNA complement: G to C, C to G, T to A and A to U.

Adapted from Exercism problem-specifications (MIT), exercises/rna-transcription.

## Choices

- `to_rna` returns `Result<String, String>`, though the canonical data has no
  error case. A character other than the four nucleotides has no complement,
  and the alternatives were a wrong answer or a trap. The tests compare with
  `Ok(expected)`.
- The nucleotides stay one-letter Strings matched by a `case`, not an enum.
  An enum would need a parse step and a render step around a four-arm table
  for no gain in a function this size.
- `Result.collect` turns the per-nucleotide results into one, so the first
  invalid nucleotide becomes the error.
- The parts are joined with `Result.map(&String.join(&1))`, a capture,
  because `String.join` cannot be passed bare (see below).

## Friction

- [feature] A function with a defaulted parameter cannot be passed where a
  function of fewer parameters is expected. `Result.map(String.join)` is
  rejected, because `String.join` is `(Iter<String>, String) -> String` with
  the separator defaulted:

  ```
  error: argument 2: expected (List<String>) -> U, got (Iter<String>, String) -> String
  ```

  The capture `&String.join(&1)` works. Gleam has no defaults; Elixir names
  the arity, `&Enum.join/1`, which is what this wants.

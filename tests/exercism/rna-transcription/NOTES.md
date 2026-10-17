# RNA Transcription

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

## Friction

- [feature] A function with a defaulted parameter cannot be passed where a
  function of fewer parameters is expected. `Result.map(String.join)` is
  rejected because `String.join` is `(Iter<String>, String) -> String` with
  the separator defaulted:

  ```
  error: argument 2: expected (List<String>) -> U, got (Iter<String>, String) -> String
   --> tests/exercism/rna-transcription/rna_transcription.nomi:7:19
    |
  7 |     |> Result.map(String.join)
    |                   ^^^^^^^^^^^
    = help: defaults do not apply to a function value, so pass a lambda that calls it: fn a -> String.join(a)
  ```

  The help is clear, and `fn parts -> String.join(parts)` works. It is the one
  place in these exercises where style §22's "pass the function, not a
  lambda" could not be followed. Gleam has no defaults; in Elixir
  `&Enum.join/1` names the one-argument arity, which is what this wants.

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
- The parts are joined with `Result.map(String.join)`: where a function of
  one parameter is expected, `String.join` takes its default separator.

## Friction

None.

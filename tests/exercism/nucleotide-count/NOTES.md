# Nucleotide Count

Count how often each of the four nucleotides occurs in a DNA strand, rejecting any other letter.

Adapted from Exercism problem-specifications (MIT), exercises/nucleotide-count.

## Choices

- The nucleotides are an enum, `Nucleotide`, with `derive Variants` so the zero counts come from `Nucleotide.variants()` rather than a hand-written four-entry map.
- The result is `Result<Map<Nucleotide, Int>, String>`; the error is the canonical message as a `String`, since there is only one way to fail.
- Parsing and counting are separate steps: `Iter.map(Nucleotide.parse)` then `Result.collect()` with `try`, then `Iter.frequencies`, merged over the zero map with `+`.
- `fmt` turned `|> Result.collect() |> try` into `|> try Result.collect()`; both read fine.
- The expected maps use dot-shorthand keys after the first (`%{Nucleotide.A => 0, .C => 0, ...}`), which the map literal accepts.

## Friction

None.

# Nucleotide Count

Subject: Strings & Text

Count how often each of the four nucleotides occurs in a DNA strand, rejecting any other letter.

Adapted from Exercism problem-specifications (MIT), exercises/nucleotide-count.

## Choices

- The nucleotides are an enum, `Nucleotide`, with `derive Variants` so the zero counts come from `Nucleotide.variants()` rather than a hand-written four-entry map.
- The result is `Result<Map<Nucleotide, Int>, String>`; the error is the canonical message as a `String`, since there is only one way to fail.
- Parsing and counting are separate steps: `Iter.map(Nucleotide.parse)` then `Result.collect()?`, then `Iter.frequencies`, merged over the zero map with `+`.
- The five cases are one `subtests` table named by the description column, since each checks `counts(strand) == expected`; the error case is a row whose expected value is an `Err`.
- The expected maps qualify their first key and leave the rest bare, `%{Nucleotide.A => 0, .C => 0, .G => 1, .T => 0}`: nothing outside the `subtests` table gives a map its type, and the first key sets the enum for the keys after it. Every row's map qualifies its first key, so each row reads on its own, though the first row's type would already reach the rows after it.
- The zero map pairs each nucleotide with 0 through `Iter.map(&#(&1, 0))`: a capture of the tuple literal is the function that builds it, which says the pairing without naming the nucleotide in a lambda.

## Friction

None.

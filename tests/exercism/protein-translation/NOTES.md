# Protein Translation

Subject: Encoding & Ciphers

Translate an RNA strand, three nucleotides at a time, into the amino acids of its protein, stopping at the first STOP codon.

Adapted from Exercism problem-specifications (MIT), exercises/protein-translation.

## Choices

- Amino acids are an `AminoAcid` enum, not the canonical strings.
- The error is `Error.InvalidCodon(String)`, carrying the codon that did not translate: `"XYZ"`, or the short tail `"U"` of `"AUGU"`. The canonical data gives only the message "Invalid codon".
- `proteins` is one pipeline: `Iter.chunks(3)` cuts the codons, `translate` reads each as `Result<Maybe<AminoAcid>, Error>` with `Ok(None)` for STOP, `Iter.take_while` stops at the first STOP, `Result.collect` keeps the first error, and `Maybe.values` unwraps the amino acids. Because `take_while` is lazy, nothing after a STOP is read, which is why `"UUCUUCUAAUGGU"` translates despite its short tail.
- STOP is `None` rather than a variant of a private `Codon` enum. A `Codon` enum names it better, but its `Stop` would have to be filtered out again after `take_while`, the same as `None` is.
- `Iter.map(&String.join(&1))` is a capture because a function value ignores `String.join`'s default separator, so `Iter.map(String.join)` does not type.
- "Non-existing codon can't translate" (`"AAA"`) is left out: "Unknown amino acids, not part of a codon, can't translate" reimplements it.
- The success table has no type annotation. Its first row's expected list is `[]`, and its second row writes `AminoAcid.Methionine` qualified, so the bare variants in later rows take `AminoAcid` from it.

## Friction

None.

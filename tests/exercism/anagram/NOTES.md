# Anagram

Subject: Strings & Text

Pick out the candidates that are anagrams of a word, ignoring case, where a word is not an anagram of itself.

Adapted from Exercism problem-specifications (MIT), exercises/anagram.

## Choices

- The anagram key is `Iter.sort(word)`: a `String` iterates by grapheme, so sorting it gives a `List<String>` to compare, with no codepoint conversion.
- Case is ignored with `String.fold_case` on both sides, once for the subject and once per candidate. Folding is Unicode's caseless matching, which `String.to_lower` only approximates: the two Greek sigmas `ς` and `σ` fold to one letter and lower-case to two.
- All 18 current cases are one `subtests` table, since every row is the same call and comparison. The four cases that newer ones reimplement are left out.
- Each row carries its canonical description as its first column and is named by it (`subtests cases, #(description, subject, candidates, expected), description`), so a failing row reports under its description.

## Friction

None.

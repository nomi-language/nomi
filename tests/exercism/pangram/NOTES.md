# Pangram

Decide whether a sentence uses every letter of the English alphabet at least
once, ignoring case.

Adapted from Exercism problem-specifications (MIT), exercises/pangram.

## Choices

- The sentence becomes a `Set<Codepoint>` and the check is
  `Iter.all?('a'..='z', ...)` over a codepoint range. Codepoint literals and
  ranges are what style §19 recommends for ASCII enumeration.
  `Set.subset?(Iter.to_set('a'..='z'), letters)` works as well; I picked
  `Iter.all?` because it reads as the problem statement.
- The case "case insensitive" (uuid 2577bf54) is left out because canonical
  data replaces it with "a-m and A-M are 26 different characters but not a
  pangram" (uuid 7138e389), which is included.

## Friction

None.

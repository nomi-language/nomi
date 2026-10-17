# Word Count

Count how many times each word occurs in a sentence, ignoring case and
punctuation but keeping apostrophes inside contractions.

Adapted from Exercism problem-specifications (MIT), exercises/word-count.

## Choices

- The whole solution is a four-stage pipeline: lower-case, find the words
  with one regex, and `Iter.frequencies`. The regex
  `[a-z0-9]+('[a-z0-9]+)*` admits an apostrophe only between two letters or
  digits, so quotes around a word fall away without a trimming step.
- Lower-casing happens before matching, so the regex needs no upper-case
  class.
- The expected values are map literals in the table; `Map` equality ignores
  order, so they are written in the canonical order.
- The first "with apostrophes" case (uuid 4185a902) is left out because
  canonical data replaces it with uuid 4ff6c7d7, which is included.

## Friction

None.

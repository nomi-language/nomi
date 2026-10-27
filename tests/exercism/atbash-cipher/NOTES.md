# Atbash Cipher

Subject: Encoding & Ciphers

Encode and decode text by swapping each letter with its mirror in the alphabet.

Adapted from Exercism problem-specifications (MIT), exercises/atbash-cipher.

## Choices

- The cipher is a map from each letter to its mirror, `Iter.zip('a'..='z', Iter.reverse('a'..='z')) |> Iter.to_map()`, built once. Arithmetic on code point numbers (`219 - n`) was the other option; the map shows the two alphabets the description shows.
- `translate` works on code points, since the cipher is ASCII. `swap` keeps a digit and looks a letter up in the map, so anything else answers `None` and `Maybe.values` drops it.
- The cipher is its own inverse, so `encode` and `decode` share `translate` and differ only in how they join the letters: groups of five from `Iter.chunks(5)`, or one run.
- Each canonical group is a test named by the group's description, holding one `subtests` table; a `group` block is for shared `clock`, `boot` or `setup`, which none needs.

## Friction

- [feature] A function with a defaulted parameter cannot be passed where a function of fewer parameters is expected, so joining each group of five is `Iter.map(&String.join(&1))` rather than `Iter.map(String.join)`. The error's hint names the capture to write:

  ```
  error: argument 2: expected (List<String>) -> U, got (Iter<String>, String) -> String
    = help: defaults do not apply to a function value, so pass a capture that calls it: &String.join(&1)
  ```

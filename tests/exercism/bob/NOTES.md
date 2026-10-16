# Bob

Answer a remark the way a lackadaisical teenager would, depending on whether
it is a question, a shout, both, or silence.

Adapted from Exercism problem-specifications (MIT), exercises/bob.

## Choices

- Silence leaves early with `if remark == "" { return ... }`, and the other
  four answers are a `case` on the tuple `(question, shouting)` with all four
  Bool pairs written out. The checker proves the four arms exhaustive, and the
  table reads as the problem's rules. A chain of `else if`s was the
  alternative.
- A shout is "some codepoint is `Codepoint.upper?` and none is
  `Codepoint.lower?`", which states the rule directly. Comparing the remark
  with `String.to_upper` and `String.to_lower` of it says the same through
  case mapping, and a reader has to work out that the pair means "has a
  cased letter and no lower-case one".
- The parameter is trimmed into a binding of the same name
  (`remark = String.trim(remark)`), per style §11.
- Replies are plain Strings, since the canonical data compares strings and
  nothing else consumes them.
- The case "multiple line question" (uuid 66953780) is left out because
  canonical data replaces it with uuid 2c7278ac, which is included under the
  same description.

## Friction

- [feature] Nomi strings have no `\r` escape; the "other whitespace" case
  writes `"\n\u{d} \t"`, and `nomi fmt` lower-cases the hex I wrote as
  `\u{D}`. Rust, Go, Elixir and Gleam all accept `"\n\r \t"`. The spec lists
  the omission as deliberate, but carriage returns turn up in any text read
  from a Windows file or a network protocol, and `\u{d}` is harder to
  recognize.

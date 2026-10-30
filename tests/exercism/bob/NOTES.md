# Bob

Subject: Strings & Text

Answer a remark the way a lackadaisical teenager would, depending on whether
it is a question, a shout, both, or silence.

Adapted from Exercism problem-specifications (MIT), exercises/bob.

## Choices

- Silence leaves early with `if remark == "" { return ... }`, and the other
  four answers are a `case` on the tuple `#(question, shouting)` with all four
  Bool pairs written out. The checker proves the four arms exhaustive, and the
  table reads as the problem's rules. A chain of `else if`s was the
  alternative.
- A shout is `String.is_upper(remark)`: the remark has a letter, and every
  letter is upper case. That is the problem's rule in one call. Comparing the
  remark with `String.uppercase` of it was the alternative, but `"1, 2, 3"`
  equals its own upper case, so that needs a second check for a letter.
- The parameter is trimmed into a binding of the same name
  (`remark = String.trim(remark)`), per style §11.
- Replies are plain Strings, since the canonical data compares strings and
  nothing else consumes them.
- The "other whitespace" case writes its remark `"\n\r \t"`, the escapes the
  canonical data uses, so the test reads as the specification does.
- The case "multiple line question" (uuid 66953780) is left out because
  canonical data replaces it with uuid 2c7278ac, which is included under the
  same description.

## Friction

None.

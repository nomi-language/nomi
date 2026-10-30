# SGF Parsing

Subject: Parsing & Validation

Parse a Smart Game Format game tree into nodes of properties and their child trees, with SGF's escaping rules for text values.

Adapted from Exercism problem-specifications (MIT), exercises/sgf-parsing.

## Choices

- The parser is recursive descent over `String.to_codepoints`, one function per part of the grammar (`node`, `variations`, `read_properties`, `read_values`, `read_text`). Each answers what it read and the codepoints after it, `Result<#(T, List<Codepoint>), Error>`, and `?` passes a failure up. The delimiters are codepoint literals in list patterns, `[';', ..rest]` and `['\\', '\n', ..rest]` (style §19).
- A `;` after a node starts its one child, and `(` starts its variations. The node that ends a sequence reads the `)` that closes the sequence's tree, so `(;A[B];B[C])` needs no count of open parentheses.
- `Tree` is a struct of a `Map<String, List<String>>` and a `List<Tree>`, as the canonical data's JSON is shaped, so the expected trees in the tests are literals of it.
- SGF Text's rules are three arms of `read_text`: a `\` before a newline drops both, a `\` before anything else keeps that character, and any whitespace but a newline becomes a space. The escaped and unescaped character share one arm, `['\\', c, ..rest] or [c, ..rest]`.
- The canonical errors are variants of `Error`. Text the canonical data has no case for, an unclosed tree or value or text after the tree, is one more variant, `Malformed`; five tests marked as not canonical cover it.
- The canonical cases are one `subtests` table. The canonical data replaces "escaped property" with "mixing various kinds of whitespace and escaped characters in property value" (`reimplements`); the table holds only the replacement.

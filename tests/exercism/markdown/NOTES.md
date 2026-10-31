# Markdown

Subject: Parsing & Validation

Turn a small part of Markdown into HTML: headings, unordered lists, paragraphs, and bold and italic text.

Adapted from Exercism problem-specifications (MIT), exercises/markdown.

## Choices

- The exercise is a refactoring one: tracks ship a confusing parser to clean up. There is no Nomi stub to start from, so this is the solution written directly, against the canonical cases.
- Parsing and rendering are separate. Each line becomes a private `Block` (`Heading`, `Item`, `Paragraph`), and `render` writes one block's HTML. Lists are the only structure wider than a line, so they are found between the two: `Iter.chunk_by(&(&1 is .Item(_)))` groups the blocks into runs by whether each is an item, and a run that starts with an item is wrapped in `<ul>`.
- `render_run` matches `[.Item(_), ..]` and leaves everything else to `_`, so the empty run `chunk_by` never yields needs no arm of its own.
- A heading is ``Regex`^(#{1,6}) (.*)$` ``, so seven `#`s fall through to a paragraph with no level check. The literal's type makes a match's groups a `#(String, String)`, so the heading arm destructures them, and the `_` arm is the line that is not a heading.
- Bold and italic are two `String.replace` pipe stages over the text, each a `Regex` with a lazy group that the replacement names as `$1`, bold first so that `__` is not read as two italic marks.
- The canonical cases are one `subtests` table. The canonical data replaces "with h7 header level", which expected the line back unchanged, with "h7 header level is a paragraph" (`reimplements`); the table holds only the replacement.

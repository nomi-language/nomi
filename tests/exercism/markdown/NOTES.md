# Markdown

Subject: Parsing & Validation

Turn a small part of Markdown into HTML: headings, unordered lists, paragraphs, and bold and italic text.

Adapted from Exercism problem-specifications (MIT), exercises/markdown.

## Choices

- The exercise is a refactoring one: tracks ship a confusing parser to clean up. There is no Nomi stub to start from, so this is the solution written directly, against the canonical cases.
- Parsing and rendering are separate. Each line becomes a private `Block` (`Heading`, `Item`, `Paragraph`), and `render` writes one block's HTML. Lists are the only structure wider than a line, so they are found between the two: `Iter.chunk_by(is_item)` groups the blocks into runs, and a run that starts with an item is wrapped in `<ul>`.
- `render_run` matches `[.Item(_), .._]` and leaves everything else to `_`, so the empty run `chunk_by` never yields needs no arm of its own.
- A heading is ``Regex`^(#{1,6}) (.*)$` ``, so seven `#`s fall through to a paragraph with no level check. Its `case` has a real `_` arm, the line that is not a heading, which also covers the group shapes the pattern cannot produce.
- Bold and italic are two `Regex.replace_all` pipe stages over the text, with lazy groups, bold first so that `__` is not read as two italic marks.
- The canonical cases are one `subtests` table. The canonical data replaces "with h7 header level", which expected the line back unchanged, with "h7 header level is a paragraph" (`reimplements`); the table holds only the replacement.

## Friction

- [idiom] Two calls replace a regex's matches: `String.replace` takes its replacement literally and `Regex.replace_all` expands `$1`, so the emphasis stages use the second. Elixir's `String.replace` expands groups.
- [verbose] There is no expression that asks whether a value matches a pattern, so `is_item` is a four-line `case` with a `True` arm and a `False` arm. Rust has `matches!(block, Block::Item(_))` and Elixir `matches({:item, _}, block)`.

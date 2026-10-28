# Markdown

Subject: Parsing & Validation

Turn a small part of Markdown into HTML: headings, unordered lists, paragraphs, and bold and italic text.

Adapted from Exercism problem-specifications (MIT), exercises/markdown.

## Choices

- The exercise is a refactoring one: tracks ship a confusing parser to clean up. There is no Nomi stub to start from, so this is the solution written directly, against the canonical cases.
- Parsing and rendering are separate. Each line becomes a private `Block` (`Heading`, `Item`, `Paragraph`), and `render` writes one block's HTML. Lists are the only structure wider than a line, so they are found between the two: `Iter.chunk_by(item?)` groups the blocks into runs, and a run that starts with an item is wrapped in `<ul>`.
- `render_run` matches `[.Item(_), .._]` and leaves everything else to `_`, so the empty run `chunk_by` never yields needs no arm of its own.
- A heading is ``Regex`^(#{1,6}) (.*)$` ``, so seven `#`s fall through to a paragraph with no level check. Its `case` has a real `_` arm, the line that is not a heading, which also covers the group shapes the pattern cannot produce.
- Bold and italic are two `Regex.replace_all` calls with lazy groups, bold first so that `__` is not read as two italic marks.
- The canonical cases are one `subtests` table. The canonical data replaces "with h7 header level", which expected the line back unchanged, with "h7 header level is a paragraph" (`reimplements`); the table holds only the replacement.

## Friction

- [verbose] `Regex.replace_all` takes the regex first and the text second, so it cannot be a plain pipe stage over the text: `emphasize` writes `` |> then &Regex.replace_all(Regex`__(.+?)__`, &1, "<strong>$1</strong>") ``. `String.replace` takes the text first but does not expand `$1`. Elixir's `Regex.replace(regex, string, ...)` has the same order, but Elixir's `String.replace` expands groups; Rust's `regex.replace_all(text, "$1")` is a method on the regex.
- [verbose] There is no expression that asks whether a value matches a pattern, so `item?` is a four-line `case` with a `True` arm and a `False` arm. Rust has `matches!(block, Block::Item(_))` and Elixir `match?({:item, _}, block)`.

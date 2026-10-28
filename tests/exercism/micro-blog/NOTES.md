# Micro Blog

Subject: Strings & Text

Truncate a post to its first five characters, where a character may be an emoji or any other Unicode.

Adapted from Exercism problem-specifications (MIT), exercises/micro-blog.

## Choices

- `truncate` is `String.take(post, 5)`. A `String` is iterated, counted and taken by grapheme cluster, so an emoji is one character, and so is a letter with a combining accent. The exercise's trap, counting UTF-16 code units or bytes, has no API to fall into.
- The canonical data avoids characters of more than one codepoint. A test marked as not canonical adds two: a family emoji (five codepoints joined by zero-width joiners) and an `e` followed by a combining acute accent. Both count as one character, where a codepoint count would cut them apart.
- The twelve canonical cases are one `subtests` table.

## Friction

None.

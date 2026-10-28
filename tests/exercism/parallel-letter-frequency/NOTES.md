# Parallel Letter Frequency

Subject: Strings & Text

Count how often each letter appears in a list of texts, counting the texts in parallel.

Adapted from Exercism problem-specifications (MIT), exercises/parallel-letter-frequency.

## Choices

- Each text is counted by its own task: `Task.spawn_all(letter_counts, max_running: workers)` inside a `concurrent { }` block, then `Task.await_all`, then the per-text maps are added up. The tasks share nothing and each answers a value, so there is no lock or channel; the block ends only when every task has.
- `spawn_all` requires a bound on how many tasks run at once. `workers` is 4, a `once` so the number has a name.
- A text's counts are `String.to_lower`, `String.to_codepoints`, `Codepoint.letter?` and `Iter.frequencies`. `Codepoint.letter?` is Unicode's letter class, so the "Unicode letters" case (`本`, `φ`, `ほ`, `ø`) needs nothing special, and digits, punctuation and whitespace drop out.
- The result is a `Map<String, Int>` keyed by each letter as a one-letter string, as the canonical data writes it, because of the friction below.
- The four texts of the "large texts" case, the opening of Dostoevsky's *Notes from Underground* in Constance Garnett's public-domain translation, are a `pub once` in their own file, `novel.nomi`, so the test file stays readable. Each is a `"""` string with the canonical line breaks; only letters are counted, so the indentation the string keeps changes nothing.
- The canonical "combination of lower- and uppercase letters, punctuation and white space" case counts a passage from a novel still under copyright, so this port does not reproduce it. A test of the same name, marked not canonical, counts a sentence written for it that mixes the same things.
- The other twelve canonical cases are one `subtests` table. "many small texts" builds its fifty copies with `Iter.repeat` and `Iter.take`.

## Friction

- [stdlib] Adding one count map into another is a hand-written `Iter.reduce` with `Map.get`, `Maybe.with_default(0)` and `Map.put`, since std's `Map.merge` keeps the right-hand value and has no form that combines the two. Elixir has `Map.merge/3` with a resolver, Rust the `entry` API, Python `Counter`'s `+`, and Go a loop, as here.
- [feature] A codepoint literal is ASCII only (`'本'` is an error naming the rule), so the canonical "Unicode letters" expectation cannot be a `Map<Codepoint, Int>` literal. The counts are keyed by one-letter strings instead, which costs `Iter.map(Codepoint.to_string)` after the letter test. Rust's `char` and Elixir's `?本` take any scalar value.

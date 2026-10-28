# Dominoes

Subject: Collections & Algorithms

Lay a set of domino stones in a ring, turning each as needed, so that touching halves match and the two free ends match each other.

Adapted from Exercism problem-specifications (MIT), exercises/dominoes.

## Choices

- This is the exercise's second level: `chain` returns the chain itself, `Maybe<List<Stone>>`, not only whether one exists. A stone is a `#(Int, Int)`, as the canonical comments suggest for a language with pairs. It is written out rather than named with a `typealias`: an alias is for shortening a long anonymous shape, and a pair is short.
- The search is a depth-first backtrack. The first stone starts the chain, and `extend` tries every stone left, either way round, whose first half matches the free end, and recurses. The candidates are a lazy pipeline ended by `Iter.find`, so the search stops at the first chain that closes and backs out of a dead end by moving on to the next candidate. A chain that uses every stone closes when its two ends match.
- The chain is built last stone first, with `[stone, ..laid]`, and reversed once at the end, so each step is a cons.
- `picks` pairs each stone with the stones other than it, the list with that stone taken out by `List.remove_at`.
- The canonical data lists only whether a chain exists, since a set has many chains. The test file checks a found chain with `chain_of?`, the four properties the canonical comments give: each stone's second half matches the next one's first and the last one's matches the first's (one zip of the chain with itself rotated by one), and it holds the same stones as often as the input, either way round (the frequencies of each stone with its smaller half first). Matching frequencies make the counts equal too.
- The cases are two `subtests` tables, the seven that chain and the six that do not, since they assert different things.

## Friction

- [stdlib] There is no `Iter.find_map` and no `Maybe.flatten`. The search maps each candidate to a `Maybe` of a chain and wants the first `Some`, which is `Iter.find(Maybe.some?)` and then `Maybe.flat_map(fn found -> found)` to undo the double `Maybe`. Rust has `Iterator::find_map`, Elixir `Enum.find_value` and Gleam `list.find_map`.

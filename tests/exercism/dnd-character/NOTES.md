# D&D Character

Subject: Games & Simulation

Roll a Dungeons & Dragons character: six abilities, each the best three of four dice, and hitpoints from the constitution's modifier.

Adapted from Exercism problem-specifications (MIT), exercises/dnd-character.

## Choices

- Randomness is `std/random`'s: `ability` and `character` answer a `Generator`, a recipe that `Generator.step` runs against a `Seed`. Nothing here reads entropy, so the tests draw from fixed seeds and every run checks the same 200 characters.
- An ability is `Generator.int(1, 6)` drawn four times with `Generator.list`, then sorted, the lowest dropped with `Iter.drop(1)`, and the rest summed.
- A character is six abilities drawn with `Generator.list(6)` and taken apart with one list pattern, in the order the struct declares them. The other arm is `unreachable`, since `Generator.list(6)` always draws six.
- `Character` is a struct, so an ability is a field read and is calculated once by construction. The two canonical "each ability is only calculated once" cases (strength alone, and every ability) are one test: the same seed draws an equal character twice.
- `modifier` is `Int.floor_div(score - 10, 2)`: the modifier rounds down, and `/` truncates toward zero, so a score of 3 would give `-3` where the modifier is `-4`.
- The canonical modifier cases are one `subtests` table; the property cases ("within range", "is valid") check every draw from seeds 1 to 200.

## Friction

None.

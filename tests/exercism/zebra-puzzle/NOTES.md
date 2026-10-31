# Zebra Puzzle

Subject: Games & Simulation

Solve the zebra puzzle: from fifteen statements about five houses, find who drinks water and who owns the zebra.

Adapted from Exercism problem-specifications (MIT), exercises/zebra-puzzle.

## Choices

- The two answers are a `Nationality` enum, and `drinks_water` and `owns_zebra` read them from one `solve`, as the canonical data's two properties ask.
- Each attribute is an `Order`, a five-tuple of houses named with a `typealias`: an order of the colors says where the red, green, ivory, yellow and blue houses are. Tuple patterns name each value's house where a statement uses it (`#(red, _green, _ivory, _yellow, blue)`), so each statement reads as a comparison of houses: `english == red`, `is_next_to(reading, fox)`.
- The search adds one attribute at a time: the color orders that fit statement 6, then `add_nations`, `add_drinks`, `add_hobbies` and `add_pets`, each a `flat_map` stage answering every way to extend what came before that fits the statements about the attributes it can now see. Ordering the stages by which statements become checkable prunes the 120⁵ combinations to a few thousand.
- Each stage carries what came before as a tuple of orders, `#(colors, nations, drinks)`, and takes it apart in its parameter pattern.
- The puzzle has one solution, so `solve` takes the list of solutions apart with `[solution]`, and anything else is an `unreachable` that says so.
- `orders` is a `once` of the 120 permutations of the five houses, computed once and shared by every stage. They come from std's `Iter.permutations`.
- `resident` finds the nationality in a house with a `case` of conditions, one arm per nationality (style §35).


## Friction

None.

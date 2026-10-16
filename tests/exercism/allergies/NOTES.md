# Allergies

Decode an allergy score into the allergens it includes.

Adapted from Exercism problem-specifications (MIT), exercises/allergies.

## Choices

- Allergens are an enum, and `derive Variants` gives `Allergen.variants()` in declaration order, which is the score order `list` must answer in.
- Each allergen's score is spelled out in a `case` (`value`), rather than computed as `Int.shift_left(1, index)` from its position in `variants()`. The table is the problem statement, and reordering the enum cannot silently change it.
- `allergic_to?` takes the score first, so `list` reads `Iter.filter(|allergen| allergic_to?(score, allergen))`.
- The canonical `item` strings become variants in the tests (`Allergen.Eggs`); there is no string parsing in the API.
- The `list` table needs its type written out (`List<(String, Int, List<Allergen>)>`), because `.Eggs` inside an unannotated list literal has no enum to resolve against.

## Friction

None.

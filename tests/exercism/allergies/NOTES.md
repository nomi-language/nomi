# Allergies

Subject: Numbers & Math

Decode an allergy score into the allergens it includes.

Adapted from Exercism problem-specifications (MIT), exercises/allergies.

## Choices

- Allergens are an enum, and `derive Variants` gives `Allergen.variants()` in declaration order, which is the score order `list` must answer in.
- Each allergen's score is spelled out in a `case` (`value`), rather than computed as `1 <<< index` from its position in `variants()`. The table is the problem statement, and reordering the enum cannot silently change it.
- `score &&& value(allergen) != 0` tests the allergen's bit: `&&&` binds tighter than `!=`, as `*` does, so it needs no parentheses.
- `is_allergic_to` takes the score first, so `list` reads `Iter.filter(&is_allergic_to(score, &1))`, a capture.
- The canonical `item` strings become variants in the tests (`Allergen.Eggs`); there is no string parsing in the API.
- The `list` table names the enum in its first row that needs it, `[Allergen.Eggs]`, and the rows after it write bare variants (`.Peanuts`), so the table needs no type annotation.

## Friction

None.

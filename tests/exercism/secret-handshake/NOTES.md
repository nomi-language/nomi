# Secret Handshake

Subject: Numbers & Math

Turn a number's lowest five bits into the actions of a secret handshake, reversed when the fifth bit is set.

Adapted from Exercism problem-specifications (MIT), exercises/secret-handshake.

## Choices

- Actions are an `Action` enum with `derive Variants`, so `Action.variants()` lists them in bit order, and the canonical strings become variants in the tests.
- Each action's bit is spelled out in a `case` (`bit`), as allergies does, rather than computed from the action's position.
- The reverse flag is a `once reverse_bit = 16`, tested by the same `set?` helper as the actions.
- The filter is `fn action -> set?(number, bit(action))` rather than the capture `&set?(number, bit(&1))`: the body is two calls, and style §31 keeps captures for one.
- The table qualifies only its first row's action (`[Action.Wink]`); the later rows' bare variants, and the empty lists, take that enum.

## Friction

None.

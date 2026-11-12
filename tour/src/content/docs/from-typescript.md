---
title: Coming from TypeScript
description: What carries over from TypeScript to Nomi, what differs, and the same program written in both.
---

Much of TypeScript carries over: static types with inference for locals,
generics, unions that a `case` narrows, lambdas, and `${...}` interpolation.
What differs most: values never change, nothing is `null` or `undefined`, a
function that can fail returns a `Result` instead of throwing, and functions
are called through their owner rather than as methods on a value.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| TypeScript | Nomi |
|---|---|
| `const x = 1`, `let x = 1` | `x = 1`. Nothing is mutable; a later `x = ...` binds the name again. |
| `xs.length`, `s.trim()`, `xs.map(f)` | `Iter.count(xs)`, `String.trim(s)`, `Iter.map(xs, f)`: there are no method calls. |
| `x?: T`, `T \| undefined`, `null` | `Maybe<T>`: `Some(v)` or `None` |
| `throw new Error(..)`, `try { .. }` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `switch (s.kind) { case "circle": .. }` | `case s` with arms such as `.Circle{radius:} -> ..`, then `end`; it must cover every variant |
| `[a, b]` as a pair, `pair[0]` | `(a, b)`, `pair.first` |
| `x as Item` | Also `x as Item`, but checked: it states a type the value already fits (`[] as List<String>`) and converts nothing |
| `number`, where `7 / 2` is `3.5` | `Int`, exact at any size, and `Float`, a 64-bit float. Neither converts to the other on its own. `/` on two `Int`s truncates toward zero, so `7 / 2` is `3`; convert an operand with `Int.to_float` for a `Float` result. |
| `0.1 + 0.2 !== 0.3`, or a library such as decimal.js for money | `1.50d` is a `Decimal`, which is exact: `0.1d + 0.2d == 0.3d`. `/` returns an exact quotient when it ends and otherwise rounds to 34 significant digits, half-even. `Decimal.divide(a, b, 2, RoundingMode.HalfEven)` rounds to a scale you name. See [Money](/money/). |
| `===` | `==`, which compares values structurally |
| `async` / `await`, `Promise.all` | `Task.spawn` and `Task.await` inside `concurrent` ... `end` |
| `try { .. } finally { f.close() }`, `using` | `defer Reader.close_quietly(r)` on the line after opening; it runs when its block ends, and the checker requires it for a resource |
| A module-level singleton, `export const db = ..`, or a DI container | `fn boot() -> Result<App, String>` builds an `App` struct before `main` runs, and any function reads a field as `App.db`. A test replaces one with `with App.db = ..`. See [App Fields](/app-fields/). |

## Spelled differently

| TypeScript | Nomi |
|---|---|
| `{ name: "Ada" }` | `User{name: "Ada"}` for a declared struct, `{name: "Ada"}` for an anonymous one |
| `{ ...user, age: 41 }` | `{..user, age: 41}` |
| `{ kind: "circle"; r: number } \| ..` | `enum Shape` with a variant per line, such as `Circle {radius: Float}`, then `end` |
| `Record<string, number>` | `Map<String, Int>`, written `{"a" => 1}` |
| `xs.filter(f).map(g)` | `xs \|> Iter.filter(f) \|> Iter.map(g) \|> Iter.to_list()` |
| `export function` | `pub fn` |
| `describe` / `it` | `test "name"` ... `end` beside the code |
| Express's `app.get("/users/:id", ..)`, with `req.params.id` a string | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: {id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

A TypeScript frontend can share a Nomi backend's JSON shapes: `nomi types`
writes a TypeScript declaration for each, and [Frontends](/frontends/#a-javascript-frontend)
shows the whole setup with Vite.

## The same program

```typescript
type Item = { name: string; price: number; qty?: number };

function total(items: Item[]): number {
  return items.reduce((sum, item) => sum + item.price * (item.qty ?? 1), 0);
}

function cheapest(items: Item[]): string | undefined {
  return [...items].sort((a, b) => a.price - b.price)[0]?.name;
}

function parseQty(text: string): number {
  const qty = Number.parseInt(text.trim(), 10);
  if (Number.isNaN(qty)) throw new Error(`not a number: ${text}`);
  if (qty < 1) throw new Error(`bad quantity: ${qty}`);
  return qty;
}
```

```nomi-run
// FILE: cart.nomi
struct Item
    name: String
    price: Int
    qty = 1
end

fn total(items: List<Item>) -> Int
    Iter.reduce(items, 0, fn sum, item -> sum + item.price * item.qty)
end

fn cheapest(items: List<Item>) -> Maybe<String>
    items |> Iter.min_by(.price) |> Maybe.map(.name)
end

fn parse_qty(text: String) -> Result<Int, String>
    qty = String.trim(text) |> String.to_int() |> Maybe.to_result("not a number: ${text}")?

    if qty < 1 then
        Err("bad quantity: ${qty}")
    else
        Ok(qty)
    end
end

// FILE: cart_test.nomi
import cart.{Item, cheapest, parse_qty, total}

test "the same program"
    items = [Item{name: "pen", price: 3, qty: 2}, Item{name: "pad", price: 5}]

    assert total(items) == 11
    assert cheapest(items) == Some("pen")
    assert cheapest([]) == None
    assert parse_qty(" 4 ") == Ok(4)
    assert parse_qty("0") == Err("bad quantity: 0")
end
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

A field default, `qty = 1`, takes the place of an optional property and
`??`; the field's type comes from the default. `cheapest` returns `None`
for an empty list instead of `undefined`, and the caller's `case` or `?`
has to handle it.

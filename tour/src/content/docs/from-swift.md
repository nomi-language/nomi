---
title: Coming from Swift
description: What carries over from Swift to Nomi, what differs, and the same program written in both.
---

Much of Swift carries over: value types, enums with associated values and an
exhaustive `switch`, optionals that must be handled, protocols that a type
adopts explicitly, generics with constraints, a leading dot for a variant
whose type is known, string interpolation, and structured concurrency. What
differs most: every value is immutable, not only `let` ones; errors are
`Result` values rather than `throws`; and a function is called through its
owner rather than as a method.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| Swift | Nomi |
|---|---|
| `let x = 1`, `var x` | `x = 1`. There is no `var`; a later `x = ...` binds the name again. |
| `coffee.price()` | `Priced.price(coffee)`, or `T.price(item)` in a generic |
| `xs.filter { $0 > 1 }` | `Iter.filter(xs, &(&1 > 1))` |
| `(a, b)`, `pair.0` | `(a, b)`, `pair.first` |
| `Int?`, `if let x = maybe`, `guard let` | `Maybe<Int>`; `case`, `if Some(x) = maybe then ..`, or a postfix `?` |
| `throws`, `try`, `do / catch` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `Double(x)`, `x as! Coffee` | `Int.to_float(x)`. Nomi's `x as T` is Swift's upcasting `as` only: it states a type the value already fits (`[] as List<String>`) |
| Vapor's `configure(_ app:)`, which sets up services on the `Application`, or an app that hands them down with `.environment(..)` | `fn boot() -> Result<App, String>` builds an `App` struct before `main` runs, and any function reads a field as `App.db`. A test replaces one with `with App.db = ..`. See [App Fields](/app-fields/). |

## Spelled differently

| Swift | Nomi |
|---|---|
| `struct Coffee: Equatable { let size: Size }` | `struct Coffee`, its fields one per line (`size: Size`), then `end`. `==` and hashing work field by field with nothing declared. |
| `enum Size { case small, large }` | `enum Size`, one capitalized variant per line, `Small`, `Large`, then `end` |
| `case .withdraw(let amount):` | `.Withdraw(amount) -> ..` |
| `protocol Priced { func price() -> Int }` | `interface Priced` with `fn price(value: self) -> Int` below it, then `end` |
| `extension Coffee: Priced { .. }` | `impl Priced for Coffee` with `fn price(coffee: Coffee) -> Int` and its body below, then `end` |
| `func total(_ items: [any Priced])` | `fn total(items: List<Priced>)`: a `List<Coffee>` passes as it is |
| `any Priced & Costed` | `Priced and Costed`, as a parameter, a field or an element: `List<Priced and Costed>` |
| `func larger<T: Comparable>(_ a: T, _ b: T) -> T` | `fn larger<T>(a: T, b: T) -> T where T: Comparable`: a type parameter when the type comes back |
| `[1, 2]`, `["a": 1]`, `Set([1])` | `[1, 2]`, `{"a" => 1}`, `Set[1]`; as in Swift, `[1]` is the array, vector or set its type says, so `s: Set<Int> = [1]` |
| `defer { file.close() }` | `defer Reader.close_quietly(r)`: one call, run when the block ends. The checker requires it for a resource, and `defer on error` runs only when the function fails. |
| `async let`, `TaskGroup` | `Task.spawn` and `Task.await` inside `concurrent` ... `end` |
| `#expect(a == b)`, `XCTAssertEqual(a, b)` | `assert a == b` in a `test "name"` .. `end` |
| Vapor's `app.get("users", ":id")`, then `req.parameters.get("id", as: Int.self)` | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: {id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

## The same program

```swift
enum Size { case small, large }

struct Coffee: Equatable {
    let size: Size
    var shots = 1
}

protocol Priced { func price() -> Int }

extension Coffee: Priced {
    func price() -> Int { (size == .small ? 3 : 4) + shots - 1 }
}

enum OrderError: Error { case unknownSize(String) }

func parseSize(_ text: String) throws -> Size {
    switch text {
    case "small": return .small
    case "large": return .large
    default: throw OrderError.unknownSize(text)
    }
}

func total(_ items: [any Priced]) -> Int { items.reduce(0) { $0 + $1.price() } }

func firstLarge(_ coffees: [Coffee]) -> Coffee? { coffees.first { $0.size == .large } }
```

```nomi-run
// FILE: coffee.nomi
enum Size
    Small
    Large
end

struct Coffee
    size:  Size
    shots: Int = 1
end

interface Priced
    fn price(value: self) -> Int
end

impl Priced for Coffee
    fn price(coffee: Coffee) -> Int
        base = case coffee.size
            .Small -> 3
            .Large -> 4
        end

        base + coffee.shots - 1
    end
end

fn parse_size(text: String) -> Result<Size, String>
    case text
        "small" -> Ok(.Small)
        "large" -> Ok(.Large)
        _       -> Err("unknown size: ${text}")
    end
end

fn total(items: List<Priced>) -> Int
    Iter.sum_by(items, Priced.price)
end

fn first_large(coffees: List<Coffee>) -> Maybe<Coffee>
    Iter.find(coffees, fn coffee -> coffee.size == .Large)
end

// FILE: coffee_test.nomi
import coffee.{Coffee, first_large, parse_size, total}

test "the same program"
    order = [Coffee{size: .Small}, Coffee{size: parse_size("large")!, shots: 2}]

    assert total(order) == 8
    assert first_large(order) == Some(Coffee{size: .Large, shots: 2})
    assert parse_size("huge") == Err("unknown size: huge")
end
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`parse_size("large")!` is Swift's `try!`: it traps on an `Err`. A function
that should pass the failure up writes `?` instead, and returns a `Result`
itself.

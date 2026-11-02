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

## What differs

| Swift | Nomi |
|---|---|
| `let x = 1`, `var x` | `x = 1`. There is no `var`; a later `x = ...` binds the name again. |
| `struct Coffee { let size: Size }` | `struct Coffee { size: Size }`, one field per line |
| `enum Size { case small, large }` | `enum Size`, one capitalized variant per line: `Small`, `Large` |
| `case .withdraw(let amount):` | `.Withdraw(amount) -> ..` |
| `Int?`, `if let x = maybe`, `guard let` | `Maybe<Int>`; `case`, `if Some(x) = maybe { .. }`, or a postfix `?` |
| `throws`, `try`, `do / catch` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `protocol Priced { func price() -> Int }` | `interface Priced { fn price(value: self) -> Int }` |
| `extension Coffee: Priced { .. }` | `impl Priced for Coffee { fn price(coffee: Coffee) -> Int { .. } }` |
| `coffee.price()` | `Priced.price(coffee)`, or `T.price(item)` in a generic |
| `func total<T: Priced>(..)` | `fn total<T>(..) where T: Priced` |
| `[1, 2]`, `["a": 1]`, `Set([1])` | `[1, 2]`, `%{"a" => 1}`, `%[1]` |
| `xs.filter { $0 > 1 }` | `Iter.filter(xs, &(&1 > 1))` |
| `async let`, `TaskGroup` | `Task.spawn` and `Task.await` inside `concurrent { .. }` |
| `XCTAssertEqual(a, b)` | `test "name" { assert a == b }` |

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

func total<T: Priced>(_ items: [T]) -> Int { items.reduce(0) { $0 + $1.price() } }

func firstLarge(_ coffees: [Coffee]) -> Coffee? { coffees.first { $0.size == .large } }
```

```nomi-run
// FILE: coffee.nomi
enum Size {
    Small
    Large
}

derive Equatable for Size

struct Coffee {
    size: Size
    shots: Int = 1
}

derive Equatable for Coffee

interface Priced {
    fn price(value: self) -> Int
}

impl Priced for Coffee {
    fn price(coffee: Coffee) -> Int {
        base = case coffee.size {
            .Small -> 3
            .Large -> 4
        }

        base + coffee.shots - 1
    }
}

fn parse_size(text: String) -> Result<Size, String> {
    case text {
        "small" -> Ok(.Small)
        "large" -> Ok(.Large)
        _ -> Err("unknown size: ${text}")
    }
}

fn total<T>(items: List<T>) -> Int where T: Priced {
    Iter.reduce(items, 0, fn sum, item -> sum + T.price(item))
}

fn first_large(coffees: List<Coffee>) -> Maybe<Coffee> {
    Iter.find(coffees, fn coffee -> coffee.size == .Large)
}

// FILE: coffee_test.nomi
import coffee.{Coffee, first_large, parse_size, total}

test "the same program" {
    order = [Coffee{size: .Small}, Coffee{size: parse_size("large")!, shots: 2}]

    assert total(order) == 8
    assert first_large(order) == Some(Coffee{size: .Large, shots: 2})
    assert parse_size("huge") == Err("unknown size: huge")
}
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`parse_size("large")!` is Swift's `try!`: it traps on an `Err`. A function
that should pass the failure up writes `?` instead, and returns a `Result`
itself.

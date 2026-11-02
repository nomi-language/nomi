---
title: Coming from Rust
description: What carries over from Rust to Nomi, what differs, and the same program written in both.
---

Much of Rust carries over: enums with payloads, exhaustive matching,
`Option`-style and `Result` values with a postfix `?`, interfaces with
`impl` blocks, generics with bounds, `derive`, and blocks that are
expressions. What Rust spends on ownership, Nomi does not need. Every value
is immutable, so values are shared freely and there are no references,
lifetimes or `mut`.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## What differs

| Rust | Nomi |
|---|---|
| `let x = 1;`, `let mut x` | `x = 1`. Nothing is mutable; a later `x = ...` binds the name again. |
| `xs.len()`, `s.trim()` | `Iter.count(xs)`, `String.trim(s)`: there are no method calls. |
| `v.iter().map(f).collect()` | `xs \|> Iter.map(f) \|> Iter.to_list()` |
| `match x { A => .. }` | `case x { A -> .. }`, with guards written `when` |
| `Point { x: 1 }`, `(1, "a")` | `Point{x: 1}`, `#(1, "a")` |
| `vec![1, 2]`, `HashMap` | `[1, 2]` (a list) or `#[1, 2]` (a vector), `%{"a" => 1}` |
| `Option<T>`, `.unwrap()` | `Maybe<T>`, a postfix `!` |
| `trait`, `fn f(&self)` | `interface`, `fn f(value: self)` |
| `Shape::new()`, `Shape::Circle { radius: 1 }` | `Shape.new()`, `Shape.Circle{radius: 1}` or `.Circle{radius: 1}` |
| `format!("{x}")` | `"${x}"` |
| `for x in xs { .. }` | `Iter.each(xs, fn x -> ..)`, a fold, or recursion |
| `tokio::spawn` | `Task.spawn` inside a `concurrent { .. }` block |
| `#[test] fn t()` | `test "name" { assert .. }` |
| `mod`, `use a::b` | A file is the unit of import: `import a.{b}` |

Statements end at a newline, with no semicolons.

## The same program

```rust
#[derive(Debug, PartialEq)]
enum Shape {
    Circle { radius: i64 },
    Square { side: i64 },
}

impl Shape {
    fn area(&self) -> i64 {
        match self {
            Shape::Circle { radius } => 3 * radius * radius,
            Shape::Square { side } => side * side,
        }
    }
}

fn total_area(shapes: &[Shape]) -> i64 {
    shapes.iter().map(|s| s.area()).sum()
}

fn parse_square(text: &str) -> Result<Shape, String> {
    let side: i64 = text.trim().parse().map_err(|_| format!("not a number: {text}"))?;
    Ok(Shape::Square { side })
}
```

```nomi-run
// FILE: shapes.nomi
enum Shape {
    Circle {radius: Int}
    Square {side: Int}
}

impl Shape {
    fn area(shape: Shape) -> Int {
        case shape {
            .Circle{radius} -> 3 * radius * radius
            .Square{side} -> side * side
        }
    }
}

derive Equatable for Shape

fn total_area(shapes: List<Shape>) -> Int {
    Iter.sum_by(shapes, Shape.area)
}

fn parse_square(text: String) -> Result<Shape, String> {
    side =
        String.trim(text)
        |> String.to_int()
        |> Maybe.to_result("not a number: ${text}")?

    Ok(.Square{side})
}

// FILE: shapes_test.nomi
import shapes.{Shape, parse_square, total_area}

test "the same program" {
    shapes = [Shape.Circle{radius: 1}, .Square{side: 2}]

    assert total_area(shapes) == 7
    assert parse_square(" 3 ") == Ok(.Square{side: 3})
    assert parse_square("x") == Err("not a number: x")
}
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`impl Shape` holds functions called through the type, `Shape.area(shape)`,
which is also how `Iter.map` takes it. The function's first parameter is a
plain parameter: there is no `self` receiver outside an interface.

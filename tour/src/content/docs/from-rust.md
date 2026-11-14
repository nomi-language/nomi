---
title: Coming from Rust
description: What carries over from Rust to Nomi, what differs, and the same program written in both.
---

Much of Rust carries over: enums with payloads, exhaustive matching,
`Option`-style and `Result` values with a postfix `?`, interfaces with
`impl` blocks, generics with bounds, `derive`, and blocks that are
expressions. What differs most: every value is immutable, so values are
shared freely and there are no ownership rules, borrows, lifetimes or
`mut`, and functions are called through their owner rather than as methods.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| Rust | Nomi |
|---|---|
| `let x = 1;`, `let mut x` | `x = 1`. Nothing is mutable; a later `x = ...` binds the name again. |
| `xs.len()`, `s.trim()` | `List.length(xs)`, `String.trim(s)`: there are no method calls. |
| `(1, "a")`, `t.0` | A tuple is `(1, "a")` as in Rust, but its items are read as `t.first`, `t.second`, ... |
| `if c { x }` needs `x: ()`; `c.then_some(x)`, `c.then(\|\| x)` | The same rule as Rust: an `if` without `else` is a statement, and its branch is `Unit` or leaves. `then_some` has no counterpart; write `if c then Some(x) else None`. |
| `x as f64` | `Int.to_float(x)`. Nomi's `x as T` is no cast: it only states a type the value already fits (`[] as List<String>`) |
| `impl Drop` | `defer close(x)` on the line after acquiring it; it runs when its block ends, and the checker requires one for a resource. `defer on error` runs only when the function fails. |
| `for x in xs { .. }` | `Iter.each(xs, fn x -> ..)`, a fold, or recursion |
| `#[derive(PartialEq, Eq, Hash, Debug)]` | Nothing: `==`, hashing and `dbg` work on every struct and enum, field by field. `derive Comparable for T` orders field by field, as `#[derive(PartialOrd, Ord)]` does. |
| `fn f(x: &dyn Speak)`, `fn f(x: impl Speak)` | `fn f(x: Speak)`: a parameter used only through one interface takes it as its type. Write a type parameter when the type comes back, as in `fn larger<T>(a: T, b: T) -> T` |
| `Box<dyn Speak + Send>`; `dyn Speak + Display` is refused, since only auto traits join a trait object | `Speak and Display`: a value of any type implementing both, through either interface, as a parameter, a field or a list element (`List<Speak and Display>`) |
| A state struct built in `main`, passed with `.with_state(state)` and read through `State<AppState>` | `fn boot() -> Result<App, String>` builds an `App` struct before `main` runs, and any function reads a field as `App.db`, with no extractor or parameter. A test replaces one with `with App.db = ..`. See [App Fields](/app-fields/). |

## Spelled differently

| Rust | Nomi |
|---|---|
| `v.iter().map(f).collect()` | `xs \|> Iter.map(f) \|> Iter.to_list()` |
| `match x { A => .. }` | `case x`, arms such as `A -> ..` one per line, then `end`; guards are written `when` |
| `Point { x: 1 }` | `Point{x: 1}` |
| `vec![1, 2]`, `HashMap` | `[1, 2]`, a list, or a vector where one is expected (`Vector[1, 2]` where nothing says which), `{"a" => 1}` |
| `Option<T>`, `.unwrap()` | `Maybe<T>`, a postfix `!` |
| `impl From<sqlx::Error> for ApiError` and `?` | The same: `impl From<sql.Error> for ApiError`, and `?` applies it. There is no `Into`, and `?` converts once, never through a chain of impls. |
| `.unwrap_or(x)`, `.or(other)`, `.or_else(f)` | `Maybe.with_default(m, x)`, `Maybe.otherwise(m, other)`, `Maybe.or_else(m, f)` |
| serde's `Option<Option<T>>` for a key missing or `null` | `Presence<Maybe<T>>`: `Absent`, `Present(None)` or `Present(Some(v))` |
| `trait`, `fn f(&self)` | `interface`, `fn f(value: self)` |
| `Vec<Dog>` into `Vec<Box<dyn Speak>>` takes a `map`; an `fn(&dyn Speak)` never stands in for an `fn(&Dog)` | A `List<Dog>` passes where a `List<Speak>` is expected, with nothing copied, and a `(Speak) -> String` where a `(Dog) -> String` is: type parameters are covariant or contravariant by where they appear, inferred from the type |
| `Shape::new()`, `Shape::Circle { radius: 1 }` | `Shape.new()`, `Shape.Circle{radius: 1}` or `.Circle{radius: 1}` |
| `format!("{x}")` | `"${x}"` |
| `todo!("parse")` | `todo "parse"` |
| `tokio::spawn` | `Task.spawn` inside a `concurrent` ... `end` block |
| `#[test] fn t()` | `test "name"` ... `end` |
| `mod`, `use a::b` | A file is the unit of import: `import a.{b}`. A path is `::`-joined like Rust's (`import db::posts`), and the file is then used by its last part, `posts.find(1)`. Importing a directory, `import db`, is like `use crate::db`: `db::posts.find(1)` reaches the file. |
| axum's `.route("/users/{id}", get(show))` with `Path(id): Path<u64>` | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: {id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

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
enum Shape
    Circle {radius: Int}
    Square {side: Int}
end

impl Shape
    fn area(shape: Shape) -> Int
        case shape
            .Circle{radius:} -> 3 * radius * radius
            .Square{side:}   -> side * side
        end
    end
end

fn total_area(shapes: List<Shape>) -> Int
    Iter.sum_by(shapes, Shape.area)
end

fn parse_square(text: String) -> Result<Shape, String>
    side =
        String.trim(text)
        |> String.to_int()
        |> Maybe.to_result("not a number: ${text}")?

    Ok(.Square{side:})
end

// FILE: shapes_test.nomi
import shapes.{Shape, parse_square, total_area}

test "the same program"
    shapes = [Shape.Circle{radius: 1}, .Square{side: 2}]

    assert total_area(shapes) == 7
    assert parse_square(" 3 ") == Ok(.Square{side: 3})
    assert parse_square("x") == Err("not a number: x")
end
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`impl Shape` holds functions called through the type, `Shape.area(shape)`,
which is also how `Iter.map` takes it. The function's first parameter is a
plain parameter: there is no `self` receiver outside an interface.

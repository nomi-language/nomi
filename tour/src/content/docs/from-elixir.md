---
title: Coming from Elixir
description: What carries over from Elixir to Nomi, what differs, and the same program written in both.
---

Much of Elixir carries over: every value is immutable, `|>` pipes, `case`
with pattern matching, `%{"a" => 1}` maps, `Map` and `Enum`-style functions
called through their module, string interpolation, supervised tasks, and tests
beside the code. What differs most: Nomi is statically typed, so every
`case` must cover every shape, failures are `Result` and `Maybe` values rather
than `{:error, _}` tuples, and there are no atoms or processes with mailboxes.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| Elixir | Nomi |
|---|---|
| `def f(x) do .. end` | `fn f(x: Int) -> Int` .. `end`, with parameter types and no `do` |
| `Enum.map(xs, &(&1 * 2))` | `Iter.map(xs, &(&1 * 2))`, lazy until `Iter.to_list` |
| `Enum.reduce(xs, 0, fn x, acc -> .. end)` | `Iter.reduce(xs, 0, fn acc, x -> ..)`: the accumulator comes first |
| `{:ok, v}`, `{:error, e}` | `Ok(v)`, `Err(e)`, and a postfix `?` instead of `with` |
| `nil` | `None`, from `Maybe<T>` |
| `:atom` | An enum variant: `.Ready` |
| `{a, b}`, `elem(t, 0)` | `(a, b)`, `t.first` |
| `%{name: "Ada"}` (atom keys) | `{name: "Ada"}`, an anonymous struct |
| `cond do`, `if .. do` | `if .. then` .. `else if .. then` .. `end`, or `case` alone on its line. Without `else` it is `Some(x)` or `None`, where Elixir's is `x` or `nil`. |
| `7 / 2` is `3.5`; `div(7, 2)`, `rem(-7, 3)` | `/` on two `Int`s is `div`: `7 / 2` is `3`, truncated toward zero. `%` is `rem`: `-7 % 3` is `-1`. Convert an operand with `Int.to_float` for a `Float` result. |
| `spawn`, `send`, `receive` | `Task.spawn` in `concurrent` ... `end`, and channels |
| `File.open(path, fn file -> .. end)` | `defer Reader.close_quietly(r)` on the line after opening; it runs when its block ends, and the checker requires it for a resource |

## Spelled differently

| Elixir | Nomi |
|---|---|
| `%User{name: "Ada"}`, `%{"a" => 1}` | `User{name: "Ada"}`, `{"a" => 1}`: no `%` |
| `"#{x}"` | `"${x}"` |
| `when` guards | `when` guards, the same |
| `defp` | A declaration without `pub` |
| `alias MyApp.Db.Posts`, then `Posts.find(1)` | `import db::posts`, then `posts.find(1)`: a file is used by its path's last part. `import db` imports the directory, and `db::posts.find(1)` reaches the file through it. |
| `Supervisor` | `Supervisor.new` in `boot` |
| The application callback, `start/2`, which starts the supervision tree; settings read with `Application.get_env` | `fn boot() -> Result<App, String>` builds an `App` struct, and starts any supervisors, before `main` runs. Any function reads a field as `App.repo`. A test replaces one with `with App.repo = ..`. See [App Fields](/app-fields/). |
| `ExUnit` | `test "name"` ... `end` |
| Phoenix's `get "/users/:id", UserController, :show`, with `%{"id" => id}` a string | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: {id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

## The same program

```elixir
defmodule Orders do
  def total(orders) do
    orders
    |> Enum.filter(&(&1.status == :paid))
    |> Enum.sum_by(& &1.amount)
  end

  def parse_amount(text) do
    case Integer.parse(text) do
      {n, ""} when n >= 0 -> {:ok, n}
      _ -> {:error, "not an amount: #{text}"}
    end
  end
end
```

```nomi-run
// FILE: orders.nomi
enum Status
    Paid
    Open
end

struct Order
    status: Status
    amount: Int
end

fn total(orders: List<Order>) -> Int
    orders
    |> Iter.filter(&(&1.status == .Paid))
    |> Iter.sum_by(.amount)
end

fn parse_amount(text: String) -> Result<Int, String>
    case String.to_int(text)
        Some(n) when n >= 0 -> Ok(n)
        _ -> Err("not an amount: ${text}")
    end
end

// FILE: orders_test.nomi
import orders.{Order, parse_amount, total}

test "the same program"
    orders = [Order{status: .Paid, amount: 5}, Order{status: .Open, amount: 7}]

    assert total(orders) == 5
    assert parse_amount("12") == Ok(12)
    assert parse_amount("-1") == Err("not an amount: -1")
end
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`.amount` is a field accessor, a function that reads the field. `.Paid` is
`Status.Paid`, with the enum taken from the type the comparison expects.

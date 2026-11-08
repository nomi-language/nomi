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

## What differs

| Elixir | Nomi |
|---|---|
| `def f(x) do .. end` | `fn f(x: Int) -> Int { .. }`, with parameter types |
| `Enum.map(xs, &(&1 * 2))` | `Iter.map(xs, &(&1 * 2))`, lazy until `Iter.to_list` |
| `Enum.reduce(xs, 0, fn x, acc -> .. end)` | `Iter.reduce(xs, 0, fn acc, x -> ..)`: the accumulator comes first |
| `{:ok, v}`, `{:error, e}` | `Ok(v)`, `Err(e)`, and a postfix `?` instead of `with` |
| `nil` | `None`, from `Maybe<T>` |
| `:atom` | An enum variant: `.Ready` |
| `{a, b}` | `(a, b)` |
| `%{name: "Ada"}` (atom keys) | `#{name: "Ada"}`, an anonymous struct |
| `%User{name: "Ada"}` | `User{name: "Ada"}`: `#` is a struct, `%` a map or set |
| `"#{x}"` | `"${x}"` |
| `cond do`, `if .. do` | `if .. { } else if .. { }`. Without `else` it is `Some(x)` or `None`, where Elixir's is `x` or `nil`. |
| `when` guards | `when` guards, the same |
| `defp` | A declaration without `pub` |
| `alias MyApp.Db.Posts`, then `Posts.find(1)` | `import db::posts`, then `posts.find(1)`: a file is used by its path's last part. `import db` imports the directory, and `db::posts.find(1)` reaches the file through it. |
| `spawn`, `send`, `receive` | `Task.spawn` in `concurrent { .. }`, and channels |
| `Supervisor` | `Supervisor.new` in `boot` |
| `ExUnit` | `test "name" { assert .. }` |

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
enum Status {
    Paid
    Open
}

struct Order {
    status: Status
    amount: Int
}

fn total(orders: List<Order>) -> Int {
    orders
    |> Iter.filter(&(&1.status == .Paid))
    |> Iter.sum_by(.amount)
}

fn parse_amount(text: String) -> Result<Int, String> {
    case String.to_int(text) {
        Some(n) when n >= 0 -> Ok(n)
        _ -> Err("not an amount: ${text}")
    }
}

// FILE: orders_test.nomi
import orders.{Order, parse_amount, total}

test "the same program" {
    orders = [Order{status: .Paid, amount: 5}, Order{status: .Open, amount: 7}]

    assert total(orders) == 5
    assert parse_amount("12") == Ok(12)
    assert parse_amount("-1") == Err("not an amount: -1")
}
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`.amount` is a field accessor, a function that reads the field. `.Paid` is
`Status.Paid`, with the enum taken from the type the comparison expects.

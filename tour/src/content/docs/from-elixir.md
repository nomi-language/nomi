---
title: Coming from Elixir
description: What carries over from Elixir to Nomi, what differs, and the same program written in both.
---

Much of Elixir carries over: every value is immutable, `|>` pipes, `case`
with pattern matching, `%{"a" => 1}` maps, `Map` and `Enum`-style functions
called through their module, string interpolation, supervised tasks, and tests
beside the code. What differs most: Nomi is statically typed, so every
`case` must cover every shape, failures are `Result` and `Option` values rather
than `{:error, _}` tuples, there are no atoms, and a task has no mailbox of
its own: an `Actor` is a task with an inbox.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| Elixir | Nomi |
|---|---|
| `def f(x) do .. end` | `fn f(x: Int) -> Int` .. `end`, with parameter types and no `do` |
| `Enum.map(xs, &(&1 * 2))` | `Iter.map(xs, &(&1 * 2))`, lazy until `Iter.to_list` |
| `Enum.reduce(xs, 0, fn x, acc -> .. end)` | `Iter.reduce(xs, 0, fn acc, x -> ..)`: the accumulator comes first |
| `{:ok, v}`, `{:error, e}` | `Ok(v)`, `Err(e)`, and a postfix `?` instead of `with` |
| `nil` | `None`, from `Option<T>` |
| `:atom` | An enum variant: `.Ready` |
| `{a, b}`, `elem(t, 0)` | `(a, b)`, `t.first` |
| `%{name: "Ada"}` (atom keys) | `{name: "Ada"}`, an anonymous struct |
| `cond do`, `if .. do` | `case` alone on its line for `cond`, and `if .. then` .. `else` .. `end` for `if`. Without `else`, Elixir's gives `nil` when the condition fails; Nomi's is a statement with no value, and its branch must be `Unit` or leave. Nothing stands in for `nil` there: Elixir's `if c, do: x` is `if c then Some(x) else None`. |
| `7 / 2` is `3.5`; `div(7, 2)`, `rem(-7, 3)` | `/` on two `Int`s is `div`: `7 / 2` is `3`, truncated toward zero. `%` is `rem`: `-7 % 3` is `-1`. Convert an operand with `Int.to_float` for a `Float` result. The divisor must be a `NonZeroInt`: a literal other than 0 is one, and so is `d` after `if d == 0 then return ..`. |
| `Task.async(fn -> .. end)`, `Task.await(t)` | `Task.spawn(fn -> ..)` and `Task.await(t)` inside a `concurrent` ... `end` block, which waits for its tasks |
| `Task.async_stream(xs, f, max_concurrency: 8)` | `Task.stream(xs, f, max_running: 8)`, a lazy stage that yields the results in order as they are read, or as they finish with `ordered: False`. For a batch, `Task.spawn_all(xs, f, max_running: 8)`, then `Task.await_all` for every result in order, or `Task.try_await_all` to stop at the first `Err` and cancel the rest. `max_running` has no default. |
| `send(pid, msg)`, `receive do .. end` | An actor's messages: `Actor.cast` and `Actor.call`, which its handler takes one at a time. Channels from `std::channels` are the layer underneath, for a stream of values between tasks. |
| `File.open(path, fn file -> .. end)` | `defer Reader.close_quietly(r)` on the line after opening; it runs when its block ends, and the checker requires it for a resource |

## Spelled differently

| Elixir | Nomi |
|---|---|
| `%User{name: "Ada"}`, `%{"a" => 1}` | `User{name: "Ada"}`, `{"a" => 1}`: no `%` |
| `"#{x}"` | `"${x}"` |
| `Decimal.new("1.50")` from the `decimal` package | `1.50d`, a `Decimal` literal, in the language: `0.1d + 0.2d == 0.3d`. See [Exact arithmetic with `Decimal`](/scalars-and-strings/#exact-arithmetic-with-decimal). |
| `when` guards | `when` guards, the same |
| `def`, `defp` | `pub fn`, `private fn`. Between them, an unmarked `fn` is seen by every file of its project and by no other project, a level Elixir lacks |
| `defprotocol Speak`, `defimpl Speak, for: Dog` | `interface Speak`, `impl Speak for Dog` |
| `alias MyApp.Db.Posts`, then `Posts.find(1)` | `import db::posts`, then `posts.find(1)`: a file is used by its path's last part. `import db` imports the directory, and `db::posts.find(1)` reaches the file through it. |
| `Supervisor` | `Supervisor.new` in `boot` |
| `Task.yield_many(tasks, timeout)`, or racing tasks | `Task.within(wait, f)`, `Err(TimedOut)` when the wait runs out, and `Task.race([..])`, the first body to finish. Both cancel what is still running. |
| `Agent.start_link(fn -> 0 end)`, `Agent.update`, `Agent.get` | `Agent.start(supervisor, 0)` in `boot`, `Agent.update`, `Agent.get`, from `std::actors` |
| `GenServer`, with `call` and `cast` | `Actor.start`, `Actor.call` and `Actor.cast` from `std::actors`: one task that folds each message into its state |
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
        _                   -> Err("not an amount: ${text}")
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

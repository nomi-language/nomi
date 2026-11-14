---
title: Coming from Ruby
description: What carries over from Ruby to Nomi, what differs, and the same program written in both.
---

Much of Ruby carries over. The last expression is the result, so `return`
is for an early exit. Blocks and `Enumerable` chains become lambdas and
`Iter` pipelines that read the same way. Hashes keep `=>`, interpolation
keeps its shape, `case`/`in` pattern matching becomes `case`, and
`Struct` or `Data.define` becomes a `struct`. What differs most: types are
checked before the program runs, values never change, and there are no
methods. A function is called through its owner, and a type gets behavior
from `impl` blocks rather than by reopening a class.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| Ruby | Nomi |
|---|---|
| `s.upcase`, `xs.size` | `String.uppercase(s)`, `List.length(xs)`: there are no methods to call on a value |
| `class String; def shout ..` | No reopening: a new function goes in your own file or an `impl` of your own type |
| `x = x + 1` with `x` reassigned | The same text, but it binds a new `x`; the old value is unchanged |
| `xs.each { \|x\| total += x }` | `Iter.reduce(xs, 0, fn total, x -> total + x)`: nothing mutates, so a fold carries the state |
| `nil`, `&.` | `Maybe<T>`: `None` or `Some(v)`, with `Maybe.map` |
| `raise`, `rescue` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `:paid` | An enum variant, `.Paid`, or a string where any text will do |
| `-7 / 2` is `-4`; `-7 % 3` is `2` | `/` on two `Int`s truncates toward zero, so `-7 / 2` is `-3`. `%` takes the sign of its left operand, so `-7 % 3` is `-1`. `Int.floor_div` and `Int.modulo` are Ruby's `/` and `%`. |
| `File.open(path) do \|f\| .. end` | `defer Reader.close_quietly(r)` on the line after opening; it runs when its block ends, and the checker requires it for a resource |
| Rails initializers in `config/initializers/` that set up shared clients | `fn boot() -> Result<App, String>` builds an `App` struct before `main` runs, and any function reads a field as `App.db`. A test replaces one with `with App.db = ..`. See [App Fields](/app-fields/). |

## Spelled differently

| Ruby | Nomi |
|---|---|
| `xs.select { \|x\| x.paid? }.map(&:total)` | `xs \|> Iter.filter(fn x -> x.paid) \|> Iter.map(.total)` |
| `{a: 1}`, `{"k" => v}` | `{a: 1}`, `{"k" => v}` |
| `"#{name} has #{n}"` | `"${name} has ${n}"` |
| `Order = Data.define(:customer, :total)` | `struct Order` with `customer: String` and `total: Int`, one field per line |
| `case x in {status: :paid, total:}` | `case x`, an arm `{status: .Paid, total:} -> ..`, `end` |
| `do .. end` | `end` without `do`: `fn x ->` .. `end`, `if c then` .. `end` |
| `empty?`, `key?` | `is_empty`, `has_key` |
| `fetch`, `save!` that raise | A postfix `!` on a `Maybe` or `Result`, which traps on `None` or `Err` |
| `BigDecimal("1.50")` | `1.50d`, a `Decimal` literal. `+`, `-` and `*` are exact. `/` returns an exact quotient when it ends and otherwise rounds to 34 significant digits, half-even. `Decimal.divide(a, b, 2, RoundingMode.HalfEven)` rounds to a scale you name. See [Money](/money/). |
| RSpec, Minitest | `test "name"` ... `end` beside the code |
| `get "/users/:id", to: "users#show"` in Rails, with `params[:id]` a string | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: {id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

Parentheses are required on calls, and statements end at a newline.

## The same program

```ruby
Order = Data.define(:customer, :total, :status)

def revenue_by_customer(orders)
  orders
    .select { |o| o.status == :paid }
    .group_by(&:customer)
    .transform_values { |paid| paid.sum(&:total) }
end

def describe(order)
  case order
  in {status: :paid, total:} if total > 100 then "big order"
  in {status: :paid} then "paid"
  in {status: :refunded} then "refunded"
  end
end

def find_order(orders, customer)
  orders.find { |o| o.customer == customer }
end
```

```nomi-run
// FILE: orders.nomi
enum Status
    Paid
    Refunded
end

struct Order
    customer: String
    total:    Int
    status:   Status
end

fn revenue_by_customer(orders: List<Order>) -> Map<String, Int>
    orders
    |> Iter.filter(fn order -> order.status == .Paid)
    |> Iter.group_by(.customer)
    |> Map.map_values(fn paid -> Iter.sum_by(paid, .total))
end

fn describe(order: Order) -> String
    case order
        {status: .Paid, total:} when total > 100 -> "big order"
        {status: .Paid} -> "paid"
        {status: .Refunded} -> "refunded"
    end
end

fn find_order(orders: List<Order>, customer: String) -> Maybe<Order>
    Iter.find(orders, fn order -> order.customer == customer)
end

// FILE: orders_test.nomi
import orders.{Order, describe, find_order, revenue_by_customer}

test "the same program"
    orders = [
        Order{customer: "ada", total: 150, status: .Paid},
        Order{customer: "bo", total: 20, status: .Refunded},
        Order{customer: "ada", total: 30, status: .Paid},
    ]

    assert revenue_by_customer(orders) == {"ada" => 180}
    assert orders |> Iter.map(describe) |> Iter.to_list() == ["big order", "refunded", "paid"]
    assert find_order(orders, "cy") == None
end
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

The chain keeps its order and its words: `select` is `Iter.filter`,
`group_by` is `Iter.group_by`, `transform_values` is `Map.map_values`, and
`sum(&:total)` is `Iter.sum_by(paid, .total)`. `&:customer` becomes the
field accessor `.customer`. `find_order` returns
`None` where Ruby returns `nil`, and its type says so.

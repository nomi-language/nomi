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

## What differs

| Ruby | Nomi |
|---|---|
| `s.upcase`, `xs.size` | `String.uppercase(s)`, `Iter.count(xs)`: there are no methods to call on a value |
| `class String; def shout ..` | No reopening: a new function goes in your own file or an `impl` of your own type |
| `xs.select { \|x\| x.paid? }.map(&:total)` | `xs \|> Iter.filter(fn x -> x.paid) \|> Iter.map(.total)` |
| `xs.each { \|x\| total += x }` | `Iter.reduce(xs, 0, fn total, x -> total + x)`: nothing mutates, so a fold carries the state |
| `nil`, `&.` | `Maybe<T>`: `None` or `Some(v)`, with `Maybe.map` |
| `:paid` | An enum variant, `.Paid`, or a string where any text will do |
| `{a: 1}`, `{"k" => v}` | `#{a: 1}`, `%{"k" => v}` |
| `"#{name} has #{n}"` | `"${name} has ${n}"` |
| `Order = Data.define(:customer, :total)` | `struct Order` with `customer: String` and `total: Int`, one field per line |
| `case x in {status: :paid, total:}` | `case x { #{status: .Paid, total} -> .. }` |
| `raise`, `rescue` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `do .. end` | Braces: `fn x -> { .. }`, `if c { .. }` |
| `empty?`, `save!` | `is_empty`, `has_key`; a postfix `!` on a value unwraps a `Maybe` or `Result` |
| `x = x + 1` with `x` reassigned | The same text, but it binds a new `x`; the old value is unchanged |
| RSpec, Minitest | `test "name" { assert .. }` beside the code |

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
enum Status {
    Paid
    Refunded
}

derive Equatable for Status

struct Order {
    customer: String
    total: Int
    status: Status
}

fn revenue_by_customer(orders: List<Order>) -> Map<String, Int> {
    orders
    |> Iter.filter(fn order -> order.status == .Paid)
    |> Iter.group_by(.customer)
    |> Map.map_values(fn paid -> Iter.sum_by(paid, .total))
}

fn describe(order: Order) -> String {
    case order {
        #{status: .Paid, total} when total > 100 -> "big order"
        #{status: .Paid} -> "paid"
        #{status: .Refunded} -> "refunded"
    }
}

fn find_order(orders: List<Order>, customer: String) -> Maybe<Order> {
    Iter.find(orders, fn order -> order.customer == customer)
}

// FILE: orders_test.nomi
import orders.{Order, describe, find_order, revenue_by_customer}

test "the same program" {
    orders = [
        Order{customer: "ada", total: 150, status: .Paid},
        Order{customer: "bo", total: 20, status: .Refunded},
        Order{customer: "ada", total: 30, status: .Paid},
    ]

    assert revenue_by_customer(orders) == %{"ada" => 180}
    assert Iter.map(orders, describe) |> Iter.to_list() == ["big order", "refunded", "paid"]
    assert find_order(orders, "cy") == None
}
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

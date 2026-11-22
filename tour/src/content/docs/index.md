---
title: Nomi Lang
description: A friendly, statically typed language that catches mistakes before your program runs.
# The splash template gives the home page the whole width: no sidebar and no
# table of contents. Every other page keeps both, with Home first in the
# sidebar.
template: splash
# The tab would read "Nomi Lang | Nomi Lang": the page's title and the site's
# are the same words.
head:
  - tag: title
    content: Nomi Lang
next: false
prev: false
# The wordmark carries the NAME, so the <h1> carries the PITCH rather than
# repeating it: a mark reading "NOMI" above a heading reading "Nomi Lang" is
# the same word twice. `title` above still supplies the browser tab, the
# sidebar label and the image's alt text, so the name is not lost anywhere
# the mark cannot be seen.
hero:
  title: "A friendly language that catches mistakes before your program runs."
  tagline: "Statically typed, with values that never change. No null, no exceptions, no forgotten case."
  image:
    alt: Nomi Lang
    light: ../../assets/nomi-wordmark.svg
    dark: ../../assets/nomi-wordmark-dark.svg
  # One way in here; "Pick your way in" below holds all three, one per
  # reader. Install is for a reader already convinced.
  actions:
    - text: First Steps
      link: /learn/values/
      variant: primary
    - text: Install
      link: /tooling/install/
      variant: minimal
---

<!-- The home page's sections are plain HTML around Markdown, styled in
tour.css under "Home page". A blank line after each opening tag lets the
Markdown inside it render, the live example included. -->

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">Reads in order</p>

## Data flows down the page

A pipe passes a value to the next step, so the code reads in the order it
runs, and `.paid` is short for a function that reads that field. The
steps are lazy: nothing is computed until `Iter.sum` asks for it, and no
list is built in between.

Values never change: `{..order, paid: True}` makes a new order and leaves
`cart` as it was.

You can edit most examples on this site. Change one and it runs in your
browser.

</div>

```nomi-run
struct Order
    price: Int
    paid:  Bool
end

fn total_paid(orders: List<Order>) -> Int
    orders
    |> Iter.filter(.paid)
    |> Iter.map(.price)
    |> Iter.sum()
end

fn main()
    cart = [
        Order{price: 4, paid: True},
        Order{price: 9, paid: False},
    ]

    settled =
        cart
        |> Iter.map(fn order -> {..order, paid: True})
        |> Iter.to_list()

    dbg total_paid(cart)
    dbg total_paid(settled)
end
```
<!-- expect
dbg line 24: total_paid(cart) = 4
dbg line 25: total_paid(settled) = 13
-->

</div>

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">Control flow</p>

## Early exits, nothing mutable

`return`, `break` and `continue` work as they do in a language with
loops, and no value ever changes.

In a callback, [`continue` skips an element and `break` stops the
iteration](/iteration-and-loops/#break-and-continue-in-iter-callbacks),
keeping the total so far. `return` leaves a function early.

</div>

```nomi-run
fn label(price: Int) -> String
    if price == 0 then return "free"
    if price > 100 then return "expensive"

    "fair"
end

fn main()
    prices = [30, 0, 50, 40, 20]

    // Skip free items; stop before the total passes 100.
    spent = Iter.reduce(prices, 0, fn total, price ->
        if price == 0 then continue
        if total + price > 100 then break
        total + price
    end)

    dbg spent
    dbg label(0)
    dbg label(250)
end
```
<!-- expect
dbg line 18: spent = 80
dbg line 19: label(0) = "free"
dbg line 20: label(250) = "expensive"
-->

</div>

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">Tests</p>

## Tests beside the code

A `//!` line above a function is a
[test attached to it](/testing/#attached-tests), and it doubles as an
example of how to call it. A longer test is a
[`test` block](/testing/) in the same file.

`nomi test` runs both. A test with no effects is
[not run again](/tooling/running-tests/#unchanged-tests) until the code it
reaches changes.

</div>

```nomi-run
//! assert initials("Ada Lovelace") == "AL"
//
fn initials(name: String) -> String
    name
    |> String.split(" ")
    |> Iter.map(fn word -> String.slice(word, 0, 1))
    |> String.join("")
end

test "one word gives one letter"
    assert initials("Ada") == "A"
end
```
<!-- expect
ok initials //! test line 1
ok one word gives one letter
test result: ok. 2 passed, 0 failed
-->

</div>

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">Pattern matching</p>

## Match on shape, miss nothing

A [`case`](/pattern-matching/) takes a value apart by its shape: `.Card`
binds the text it carries, and `when` adds a condition to an arm.

It also has to cover every possibility. This one forgets `Cash`, so Nomi
won't run the program and says what is missing. Add the arm and it runs.

There is no null. A lookup that can miss gives back a
[`Maybe`](/pattern-matching/#maybet-and-resultt-e), and a call that can
fail gives back a [`Result`](/pattern-matching/#maybet-and-resultt-e),
so both outcomes are cases you have to cover.
[What can still stop a program](/pattern-matching/#what-can-fail-at-run-time).

</div>

```nomi-run error
enum Payment
    Card String
    Transfer (String, Int)
    Cash
end

fn describe(payment: Payment) -> String
    case payment
        .Card(last4)                        -> "card ${last4}"
        .Transfer(bank, days) when days > 2 -> "slow: ${bank}"
        .Transfer(bank, _)                  -> "from ${bank}"
    end
end

fn main()
    dbg describe(.Card("4242"))
    dbg describe(.Transfer("Acme", 5))
end
```
<!-- expect
main.nomi:8:5: non-exhaustive case on Payment: missing Cash
main.nomi:8:5: help: add an arm for each missing variant, or a `_` arm
-->

</div>

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">Types with rules</p>

## A type can carry its rule

A [`subtype`](/structs-enums-distinct/#subtypes) is the values of another
type that a rule allows. A literal is checked when the program compiles:
change `8080` to `0` and it won't run. A value known only when the program
runs, such as text a user typed, goes through `try_from`, which gives back
a `Maybe`.

Nomi comes with `PositiveInt`, `NonZeroInt`, `NonEmptyString`,
`NonEmptyList` and more, and the library uses them. `/` takes a divisor
that cannot be zero, so `share` needs no check of its own.

</div>

```nomi-run
subtype Port Int when fn p -> p >= 1 and p <= 65535

fn serve(port: Port) -> String
    "listening on ${port}"
end

fn port_from(text: String) -> Maybe<Port>
    text |> String.to_int() |> Maybe.flat_map(Port.try_from)
end

fn share(total: Int, people: PositiveInt) -> Int
    total / people
end

fn main()
    dbg serve(8080)
    dbg port_from("70000")
    dbg share(120, 4)
end
```
<!-- expect
dbg line 16: serve(8080) = "listening on 8080"
dbg line 17: port_from("70000") = None
dbg line 18: share(120, 4) = 30
-->

</div>

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">App fields</p>

## One place wires the program

[`boot`](/app-fields/) builds one value that holds what the program needs
everywhere: settings, a database, a logger. The struct's name is yours;
here it is `Shop`. Any function reads a field as `Shop.tax_percent`, with
nothing passed down through its callers.

These are [not globals](/app-fields/#app-fields-are-not-globals), and
nothing is assigned. `with` gives the rest of
its block, and the functions it calls, a value of its own. Nothing outside
the block sees it, a task running elsewhere included, and the earlier
value is back when the block ends. That is how a
[test swaps a real service for a fake](/app-fields/#testing-code-that-reads-app-fields),
with no mocking library.

</div>

```nomi-run
struct Shop
    tax_percent: Int
    currency:    String
end

fn boot() -> Result<Shop, String>
    Ok(Shop{tax_percent: 8, currency: "USD"})
end

fn price_tag(price: Int) -> String
    total = price + price * Shop.tax_percent / 100

    "${total} ${Shop.currency}"
end

fn main()
    dbg price_tag(200)

    with Shop.tax_percent = 20
    dbg price_tag(200)
end
```
<!-- expect
dbg line 17: price_tag(200) = "216 USD"
dbg line 20: price_tag(200) = "240 USD"
-->

</div>

<div class="nomi-home-showcase">

<div class="nomi-home-showcase-text">

<p class="nomi-home-kicker">Effects</p>

## It knows what is pure

The compiler knows which functions only compute and which can touch files,
the network or the clock. This project's `nomi.toml` allows output and
nothing else, so the file read in `saved_rate` is a compile error.

A call that can do more than compute is drawn bold, here and in your
editor, and hovering it says which effects it has. `discount` stays plain:
it only computes.

The same knowledge lets `nomi test`
[skip pure tests whose code did not change](/tooling/running-tests/#unchanged-tests),
and lets a project
[hold a dependency to no effects](/modules-and-imports/#the-effects-a-project-may-have).

</div>

```nomi-run error
// FILE: main.nomi
import
    std::fs
    std::io
end

fn discount(price: Int, loyal: Bool) -> Int
    if loyal then price * 9 / 10 else price
end

fn saved_rate() -> String
    "rate.txt"
    |> fs.read_text()
    |> Result.with_default("0")
    |> String.trim()
end

fn main()
    io.print("${discount(200, True)} ${saved_rate()}")
end

// FILE: nomi.toml
[module]
name = "pricing"
entry_points = ["main"]

[effects.self]
std = ["std::io"]
```
<!-- expect
main.nomi:12:8: this module's own code has the effect std::fs, and [effects.self] allows it only std::io
main.nomi:12:8: help: reached from `main`; `main` calls `saved_rate`
main.nomi:12:8: help: add "std::fs" to std under [effects.self] in nomi.toml, or remove the call
-->

</div>

<div class="nomi-home-section">

<p class="nomi-home-kicker">The rest</p>

## At a glance

Nomi is for web services and command-line tools. It is garbage collected,
runs on its own virtual machine, and comes with a
[standard library](/reference/) that covers HTTP, SQL, JSON, dates and
money.

<div class="nomi-home-features">
<div class="nomi-home-feature">
<h3>Types that stay out of the way</h3>
<p>Only <a href="/functions-and-lambdas/">functions state their types</a>; the rest is inferred. <a href="/interfaces-and-dispatch/">Interfaces</a>, <a href="/generics/">generics</a>, <a href="/structs-enums-distinct/#tuples">tuples</a>, <a href="/structs-enums-distinct/">distinct types</a>, and <a href="/typed-literals/">typed literals</a> such as <code>Url"…"</code>, checked before the program runs.</p>
</div>
<div class="nomi-home-feature">
<h3>One file to ship</h3>
<p><code>nomi build</code> writes one executable that needs nothing installed, for your platform or another.</p>
</div>
<div class="nomi-home-feature">
<h3>Tools that come with it</h3>
<p><a href="/tooling/formatting/"><code>nomi fmt</code></a> lays every file out one way, with no settings to argue over. A language server, a <a href="/tooling/editors-and-debugger/#debugging">debugger</a>, a REPL and an <a href="/tooling/editors-and-debugger/#coding-agents-nomi-mcp">MCP server for coding agents</a> are built in.</p>
</div>
</div>

</div>

<div class="nomi-home-section">

<p class="nomi-home-kicker">Where to start</p>

## Pick your way in

<div class="nomi-home-paths">
<a class="nomi-home-path is-primary" href="/learn/values/">
<span class="nomi-home-path-for">New to programming, or to types</span>
<span class="nomi-home-path-name">First Steps</span>
<span class="nomi-home-path-what">Seventeen short lessons from the very start, each with a task you check in the page. Good too if types, values that never change or <code>Maybe</code> are new to you.</span>
</a>
<a class="nomi-home-path" href="/introduction/">
<span class="nomi-home-path-for">You program already</span>
<span class="nomi-home-path-name">Language Tour</span>
<span class="nomi-home-path-what">The whole language, a chapter at a time, every example runnable.</span>
</a>
<a class="nomi-home-path" href="/coming-from/">
<span class="nomi-home-path-for">You know a language well</span>
<span class="nomi-home-path-name">Coming from…</span>
<span class="nomi-home-path-what">Rust, Go, Elixir, TypeScript, Python, Kotlin, Swift or Ruby, mapped onto Nomi.</span>
</a>
</div>

<p class="nomi-home-aside"><a href="/tooling/install/">Install Nomi</a> to run it on your own machine. The source is on <a href="https://github.com/nomi-language/nomi">GitHub</a>. Working with an AI agent? Give it the <a href="/cheat-sheet/">cheat sheet</a> or <a href="https://nomi-lang.org/llms-full.txt">llms-full.txt</a>.</p>

</div>

<div class="nomi-home-section">

:::note[Design Status]
Nomi is very early. This site is the main place to try the language while the
design is still being worked out. Expressions, types, pattern matching,
imports, interfaces, derives, tests, collections and control flow are the most
used and tested parts. Concurrency and module management are newer.
:::

</div>

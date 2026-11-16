---
title: "13. Choosing by kind"
description: "First Steps, lesson 13: a type with a few kinds of value, with enum, and choosing by kind with case."
---

Some values come in a few kinds and no others: the weather is sunny, rainy
or snowy. An **enum**, short for *enumeration*, a list of the
possibilities, names each kind, one per line:

```nomi-run
enum Weather
    Sunny
    Rainy
    Snowy
end

fn advice(weather: Weather) -> String
    case weather
        .Sunny -> "wear a hat"
        .Rainy -> "take an umbrella"
        .Snowy -> "wear boots"
    end
end

fn main()
    today = Weather.Rainy
    dbg today
    dbg advice(today)
    dbg advice(.Snowy)
end
```
<!-- expect
dbg line 17: today = Rainy
dbg line 18: advice(today) = "take an umbrella"
dbg line 19: advice(.Snowy) = "wear boots"
-->

`Weather.Rainy` is one of the kinds. Where Nomi already knows a Weather is
wanted, as in `advice(.Snowy)`, the dot and the kind's name are enough.

`case` from the lesson on decisions can also take a value, written right
after it: `case weather`. Then it chooses by that value's kind. Each line
under it is an **arm**: a kind, `->`, and the value to give back for it.
Nomi checks that the arms cover every kind, so if you add `Windy` to the
enum, it tells you which `case` doesn't handle it yet.

A Maybe has two kinds, `Some` and `None`, so `case` works on it too. In an
arm, `Some(n)` matches a Some and names the value inside it `n`. The type
`Maybe<String>` is a Maybe whose value, when there is one, is a String:

```nomi-run
fn hello(name: Maybe<String>) -> String
    case name
        Some(n) -> "Hello, ${n}"
        None    -> "Hello, stranger"
    end
end

fn main()
    dbg hello(Some("Ada"))
    dbg hello(None)
end
```
<!-- expect
dbg line 9: hello(Some("Ada")) = "Hello, Ada"
dbg line 10: hello(None) = "Hello, stranger"
-->

By now `->` has three jobs: after a function's inputs it points to the type
the function gives back, in `fn n -> ...` to the value, and in an arm to
the answer. `=>` appears only in maps, and `:` gives a name its type or a
field its value.

## Your turn

A traffic light goes from red to green, from green to yellow, and from
yellow back to red. `next` should give back the color that comes after
`light`. Right now it gets red right and sends every other color to red.

`_` in an arm matches any kind the arms above it didn't. Use it only when
every remaining kind gets the same answer.

```nomi-run lesson
enum Light
    Red
    Yellow
    Green
end

fn next(light: Light) -> Light
    case light
        .Red -> .Green
        _    -> .Red
    end
end

// The checks below run when you press Check. You don't need to change them.
test "red goes to green"
    assert next(.Red) == .Green
end

test "green goes to yellow"
    assert next(.Green) == .Yellow
end

test "yellow goes back to red"
    assert next(.Yellow) == .Red
end
```

```nomi-hint
Give each color its own arm, so there is no _ arm left: .Green -> .Yellow
and .Yellow -> .Red.
```

```nomi-solution
enum Light
    Red
    Yellow
    Green
end

fn next(light: Light) -> Light
    case light
        .Red    -> .Green
        .Green  -> .Yellow
        .Yellow -> .Red
    end
end

// The checks below run when you press Check. You don't need to change them.
test "red goes to green"
    assert next(.Red) == .Green
end

test "green goes to yellow"
    assert next(.Green) == .Yellow
end

test "yellow goes back to red"
    assert next(.Yellow) == .Red
end
```

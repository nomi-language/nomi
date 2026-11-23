---
title: "12. When something might be missing"
description: "First Steps, lesson 12: looking something up that might not be there, with Option."
---

A **map** connects keys to values, like a dictionary connects words to
their meanings. Write it with braces and `=>`:

```nomi-run
fn main()
    ages = {"Ada" => 36, "Bo" => 7}
    dbg Map.get(ages, "Ada")
    dbg Map.get(ages, "Cy")
    dbg ages |> Map.get("Cy") |> Option.with_default(0)
end
```
<!-- expect
dbg line 3: Map.get(ages, "Ada") = Some(36)
dbg line 4: Map.get(ages, "Cy") = None
dbg line 5: ages |> Map.get("Cy") |> Option.with_default(0) = 0
-->

Looking up `"Cy"` can't give back a number, because there is none. So
`Map.get` gives back an **Option**: `Some(36)` when the key is there, and
`None` when it isn't. Nomi makes you decide what happens when a value is
missing, so a missing value never stops a program by surprise.

The last line decides. It pipes the Option from `Map.get` into
`Option.with_default`, which gives back the value inside a `Some`, or its
fallback for `None`. Cy isn't in the map, so it gives back the fallback,
`0`.

Lists work the same way. `Iter.get(scores, 0)` gives back the first score
as an Option, and `None` when the list is too short. Positions count from 0,
so `Iter.get(scores, 1)` is the second.

## Your turn

`favorite_color` gives back a person's favorite color, or `"unknown"`.
Right now it uses an `if` that knows only Ada. Make it look the name up in
a map instead, `{"Ada" => "green", "Bo" => "blue"}`, so adding a person
means adding one entry.

```nomi-run lesson
fn favorite_color(name: String) -> String
    if name == "Ada" then "green" else "unknown"
end

// The checks below run when you press Check. You don't need to change them.
test "Ada likes green"
    assert favorite_color("Ada") == "green"
end

test "Bo likes blue"
    assert favorite_color("Bo") == "blue"
end

test "someone not in the map"
    assert favorite_color("Cy") == "unknown"
end
```

```nomi-hint
Name the map colors = {...}, then pipe it through Map.get(name) and
Option.with_default("unknown").
```

```nomi-solution
fn favorite_color(name: String) -> String
    colors = {"Ada" => "green", "Bo" => "blue"}
    colors |> Map.get(name) |> Option.with_default("unknown")
end

// The checks below run when you press Check. You don't need to change them.
test "Ada likes green"
    assert favorite_color("Ada") == "green"
end

test "Bo likes blue"
    assert favorite_color("Bo") == "blue"
end

test "someone not in the map"
    assert favorite_color("Cy") == "unknown"
end
```

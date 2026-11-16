---
title: "5. Text"
description: "First Steps, lesson 5: joining text, putting values into it with ${...}, and two String functions."
---

A `String` is a piece of text. There are two ways to build one from other
values:

```nomi-run
fn main()
    first = "Ada"
    last = "Lovelace"
    dbg first + " " + last
    dbg "Hello, ${first}!"
    dbg "${first} is ${String.length(first)} letters long"
end
```
<!-- expect
dbg line 4: first + " " + last = "Ada Lovelace"
dbg line 5: "Hello, ${first}!" = "Hello, Ada!"
dbg line 6: "${first} is ${String.length(first)} letters long" = "Ada is 3 letters long"
-->

- `+` joins two pieces of text. It adds no space of its own, so the space
  is a piece of text too: `" "`.
- `${...}` inside the quotes puts a value into the text. What goes between
  the braces can be a name, a sum or a call, and it can be any type: a
  number becomes its digits.

`String.length` counts the characters in a piece of text: letters,
spaces, and anything else, so `"Ada Lovelace"` has 12. The part before the
dot names where a group of functions lives, here the ones for text.
`String.uppercase` gives back the text in capital letters, so
`String.uppercase("Ada")` is `"ADA"`.

## Your turn

`badge` should make the text for a name badge: the name in capital
letters, then the age in parentheses. For Ada, 36, that is `"ADA (36)"`.
Right now the name isn't in capitals.

```nomi-run lesson
fn badge(name: String, age: Int) -> String
    "${name} (${age})"
end

// The checks below run when you press Check. You don't need to change them.
test "Ada's badge"
    assert badge("Ada", 36) == "ADA (36)"
end

test "Bo's badge"
    assert badge("Bo", 7) == "BO (7)"
end
```

```nomi-hint
Inside the first ${...}, call String.uppercase on the name.
```

```nomi-solution
fn badge(name: String, age: Int) -> String
    "${String.uppercase(name)} (${age})"
end

// The checks below run when you press Check. You don't need to change them.
test "Ada's badge"
    assert badge("Ada", 36) == "ADA (36)"
end

test "Bo's badge"
    assert badge("Bo", 7) == "BO (7)"
end
```

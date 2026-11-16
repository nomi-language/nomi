---
title: "2. Arithmetic and names"
description: "First Steps, lesson 2: doing arithmetic, and giving a value a name so you can use it again."
---

Nomi does arithmetic with the signs you know: `+` adds, `-` subtracts, `*`
multiplies (a star, not an `x`), and `/` divides. Parentheses work as in
math: `(2 + 3) * 4` is `20`.

Whole numbers divide to a whole number: `7 / 2` is 3, and what's left
over is dropped. `%` gives you what's left over, the **remainder**:
`7 % 2` is 1.

You can give a value a **name** with `=`, then use the name wherever you
would write the value:

```nomi-run
fn main()
    price = 4
    count = 3
    total = price * count
    dbg total
end
```
<!-- expect
dbg line 5: total = 12
-->

`price = 4` means "from now on, `price` is 4". The value itself never
changes: 4 is always 4. Writing `price = 5` further down makes `price` name
a new value from that line on. Giving each new value its own name, as
`total` does here, usually makes code easier to follow. A name is written
in lower case, with `_` between words: `unit_price`.

## Your turn

`rectangle_area` gets two numbers, the `width` and `height` of a rectangle,
and should give back its area: width times height. Right now it names
the sum of them `area` instead. Fix it, then press **Check**.

`width: Int` says that `width` is an Int, a whole number, and `-> Int`
that the function gives back one. The next lesson is about these.

```nomi-run lesson
fn rectangle_area(width: Int, height: Int) -> Int
    area = width + height
    area
end

// The checks below run when you press Check. You don't need to change them.
test "a 3 by 4 rectangle"
    assert rectangle_area(3, 4) == 12
end

test "a 10 by 2 rectangle"
    assert rectangle_area(10, 2) == 20
end
```

```nomi-hint
The area of a rectangle is its width multiplied by its height. Which sign
multiplies?
```

```nomi-solution
fn rectangle_area(width: Int, height: Int) -> Int
    area = width * height
    area
end

// The checks below run when you press Check. You don't need to change them.
test "a 3 by 4 rectangle"
    assert rectangle_area(3, 4) == 12
end

test "a 10 by 2 rectangle"
    assert rectangle_area(10, 2) == 20
end
```

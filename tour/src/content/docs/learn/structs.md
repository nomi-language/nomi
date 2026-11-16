---
title: "11. Structs"
description: "First Steps, lesson 11: describing a thing with named fields in a struct."
---

A **struct** groups values that belong together, each under a name of its
own, its **field**. You declare the struct once, then build values of it:

```nomi-run
struct Book
    title: String
    pages: Int
end

fn main()
    book = Book{title: "Matilda", pages: 240}
    dbg book.title
    dbg book.pages
end
```
<!-- expect
dbg line 8: book.title = "Matilda"
dbg line 9: book.pages = 240
-->

`book.title` reads the `title` field. A struct's name starts with a
capital letter.

## Your turn

`describe` gets a `Pet` and should give back a sentence such as
`"Rex is a dog, 3 years old"`. Right now it puts only the name in the
text.

```nomi-run lesson
struct Pet
    name: String
    kind: String
    age:  Int
end

fn describe(pet: Pet) -> String
    "${pet.name} is a pet"
end

// The checks below run when you press Check. You don't need to change them.
test "a dog"
    assert describe(Pet{name: "Rex", kind: "dog", age: 3}) == "Rex is a dog, 3 years old"
end

test "a cat"
    assert describe(Pet{name: "Tom", kind: "cat", age: 7}) == "Tom is a cat, 7 years old"
end
```

```nomi-hint
Put each field in the text with ${...}: ${pet.kind} and ${pet.age}.
```

```nomi-solution
struct Pet
    name: String
    kind: String
    age:  Int
end

fn describe(pet: Pet) -> String
    "${pet.name} is a ${pet.kind}, ${pet.age} years old"
end

// The checks below run when you press Check. You don't need to change them.
test "a dog"
    assert describe(Pet{name: "Rex", kind: "dog", age: 3}) == "Rex is a dog, 3 years old"
end

test "a cat"
    assert describe(Pet{name: "Tom", kind: "cat", age: 7}) == "Tom is a cat, 7 years old"
end
```

---
title: "JSON & Dynamic"
description: "Encode and decode JSON with std::json, through your own types or the Json value, and read data of unknown shape with Dynamic."
---

Data from outside a program has a shape the program did not choose: a
request body, a configuration file, a message from another service.
[`std::json`](/reference/json/) reads and writes JSON, and
[`std::dynamic`](/reference/dynamic/) reads a value whose shape is known only
at run time.

## Your own types as JSON

`derive ToJson` and `derive FromJson` write the conversions for a type, field
by field. Options after `with` rename the fields:

```nomi-run
import std::json.{FromJson, Json, ToJson}

struct User {
    first_name: String
    age: Int
}

derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for User with FromJson.Options{rename_all: Json.Case.Camel}

fn main() -> Result<Unit, Json.Error> {
    text = Json.encode_as(User{first_name: "Ada", age: 36})
    dbg text

    user: User = Json.decode_as(text)?
    dbg user

    Ok(Unit)
}

```
<!-- expect
dbg line 13: text = "{\"firstName\":\"Ada\",\"age\":36}"
dbg line 16: user = User{first_name: "Ada", age: 36}
-->

`Json.encode_as` turns a value into JSON text, and `Json.decode_as` reads it
back. The annotation `user: User` tells `decode_as` which type to read, since
no argument says so. [`std::json`](/reference/json/) has the other options,
and how enums and `Maybe` fields map to JSON.

## The Json value

`Json.decode` reads JSON text into a [`Json`](/reference/json/#enum-json), an
enum with a variant for each kind of JSON value. Match on it with `case` when
the shape varies, or when you need only a part of a document:

```nomi-run
import {
    std::io
    std::json.Json
}

fn describe(jv: Json) -> String {
    case jv {
        .String(s) -> "the text ${s}"
        .Int(n) -> "the whole number ${n}"
        .Arr(items) -> "a list of ${Iter.count(items)}"
        .Obj(fields) -> "an object with the keys ${Map.keys(fields) |> String.join(", ")}"
        _ -> Json.kind(jv)
    }
}

fn main() {
    ["\"hi\"", "42", "[1, 2, 3]", "{\"name\": \"Ada\", \"age\": 36}", "null", "{"]
    |> Iter.each(fn text -> {
        case Json.decode(text) {
            Ok(jv) -> io.print(describe(jv))
            Err(e) -> io.print("not JSON: ${e}")
        }
    })
}
```
<!-- expect
the text hi
the whole number 42
a list of 3
an object with the keys age, name
null
not JSON: json decode error at line 1, col 2: unexpected end of input: incomplete JSON value
-->

A number with no fraction or exponent is an `Int`, exact at any size, and
any other number is a `Float`.

## Dynamic values

Sometimes you get a value with no known Nomi type: a plugin message, a
decoded payload, loosely typed configuration, or data from another runtime.
[`Dynamic`](/reference/dynamic/) holds such a value.

Take the value in as `Dynamic`, then decode it into a real type once you
know what shape to expect. Every function in
[`std::dynamic`](/reference/dynamic/) that reads a `Dynamic` returns a
`Result`. Its error is a [`dynamic.Error`](/reference/dynamic/#struct-error),
which holds the path to the bad part, what was expected, and what was
there. So when a payload is wrong, the error tells you where.

`Json.to_dynamic` turns a decoded `Json` into a `Dynamic`. Here a file of
users has one bad entry:

```nomi-run
// FILE: main.nomi
import {
    std::dynamic.{self, Dynamic}
    std::fs
    std::io
    std::json.Json
}

struct User {
    name: String
    age: Int
}

// One user, from a value read as Dynamic.
fn user(d: Dynamic) -> Result<User, dynamic.Error> {
    name = Dynamic.at_field(d, "name", Dynamic.as_string)?
    age = Dynamic.at_field(d, "age", Dynamic.as_int)?
    Ok(User{name, age})
}

// Print each user, or why it could not be read.
fn print_users(users: Dynamic, count: Int) {
    0..count
    |> Iter.each(fn i -> {
        case Dynamic.at_index(users, i, user) {
            Ok(u) -> io.print("${u.name} is ${u.age}")
            Err(e) -> io.print("user ${i}: ${e}")
        }
    })
}

fn main() -> Result<Unit, String> {
    text = fs.read_text("users.json") |> Result.map_err(fn e -> "${e}")?
    users = Json.decode(text) |> Result.map_err(fn e -> "${e}")? |> Json.to_dynamic()

    count = Dynamic.as_list(users) |> Result.map_err(fn e -> "${e}")? |> Iter.count()
    print_users(users, count)
    Ok(Unit)
}

// FILE: users.json
[
    {"name": "Ada", "age": 36},
    {"name": "Grace", "age": "unknown"}
]
```
<!-- expect
Ada is 36
user 1: decode error at [1].age: expected Int, got String
-->

`user` reads one entry with `Dynamic.at_field`, which adds the field's name
to the path of any error, and `Dynamic.at_index` adds the entry's position.
So the second entry's error says where it is, `[1].age`, and what was wrong.

Use `Dynamic` where data comes in from outside. Inside your program, give
data real types.

## Bytes in text

JSON, URLs and HTTP headers carry text, so bytes travel in them encoded.
[`std::base64`](/reference/base64/) and [`std::hex`](/reference/hex/) write
bytes as text and read them back, a decode returning a `Result`.
[`std::url`](/reference/url/) percent-encodes text for a query or a path, and
[`std::hash`](/reference/hash/) signs data with an HMAC:

```nomi
signature = hash.hmac_sha256(key, body) |> base64.encode_url(padded: False)

link = "/search?q=${url.escape_query(term)}"
```

Make a key with `random.secure_bytes(32)`, never with a seeded `Generator`.

[HTTP](/http/) is next. It sends requests and serves them.

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

struct User
    first_name: String
    age:        Int
end

derive ToJson for User with ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for User with FromJson.Options{rename_all: Json.Case.Camel}

fn main() -> Result<Unit, Json.Error>
    text = Json.encode_as(User{first_name: "Ada", age: 36})
    dbg text

    user: User = Json.decode_as(text)?
    dbg user

    Ok(Unit)
end
```
<!-- expect
dbg line 13: text = "{\"firstName\":\"Ada\",\"age\":36}"
dbg line 16: user = User{first_name: "Ada", age: 36}
-->

`Json.encode_as` turns a value into JSON text, and `Json.decode_as` reads it
back. The annotation `user: User` tells `decode_as` which type to read, since
no argument says so. [`std::json`](/reference/json/) has the other options,
and how enums and `Maybe` fields map to JSON.

A shape used in one place needs no struct. An anonymous struct is JSON when
each field is, as a struct deriving both with no options: its field names
are the keys, and a missing `Maybe` field reads as `None`. A PATCH body that
must tell a key left out from one set to `null` types the field
`Presence<Maybe<T>>` ([`std::presence`](/reference/presence/)): a missing key
is `Absent`, `null` is `Present(None)`, and an `Absent` field is left out
when written.

```nomi-run
import std::json.Json

fn main() -> Result<Unit, Json.Error>
    reply = Json.encode_as({error: "no todo 99"})
    dbg reply

    todo = Json.decode_as<{title: String, order: Maybe<Int>}>(`{"title": "milk"}`)?
    dbg todo

    Ok(Unit)
end
```
<!-- expect
dbg line 5: reply = "{\"error\":\"no todo 99\"}"
dbg line 8: todo = {order: None, title: "milk"}
-->

A frontend written in TypeScript can share these shapes. `nomi types` writes
a TypeScript declaration for each `pub` type of the named files and
directories that crosses as JSON, and for each type those reach. Were `User`
above `pub`, in users.nomi, `nomi types users.nomi -o web/src/api.ts` would
write:

```ts
export interface User {
  firstName: string;
  age: number;
}
```

An `Int` is a `number` there, exact up to 2^53. The
[tooling reference](/tooling/commands/#typescript-types-nomi-types) has the
whole mapping.

## The Json value

`Json.decode` reads JSON text into a [`Json`](/reference/json/#enum-json), an
enum with a variant for each kind of JSON value. Match on it with `case` when
the shape varies, or when you need only a part of a document:

```nomi-run
import
    std::io
    std::json.Json
end

fn describe(jv: Json) -> String
    case jv
        .String(s)   -> "the text ${s}"
        .Int(n)      -> "the whole number ${n}"
        .Arr(items)  -> "a list of ${Iter.count(items)}"
        .Obj(fields) -> "an object with the keys ${Map.keys(fields) |> String.join(", ")}"
        _            -> Json.kind(jv)
    end
end

fn main()
    ["\"hi\"", "42", "[1, 2, 3]", "{\"name\": \"Ada\", \"age\": 36}", "null", "{"]
    |> Iter.each(fn text ->
        case Json.decode(text)
            Ok(jv) -> io.print(describe(jv))
            Err(e) -> io.print("not JSON: ${e}")
        end
    end)
end
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
import
    std::dynamic.{self, Dynamic}
    std::fs
    std::io
    std::json.Json
end

struct User
    name: String
    age:  Int
end

// One user, from a value read as Dynamic.
fn user(d: Dynamic) -> Result<User, dynamic.Error>
    name = Dynamic.at_field(d, "name", Dynamic.as_string)?
    age = Dynamic.at_field(d, "age", Dynamic.as_int)?
    Ok(User{name:, age:})
end

// Print each user, or why it could not be read.
fn print_users(users: Dynamic, count: Int)
    0..count
    |> Iter.each(fn i ->
        case Dynamic.at_index(users, i, user)
            Ok(u)  -> io.print("${u.name} is ${u.age}")
            Err(e) -> io.print("user ${i}: ${e}")
        end
    end)
end

fn main() -> Result<Unit, String>
    text = fs.read_text("users.json") |> Result.map_err(&"${&1}")?
    users = Json.decode(text) |> Result.map_err(&"${&1}")? |> Json.to_dynamic()

    count = Dynamic.as_list(users) |> Result.map_err(&"${&1}")? |> Iter.count()
    print_users(users, count)
    Ok(Unit)
end

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

## CSV and XML

[`std::csv`](/reference/csv/) reads records, or rows found by a header's
column names, and writes them quoted as RFC 4180 says.
[`std::xml`](/reference/xml/) reads a document into a tree of elements
and writes one back. Both read a `Reader` a record or a token at a time
when a file is too large to hold, and both name the line and column of
anything malformed:

```nomi-run
import
    std::csv
    std::xml.{self, Element}
end

fn main()
    rows = csv.decode_rows("name,age\nAda,36\n")!
    dbg rows |> Iter.map(&csv.Row.get(&1, "age")) |> Iter.to_list()

    doc = xml.decode("<feed><title>News &amp; notes</title></feed>")!
    dbg Element.child_text(doc, "title")
    dbg csv.decode("a,\"b")
end
```
<!-- expect
dbg line 8: rows |> Iter.map(&csv.Row.get(&1, "age")) |> Iter.to_list() = [Some("36")]
dbg line 11: Element.child_text(doc, "title") = Some("News & notes")
dbg line 12: csv.decode("a,\"b") = Err(Syntax{message: "a quoted field is not closed, or has a quote in it that is not doubled", line: 1, column: 5})
-->

## Bytes in text

JSON, URLs and HTTP headers carry text, so bytes travel in them encoded.
[`std::base64`](/reference/base64/) and [`std::hex`](/reference/hex/) write
bytes as text and read them back, a decode returning a `Result`.
A [`Url"..."`](/reference/url/) literal percent-encodes each slot for a
query or a path, and [`std::hash`](/reference/hash/) signs data with an HMAC:

```nomi
signature = hash.hmac_sha256(key, body) |> base64.encode_url(padded: False)

link = Url"/search?q=${term}"
```

Make a key with `random.secure_bytes(32)`, never with a seeded `Generator`.

[HTTP](/http/) is next. It sends requests and serves them.

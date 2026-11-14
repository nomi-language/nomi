---
title: Coming from Go
description: What carries over from Go to Nomi, what differs, and the same program written in both.
---

Much of Go carries over: static types with inference for locals, errors as
values rather than exceptions, structs, interfaces, backtick raw strings, one
formatter for all code, tasks and channels, and a single executable from
`nomi build`. Nomi programs can also [call Go packages](/calling-go/)
directly. What differs most: values never change, errors pass up with a
postfix `?`, and a type implements an interface by saying so.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| Go | Nomi |
|---|---|
| `x := 1`, `var x int`, `x++` | `x = 1`, `x: Int = 1`. Nothing is mutable; a later `x = ...` binds the name again. |
| `func (s Shape) Area() int`, `s.Area()` | `fn area(shape: Shape) -> Int` in an `impl Shape` block, `Shape.area(s)`: there are no method calls on a value |
| An interface is satisfied implicitly | `impl Interface for Type` |
| `nil` | `None`, from `Maybe<T>` |
| `if err != nil { return err }` | A postfix `?` on a `Result` or `Maybe` |
| `switch x { case 1: .. }` | `case x`, then an arm per line such as `1 -> ..`, then `end`; it must cover every value |
| `defer f.Close()` | `defer Reader.close_quietly(r)` on the line after opening. It runs when its block ends, where Go's waits for the function to return, and the checker requires it for a resource. `defer on error` runs only when the function fails. |
| `for i, x := range xs { .. }` | `Iter` functions: `Iter.each`, `Iter.map`, `Iter.reduce`, `Iter.with_index` |
| `float64(x)` | `Int.to_float(x)`. Nomi's `x as T` converts nothing: it only states a type the value already fits (`[] as List<String>`) |
| `go f()`, `sync.WaitGroup` | `Task.spawn(fn -> f())` inside `concurrent` ... `end`, which waits for its tasks |
| Dependencies built in `main` and passed to handlers in a struct | `fn boot() -> Result<App, String>` builds an `App` struct before `main` runs, and any function reads a field as `App.db`, with nothing passed down. A test replaces one with `with App.db = ..`. See [App Fields](/app-fields/). |

## Spelled differently

| Go | Nomi |
|---|---|
| `[]int{1, 2}`, `map[string]int{"a": 1}` | `[1, 2]`, `{"a" => 1}` |
| `Point{X: 1}` | `Point{x: 1}` |
| An exported name starts with a capital | `pub` |
| A `[]Dog` is no `[]Speaker`; copy it in a loop | A `List<Dog>` passes where a `List<Speaker>` is expected, with nothing copied |
| `select` | `Channel.select([..])` |
| `fmt.Sprintf("%d items", n)` | `"${n} items"` |
| `func Map[T any](..)` | `fn map<T>(..)` |
| `_test.go` files | `test "name"` ... `end` blocks beside the code |
| `import "app/db/posts"`, then `posts.Find(1)` | `import db::posts`, then `posts.find(1)`; where `posts` is taken, `import db` imports the directory and `db::posts.find(1)` reaches the file, instead of an import alias |
| `mux.HandleFunc("GET /users/{id}", show)`, then `r.PathValue("id")` | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: {id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. `Route"GET /users/{id}"` on its own has a `String` field, as `PathValue` returns. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

## The same program

```go
func countWords(text string) map[string]int {
	counts := map[string]int{}
	for _, w := range strings.Fields(text) {
		counts[strings.ToLower(w)]++
	}
	return counts
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("not a number: %s", s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("out of range: %d", n)
	}
	return n, nil
}
```

```nomi-run
// FILE: words.nomi
fn count_words(text: String) -> Map<String, Int>
    text
    |> String.words()
    |> Iter.map(String.lowercase)
    |> Iter.frequencies()
end

fn parse_port(text: String) -> Result<Int, String>
    n =
        text
        |> String.to_int()
        |> Maybe.to_result("not a number: ${text}")?

    if n < 1 or n > 65535 then
        Err("out of range: ${n}")
    else
        Ok(n)
    end
end

// FILE: words_test.nomi
import words.{count_words, parse_port}

test "the same program"
    assert count_words("the The cat") == {"the" => 2, "cat" => 1}
    assert parse_port("8080") == Ok(8080)
    assert parse_port("x") == Err("not a number: x")
    assert parse_port("0") == Err("out of range: 0")
end
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`Iter.frequencies` counts each value into a new map, where the Go loop
changes one map in place. `||` and `&&` are written `or` and `and`.

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

## What differs

| Go | Nomi |
|---|---|
| `x := 1`, `var x int` | `x = 1`, `x: Int = 1`. Nothing is mutable; a later `x = ...` binds the name again. |
| `if err != nil { return err }` | A postfix `?` on a `Result` or `Maybe` |
| `func (s Shape) Area() int`, `s.Area()` | `impl Shape { fn area(shape: Shape) -> Int }`, `Shape.area(s)` |
| An interface is satisfied implicitly | `impl Interface for Type { .. }` |
| `nil` | `None`, from `Maybe<T>` |
| `[]int{1, 2}`, `map[string]int{"a": 1}` | `[1, 2]`, `%{"a" => 1}` |
| `Point{X: 1}` | `Point{x: 1}` |
| An exported name starts with a capital | `pub` |
| `for i, x := range xs { .. }` | `Iter` functions: `Iter.each`, `Iter.map`, `Iter.reduce`, `Iter.with_index` |
| `switch x { case 1: .. }` | `case x { 1 -> .. }`, which must cover every value |
| `go f()`, `sync.WaitGroup` | `Task.spawn(fn -> f())` inside `concurrent { .. }`, which waits for its tasks |
| `select` | `Channel.select([..])` |
| `fmt.Sprintf("%d items", n)` | `"${n} items"` |
| `func Map[T any](..)` | `fn map<T>(..)` |
| `_test.go` files | `test "name" { assert .. }` blocks beside the code |
| `import "app/db/posts"`, then `posts.Find(1)` | `import db::posts`, then `posts.find(1)`; the full path `db::posts.find(1)` works where `posts` is taken, instead of an import alias |

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
fn count_words(text: String) -> Map<String, Int> {
    text
    |> String.words()
    |> Iter.map(String.lowercase)
    |> Iter.frequencies()
}

fn parse_port(text: String) -> Result<Int, String> {
    n =
        text
        |> String.to_int()
        |> Maybe.to_result("not a number: ${text}")?

    if n < 1 or n > 65535 {
        Err("out of range: ${n}")
    } else {
        Ok(n)
    }
}

// FILE: words_test.nomi
import words.{count_words, parse_port}

test "the same program" {
    assert count_words("the The cat") == %{"the" => 2, "cat" => 1}
    assert parse_port("8080") == Ok(8080)
    assert parse_port("x") == Err("not a number: x")
    assert parse_port("0") == Err("out of range: 0")
}
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`Iter.frequencies` counts each value into a new map, where the Go loop
changes one map in place. `||` and `&&` are written `or` and `and`.

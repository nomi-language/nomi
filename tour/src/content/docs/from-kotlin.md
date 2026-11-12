---
title: Coming from Kotlin
description: What carries over from Kotlin to Nomi, what differs, and the same program written in both.
---

Much of Kotlin carries over: `val` bindings, data classes, sealed
hierarchies matched by an exhaustive `when`, named arguments with defaults,
lambdas with inferred parameters, string templates, and structured
concurrency. What differs most: nothing is mutable at all, nullable types
become `Maybe`, errors are values rather than exceptions, and there are no
methods or extension functions: a function is called through its owner.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## Will trip you up

| Kotlin | Nomi |
|---|---|
| `val x = 1`, `var x` | `x = 1`. There is no `var`; a later `x = ...` binds the name again. |
| `list.map { it.name }` | `Iter.map(list, .name)`: there are no method calls on a value |
| `fun String.shout() = ..`, `"a".shout()` | A plain function, `shout("a")`, or one in `impl` called through its type |
| `String?`, `?.`, `?:` | `Maybe<String>`, `Maybe.map`, `Maybe.with_default`, or `Maybe.otherwise` when the fallback may be missing too |
| `throw`, `runCatching` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `x.toDouble()`, `x as Shape` | `Int.to_float(x)`. Nomi's `x as T` never casts or throws: it only states a type the value already fits (`[] as List<String>`) |
| `file.use { .. }` | `defer Reader.close_quietly(r)` on the line after opening; it runs when its block ends, and the checker requires it for a resource |
| Spring's constructor injection, or Koin or Ktor's `dependencies { provide { .. } }` | `fn boot() -> Result<App, String>` builds an `App` struct before `main` runs, and any function reads a field as `App.db`, with nothing injected. A test replaces one with `with App.db = ..`. See [App Fields](/app-fields/). |

## Spelled differently

| Kotlin | Nomi |
|---|---|
| `data class Account(val owner: String)` | `struct Account`, its fields one per line (`owner: String`), then `end`. `==` compares the fields, as a data class's `equals` does. |
| `account.copy(balance = 5)` | `{..account, balance: 5}` |
| `sealed class Event` with subclasses | `enum Event`, one variant per line: `Deposit Int` |
| `when (e) { is Deposit -> .. }` | `case e` with arms such as `.Deposit(amount) -> ..`, then `end`; it must cover every variant |
| `Pair(a, b)`, `a to b` | `(a, b)`, read as `.first` and `.second` as a `Pair` is |
| `listOf(1, 2)`, `mapOf("a" to 1)` | `[1, 2]`, `{"a" => 1}` |
| `interface` with default methods | `interface`, where a default an impl may replace is an `open fn`; each implementation goes in `impl Interface for Type` |
| `TODO("parse")` | `todo "parse"` |
| `coroutineScope { launch { .. } }` | `Task.spawn(fn -> ..)` inside `concurrent` .. `end` |
| `@Test fun t()` | `test "name"` ... `end` |
| Ktor's `get("/users/{id}") { call.parameters["id"] }`, Spring's `@GetMapping("/users/{id}")` with `@PathVariable id: Long` | `Route.to_with("GET /users/{id}", show)` with `fn show(Request, user: {id: Int}) -> Response`. The handler gets `user.id` already parsed, `/users/ann` gets a 404, and the pattern is checked when the program is. See [HTTP](/http/#handlers-and-routes) and [Frontends](/frontends/). |

Named arguments and defaults work as in Kotlin: `area(5, height: 4)`.

## The same program

```kotlin
data class Account(val owner: String, val balance: Int = 0)

sealed class Event {
    data class Deposit(val amount: Int) : Event()
    data class Withdraw(val amount: Int) : Event()
}

fun apply(account: Account, event: Event): Result<Account> {
    val balance = when (event) {
        is Event.Deposit -> account.balance + event.amount
        is Event.Withdraw -> account.balance - event.amount
    }
    return if (balance < 0) Result.failure(Exception("short by ${-balance}"))
    else Result.success(account.copy(balance = balance))
}

fun replay(account: Account, events: List<Event>): Result<Account> =
    events.fold(Result.success(account)) { acc, e -> acc.mapCatching { apply(it, e).getOrThrow() } }

fun findOwner(accounts: List<Account>, owner: String): Account? =
    accounts.find { it.owner == owner }
```

```nomi-run
// FILE: accounts.nomi
struct Account
    owner: String
    balance: Int = 0
end

enum Event
    Deposit Int
    Withdraw Int
end

fn apply(account: Account, event: Event) -> Result<Account, String>
    balance = case event
        .Deposit(amount) -> account.balance + amount
        .Withdraw(amount) -> account.balance - amount
    end

    if balance < 0 then
        Err("short by ${-balance}")
    else
        Ok({..account, balance:})
    end
end

fn replay(account: Account, events: List<Event>) -> Result<Account, String>
    Iter.try_reduce(events, account, apply)
end

fn find_owner(accounts: List<Account>, owner: String) -> Maybe<Account>
    Iter.find(accounts, fn account -> account.owner == owner)
end

// FILE: accounts_test.nomi
import accounts.{Account, find_owner, replay}

test "the same program"
    ada = Account{owner: "Ada"}

    assert replay(ada, [.Deposit(10), .Withdraw(4)]) == Ok(Account{owner: "Ada", balance: 6})
    assert replay(ada, [.Withdraw(3)]) == Err("short by 3")
    assert find_owner([ada], "Bo") == None
end
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`Iter.try_reduce` is the fold that stops at the first `Err`, which Kotlin
spells with `mapCatching`. `{..account, balance:}` is `copy`: a new value
with one field replaced.

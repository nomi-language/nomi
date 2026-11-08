---
title: Coming from Kotlin
description: What carries over from Kotlin to Nomi, what differs, and the same program written in both.
---

Much of Kotlin carries over: `val` by default, data classes, sealed
hierarchies matched by an exhaustive `when`, named arguments with defaults,
lambdas with inferred parameters, string templates, and structured
concurrency. What differs most: nothing is mutable at all, nullable types
become `Maybe`, errors are values rather than exceptions, and there are no
methods or extension functions: a function is called through its owner.

The [cheat sheet](/cheat-sheet/) lists the rules in one place.

## What differs

| Kotlin | Nomi |
|---|---|
| `val x = 1`, `var x` | `x = 1`. There is no `var`; a later `x = ...` binds the name again. |
| `data class Account(val owner: String)` | `struct Account { owner: String }`, one field per line |
| `account.copy(balance = 5)` | `#{..account, balance: 5}` |
| `sealed class Event` with subclasses | `enum Event`, one variant per line: `Deposit Int` |
| `when (e) { is Deposit -> .. }` | `case e { .Deposit(amount) -> .. }`, which must cover every variant |
| `String?`, `?.`, `?:` | `Maybe<String>`, `Maybe.map`, `Maybe.with_default` |
| `throw`, `runCatching` | Return `Result<T, E>`; a postfix `?` passes an `Err` up |
| `fun String.shout() = ..`, `"a".shout()` | A plain function, `shout("a")`, or one in `impl` called through its type |
| `list.map { it.name }` | `Iter.map(list, .name)` |
| `Pair(a, b)`, `a to b` | `(a, b)` |
| `listOf(1, 2)`, `mapOf("a" to 1)` | `[1, 2]`, `%{"a" => 1}` |
| `interface` with default methods | `interface`, with each implementation in `impl Interface for Type { .. }` |
| `coroutineScope { launch { .. } }` | `concurrent { Task.spawn(fn -> ..) }` |
| `@Test fun t()` | `test "name" { assert .. }` |

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
struct Account {
    owner: String
    balance: Int = 0
}

derive Equatable for Account

enum Event {
    Deposit Int
    Withdraw Int
}

fn apply(account: Account, event: Event) -> Result<Account, String> {
    balance = case event {
        .Deposit(amount) -> account.balance + amount
        .Withdraw(amount) -> account.balance - amount
    }

    if balance < 0 {
        Err("short by ${-balance}")
    } else {
        Ok(#{..account, balance})
    }
}

fn replay(account: Account, events: List<Event>) -> Result<Account, String> {
    Iter.try_reduce(events, account, apply)
}

fn find_owner(accounts: List<Account>, owner: String) -> Maybe<Account> {
    Iter.find(accounts, fn account -> account.owner == owner)
}

// FILE: accounts_test.nomi
import accounts.{Account, find_owner, replay}

test "the same program" {
    ada = Account{owner: "Ada"}

    assert replay(ada, [.Deposit(10), .Withdraw(4)]) == Ok(Account{owner: "Ada", balance: 6})
    assert replay(ada, [.Withdraw(3)]) == Err("short by 3")
    assert find_owner([ada], "Bo") == None
}
```
<!-- expect
ok the same program
test result: ok. 1 passed, 0 failed
-->

`Iter.try_reduce` is the fold that stops at the first `Err`, which Kotlin
spells with `mapCatching`. `#{..account, balance}` is `copy`: a new value
with one field replaced.

# Bank Account

Subject: Games & Simulation

A bank account that can be opened, closed, paid into and drawn from, by many tasks at once, without its balance going wrong.

Adapted from Exercism problem-specifications (MIT), exercises/bank-account.

## Choices

- Adapted: the exercise asks for a mutable account object whose methods change its balance, safely under concurrent calls. Nomi values are immutable, so the balance is the state of one task, the teller, which answers requests one at a time (`std/tasks` and `std/channels`). An `Account` is the teller's inbox, and each operation sends a request with its own reply channel and waits for the answer. Two tasks never touch the balance, so there is nothing to lock.
- `Account` is an opaque type over the `Sender` half of the teller's inbox, and the operations are its functions (`Account.deposit(account, 1)`). Code outside the file cannot take it apart, so `Request` stays private.
- `with_account(body)` makes an account that is not open yet, runs its teller beside `body` inside a `concurrent` block, closes the inbox when `body` returns, and answers what `body` answers. A teller cannot outlive the block that spawned it, so the account is scoped to `body` rather than returned.
- The teller is an `Iter.loop` whose state is the balance, `None` while the account is not open. `handle` answers one request with the pure functions `opened`, `closed`, `deposited` and `withdrawn`, which check in the canonical order: not open, then a non-positive amount, then an amount above the balance.
- The four canonical errors are an `Error` enum with an `impl Display` giving the canonical messages, which one test checks.
- The canonical cases are lists of operations ending in the one whose result is expected. The test file's `replay` runs a case's operations as `Step`s on a new account, stopping at the first refused one, and then asks the balance, so each row is the operations before the final balance check and the expected `Result<Int, Error>`. A case that ends in a refused operation never reaches the balance check.
- The 16 sequential cases are one `subtests` table named by the description column. "Can handle concurrent transactions" is its own test: 1000 tasks (100 running at once, `Task.spawn_all`) each deposit 1 and withdraw 1, and the balance is 0 after.

## Friction

None.

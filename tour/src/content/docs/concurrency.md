---
title: "Concurrency"
description: "Structured concurrency with `concurrent { ... }`, tasks, channels, supervisors, cancellation, and restart policies."
---

Nomi's concurrency is **structured**. Every task belongs to an owner, and
that owner is responsible for finishing, cancelling, or reporting it.
Nothing is spawned into the void.

There are two common lifetimes:

> **Need the value here?** → `concurrent { … }` and `Task.spawn(|| …)`
>
> **Should the work outlive this call?** → `Supervisor.spawn(supervisor, || …)`

They fit together. A request handler might use a `concurrent` block to
compute the response, then hand one audit write to a supervisor so the
response can return while the audit finishes.

Underneath, tasks run as goroutines, channels map closely to Go channels,
and cancellation flows through Go-style contexts. Tasks are cheap. The
limits you choose are usually about what the work touches: a database pool,
an API rate limit, a single worker, or the number of cores for CPU-bound
work.

## `concurrent`, `Task.spawn`, `Task.await`

Start with a block. It owns the tasks spawned inside it and returns after
those tasks are consumed:

```nomi-run
import std/tasks.Task

fn double(n: Int): Int {
    n * 2
}

fn main(): Int {
    total = concurrent {
        a = Task.spawn(|| double(10))
        b = Task.spawn(|| double(20))
        c = Task.spawn(|| double(30))
        Task.await(a) + Task.await(b) + Task.await(c)
    }

    dbg total
}

```
<!-- expect
dbg line 15: total = 120
-->

- **`concurrent { ... }`** opens a scope that owns the tasks spawned inside
  it. The block's value is its last expression.
- **[`Task.spawn(|| expr)`](/reference/tasks/#taskspawn)** starts the
  lambda as a `Task<T>` owned by that scope and returns its handle
  immediately.
- **[`Task.await(task)`](/reference/tasks/#taskawait)** waits for the
  task and yields its return value.

The compiler enforces the ownership rules: every block-owned `Task<T>`
must be consumed before the block ends, and it cannot escape the block it
was spawned in. `Task.await` is one way to consume a task;
[`Task.outcome`](#when-a-task-actually-breaks), later on this page, is the
other.

`Task.spawn` does not name an owner because the current `concurrent` block
is the only possible one. Supervisors, introduced below, are named owners
with longer lifetimes.

## Channels

Use a [`Channel`](/reference/channels/) to pass values between tasks. Create
the channel, then hand out the two halves: producers send on the
[`Sender`](/reference/channels/#type-sender), the consumer receives on the
[`Receiver`](/reference/channels/#type-receiver) until the channel is closed.

```nomi-run
import {
    std/channels.{Channel, Receiver, Sender}
    std/io
    std/tasks.Task
}

fn produce(out: Sender<Int>, n: Int): Unit {
    case Sender.send(out, n) {
        Ok(_) -> Unit
        Err(_) -> io.print("send failed")
    }
}

fn consume(inbox: Receiver<Int>): Int {
    Iter.loop(|sum = 0|
        case Receiver.receive(inbox) {
            Some(v) -> sum + v
            None -> break sum
        }
    )
}

fn main(): Int {
    ch = Channel.buffered<Int>(4)

    total = concurrent {
        p1 = Task.spawn(|| produce(ch.sender, 10))
        p2 = Task.spawn(|| produce(ch.sender, 20))
        p3 = Task.spawn(|| produce(ch.sender, 30))
        cons = Task.spawn(|| consume(ch.receiver))

        // Drain the producers, close so the consumer's loop ends on None.
        Task.await(p1)
        Task.await(p2)
        Task.await(p3)
        Sender.close(ch.sender)
        Task.await(cons)
    }

    dbg total
}
```
<!-- expect
dbg line 40: total = 60
-->

[`Receiver.receive`](/reference/channels/#receiverreceive) returns
[`Maybe<T>`](/reference/maybe/). It blocks until a value arrives, returns
`Some(v)` for a value, and returns `None` after the channel is closed and
drained. That `None` ends the consumer's loop, so the example closes the
sender only after all producers have finished.

`consume` only receives a `Receiver<Int>`. It cannot send, and it cannot
close the channel. Closing is on [`Sender.close`](/reference/channels/#senderclose)
because it means "nothing more is coming," which is a promise only the
writing side can make.

There are two constructors:

- **[`Channel.buffered<T>(n)`](/reference/channels/#channelbuffered)** lets a
  producer run up to `n` values ahead of its consumer before `send` blocks.
  Pick `n` the way you'd pick any queue depth: how far ahead is useful, and
  how much are you willing to hold in memory.
- **[`Channel.unbuffered<T>()`](/reference/channels/#channelunbuffered)** makes
  every `send` a rendezvous — it doesn't return until somebody receives.

Unbuffered channels couple the producer's schedule to the consumer's
schedule. That is occasionally useful, but it is the easy way to deadlock
when one task sends and then receives on the same channel. `buffered` is
the common choice; reach for `unbuffered` when the hand-off itself is the
synchronisation you need.

## Errors cancel siblings

A `concurrent` block still owns its tasks when something goes wrong. When
`try` sees an `Err` from [`Task.await`](/reference/tasks/#taskawait),
the block short-circuits, cancels sibling tasks, waits for them to unwind,
and returns the error:

```nomi-run
import {
    std/tasks.Task
    std/duration.Duration
    std/timer
}

fn short_fail(): Result<Int, String> {
    timer.sleep(Duration.milliseconds(10))
    Err("boom")
}

fn long_sleep(): Result<Int, String> {
    timer.sleep(Duration.seconds(5))
    Ok(0)
}

fn main() {
    outcome = concurrent {
        short = Task.spawn(|| short_fail())
        long = Task.spawn(|| long_sleep())
        n = try Task.await(short)
        _ = try Task.await(long)
        Ok(n)
    }

    _ = dbg outcome
}

```
<!-- expect
dbg line 26: outcome = Err("boom")
-->

Without structured concurrency, the long sleep could keep running for
five seconds after the short task returned `Err`. Here the block returns
in about 10 ms because `try` sees the `Err`, then cancels and unwinds the
sibling.

Await order still matters. The block reacts when you await the task that
went wrong; it does not watch all tasks in the background. Swap the two
awaits above:

```nomi
_ = try Task.await(long) // blocks the full five seconds

n = try Task.await(short) // the Err has been sitting here the whole time
```

Now the same program takes five seconds instead of ten milliseconds. It
still returns `Err("boom")`; it just waited for the slower sibling first.

The same rule applies to runtime failures such as divide-by-zero: the
failure surfaces when that task is awaited. Await first whatever you most
want to hear from.

[`Task.await_all`](#running-a-batch) is different. It is watching a whole
batch whose results you have committed to collecting, so a failure
anywhere in the batch surfaces immediately.

## Running a batch

You can spawn one task per item:

```nomi
// Ten thousand items, ten thousand simultaneous database queries.
concurrent {
    handles =
        ids
        |> Iter.map(|id| Task.spawn(|| fetch_user(id)))
        |> Iter.to_list()

    handles
    |> Iter.map(|h| Task.await(h))
    |> Iter.to_list()
}
```

The tasks are cheap. The queries are not: ten thousand simultaneous
database calls will exhaust the connection pool.

`Task.spawn_all` spawns a bounded batch, and `Task.await_all` collects
the results in order:

```nomi-run
import {
    std/tasks.Task
}

fn active?(id: Int): Bool {
    id != 2
}

fn fetch_user(id: Int): String {
    "user-${Display.to_string(id)}"
}

fn main(): List<String> {
    results = concurrent {
        [3, 2, 1]
        |> Iter.filter(active?)
        |> Task.spawn_all(fetch_user, max_running: 8)
        |> Task.await_all()
    }

    dbg results
}

```
<!-- expect
dbg line 21: results = ["user-3", "user-1"]
-->

That means eight at a time no matter how many inputs you pass. The ten
thousand queries above would queue behind the bound instead of opening ten
thousand connections.

Results come back in spawn order, not finish order. Here that is `3` then
`1`, matching the filtered input.

**`max_running` is required.** For CPU-bound work, the core count is a
good default. For most concurrent work, the limit protects something
downstream: a pool, a queue, an API, or a single worker. Nomi asks for the
bound instead of guessing.

The bound belongs on the spawn, because by the time you await, the work is
already running. It is per batch rather than per block, because one block
may talk to more than one service:

```nomi
concurrent {
    users =
        ids
        |> Task.spawn_all(fetch_user, max_running: 8)
        |> Task.await_all()

    users
    |> Task.spawn_all(send_digest, max_running: 50)
    |> Task.await_all()
}
```

Eight database queries at a time, fifty API calls at a time. A single
block-wide limit could not express both.

If a body returns a `Result`, you get a list of them back. A returned
`Err` is a task that *completed* — it isn't a failure, so it doesn't
short-circuit the batch, and you decide what a partial success means:

```nomi
outcomes = concurrent {
    ids
    |> Task.spawn_all(fetch_user, max_running: 8)
    |> Task.await_all()
}

failures = Iter.filter(outcomes, |r| Result.err?(r))
```

The short-circuiting from [a moment ago](#errors-cancel-siblings) is for
tasks that *break*. Returned `Err` values travel in ordinary values;
failure and cancellation travel through task ownership.

### Choosing `max_running`

The number comes from whatever is at the other end, and what it will
tolerate.

| The work talks to | Reach for |
| --- | --- |
| A database | Your connection pool size, or under it — more just queues inside the driver |
| An API you own | Tens, and watch what the other side does |
| A rate-limited third party | The limit, with headroom for retries |
| One long-lived worker | `1` |
| Pure computation | The core count is finally the right answer |

When you do not know, start low. Too low costs latency you can measure and
raise; too high may cause errors in the system you are calling.

---

## Work that outlives the call

Some work shouldn't make the caller wait. A signup responds now and sends
the welcome email after. A request returns before its audit record is
written. A worker drains a queue for the life of the process.

A `concurrent` block can't express any of these: **it can't return until
every task inside it has finished.**

A `concurrent { ... }` block is an anonymous owner that ends with the
block. A named `Supervisor` is an owner with a longer lifetime: your
function can return while the work keeps running, and the supervisor
decides how shutdown and failures are handled.

Either way, the task is owned by something with a policy.

## Declaring your supervisors

Define one supervisor for each policy your app needs. Supervisors are
built while `boot` assembles the app value, and the usual place to keep
them is the app payload:

```nomi
import {
    std/channels.{Channel, Receiver, Sender}
    std/supervisors.{Supervisor}
    std/duration.Duration
}

struct Config {
    context: Context

    audit: Supervisor
    notify: Supervisor
    // Where background work reports failures nobody is awaiting.
    alerts: Channel<String>
}

fn boot(): Config {
    Config{
        context: Context.root(),
        audit: Supervisor.new(
            max_running: 4,
            shutdown_timeout: Duration.seconds(30),
        ),
        notify: Supervisor.new(
            max_running: 50,
            shutdown_timeout: Duration.milliseconds(200),
        ),
        alerts: Channel.buffered<String>(16),
    }
}
```

The important parts:

- **`max_running:` is required.** It is the number of tasks under this
  supervisor that may run at once. A supervisor with one long-lived worker
  uses `max_running: 1`; for anything else, see
  [choosing `max_running`](#choosing-max_running).
- **`shutdown_timeout:` is how long shutdown waits** for that
  supervisor's work when the program is stopping. Each has its own, and
  they're unrelated to each other. It defaults to five seconds, which is
  why most of the examples below don't mention it — set it when this
  supervisor's work is unusually slow (a large upload) or unusually
  disposable (a notification nobody will miss).
- **Supervisors are built during `boot`.** A supervisor is a program-wide
  bound, not a per-request object. If each request built its own
  `max_running: 8` supervisor, a thousand in-flight requests could run
  eight thousand tasks against the same database.

Built *during* `boot` does not mean written directly inside `boot`. A
constructor called by `boot` may build a supervisor, which is how a server
type owns its lifetime and restart behavior. Calling that constructor from
outside the boot path is a compile error.

Think in policies, not features. The `notify` supervisor owns all outbound
notifications, not one email or one request.

That advice is about fire-and-forget work, which shares. A long-lived
worker is the other case: its shutdown budget and restart policy describe
that one worker, so it usually owns a supervisor of its own and isn't
stored here at all — see [where mutable state lives](#where-mutable-state-lives).

A supervisor is a set of tasks that share one policy. Those tasks share
one `max_running` bound, one shutdown budget, one restart policy, and one
flush target. Put work together when it should be treated together.

## Spawning

Read supervisors straight off the app payload, by naming its type. There
is no need to pass them through parameters:

```nomi
fn handle_order(id: Int): Response {
    resp = build_response(id)

    _ = Supervisor.spawn(Config.audit, || record_audit(id, resp))
    _ = Supervisor.spawn(Config.notify, || try_notify(id))

    resp
}
```

`handle_order` returns immediately. The audit write and the notification
keep running, owned by their supervisors.

The `_ =` is there because spawning returns a `Task` either way. Here you
want neither to await it nor cancel it, so you discard it explicitly.

Here it is end to end: a supervisor declared in `boot`, two tasks spawned
into it, and a handler that returns before either task finishes:

```nomi-run
import {
    std/channels.{Channel, Receiver, Sender}
    std/supervisors.{Supervisor}
    std/duration.Duration
    std/timer
}

struct Config {
    context: Context

    audit: Supervisor
    // Where the audit tasks put what they wrote, so `main` can look.
    written: Channel<Int>
}

fn boot(): Config {
    Config{
        context: Context.root(),
        audit: Supervisor.new(max_running: 4),
        written: Channel.buffered<Int>(8),
    }
}

fn record_audit(id: Int): Unit {
    // Stands in for the slow part — a write, an API call.
    timer.sleep(Duration.milliseconds(50))

    case Sender.send(Config.written.sender, id) {
        Ok(_) -> Unit
        Err(_) -> Unit
    }
}

fn handle_order(id: Int): String {
    _ = Supervisor.spawn(Config.audit, || record_audit(id))
    "order ${Display.to_string(id)} accepted"
}

// Both ids are on the channel by the time this runs, but in whichever
// order the two tasks got there — so it sums them rather than reading
// them one at a time and claiming an order.
fn audited_total(): Int {
    first = Maybe.with_default(Receiver.receive(Config.written.receiver), 0)
    second = Maybe.with_default(Receiver.receive(Config.written.receiver), 0)
    first + second
}

fn main(): Int {
    dbg handle_order(1)
    dbg handle_order(2)

    // Only here, at the end, do we wait for the audit trail.
    _ = Supervisor.flush(Config.audit)
    dbg audited_total()
}
```
<!-- expect
dbg line 49: handle_order(1) = "order 1 accepted"
dbg line 50: handle_order(2) = "order 2 accepted"
dbg line 54: audited_total() = 3
-->

Both responses come back before either audit lands. The example does not
claim which audit finishes first: `max_running` bounds how many run at
once, not their order.

The API name follows the value it needs. Creating a supervisor, spawning
into one, or waiting for all its work needs the supervisor:
`Supervisor.new`, `Supervisor.spawn`, `Supervisor.flush`. Awaiting,
inspecting, or cancelling needs only the task handle: `Task.await`,
`Task.outcome`, `Task.cancel`. Those task operations behave the same for
block-owned and supervisor-owned tasks.

Consuming the handle is where the two owners differ: a block-owned task
**must** be awaited or inspected before its `concurrent` block can close,
while a supervisor-owned task needn't be, because the supervisor drains it.

For batches, `Supervisor.spawn_all` mirrors
[`Task.spawn_all`](#running-a-batch) — without `max_running`, since the
supervisor already carries one:

```nomi
_ = Supervisor.spawn_all(Config.notify, users, send_welcome)
```

Both owners in one function, each doing what the other can't:

```nomi
fn handle_order(id: Int): Response {
    // Need both values, right here, to build the response.
    priced = concurrent {
        validation = Task.spawn(|| validate(id))
        pricing = Task.spawn(|| price(id))
        Ok((try Task.await(validation), try Task.await(pricing)))
    }

    resp = case priced {
        Ok((v, p)) -> build_response(id, v, p)
        Err(e) -> error_response(id, e)
    }

    // Outlives the call — the response goes back without waiting.
    _ = Supervisor.spawn(Config.audit, || record_audit(id, resp))
    resp
}
```

## Shutdown

Two things start a shutdown: `main` returning, or the process getting a
`SIGINT`/`SIGTERM` — Ctrl-C, or a container stopping you.

Each supervisor gets its `shutdown_timeout:` to finish work already in
progress. Supervisors drain concurrently, so a 30 second budget and a
200 ms budget do not add together. Shutdown takes as long as the slowest
supervisor that still has work.

The budget is a grace period, not an estimate. In-flight work may finish.
When the budget runs out, whatever remains is **cancelled and abandoned**;
the runtime does not keep waiting. Nothing rewinds work a cancelled task
already performed, so a multi-step write can still be left halfway done.

A second Ctrl-C skips the drain entirely and exits now.

## Failures can't vanish quietly

The classic fire-and-forget bug looks like this:

```nomi
// Rejected at compile time — send_email returns Result, and nobody
// is around to look at it.
_ = Supervisor.spawn(Config.notify, || send_email(user.email, body))
```

Nobody is obliged to await a supervisor-owned task, so a returned
`Result` has nowhere reliable to go. Nomi rejects it:
**a body spawned into a supervisor must return `Unit`.**

`Unit` does not mean "does nothing." It means "nothing is left over." The
task still does the work, but it must decide what a failure means before
it returns:

```nomi
_ = Supervisor.spawn(Config.notify, || send_welcome(user))

fn send_welcome(user: User): Unit {
    // The work. This is what takes time; the task isn't done until it returns.
    sent = send_email(user.email, welcome_body(user))

    // The decision. Both branches end in Unit — nothing left unhandled.
    case sent {
        Ok(_) -> Unit
        Err(e) -> report(Config.alerts, "welcome to ${user.email} failed: ${e}")
    }
}

// Failures nobody is awaiting go on a channel, for a long-lived consumer
// to drain. A full alerts queue at shutdown is not worth crashing over.
fn report(ch: Channel<String>, msg: String): Unit {
    case Sender.send(ch.sender, msg) {
        Ok(_) -> Unit
        Err(_) -> Unit
    }
}
```

`Supervisor.spawn` returns when the task is enrolled, not when the body
finishes. `send_welcome` can still block on the mail server; it simply
leaves no unhandled value behind.

This is the same ownership idea as block-owned tasks: work must end with
its result handled somewhere.

## Cancelling one task

`Task.cancel` stops one task and leaves its siblings alone. It works on
any task handle, whether the task came from a block or a supervisor.

```nomi
fn start_conn(conn: Int): Task<Unit> {
    Supervisor.spawn(Config.connections, || serve_conn(conn))
}

fn main() {
    conn1 = start_conn(1)
    _conn2 = start_conn(2)

    // Connection 1 hangs up. Connection 2 is untouched.
    Task.cancel(conn1)
}
```

This is what a `concurrent` block cannot express by itself: stopping work
from a different place than the spawn.

A cancelled task stops promptly rather than running to completion, and
`Task.outcome` is how you see which happened:

```nomi-run
import {
    std/tasks.{Outcome, Task}
    std/duration.Duration
    std/timer
}

fn serve_conn(): Int {
    // Would take half a minute if left alone.
    timer.sleep(Duration.seconds(30))
    1
}

fn main(): Outcome<Int> {
    outcome = concurrent {
        conn = Task.spawn(|| serve_conn())
        Task.cancel(conn)
        // `outcome`, not `await` — inheriting a cancellation you asked for
        // makes no sense.
        Task.outcome(conn)
    }

    dbg outcome
}

```
<!-- expect
dbg line 22: outcome = Cancelled
-->

That returned immediately, even though the body was sleeping for thirty
seconds. Cancellation reaches blocking operations rather than waiting them
out.

Cancellation cascades. `serve_conn` might run its read and write loops in
a nested `concurrent` block; cancelling the task unwinds those too.

Some useful guarantees:

- **Dropping the handle leaks nothing.** The supervisor still owns the task
  and still drains it at shutdown. The handle is an *extra* way to stop
  work early, never the only one.
- **Cancelling twice is fine**, as is cancelling after shutdown already
  took the task. Both are no-ops.
- **You can't escape shutdown.** A task's cancellation is always derived
  from its owner's, so `SIGTERM` still reaches it no matter what.

It works on block-owned tasks too, so you can stop the losers in a race.
Cancelling does not excuse you from consuming the handle; every task still
gets an `await` or an `outcome`. A cancelled task comes back promptly
instead of making the block wait for the slowest task.

## A task's own context

Inside a task, `Config.context` is *that task's* context, not the one
`boot` built. Two things follow from that.

**You don't poll for cancellation.** The runtime checks at every loop
iteration, every function call, and every blocking operation, and unwinds
the task there — sooner than a check of your own could run, since your
check sits behind one of those same points. A worker loop is written as
though it runs forever:

```nomi
fn outbox_worker() {
    Iter.loop(|| {
        drain_pending(Config.outbox)
        timer.sleep(Duration.seconds(1))
    })
}
```

Cancelling it stops the sleep, unwinds the loop, and reports `Cancelled`
to whoever asks with `Task.outcome`. Cleanup belongs in `defer`, which
runs on the way out however the work ends, not in a branch you hope to
reach.

A `Context` carries a deadline and any typed values you put on it. It
does not carry a cancellation flag. `Context.deadline_remaining` answers
`Some(0)` once a deadline has passed, but that is information about the
clock, not the task. Being stopped is not a state a task reads; it is what
the runtime does to the task.

**Deriving from the context gives you a deadline:**

```nomi
fn send_welcome(user: User): Unit {
    // `deliver` is the body from the section above, under its own name: the
    // part that sends and turns the mail server's Result into Unit. All
    // `send_welcome` adds is a ceiling everything below inherits.
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(30))
    deliver(user)
}
```

There is no `timeout:` argument on the spawn. A context deadline is the
general way to bound blocking work, and the task stops on whichever comes
first: the timeout, `Task.cancel`, or shutdown.

Supervisor-owned work does not inherit the deadline of the call that
spawned it. Its owner is the supervisor, and the supervisor's budget is
its `shutdown_timeout:`. Inheriting the request deadline would defeat the
point of background work. Context values still come along, so a trace id
set on the request is still readable from the background task; only the
deadline is dropped. Block-owned tasks inherit the caller's deadline
because the caller is waiting for them.

An operation duration and a context deadline answer different questions.
The `Duration` passed to `timer.sleep` is how long that operation intends
to wait. The deadline is how long the caller will keep the whole activity
alive. Whichever is shorter wins.

Deadlines only tighten. A nested context cannot extend an outer deadline,
and rebinding to a context with no deadline cannot remove one already in
force. Usually you bound a region, not one call, and everything inside
inherits the ceiling without taking its own `timeout:` argument.

### A deadline aborts; it doesn't return

Exceeding a deadline is not a value you inspect. The work unwinds at its
next cancellation point and everything after it in that region is skipped.
In `main` that ends the program; in a task it ends the task.

That means this attempt to "try for a while, then fall back" **does not
work**:

```nomi
fn attempt(): String {
    // Broken: the `with` line covers the rest of the function, the observer
    // as well as the work, so the deadline is over `Task.outcome` too. The
    // observer unwinds along with the work and the `case` never runs.
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(1))

    outcome = concurrent {
        t = Task.spawn(|| slow())
        Task.outcome(t)
    }

    case outcome {
        .Completed(v) -> "got " + Display.to_string(v)
        .Cancelled -> "timed out, falling back"
        .Failed(_) -> "broke"
    }
}
```

**Put the deadline inside the task and watch from outside it.** Observe a
deadline from outside the region it covers:

```nomi-run
import {
    std/tasks.Task
    std/duration.Duration
    std/timer
}

struct Config {
    context: Context
}

fn boot(): Config {
    Config{context: Context.root()}
}

fn slow(): Int {
    timer.sleep(Duration.minutes(5))
    1
}

// The deadline covers `slow` and nothing else.
fn slow_bounded(): Int {
    with Config.context = Context.with_timeout(Config.context, Duration.milliseconds(50))
    slow()
}

fn main(): String {
    outcome = concurrent {
        t = Task.spawn(|| slow_bounded())
        Task.outcome(t)
    }

    // Reached because `Task.outcome` sits outside the deadline.
    result = case outcome {
        .Completed(v) -> "got " + Display.to_string(v)
        .Cancelled -> "timed out, falling back"
        .Failed(_) -> "broke"
    }

    dbg result
}
```
<!-- expect
dbg line 39: result = "timed out, falling back"
-->

Five minutes of sleeping resolves in fifty milliseconds, and the caller
gets an ordinary outcome value to branch on.

## When a task actually breaks

Everything above is about work ending in ways you asked for. Tasks can
also break: a bug, a bad FFI call, a divide by zero.

A task ends one of three ways:

| Outcome | Meaning |
| --- | --- |
| **Completed** | The body returned normally. |
| **Cancelled** | You asked it to stop — `Task.cancel`, or shutdown. |
| **Failed** | It broke. Nobody asked. |

`Task.await` gives you the value from a completed task. If the task was
cancelled or failed, `await` propagates that outcome to your owner.

One thing `await` leaves alone is an `Err` your body returned
deliberately. That's a `Completed` task carrying an `Err` value,
and `await` hands it straight back to you — propagating it is `try`'s job,
as in the [example above](#errors-cancel-siblings). The two travel by
different routes:

| What happened | Travels via | Through |
| --- | --- | --- |
| Body returned `Err(e)` | `try` | the value channel |
| Task `Failed` or `Cancelled` | `await` | the ownership chain |

You will not see a marker at the call site for the second route:
`Task.await` is typed `Task<Int> → Int`. It behaves like any other Nomi
call: it returns the promised value unless something breaks. A failing
task is in the same category as divide-by-zero, not the same category as
an `Err` your code chose to return.

When you'd rather look at the outcome than inherit it, ask for it
directly:

```nomi
case Task.outcome(worker) {
    .Completed(v) -> io.print("done: ${v}")
    .Cancelled -> io.print("we stopped it")
    .Failed(e) -> io.print("broke: ${e}")
}
```

`Task.await` is `Task.outcome` with the non-`Completed` cases propagated
for you. Use `outcome` when you cancelled the task yourself or want to
inspect background work.

If you `await` and do not catch a failure, it keeps travelling through
owners. A failure stops in one of three places:

| Where | How | When |
| --- | --- | --- |
| At the await | `Task.outcome(t)` instead of `Task.await(t)` | You want to inspect or recover right here |
| At a supervisor | its restart policy | Background work — restart it, or report it |
| At the root | nothing — the program exits with a diagnostic | The default for main-line work |

Programs still die. What changes is that the failure is seen by an owner:

- **Supervised work doesn't take the program down.** Its supervisor
  handles it — reporting, restarting, or giving up according to its
  policy.
- **An uncaught main-line failure ends the program**, as a reported exit
  rather than a panic dump.
- **You can catch it anywhere along the chain**, with `Task.outcome`.

This is useful because Nomi values are immutable. A task that dies halfway
through cannot leave shared memory in a half-written state; there is no
shared mutable memory to corrupt.

## Testing background work

Background work is hard to test: your test calls the code, the code
returns immediately, and the work happens elsewhere. Sleeping for a fixed
duration and hoping gives you a slow test that fails intermittently.

`Supervisor.flush` blocks until the work outstanding under a supervisor has
finished. The unbounded form has no useful value to inspect, so examples
usually bind it to `_`:

```nomi
test "signup sends a welcome email" {
    signup("ada@example.com")

    _ = Supervisor.flush(Config.notify)

    assert sent_to?("ada@example.com")
}
```

To cap it, hand it a `Wait`:

```nomi
case Supervisor.flush(Config.notify, Wait.UpTo(Duration.seconds(5))) {
    .Flushed -> report_sent()
    .TimedOut -> carry_on()
}
```

The bound is a **ceiling, not a duration to spend**. It returns when the
work is done, so a flush that usually takes 3 ms costs 3 ms. `TimedOut`
cancels nothing: the work carries on, and shutdown is still what
eventually bounds it. You stopped watching.

That is different from wrapping the call in a context deadline:

```nomi
// Exceeding this deadline aborts the rest of the block.
with Config.context = Context.with_timeout(Config.context, Duration.seconds(5))
_ = Supervisor.flush(Config.notify)
```

A deadline unwinds a whole region when it expires. `Wait.UpTo` asks one
question and answers it either way. Use a deadline when nothing past the
bound is worth doing; use `Wait.UpTo` when you want to know and then
decide.

In tests, prefer [`clock Clock.Virtual`](/testing/#virtual-time). It makes
waits free and reports work that can never finish as a deadlock.

Three things it does **not** do:

- **It doesn't cancel anything**, bounded or not. Shutdown cancels what is
  left when its budget expires; a flush never cancels, it only stops
  watching.
- **It doesn't only wait for work that existed when you called it.** If a
  task spawns more work under the same supervisor, that work is waited for
  too. Work that endlessly spawns work never goes quiet; `Wait.UpTo` is
  the answer if that's a risk.
- **It doesn't answer anything useful about a
  [permanent](#work-that-shouldnt-finish) supervisor.** Unbounded it never
  returns; a bound gets you out with `TimedOut`, but the answer is always
  the same, because work declared never to finish never flushes. To
  synchronise with a permanent worker, send it a request that wants a
  reply. Getting the answer proves everything queued before it has been
  handled.

Outside tests, the same call flushes at a checkpoint: make sure the
queued notifications actually went out before you report success.

## Restarting work that breaks

Supervisors do not restart anything by default: `Restart.Temporary`. Add a
restart policy when a failure should be handled by running the work again:

```nomi
fn boot(): Config {
    Config{
        context: Context.root(),
        // A long-lived worker: retry patiently through a long outage,
        // and if it still can't run, take the process down so we get
        // restarted clean.
        workers: Supervisor.new(
            max_running: 8,
            restart: Restart.Transient,
            backoff: Backoff.Exponential{
                max_restarts: 60,
                max_elapsed: Duration.hours(1),
            },
            on_give_up: GiveUp.Exit,
        ),
        // One-shot work: the built-in schedule, then give up and report it.
        notify: Supervisor.new(max_running: 50, restart: Restart.Transient),
    }
}
```

Work that **fails** gets restarted. Work that completed normally, or that
you cancelled, is left alone.

A supervisor without a `restart:` keeps the default and simply reports
failures.

### Work that shouldn't finish

Restart fires on failure. For one-shot work, a task that returns is done,
and re-running it would be a bug. A worker meant to run until stopped is
different: if it returns on its own, something went wrong.

Nothing can tell those two apart from the outside, so say which you have:

```nomi
workers: Supervisor.new(
    max_running: 8,
    restart: Restart.Permanent),
```

`Restart.Permanent` says the work has no normal end. A task that returns
has failed, so it is reported and restarted like any other failure.
Without it, the worker would stop while `boot` still lists it as running.

Being stopped isn't returning on your own, so shutdown stays clean: a
permanent task cancelled by the drain or by `Task.cancel` is cancelled,
never a failure.

### Restarts wait, and the wait grows

A task that breaks instantly and restarts instantly can spin, consume CPU,
and fill logs. Built-in restarts back off: the first retry waits a second,
each retry after that waits twice as long, and the delay levels off at a
minute. Randomness is added so identical tasks do not all retry at the
same instant.

The configurable parts are the bounds: `max_restarts` and `max_elapsed`.

If a task runs for longer than the ceiling and *then* fails, it counts as
recovered: the next failure starts over from the first delay with a full
count and a fresh clock, so a worker that fails once a month doesn't
inherit last month's backoff. And if shutdown starts while a task is
waiting out its backoff, it isn't restarted.

### When it gives up

The runtime stops trying when either bound runs out: `max_restarts`
attempts, or `max_elapsed` spent trying, whichever comes first. Both have
defaults, ten restarts and fifteen minutes, so setting only `restart:` uses
the built-in schedule. What happens next is
`on_give_up`:

- **`GiveUp.Report`** (the default) — report the failure and carry on.
  The rest of the supervisor's work keeps running. Suitable for the welcome
  email.
- **`GiveUp.Exit`** — end the process. Every supervisor is drained on the
  way out, then the program exits non-zero. Suitable for the outbox drainer,
  where continuing without a working queue drain isn't useful.

**`Exit` exits; it does not restart.** Nomi does not restart itself. The
process dies, and whatever runs it decides whether to bring it back:
systemd's `Restart=`, a Kubernetes `restartPolicy`, `docker run --restart`,
or something similar. A new process runs `boot` again, rebuilding
connections, app state, and supervisors from scratch.

That makes `Exit` a poor fit when nothing is supervising the process: a
desktop app, an embedded runtime, or anything else without a service
manager. Use `GiveUp.Report` and your own monitoring there.

`max_restarts` counts **restarts, not runs**, so
`Backoff.Exponential{max_restarts: 3, max_elapsed: Duration.minutes(5)}` sends that email at most four
times: once, then three retries, waiting 1s, 2s, and 4s in between.

Choose `max_restarts` and `max_elapsed` against the outage you want to
survive. They are two ways to stop, and whichever comes first wins. The
worker above tolerates roughly an hour of database downtime; three retries
would give up much sooner.

A task can retry its own `Err` values in a loop, but a panic or runtime
error is not catchable inside the body; the restart policy is the first
code that sees it.

When you test restart schedules, use `clock Clock.Virtual`; the schedule
is still the production schedule, but the wait costs nothing.

## Where mutable state lives

Nomi has no mutable cell. Values are immutable, and app fields are fixed
once `boot` returns. When several callers need to share something that
*changes* — a rate limiter's tokens, a connection pool's free list, an
in-memory cache — give that state to one task and talk to it over
channels.

```nomi-run
import {
    std/channels.{Channel, Receiver, Sender}
    std/io
    std/supervisors.{Restart, Supervisor}
    std/testing.Clock
}

pub enum Request {
    Increment Int
    Get {reply: Channel<Int>}
}

pub struct Counter {
    inbox: Channel<Request>
}

fn start(): Counter {
    inbox = Channel.buffered<Request>(16)

    _ =
        Supervisor.new(max_running: 1, restart: Restart.Permanent)
        |> Supervisor.spawn(|| serve(inbox))

    Counter{inbox}
}

fn serve(inbox: Channel<Request>): Unit {
    _final = Iter.loop(|count = 0|
        case Receiver.receive(inbox.receiver) {
            Some(.Increment(n)) ->
                count + n

            Some(.Get{reply}) -> {
                _ = Sender.send(reply.sender, count)
                count
            }

            None ->
                break count
        }
    )

    Unit
}

fn increment(counter: Counter, n: Int): Unit {
    case Sender.send(counter.inbox.sender, .Increment(n)) {
        Ok(_) -> Unit
        Err(_) -> Unit
    }
}

fn get(counter: Counter): Int {
    reply = Channel.buffered<Int>(1)
    _ = Sender.send(counter.inbox.sender, .Get{reply})

    case Receiver.receive(reply.receiver) {
        Some(v) -> v
        None -> 0
    }
}

struct Config {
    context: Context

    counter: Counter
}

fn boot(): Config {
    Config{context: Context.root(), counter: start()}
}

fn main() {
    increment(Config.counter, 2)
    io.print("count: ${get(Config.counter)}")
}

tests "a counter server" {
    // Nothing here sleeps, but the worker blocks on its inbox and the
    // supervisor drains at the end of every case — both instant under a
    // virtual clock, and repeatable, which is what you want when the thing
    // under test is a running server.
    clock Clock.Virtual

    boot boot()

    test "messages accumulate into state a caller can read back" {
        increment(Config.counter, 5)
        increment(Config.counter, 7)

        // `increment` returned the moment the message was queued, so nothing
        // has necessarily run yet. `get` round-trips through the worker, and
        // the inbox is FIFO, so both increments are applied by the time the
        // reply comes back.
        assert get(Config.counter) == 12
    }
}
```
<!-- expect
count: 2
-->

The state is `count`, an ordinary loop accumulator. It is private because
a binding inside a running task is not reachable from anywhere else. One
task drains the inbox, so requests are handled one at a time and there is
nothing to guard.

`start()` builds the supervisor during `boot`, so the server owns
its `Restart.Permanent` policy and callers only see `increment`
and `get`. They never see the channel or supervisor directly.

`increment` is a **tell**: it returns as soon as the message is queued.
`get` is an **ask**: the request carries a fresh reply channel, so two
callers in flight cannot take each other's answer. The ask also
synchronises with earlier tells, because the inbox is FIFO and one worker
drains it.

Every read is a round-trip: two channel operations and a task switch. For
read-mostly state, ask whether the state needs to be in-process and
mutable at all. A value you can recompute, or a row in a store you already
query, avoids the machinery entirely.

It earns its keep when several callers mutate shared state and
serializing the mutations is the point.

## What background work can't promise

Supervised work is **in-process**. If the machine dies between "response
sent" and "email sent", that email is gone. The shutdown budget buys a
graceful finish; it does not buy delivery.

For a welcome email that's fine. For a password reset or a receipt it
isn't. The fix is not a longer budget; it is not backgrounding the send at
all:

1. On the request's critical path, write a durable outbox row **in the
   same transaction** as the business change. Once that commits, the
   message is guaranteed — it's a row in a database, not a task in memory.
2. Run a long-lived worker in the background that drains the outbox:
   fetch pending, send, mark sent. If a send fails the row stays pending
   and gets retried. If the process dies the rows are still there when it
   comes back.

A supervisor still appears here — for the *worker*, which is genuinely
long-lived background work — but not for the send itself.

---

The next chapter — [Dates & Times](/dates-and-times/) — covers Nomi's
civil-time types: `Date`, `Time`, `DateTime`, `NaiveDateTime`, and the DST
handling that comes with them.

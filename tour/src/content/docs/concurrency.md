---
title: "Concurrency"
description: "Run work at the same time with `concurrent { ... }` and tasks, pass values over channels, and keep background work running under supervisors."
---

In Nomi, every task has an owner. The owner makes sure the task finishes, is
cancelled, or has its failure reported. You can't start a task that nothing
owns.

There are two kinds of owner. Pick by asking how long the work should live:

- If you need the result here, use a `concurrent { … }` block and
  `Task.spawn(fn -> …)`.
- If the work should keep running after this call returns, use
  `Supervisor.spawn(supervisor, fn -> …)`.

One function can use both. A request handler might compute its response in
a `concurrent` block, then give an audit write to a supervisor. The response
goes back without waiting for the audit.

Each task runs as a Go goroutine, so tasks are cheap to start. Channels work
much like Go channels. When you limit how many tasks run at once, the reason
is usually what the work talks to: a database pool, an API's rate limit, a
single worker, or the number of CPU cores.

## `concurrent`, `Task.spawn`, `Task.await`

Start with a block:

```nomi-run
import std/tasks.Task

fn double(n: Int) -> Int {
    n * 2
}

fn main() {
    total = concurrent {
        a = Task.spawn(fn -> double(10))
        b = Task.spawn(fn -> double(20))
        c = Task.spawn(fn -> double(30))
        Task.await(a) + Task.await(b) + Task.await(c)
    }

    dbg total
}

```
<!-- expect
dbg line 15: total = 120
-->

- `concurrent { ... }` owns every task started inside it. Its value is its
  last expression.
- [`Task.spawn(fn -> expr)`](/reference/tasks/#taskspawn) starts the lambda as
  a task and returns a handle to it, a `Task<T>`, right away.
- [`Task.await(task)`](/reference/tasks/#taskawait) waits for the task and
  returns its result.

The compiler checks two rules. You must use each task's handle before the
block ends, and the handle can't leave the block. `Task.await` is one way to
use a handle. [`Task.outcome`](#when-a-task-fails), later on this page, is the
other.

`Task.spawn` doesn't name an owner, because the enclosing `concurrent` block
is the only one it could be. Supervisors, later on this page, are owners
with names, and they live longer.

## Channels

A [`Channel`](/reference/channels/) passes values between tasks. It has two
halves. Producers send on the [`Sender`](/reference/channels/#type-sender).
A consumer receives on the [`Receiver`](/reference/channels/#type-receiver)
until the channel is closed.

```nomi-run
import {
    std/channels.{Channel, Receiver, Sender}
    std/io
    std/tasks.Task
}

fn produce(out: Sender<Int>, n: Int) -> Unit {
    case Sender.send(out, n) {
        Ok(_) -> Unit
        Err(_) -> io.print("send failed")
    }
}

fn consume(inbox: Receiver<Int>) -> Int {
    Iter.loop(fn sum = 0 -> {
        case Receiver.receive(inbox) {
            Some(v) -> sum + v
            None -> break sum
        }
    })
}

fn main() {
    ch = Channel.buffered<Int>(4)

    total = concurrent {
        p1 = Task.spawn(fn -> produce(ch.sender, 10))
        p2 = Task.spawn(fn -> produce(ch.sender, 20))
        p3 = Task.spawn(fn -> produce(ch.sender, 30))
        cons = Task.spawn(fn -> consume(ch.receiver))

        // Wait for the producers, then close so the consumer's loop ends on None.
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

[`Receiver.receive`](/reference/channels/#receiverreceive) waits until a
value arrives and returns `Some(v)`. Once the channel is closed and empty,
it returns `None` instead. That `None` ends the consumer's loop, so the
example closes the sender only after all three producers have finished.

`consume` gets only the `Receiver<Int>`, so it can't send or close the
channel. Closing is done with
[`Sender.close`](/reference/channels/#senderclose). Closing means "nothing
more is coming", and only the sending side can promise that.

There are two ways to make a channel:

- [`Channel.buffered<T>(n)`](/reference/channels/#channelbuffered) holds up
  to `n` values. A producer can get `n` values ahead of its consumer before
  `send` waits. Choose `n` by how far ahead is useful and how much you are
  willing to hold in memory.
- [`Channel.unbuffered<T>()`](/reference/channels/#channelunbuffered) holds
  nothing. Each `send` waits until another task receives the value.

With an unbuffered channel, the producer runs only as fast as the consumer.
That makes deadlock easy: a task that sends and then receives on the same
channel waits forever. Use `buffered` by default. Use `unbuffered` when you
want the sender to wait for the hand-off.

## Errors cancel siblings

Suppose one task in a block returns an `Err`. When a `?` sees that `Err`
from [`Task.await`](/reference/tasks/#taskawait), the block stops. It
cancels the other tasks, waits for them to stop, and returns the error:

```nomi-run
import {
    std/duration.Duration
    std/tasks.Task
    std/timer
}

fn short_fail() -> Result<Int, String> {
    timer.sleep(Duration.milliseconds(10))
    Err("boom")
}

fn long_sleep() -> Result<Int, String> {
    timer.sleep(Duration.seconds(5))
    Ok(0)
}

fn main() {
    outcome = concurrent {
        short = Task.spawn(fn -> short_fail())
        long = Task.spawn(fn -> long_sleep())
        n = Task.await(short)?
        _ = Task.await(long)?
        Ok(n)
    }

    dbg outcome
}

```
<!-- expect
dbg line 26: outcome = Err("boom")
-->

The block returns after about 10 ms. The five-second sleep is cancelled; it
doesn't keep running after the error.

The order of the awaits matters. The block only notices a problem when you
await the task that has it. It doesn't watch all its tasks in the
background. Swap the two awaits above:

```nomi
_ = Task.await(long)? // blocks the full five seconds

n = Task.await(short)? // the Err has been sitting here the whole time
```

Now the program takes five seconds. It still returns `Err("boom")`, but only
after waiting for the slow task.

A runtime failure, such as dividing by zero, works the same way. You find
out about it when you await that task. So await first the task you most want
to hear from.

[`Task.await_all`](#running-a-batch) is different. It waits for a whole
batch, so a failure in any task of the batch shows up right away.

## Running a batch

You could start one task per item:

```nomi
// Ten thousand items, ten thousand simultaneous database queries.
concurrent {
    handles =
        ids
        |> Iter.map(fn id -> Task.spawn(fn -> fetch_user(id)))
        |> Iter.to_list()

    handles
    |> Iter.map(&Task.await(&1))
    |> Iter.to_list()
}
```

The tasks are cheap, but the queries are not. Ten thousand database queries
at once will use up the connection pool.

`Task.spawn_all` starts a batch with a limit on how many run at once.
`Task.await_all` collects the results in order:

```nomi-run
// FILE: main.nomi
import {
    std/fs
    std/io
    std/tasks.Task
}

// How many words a file holds.
fn words(path: String) -> Result<Int, io.Error> {
    path
    |> fs.read_text()?
    |> String.words()
    |> Iter.count()
    |> Ok()
}

fn main() {
    counts = concurrent {
        ["b.txt", "a.txt", "missing.txt"]
        |> Task.spawn_all(words, max_running: 8)
        |> Task.await_all()
    }

    dbg counts
}

// FILE: a.txt
the cat sat
on the mat

// FILE: b.txt
a dog
```
<!-- expect
dbg line 23: counts = [Ok(2), Ok(6), Err(#NotFound{path: "missing.txt"})]
-->

At most eight run at a time, however many inputs you pass. With ten
thousand ids, the rest wait their turn instead of opening ten thousand
connections.

Results come back in the order the tasks were started, not the order they
finished: `b.txt`, `a.txt`, then `missing.txt`, the order of the input.
`words` returns a `Result`, so the file that isn't there is an `Err` in the
list, and the other two still finish.

`max_running` is required. For work that only uses the CPU, the number of
cores is a good choice. For most other work, the limit protects something
the tasks talk to: a pool, a queue, an API, or a single worker. If you don't
know, start low; a limit that is too low is only slower, and you can measure
it and raise it.

The limit goes on the spawn, because by the time you await, the work is
already running. Each batch has its own limit, because one block may talk to
more than one service:

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

This runs eight database queries at a time, then fifty API calls at a time.
One limit for the whole block couldn't do both.

A function that returns a `Result` gives a list of `Result`s, as `words`
did. A task that returns `Err` still finished normally, so it doesn't stop
the batch. You decide what to do when some succeed and some don't:

```nomi
outcomes = concurrent {
    ids
    |> Task.spawn_all(fetch_user, max_running: 8)
    |> Task.await_all()
}

failures = Iter.filter(outcomes, Result.is_err)
```

The stopping in [Errors cancel siblings](#errors-cancel-siblings) happens
because a `?` sees an `Err`, or because a task breaks. Here, nothing uses
`?` on the results, so each `Err` is a value in the list.

---

## Work that outlives the call

Some work shouldn't make the caller wait. A signup responds right away and
sends the welcome email afterwards. A request returns before its audit
record is written. A worker processes a queue for as long as the program
runs.

A `concurrent` block can't do any of these, because it doesn't return until
every task inside it has finished.

A `Supervisor` is an owner that lives longer. Your function can return while
the supervisor's tasks keep running. The supervisor decides what happens
when a task fails and when the program shuts down.

## Declaring your supervisors

Make one supervisor for each set of rules your app needs. Build them in
`boot`, and keep them in the app struct:

```nomi
import {
    std/channels.Channel
    std/duration.Duration
    std/supervisors.Supervisor
}

struct Config {
    context: Context

    audit: Supervisor
    notify: Supervisor
    // Where background work reports failures nobody is awaiting.
    alerts: Channel<String>
}

fn boot() -> Result<Config, String> {
    Ok(#Config{
        context: Context.root(),
        audit: Supervisor.new(max_running: 4, shutdown_timeout: Duration.seconds(30)),
        notify: Supervisor.new(max_running: 50, shutdown_timeout: Duration.milliseconds(200)),
        alerts: Channel.buffered<String>(16),
    })
}
```

- `max_running:` is required. It is how many of this supervisor's tasks can
  run at once. A supervisor for one long-lived worker uses
  `max_running: 1`. For anything else, choose it as for
  [a batch](#running-a-batch).
- `shutdown_timeout:` is how long the program waits for this supervisor's
  work when it shuts down. Each supervisor has its own. The default is five
  seconds, so most examples below leave it out. Set it when the work is
  slow, like a large upload, or when it doesn't matter much if it is lost,
  like a notification.
- You build supervisors during `boot`. A supervisor is a limit for the whole
  program. If each request built its own `max_running: 8` supervisor, a
  thousand requests at once could run eight thousand tasks against the same
  database.

"During `boot`" includes functions that `boot` calls. A server type can have
a constructor that builds its own supervisor, and `boot` calls it. Calling
that constructor from anywhere outside `boot` is a compile error.

Group work by how it should be handled, not by feature. The `notify`
supervisor handles all outgoing notifications, not one email or one request.
Its tasks share one `max_running` limit, one shutdown timeout, one restart
rule, and one `Supervisor.flush`.

That advice is for short tasks you start and don't wait for. A long-lived
worker is different. Its shutdown timeout and restart rule are about that
one worker, so it usually builds its own supervisor and isn't stored in the
app struct. See [Where mutable state lives](#where-mutable-state-lives).

## Spawning

Read a supervisor from the app struct by its type name. You don't need to
pass it as a parameter:

```nomi
fn handle_order(id: Int) -> Response {
    resp = build_response(id)

    _ = Supervisor.spawn(Config.audit, fn -> record_audit(id, resp))
    _ = Supervisor.spawn(Config.notify, fn -> try_notify(id))

    resp
}
```

`handle_order` returns right away. The audit write and the notification keep
running under their supervisors.

`Supervisor.spawn` returns a `Task`. This code doesn't want to await or
cancel either task, so it throws the handle away with `_ =`.

Here is a complete program. It builds a supervisor in `boot`, starts two
tasks under it, and the handler returns before either task finishes.
[`Supervisor.flush`](/reference/supervisors/#supervisorflush) waits until
every task under the supervisor has finished:

```nomi-run
import {
    std/channels.{Channel, Receiver, Sender}
    std/duration.Duration
    std/supervisors.Supervisor
    std/timer
}

struct Config {
    context: Context

    audit: Supervisor
    // Where the audit tasks put what they wrote, so `main` can look.
    written: Channel<Int>
}

fn boot() -> Result<Config, String> {
    Ok(#Config{
        context: Context.root(),
        audit: Supervisor.new(max_running: 4),
        written: Channel.buffered<Int>(8),
    })
}

fn record_audit(id: Int) -> Unit {
    // Stands in for the slow part: a write or an API call.
    timer.sleep(Duration.milliseconds(50))
    _ = Sender.send(Config.written.sender, id)
}

fn handle_order(id: Int) -> String {
    _ = Supervisor.spawn(Config.audit, fn -> record_audit(id))
    "order ${id} accepted"
}

// The two ids arrive in either order, so this sums them.
fn audited_total() -> Int {
    first = Receiver.receive(Config.written.receiver)!
    second = Receiver.receive(Config.written.receiver)!
    first + second
}

fn main() {
    dbg handle_order(1)
    dbg handle_order(2)

    // Wait for the audit trail only here, at the end.
    _ = Supervisor.flush(Config.audit)
    dbg audited_total()
}
```
<!-- expect
dbg line 43: handle_order(1) = "order 1 accepted"
dbg line 44: handle_order(2) = "order 2 accepted"
dbg line 48: audited_total() = 3
-->

Both responses come back before either audit is written. Either audit may
finish first. `max_running` limits how many run at once, not their order.

Each function lives on the type it needs. Creating a supervisor, starting a
task under it, and waiting for all its work need the supervisor:
`Supervisor.new`, `Supervisor.spawn`, `Supervisor.flush`. Awaiting,
inspecting and cancelling one task need only its handle: `Task.await`,
`Task.outcome`, `Task.cancel`. Those work the same whichever kind of owner
the task has.

The difference is whether you must use the handle. A task owned by a
`concurrent` block must be awaited or inspected before the block can end. A
task owned by a supervisor need not be, because the supervisor waits for it.

For batches, `Supervisor.spawn_all` works like
[`Task.spawn_all`](#running-a-batch). It takes no `max_running`, because the
supervisor already has one:

```nomi
_ = Supervisor.spawn_all(Config.notify, users, send_welcome)
```

This handler uses both kinds of owner:

```nomi
fn handle_order(id: Int) -> Response {
    // Need both values, right here, to build the response.
    priced = concurrent {
        validation = Task.spawn(fn -> validate(id))
        pricing = Task.spawn(fn -> price(id))
        Ok(#(Task.await(validation)?, Task.await(pricing)?))
    }

    resp = case priced {
        Ok(#(v, p)) -> build_response(id, v, p)
        Err(e) -> error_response(id, e)
    }

    // Outlives the call: the response goes back without waiting.
    _ = Supervisor.spawn(Config.audit, fn -> record_audit(id, resp))
    resp
}
```

## Shutdown

Shutdown starts when `main` returns, or when the process gets `SIGINT` or
`SIGTERM`. Pressing Ctrl-C sends `SIGINT`. A container being stopped sends
`SIGTERM`.

Each supervisor then gets its `shutdown_timeout:` to finish the work it
already started. The supervisors wait in parallel, so a 30-second timeout
and a 200 ms timeout don't add up. Shutdown takes as long as the
slowest supervisor that still has work.

Work that finishes within the timeout finishes normally. When the timeout
runs out, the remaining work is cancelled and the program stops waiting for
it. Anything a cancelled task already did stays done, so a write with
several steps can be left half finished.

A second Ctrl-C skips the wait and exits immediately.

## Failures can't vanish quietly

Here is a common bug with background work:

```nomi
// Rejected at compile time: send_email returns a Result, and nobody
// is around to look at it.
_ = Supervisor.spawn(Config.notify, fn -> send_email(user.email, body))
```

Nobody has to await a supervisor's task, so a `Result` it returns could be
lost without anyone seeing the error. Nomi rejects this. A function you start
under a supervisor must return `Unit`.

The task still does its work. Before it returns, it decides what to do if
that work failed:

```nomi
_ = Supervisor.spawn(Config.notify, fn -> send_welcome(user))

fn send_welcome(user: User) -> Unit {
    // The work. This is what takes time; the task isn't done until it returns.
    sent = send_email(user.email, welcome_body(user))

    // The decision. Both branches end in Unit, so nothing is left unhandled.
    case sent {
        Ok(_) -> Unit
        Err(e) -> report(Config.alerts, "welcome to ${user.email} failed: ${e}")
    }
}

// Failures nobody is awaiting go on a channel, for a long-lived consumer
// to drain. A full alerts queue at shutdown is not worth crashing over.
fn report(ch: Channel<String>, msg: String) -> Unit {
    _ = Sender.send(ch.sender, msg)
}
```

`Supervisor.spawn` returns as soon as the task is started. `send_welcome`
can still take a long time waiting on the mail server.

## Cancelling one task

`Task.cancel` stops one task and leaves the others running. It works on any
task handle, from a block or from a supervisor.

```nomi
fn start_conn(conn: Int) -> Task<Unit> {
    Supervisor.spawn(Config.connections, fn -> serve_conn(conn))
}

fn main() {
    conn1 = start_conn(1)
    _conn2 = start_conn(2)

    // Connection 1 hangs up. Connection 2 is untouched.
    Task.cancel(conn1)
}
```

This lets you stop work from somewhere other than where it started, which a
`concurrent` block alone can't do.

A cancelled task stops right away. `Task.outcome` tells you whether a task
finished or was cancelled:

```nomi-run
import {
    std/duration.Duration
    std/tasks.Task
    std/timer
}

fn serve_conn() -> Int {
    // Would take half a minute if left alone.
    timer.sleep(Duration.seconds(30))
    1
}

fn main() {
    outcome = concurrent {
        conn = Task.spawn(fn -> serve_conn())
        Task.cancel(conn)
        // `Task.await` on a cancelled task would cancel this block too;
        // `Task.outcome` reports it instead.
        Task.outcome(conn)
    }

    dbg outcome
}

```
<!-- expect
dbg line 22: outcome = Cancelled
-->

This returns immediately, even though the task was in a 30-second sleep.
Cancelling interrupts a task that is waiting.

Cancelling a task also cancels everything it started. If `serve_conn` ran
its read and write loops in its own `concurrent` block, cancelling the task
stops those loops too.

You can drop a supervised task's handle. The supervisor still owns the task
and still waits for it at shutdown; the handle only gives you a way to stop
the task early.

`Task.cancel` works on tasks in a `concurrent` block too. For example, when
several tasks race, you can stop the ones that lose. You still have to await
or inspect each handle, but a cancelled task returns quickly, so the block
doesn't wait for the slowest one.

## A task's own context

Inside a task, `Config.context` is that task's own context, not the one
`boot` built. This has two effects.

First, you don't need to check whether your task was cancelled. The runtime
checks for you at every loop iteration, every function call, and every
operation that waits, and stops the task there. Your own check would run
later than that anyway. So you write a worker loop as if it runs forever:

```nomi
fn outbox_worker() {
    Iter.loop(fn -> {
        drain_pending(Config.outbox)
        timer.sleep(Duration.seconds(1))
    })
}
```

Cancelling it interrupts the sleep, ends the loop, and makes `Task.outcome`
return `Cancelled`. Put cleanup in a `defer`, which runs however the work
ends.

A `Context` holds a deadline and any typed values you add to it. It has no
"cancelled" flag. `Context.deadline_remaining` returns a zero `Duration` once
the deadline has passed, but that only tells you about the clock. A task
never reads whether it was cancelled; the runtime stops it.

Second, you can give the work a deadline by deriving a new context:

```nomi
fn send_welcome(user: User) -> Unit {
    // `deliver` sends the email and reports a failure, as `send_welcome` did above.
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(30))
    deliver(user)
}
```

`Supervisor.spawn` has no `timeout:` argument. A context deadline is how you
limit how long work can take. The task stops at whichever comes first: the
deadline, `Task.cancel`, or shutdown.

A task under a supervisor doesn't get the deadline of the code that started
it. Its owner is the supervisor, and the supervisor's limit is its
`shutdown_timeout:`. If background work had the request's deadline, the
request's deadline would stop it, and the point of running it in the
background is to let it outlast the request. The context's values do carry
over. `Context.with_value(Config.context, TraceId("t-1"))` adds a value, and
`Context.value(Config.context, TraceId)` reads it, in the background task
too. Only the deadline is dropped.

A task in a `concurrent` block does get the caller's deadline, because the
caller is waiting for it.

A deadline can only get shorter: a context made inside it can't extend it.

### A deadline aborts; it doesn't return

When a deadline passes, you don't get a value back. The work stops at the
next point where the runtime checks, and the rest of the code under that
deadline is skipped. In `main`, the program ends. In a task, the task ends.

So this attempt to try for a while and then fall back does not work:

```nomi
fn attempt() -> String {
    // Broken: the deadline covers the `case` too, so the `case` never runs.
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(1))

    outcome = concurrent {
        t = Task.spawn(fn -> slow())
        Task.outcome(t)
    }

    case outcome {
        .Completed(v) -> "got ${v}"
        .Cancelled -> "timed out, falling back"
        .Failed(_) -> "broke"
    }
}
```

Instead, set the deadline inside the task, and check the result from
outside it:

```nomi-run
import {
    std/duration.Duration
    std/tasks.Task
    std/timer
}

struct Config {
    context: Context
}

fn boot() -> Result<Config, String> {
    Ok(#Config{context: Context.root()})
}

fn slow() -> Int {
    timer.sleep(Duration.minutes(5))
    1
}

// The deadline covers `slow` and nothing else.
fn slow_bounded() -> Int {
    with Config.context = Context.with_timeout(Config.context, Duration.milliseconds(50))
    slow()
}

fn main() {
    outcome = concurrent {
        t = Task.spawn(fn -> slow_bounded())
        Task.outcome(t)
    }

    // Reached because `Task.outcome` sits outside the deadline.
    result = case outcome {
        .Completed(v) -> "got ${v}"
        .Cancelled -> "timed out, falling back"
        .Failed(_) -> "broke"
    }

    dbg result
}
```
<!-- expect
dbg line 39: result = "timed out, falling back"
-->

The five-minute sleep ends after 50 milliseconds, and the caller gets an
ordinary value to `case` on.

## When a task fails

So far, tasks ended because they finished or because you stopped them. A
task can also break: a bug, a bad FFI call, a division by zero.

A task ends in one of three ways:

| Outcome | Meaning |
| --- | --- |
| Completed | The function returned normally. |
| Cancelled | Something stopped it: `Task.cancel`, or shutdown. |
| Failed | It broke. |

`Task.await` returns the value of a completed task. If the task was
cancelled or failed, `await` passes that on to the code that awaited it, as
if it had happened there.

A task that returns `Err` completed; `?` passes the `Err` on, as in
[Errors cancel siblings](#errors-cancel-siblings). A failed or cancelled
task is passed on by `Task.await` itself, up through owners until
`Task.outcome`, a supervisor or the top of the program stops it. Nothing at
the call site shows this. `Task.await` on a `Task<Int>` returns an `Int`.
A failed task is like a division by zero, not like an `Err` your code chose
to return.

To look at what happened instead of having it passed on, ask for the
outcome:

```nomi
case Task.outcome(worker) {
    .Completed(v) -> io.print("done: ${v}")
    .Cancelled -> io.print("we stopped it")
    .Failed(e) -> io.print("broke: ${e}")
}
```

`Task.await` is `Task.outcome` with the `Cancelled` and `Failed` cases passed
on for you. Use `outcome` when you cancelled the task yourself, or when you
want to check on background work.

So programs can still crash, but an owner always sees the failure:

- A failure under a supervisor doesn't stop the program. The supervisor
  reports it, restarts the task, or gives up, as you told it to.
- A failure that nothing catches ends the program with a report, not a
  crash dump.
- You can catch it at any owner along the way with `Task.outcome`.

Nomi values are immutable, so a task that dies halfway can't leave in-memory
data half written. Writes it made outside the program, to a file or a
database, stay made.

## Restarting work that breaks

By default a supervisor restarts nothing. That default is
`Restart.Temporary`, which only reports failures. Choose another restart
rule when a failed task should run again:

```nomi
fn boot() -> Result<Config, String> {
    Ok(#Config{
        context: Context.root(),
        // A long-lived worker: retry patiently through a long outage,
        // and if it still can't run, take the process down so we get
        // restarted clean.
        workers: Supervisor.new(
            max_running: 8,
            restart: Restart.Transient,
            backoff: #Backoff.Exponential{
                max_restarts: 60,
                max_elapsed: Duration.hours(1),
            },
            on_give_up: GiveUp.Exit,
        ),
        // One-shot work: the built-in schedule, then give up and report it.
        notify: Supervisor.new(max_running: 50, restart: Restart.Transient),
    })
}
```

`Restart.Transient` restarts a task that failed. A task that completed, or
that you cancelled, is not restarted.

### Work that shouldn't finish

A restart happens when a task fails. For one-off work, a task that returns
is done, and running it again would be a bug. A worker that should run until
it is stopped is different. If it returns by itself, something went wrong.

Nomi can't tell these apart, so you say which one you have:

```nomi
workers: Supervisor.new(
    max_running: 8,
    restart: Restart.Permanent,
),
```

`Restart.Permanent` means the work should never end on its own. If a task
returns, that counts as a failure, so it is reported and restarted. Without
it, a worker that returned would stay stopped and nothing would report it.

Being cancelled isn't returning on its own, so shutdown still works. When
shutdown or `Task.cancel` stops a permanent task, it is cancelled, not
failed.

### Restarts wait, and the wait grows

A task that fails at once and restarts at once could loop, use up the CPU,
and fill the logs. So restarts wait 1 second, then twice as long each time,
up to a minute. The
[`Backoff`](/reference/supervisors/#enum-backoff) reference has the details.

### When it gives up

The supervisor stops restarting a task when it reaches either limit:
`max_restarts` restarts, or `max_elapsed` time spent retrying. The defaults
are ten restarts and fifteen minutes, so if you set only `restart:`, you get
those. Then `on_give_up` decides what happens:

- `GiveUp.Report`, the default, reports the failure and carries on. The
  supervisor's other tasks keep running. This suits the welcome email.
- `GiveUp.Exit` ends the program. Every supervisor gets its shutdown
  timeout first, then the program exits with a non-zero status. This suits
  an outbox worker, where the program is no use if the queue isn't being
  processed.

`GiveUp.Exit` doesn't restart the program; whatever started the process
decides that. So `Exit` is a poor choice when nothing will restart the
process, such as a desktop app. Use `GiveUp.Report` there.

`max_restarts` counts restarts, not runs. With
`#Backoff.Exponential{max_restarts: 3, max_elapsed: Duration.minutes(5)}`,
the email is sent at most four times: the first try, then three retries
after waits of 1s, 2s and 4s.

A task can retry its own `Err` values in a loop. It can't catch a trap in
its own code. The restart rule is the first thing that sees those.

To test restart schedules, use [`clock Clock.Virtual`](#virtual-time). The
waits are the same as in production, but they take no real time.

## Testing background work

Background work is hard to test. Your test calls the code, the code returns
at once, and the work happens somewhere else. If you sleep for a while and
hope, the test is slow and sometimes fails.

`Supervisor.flush` waits until the supervisor's tasks have finished. Without
a limit it always returns `Flushed`, so the examples assign it to `_`:

```nomi
test "signup sends a welcome email" {
    signup("ada@example.com")

    _ = Supervisor.flush(Config.notify)

    assert was_sent_to("ada@example.com")
}
```

To limit how long it waits, pass a `Wait`:

```nomi
case Supervisor.flush(Config.notify, Wait.UpTo(Duration.seconds(5))) {
    .Flushed -> report_sent()
    .TimedOut -> carry_on()
}
```

The limit is the longest it will wait. It returns as soon as the work is
done, so a flush that usually takes 3 ms still takes 3 ms. `TimedOut`
doesn't cancel anything. The work carries on until it finishes or shutdown
stops it. You only stopped waiting for it.

A context deadline around the call is different:

```nomi
// Exceeding this deadline aborts the rest of the block.
with Config.context = Context.with_timeout(Config.context, Duration.seconds(5))

_ = Supervisor.flush(Config.notify)
```

When a deadline passes, it stops all the code under it. `Wait.UpTo` gives
you an answer either way. Use a deadline when there's nothing worth doing
after the limit. Use `Wait.UpTo` when you want to find out and then decide.

Outside tests, you can call flush at a checkpoint, for example to make sure
the queued notifications went out before you report success.

### Virtual time

A test that waits is slow, and on a busy machine it may fail by accident.
Add `clock Clock.Virtual` to a group to avoid both. Its tests run under a fake
clock. Whenever every task in the test is waiting, the clock jumps forward,
so waiting takes no real time.

```nomi-run
import {
    std/duration.Duration
    std/testing.Clock
    std/timer
}

group "retry policy" {
    clock Clock.Virtual

    test "gives up after backing off" {
        timer.sleep(Duration.minutes(5))
        assert True
    }
}
```

That test finishes at once. It also takes the same path every time, because
the clock only moves when the program is waiting.

`clock` takes an ordinary expression naming a
[`testing.Clock`](/reference/testing/) variant, so `Clock` has to be imported,
as above. If you import `std/testing` instead, write
`clock testing.Clock.Virtual`.

A group's `clock`, `boot` and `setup` lines can be in any order. They always
run clock first, then boot, then setup. `nomi fmt` moves them to the top of
the group in that order.

Tests outside a group with a `clock` line use the real clock.

Under the fake clock, a test that can never finish is reported as a deadlock.
It doesn't hang the run. So you can safely test calls that wait with no
timeout, such as `Supervisor.flush`.

The fake clock is off by default because it changes how a test behaves:

- Waiting on something outside the program, such as reading input or a real
  network call, never lets the clock move. The test is reported as deadlocked.
- Background work still running when a test ends is an error. It does not
  carry on into the next test.
- `Instant.now` starts at midnight UTC on 1 January 2000.

The first two usually point to a problem in the test. Still, they change
behaviour, so each group opts in.

## Where mutable state lives

Nomi has no variable you can change. Values are immutable, and app fields
can't change once `boot` returns. Sometimes several callers need to share
something that changes: a rate limiter's tokens, a connection pool's free
list, an in-memory cache. Give that state to one task, and talk to the task
over channels.

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

fn start() -> Counter {
    inbox = Channel.buffered<Request>(16)

    _ =
        Supervisor.new(max_running: 1, restart: Restart.Permanent)
        |> Supervisor.spawn(fn -> serve(inbox))

    #Counter{inbox}
}

fn serve(inbox: Channel<Request>) -> Unit {
    _final = Iter.loop(fn count = 0 -> {
        case Receiver.receive(inbox.receiver) {
            Some(.Increment(n)) ->
                count + n

            Some(#.Get{reply}) -> {
                _ = Sender.send(reply.sender, count)
                count
            }

            None ->
                break count
        }
    })

    Unit
}

fn increment(counter: Counter, n: Int) -> Unit {
    _ = Sender.send(counter.inbox.sender, .Increment(n))
}

fn get(counter: Counter) -> Int {
    reply = Channel.buffered<Int>(1)
    _ = Sender.send(counter.inbox.sender, #.Get{reply})
    Receiver.receive(reply.receiver)!
}

struct Config {
    context: Context

    counter: Counter
}

fn boot() -> Result<Config, String> {
    Ok(#Config{context: Context.root(), counter: start()})
}

fn main() {
    increment(Config.counter, 2)
    io.print("count: ${get(Config.counter)}")
}

group "a counter server" {
    // Waits take no real time under a virtual clock.
    clock Clock.Virtual

    boot boot()

    test "messages accumulate into state a caller can read back" {
        increment(Config.counter, 5)
        increment(Config.counter, 7)
        assert get(Config.counter) == 12
    }
}
```
<!-- expect
count: 2
-->

The state is `count`, the loop's running total. Nothing outside the task can
reach it. One task reads the inbox, so it handles requests one at a time,
and no locks are needed.

`start()` builds the supervisor during `boot`, so the counter has its own
`Restart.Permanent` rule. Callers use `increment` and `get`. They never see
the channel or the supervisor.

`increment` returns as soon as its message is queued. It doesn't wait for an
answer. `get` sends a request that carries a new reply channel, then waits
for the answer on it. Each `get` has its own reply channel, so two callers
can't get each other's answers. The inbox delivers messages in the order
they were sent, and one task handles them, so `get` sees every `increment`
sent before it.

Each read costs two channel operations and a switch between tasks. If the
state is mostly read, ask whether it needs to live in the program and change
at all. A value you can compute again, or a row in a database you already
query, needs none of this.

This pattern is worth it when several callers change shared state and you
need those changes to happen one at a time.

## What background work can't promise

Supervised work runs inside your program's process. If the machine dies
after the response is sent but before the email is sent, that email is lost.
The shutdown timeout gives work time to finish. It doesn't guarantee that
the work happens.

That's fine for a welcome email. It isn't fine for a password reset or a
receipt. A longer timeout won't fix it. Instead, write the message as an
outbox row in the same database transaction as the change itself, and let a
long-lived worker under a supervisor send the pending rows and mark them
sent. If the process dies, the rows are still there when it starts again.
[Databases](/databases/#transactions) shows transactions.

---

The next chapter, [Dates & Times](/dates-and-times/), covers Nomi's types
for dates and times, such as `Date`, `Time`, `DateTime` and
`NaiveDateTime`, and how they handle daylight saving time.

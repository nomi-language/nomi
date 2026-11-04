---
title: "Concurrency"
description: "Run work at the same time with `concurrent { ... }` and tasks, pass values over channels, cancel a task, and give work a deadline."
---

In Nomi, every task has an owner. The owner makes sure the task finishes, is
cancelled, or has its failure reported. You can't start a task that nothing
owns.

There are two kinds of owner. Pick by asking how long the work should live:

- If you need the result here, use a `concurrent { … }` block and
  `Task.spawn(fn -> …)`.
- If the work should keep running after this call returns, use
  `Supervisor.spawn(supervisor, fn -> …)`, which
  [Background Work](/background-work/) covers.

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
import std::tasks.Task

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
is the only one it could be. Supervisors
([Background Work](/background-work/)) are owners with names, and they live
longer.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `Task.spawn` that can run outside every `concurrent` block is the
  error `spawn outside concurrent block`. A helper that spawns is fine when
  it is called from inside one.
- A handle that is never awaited is `Task bound to 't' is never awaited`,
  and one that leaves its block, returned or sent on a channel, is
  `Task<T> cannot escape its enclosing concurrent block`.
- `return`, `?`, `break` and `continue` in the block leave the block, as in
  a lambda, not the function around it.
- A task body is a lambda, so it can't `assert`. An `assert` in the block's
  own body needs the block's value to be a `Result<_, AssertionFailure>`.

Spec: [`concurrent` and tasks](/spec/concurrency/#concurrent-and-tasks),
[Static guarantees](/spec/concurrency/#static-guarantees).

</div>

## Channels

A [`Channel`](/reference/channels/) passes values between tasks. It has two
halves. Producers send on the [`Sender`](/reference/channels/#type-sender).
A consumer receives on the [`Receiver`](/reference/channels/#type-receiver)
until the channel is closed.

```nomi-run
import {
    std::channels.{Channel, Receiver, Sender}
    std::io
    std::tasks.Task
}

fn produce(out: Sender<Int>, n: Int) -> Unit {
    case Sender.send(out, n) {
        Ok(_) -> Unit
        Err(_) -> io.print("send failed")
    }
}

fn consume(inbox: Receiver<Int>) -> Int {
    Iter.loop(0, fn sum -> {
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

### Waiting on more than one channel

[`Channel.select`](/reference/channels/#channelselect) waits on several
channels, or a timer, and runs the handler of whichever arm finishes first.
Each arm's handler turns its own outcome into the one result:

```nomi-run
import {
    std::channels.{Arm, Channel, Sender}
    std::duration.Duration
    std::tasks.Task
}

fn main() {
    replies = Channel.buffered<String>(1)
    failures = Channel.buffered<Int>(1)

    answer = concurrent {
        worker = Task.spawn(fn -> Sender.send(failures.sender, 503))
        _ = Task.await(worker)

        Channel.select([
            Arm.receive(replies.receiver, fn reply -> Maybe.with_default(reply, "closed")),
            Arm.receive(failures.receiver, fn code -> "failed: ${Maybe.with_default(code, 0)}"),
            Arm.after(Duration.seconds(1), fn -> "timed out"),
        ])
    }

    dbg answer
}
```
<!-- expect
dbg line 22: answer = "failed: 503"
-->

When several arms are ready at once, the first one listed wins. For one
channel and a timeout,
[`Receiver.receive_within`](/reference/channels/#receiverreceive_within)
answers `Ok(Some(v))`, `Ok(None)` when closed, or `Err(TimedOut)`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- `Sender.send` on a closed channel returns `Err(ChannelClosed)`.
  `Receiver.receive` keeps returning values already sent after the close,
  and `None` once they are gone.
- There is no `Receiver.close`. Only the sending side closes.
- `Channel.buffered(0)` traps. A channel with no buffer is
  `Channel.unbuffered()`.
- A channel belongs to no block. It can be made in one block and used in a
  later one.
- A `send`, `receive` or `select` that is waiting when its task is
  cancelled stops like any other wait.
- Timers in `select` run on the clock in force, so under
  `clock Clock.Virtual` a test of a timeout takes no real time.

Spec: [Channels](/spec/concurrency/#channels).

</div>

## Errors cancel siblings

Suppose one task in a block returns an `Err`. When a `?` sees that `Err`
from [`Task.await`](/reference/tasks/#taskawait), the block stops. It
cancels the other tasks, waits for them to stop, and returns the error:

```nomi-run
import {
    std::duration.Duration
    std::tasks.Task
    std::timer
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

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A cancelled task doesn't see the cancellation as a value. It stops at its
  next safe point, running its deferred calls on the way out.
- Safe points are every function call, every loop round and every
  operation that waits, so pure computation stops too.
- A single call into Go isn't interrupted. The task stops at the first safe
  point after the call returns.
- A task that returns `Err` completed: its outcome is `Completed(Err(e))`.
  `Failed` means it trapped.

Spec: [Errors and cancellation](/spec/concurrency/#errors-and-cancellation),
[Safe points](/spec/concurrency/#safe-points).

</div>

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
    std::fs
    std::io
    std::tasks.Task
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
dbg line 23: counts = [Ok(2), Ok(6), Err(NotFound{path: "missing.txt"})]
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

## Cancelling one task

`Task.cancel` stops one task and leaves the others running. A cancelled task
stops right away. `Task.outcome` tells you whether a task finished or was
cancelled:

```nomi-run
import {
    std::duration.Duration
    std::tasks.Task
    std::timer
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
    // The deadline covers `deliver` and everything it calls.
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(30))
    deliver(user)
}
```

A task in a `concurrent` block gets the caller's deadline, because the
caller is waiting for it. The task stops at whichever comes first: the
deadline or `Task.cancel`.

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
    std::duration.Duration
    std::tasks.Task
    std::timer
}

struct Config {
    context: Context
}

fn boot() -> Result<Config, String> {
    Ok(Config{context: Context.root()})
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
  reports it, restarts the task, or gives up, as you told it to
  ([Background Work](/background-work/#restarting-work-that-breaks)).
- A failure that nothing catches ends the program with a report, not a
  crash dump.
- You can catch it at any owner along the way with `Task.outcome`.

Nomi values are immutable, so a task that dies halfway can't leave in-memory
data half written. Writes it made outside the program, to a file or a
database, stay made.

---

[Calling Go](/calling-go/) is next. It calls functions from Go packages.

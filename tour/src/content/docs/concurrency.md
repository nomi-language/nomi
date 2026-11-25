---
title: "Concurrency"
description: "Run work at the same time with `concurrent` blocks and tasks: batches, errors and cancellation, races and time limits, then channels for streams of events."
---

In Nomi, every task has an owner. The owner makes sure the task finishes, is
cancelled, or has its failure reported. You can't start a task that nothing
owns.

There are two kinds of owner. Pick by asking how long the work should live:

- If you need the result here, use a `concurrent` ... `end` block and
  `Task.spawn(fn -> …)`.
- If the work should keep running after this call returns, use
  `Supervisor.spawn(supervisor, fn -> …)`, which
  [Background Work](/background-work/) covers.

One function can use both. A request handler might compute its response in
a `concurrent` block, then give an audit write to a supervisor. The response
goes back without waiting for the audit.

A task is cheap to start, since it is not an operating system thread. When
you limit how many tasks run at once, the reason is usually what the work
talks to: a database pool, an API's rate limit, a single worker, or the
number of CPU cores.

Most concurrent code needs only the task functions on this page: run a few
things at once, run a batch, stop at the first error, take the first answer,
or give up after a wait. Channels come last. Several of those functions are
written in Nomi over channels, and you reach for one yourself for a stream
of events.

## `concurrent`, `Task.spawn`, `Task.await`

Start with a block:

```nomi-run
import std::tasks.Task

fn double(n: Int) -> Int
    n * 2
end

fn main()
    total =
        concurrent
            a = Task.spawn(fn -> double(10))
            b = Task.spawn(fn -> double(20))
            c = Task.spawn(fn -> double(30))
            Task.await(a) + Task.await(b) + Task.await(c)
        end

    dbg total
end
```
<!-- expect
dbg line 16: total = 120
-->

- `concurrent` ... `end` owns every task started inside it. Its value is its
  last expression.
- [`Task.spawn(fn -> expr)`](/reference/tasks/#taskspawn) starts the lambda as
  a task and returns a handle to it, a `Task<T>`, right away. A function
  that takes no arguments can be passed by name: `Task.spawn(refresh)`.
- [`Task.await(task)`](/reference/tasks/#taskawait) waits for the task and
  returns its result.

The compiler checks two rules. You must await or cancel each task before the
block ends, and the handle can't leave the block. `Task.await` is one way.
[`Task.outcome`](#when-a-task-fails) and
[`Task.cancel`](#cancelling-one-task), later on this page, are the others.

`Task.spawn` doesn't name an owner, because the enclosing `concurrent` block
is the only one it could be. Supervisors
([Background Work](/background-work/)) are owners with names, and they live
longer.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A `Task.spawn` that can run outside every `concurrent` block is the
  error `spawn outside concurrent block`. A helper that spawns is fine when
  it is called from inside one.
- Every path to the block's end must await or cancel each handle; an await in one branch of an `if` only, or in a lambda that captures
  the handle, does not count. A handle left unsettled is `Task bound to 't' is never awaited or cancelled`, and one that leaves its block,
  returned or sent on a channel, is `Task<T> cannot escape its enclosing concurrent block`.
- `return`, `?`, `break` and `continue` in the block leave the block, as in
  a lambda, not the function around it.
- A task body is a lambda, so it can't `assert`. An `assert` in the block's
  own body needs the block's value to be a `Result<_, AssertionFailure>`.

Spec: [`concurrent` and tasks](/spec/concurrency/#concurrent-and-tasks),
[Static guarantees](/spec/concurrency/#static-guarantees).

</div>

## Running a batch

You could start one task per item:

```nomi
// Ten thousand items, ten thousand simultaneous database queries.
concurrent
    handles =
        ids
        |> Iter.map(fn id -> Task.spawn(fn -> fetch_user(id)))
        |> Iter.to_list()

    handles
    |> Iter.map(Task.await)
    |> Iter.to_list()
end
```

The tasks are cheap, but the queries are not. Ten thousand database queries
at once will use up the connection pool.

[`Task.spawn_all`](/reference/tasks/#taskspawn_all) starts a batch with a
limit on how many run at once. `Task.await_all` collects the results in
order, and `Task.try_await_all` stops at the first `Err`:

```nomi-run
// FILE: main.nomi
import
    std::fs
    std::io
    std::tasks.Task
end

// How many words a file holds.
fn words(path: String) -> Result<Int, io.Error>
    path
    |> fs.read_text()?
    |> String.words()
    |> Iter.count()
    |> Ok()
end

fn main()
    paths = ["b.txt", "a.txt", "missing.txt"]

    counts =
        concurrent
            paths
            |> Task.spawn_all(words, max_running: 8)
            |> Task.await_all()
        end

    dbg counts

    all_or_nothing =
        concurrent
            paths
            |> Task.spawn_all(words, max_running: 8)
            |> Task.try_await_all()
        end

    dbg all_or_nothing
end

// FILE: a.txt
the cat sat
on the mat

// FILE: b.txt
a dog
```
<!-- expect
dbg line 26: counts = [Ok(2), Ok(6), Err(NotFound{path: "missing.txt"})]
dbg line 35: all_or_nothing = Err(NotFound{path: "missing.txt"})
-->

At most eight run at a time, however many inputs you pass. With ten
thousand ids, the rest wait their turn instead of opening ten thousand
connections.

`Task.await_all` returns the results in the order the tasks were started,
not the order they finished: `b.txt`, `a.txt`, then `missing.txt`. `words`
returns a `Result`, so the file that isn't there is an `Err` in the list, and
the other two still finish. Use it when you want every answer.

[`Task.try_await_all`](/reference/tasks/#tasktry_await_all) is for a batch
where one `Err` makes the rest useless. It returns `Ok` with every value in
order, or the first `Err` to arrive, in the order the tasks finish. When an
`Err` arrives, it cancels the tasks still running and waits for them to stop
before it returns.

`max_running` is required, and it is a `PositiveInt`: `max_running: 0` does
not compile. For work that only uses the CPU, the number of
cores is a good choice. For most other work, the limit protects something
the tasks talk to: a pool, a queue, an API, or a single worker. If you don't
know, start low; a limit that is too low is only slower, and you can measure
it and raise it.

The limit goes on the spawn, because by the time you await, the work is
already running. Each batch has its own limit, because one block may talk to
more than one service:

```nomi
concurrent
    users =
        ids
        |> Task.spawn_all(fetch_user, max_running: 8)
        |> Task.await_all()

    users
    |> Task.spawn_all(send_digest, max_running: 50)
    |> Task.await_all()
end
```

This runs eight database queries at a time, then fifty API calls at a time.
One limit for the whole block couldn't do both. For a source too long to start at once, [`Task.stream`](/reference/tasks/#taskstream) is the lazy form: a pipeline stage that runs at most `max_running` tasks, yields their results in order as they are read (or as they finish, with `ordered: False`), and needs no `concurrent` block of your own.

## Errors cancel siblings

Outside a batch, a `?` does the stopping. Suppose one task in a block returns
an `Err`. When a `?` sees that `Err` from
[`Task.await`](/reference/tasks/#taskawait), the block stops. It cancels the
other tasks, waits for them to stop, and returns the error:

```nomi-run
import
    std::duration.Duration
    std::tasks.Task
    std::timer
end

fn short_fail() -> Result<Int, String>
    timer.sleep(Duration.milliseconds(10))
    Err("boom")
end

fn long_sleep() -> Result<Int, String>
    timer.sleep(Duration.seconds(5))
    Ok(0)
end

fn main()
    outcome =
        concurrent
            short = Task.spawn(short_fail)
            long = Task.spawn(long_sleep)
            n = Task.await(short)?
            _ = Task.await(long)?
            Ok(n)
        end

    dbg outcome
end
```
<!-- expect
dbg line 27: outcome = Err("boom")
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

A runtime failure, such as a `!` on `None`, works the same way. You find
out about it when you await that task. So await first the task you most want
to hear from.

[`Task.await_all` and `Task.try_await_all`](#running-a-batch) are different.
They wait for a whole batch, so a failure in any task of the batch, or for
`try_await_all` an `Err`, shows up as soon as it happens.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- A cancelled task doesn't see the cancellation as a value. It stops at its
  next safe point, running its deferred calls on the way out.
- Safe points are every function call, every loop round and every
  operation that waits, so pure computation stops too.
- A call to a std function the runtime implements, such as `String.repeat`,
  isn't interrupted. The task stops at the first safe point after it returns.
- A task that returns `Err` completed: its outcome is `Completed(Err(e))`.
  `Failed` means it trapped.

Spec: [Errors and cancellation](/spec/concurrency/#errors-and-cancellation),
[Safe points](/spec/concurrency/#safe-points).

</div>

## Cancelling one task

`Task.cancel` stops one task and leaves the others running. The task stops
at its next call, loop round or wait. `Task.outcome` tells you whether a
task finished or was cancelled:

```nomi-run
import
    std::duration.Duration
    std::tasks.Task
    std::timer
end

fn serve_conn() -> Int
    // Would take half a minute if left alone.
    timer.sleep(Duration.seconds(30))
    1
end

fn main()
    outcome =
        concurrent
            conn = Task.spawn(serve_conn)
            Task.cancel(conn)
            // `Task.await` on a cancelled task would cancel this block too;
            // `Task.outcome` reports it instead.
            Task.outcome(conn)
        end

    dbg outcome
end
```
<!-- expect
dbg line 23: outcome = Cancelled
-->

This returns immediately, even though the task was in a 30-second sleep.
Cancelling interrupts a task that is waiting.

Cancelling a task also cancels everything it started. If `serve_conn` ran
its read and write loops in its own `concurrent` block, cancelling the task
stops those loops too.

## The first answer, and a time limit

[`Task.race`](/reference/tasks/#taskrace) runs several bodies at once and
returns the value of the first to finish. It cancels the others and waits
for them to stop. [`Task.within`](/reference/tasks/#taskwithin) runs one
body and returns `Ok` with its value, or `Err(TimedOut)` when the wait runs
out first, after cancelling it. Each owns its own tasks, so neither needs a
`concurrent` block around it:

```nomi-run
import
    std::channels.TimedOut
    std::duration.Duration
    std::tasks.Task
    std::testing.Clock
    std::timer
end

// Asks one mirror, which answers after its own delay.
fn ask(mirror: String, delay: Duration) -> String
    timer.sleep(delay)
    mirror
end

group "the first to answer"
    // Waits take no real time under a virtual clock.
    clock Clock.Virtual

    test "race answers with the fastest and cancels the rest"
        winner = Task.race([
            fn -> ask("eu", Duration.seconds(3)),
            fn -> ask("us", Duration.seconds(1)),
        ])

        assert winner == "us"
    end

    test "within gives up on an answer that takes too long"
        assert Task.within(Duration.seconds(2), fn -> ask("us", Duration.seconds(1))) == Ok("us")

        late = Task.within(Duration.seconds(2), fn -> ask("eu", Duration.seconds(3)))
        assert late == Err(TimedOut)
    end
end
```
<!-- expect
ok the first to answer / race answers with the fastest and cancels the rest
ok the first to answer / within gives up on an answer that takes too long
test result: ok. 2 passed, 0 failed
-->

The waits count on the clock in force. In `main` the same `Task.within`
waits two real seconds at most.

## A task's own context

Inside a task, the app's context field, `Config.context` in these examples,
is that task's own context, not the one `boot` built. This has two effects.

First, you don't need to check whether your task was cancelled. The runtime
checks for you at every loop iteration, every function call, and every
operation that waits, and stops the task there. Your own check would run
later than that anyway. So you write a worker as if it runs forever.
[`timer.every`](/reference/timer/#timerevery) runs a job, waits, and runs it
again:

```nomi
fn outbox_worker()
    timer.every(Duration.seconds(1), fn -> drain_pending(Config.outbox))
end
```

Cancelling it interrupts the wait, ends `timer.every`, and makes
`Task.outcome` return `Cancelled`. When the program starts shutting down,
`timer.every` returns as soon as the job in progress does. Put cleanup in a
`defer`, which runs however the work ends.

A `Context` holds a deadline and any typed values you add to it. It has no
"cancelled" flag. `Context.deadline_remaining` returns `Some` of a zero
`Duration` once the deadline has passed, but that only tells you about the
clock. A task
never reads whether it was cancelled; the runtime stops it.

Second, you can give the work a deadline by deriving a new context:

```nomi
fn send_welcome(user: User) -> Unit
    // The deadline covers `deliver` and everything it calls.
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(30))
    deliver(user)
end
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
fn attempt() -> String
    // Broken: the deadline covers the `case` too, so the `case` never runs.
    with Config.context = Context.with_timeout(Config.context, Duration.seconds(1))

    outcome =
        concurrent
            t = Task.spawn(slow)
            Task.outcome(t)
        end

    case outcome
        .Completed(v) -> "got ${v}"
        .Cancelled    -> "timed out, falling back"
        .Failed(_)    -> "broke"
    end
end
```

[`Task.within`](#the-first-answer-and-a-time-limit) is the one that returns:

```nomi
fn attempt() -> String
    case Task.within(Duration.seconds(1), slow)
        Ok(v)  -> "got ${v}"
        Err(_) -> "timed out, falling back"
    end
end
```

Use a deadline when there is nothing worth doing after the limit. When it
should cover a region of calls and you still want to find out, set it inside
a spawned task and read that task's `Task.outcome` from outside, which is
`Cancelled` when the deadline ran out.

## When a task fails

So far, tasks ended because they finished or because you stopped them. A
task can also break: a bug, a `todo` it reaches, an index past the end.

A task ends in one of three ways:

| Outcome | Meaning |
| --- | --- |
| Completed | The function returned normally. |
| Cancelled | Something stopped it: `Task.cancel`, a deadline, or shutdown. |
| Failed | It broke. |

`Task.await` returns the value of a completed task. If the task was
cancelled or failed, `await` passes that on to the code that awaited it, as
if it had happened there.

A task that returns `Err` completed; `?` passes the `Err` on, as in
[Errors cancel siblings](#errors-cancel-siblings). A failed or cancelled
task is passed on by `Task.await` itself, up through owners until
`Task.outcome`, a supervisor or the top of the program stops it. Nothing at
the call site shows this. `Task.await` on a `Task<Int>` returns an `Int`.
A failed task is like an index past the end, not like an `Err` your code chose
to return.

To look at what happened instead of having it passed on, ask for the
outcome:

```nomi
case Task.outcome(worker)
    .Completed(v) -> io.print("done: ${v}")
    .Cancelled    -> io.print("we stopped it")
    .Failed(e)    -> io.print("broke: ${e}")
end
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

## State that outlives a request

A `concurrent` block's tasks end with the block. State that several callers
share and change, such as a counter or a cache, lives in one long-lived task
instead, which handles one message at a time. From
[`std::actors`](/reference/actors/), an `Agent` holds a value and runs the
changes callers send it, and an `Actor` handles a message type of your own
when the messages mean different things.
[Where mutable state lives](/background-work/#where-mutable-state-lives)
teaches both.

## Channels

`Task.race`, `Task.try_await_all`, `timer.every` and std's agents and actors
are written over channels, and most programs need no channel of their own.
Use one for a stream of events: signals, file changes, messages from a
websocket, or values one task makes and another reads as they come. Use
`Channel.select` to wait on several different things at once.

A [`Channel`](/reference/channels/) passes values between tasks. It has
two halves. Producers send on the
[`Sender`](/reference/channels/#type-sender). A consumer receives on the
[`Receiver`](/reference/channels/#type-receiver) until the channel is
closed.

```nomi-run
import
    std::channels.{Channel, Receiver, Sender}
    std::io
    std::tasks.Task
end

fn produce(out: Sender<Int>, n: Int) -> Unit
    case Sender.send(out, n)
        Ok(_)  -> Unit
        Err(_) -> io.print("send failed")
    end
end

fn main()
    ch = Channel.buffered<Int>(4)

    total =
        concurrent
            p1 = Task.spawn(fn -> produce(ch.sender, 10))
            p2 = Task.spawn(fn -> produce(ch.sender, 20))
            p3 = Task.spawn(fn -> produce(ch.sender, 30))
            sum = Task.spawn(fn -> Receiver.iter(ch.receiver) |> Iter.sum())

            // Wait for the producers, then close so the sum's walk ends.
            Task.await(p1)
            Task.await(p2)
            Task.await(p3)
            Sender.close(ch.sender)
            Task.await(sum)
        end

    dbg total
end
```
<!-- expect
dbg line 32: total = 60
-->

[`Receiver.iter`](/reference/channels/#receiveriter) reads the values as an
`Iter`, waiting for each, until the channel is closed and empty. So the
summing task is a pipeline, and it ends once the sender is closed. The
example closes it only after all three producers have finished.

The summing task gets only the `Receiver<Int>`, so it can't send or close
the channel. Closing is done with
[`Sender.close`](/reference/channels/#senderclose). Closing means "nothing
more is coming", and only the sending side can promise that. Where std makes
the channel, it closes it for you: a `signals.receiver` closes when the
block that made it ends or the program shuts down, and a file watcher's
events close when it stops, so a walk over either ends by itself.

There are two ways to make a channel:

- [`Channel.buffered<T>(n)`](/reference/channels/#channelbuffered) holds up
  to `n` values. A producer can get `n` values ahead of its consumer before
  `send` waits. Choose `n` by how far ahead is useful and how much you are
  willing to hold in memory.
- [`Channel.unbuffered<T>()`](/reference/channels/#channelunbuffered) holds
  nothing. Each `send` waits until another task receives the value.

With an unbuffered channel, the producer runs only as fast as the consumer.
That makes deadlock easy: a task that sends and then receives on the same
channel waits forever, and once every task waits, the program stops with a
report of each wait. Use `buffered` by default, `unbuffered` for a hand-off.

### Waiting on more than one channel

[`Channel.select`](/reference/channels/#channelselect) waits on several
channels, or a timer, and runs the handler of whichever arm finishes first.
Each arm's handler turns its own outcome into the one result:

```nomi-run
import
    std::channels.{Arm, Channel, Sender}
    std::duration.Duration
    std::tasks.Task
end

fn main()
    replies = Channel.buffered<String>(1)
    failures = Channel.buffered<Int>(1)

    answer =
        concurrent
            worker = Task.spawn(fn -> Sender.send(failures.sender, 503))
            _ = Task.await(worker)

            Channel.select([
                Arm.receive(replies.receiver, fn reply -> Option.with_default(reply, "closed")),
                Arm.receive(failures.receiver, fn code ->
                    "failed: ${Option.with_default(code, 0)}"
                end),
                Arm.after(Duration.seconds(1), fn -> "timed out"),
            ])
        end

    dbg answer
end
```
<!-- expect
dbg line 25: answer = "failed: 503"
-->

When several arms are ready at once, the first one listed wins. For one
channel and a timeout,
[`Receiver.receive_within`](/reference/channels/#receiverreceive_within)
returns `Ok(Some(v))`, `Ok(None)` when closed, or `Err(TimedOut)`.

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- `Sender.send` on a closed channel returns `Err(ChannelClosed)`.
  `Receiver.receive` keeps returning values already sent after the close,
  and `None` once they are gone.
- There is no `Receiver.close`. Only the sending side closes.
- The capacity is a `PositiveInt`, so `Channel.buffered(0)` does not
  compile. A channel with no buffer is `Channel.unbuffered()`.
- A channel belongs to no block. It can be made in one block and used in a
  later one.
- A `send`, `receive` or `select` that is waiting when its task is
  cancelled stops like any other wait.
- Timers in `select` run on the clock in force, so under
  `clock Clock.Virtual` a test of a timeout takes no real time.

Spec: [Channels](/spec/concurrency/#channels).

</div>

[Generating Code](/generating-code/) is next. It shows how a tool writes
Nomi code from data, as sqlkit does in [Databases](/databases/).

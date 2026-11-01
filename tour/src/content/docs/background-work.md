---
title: "Background Work"
description: "Keep work running after the call that started it, under supervisors: declare them in boot, spawn into them, restart what fails, shut down cleanly, and test it."
---

[Concurrency](/concurrency/) showed tasks in a `concurrent` block, which
the caller waits for. Some work shouldn't make the caller wait. A signup
responds right away and sends the welcome email afterwards. A request
returns before its audit record is written. A worker processes a queue for
as long as the program runs.

A `concurrent` block can't do any of these, because it doesn't return until
every task inside it has finished.

A `Supervisor` is an owner that lives longer. Your function can return while
the supervisor's tasks keep running. The supervisor decides what happens
when a task fails and when the program shuts down.

Supervisors come from [`std/supervisors`](/reference/supervisors/), and
they live on the app value that `boot` builds
([App Fields, Defer & Context](/app-fields/)).

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
  [a batch](/concurrency/#running-a-batch).
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

<div class="tour-details">
<p class="tour-details-label">Fine print</p>

- `Supervisor.new` outside `boot` and the functions it calls is an error
  that explains the bound would be per call rather than per downstream.
- A supervised task's body must return `Unit`, since nobody has to await
  it. Its handle may be discarded with `_ =`.
- A supervised task may open its own `concurrent` block. The block owns the
  tasks it starts, and the supervisor owns the task.
- At shutdown, on `SIGINT` or `SIGTERM` or when `main` returns, every
  supervisor drains at once, each within its own `shutdown_timeout`. A
  second interrupt exits at once.

Spec: [Supervision](/spec/concurrency/#supervision),
[Flushing and shutdown](/spec/concurrency/#flushing-and-shutdown).

</div>

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
[`Task.spawn_all`](/concurrency/#running-a-batch). It takes no `max_running`, because the
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

## Stopping a task and giving it a deadline

`Task.cancel` stops a supervised task as it stops one in a `concurrent`
block ([Cancelling one task](/concurrency/#cancelling-one-task)). You can
drop a supervised task's handle. The supervisor still owns the task and
still waits for it at shutdown; the handle only gives you a way to stop
the task early.

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

### When restarts stop

A task that fails at once and restarts at once could loop, use up the CPU,
and fill the logs. So each restart waits longer than the one before, and the
supervisor gives up after a number of restarts or a length of time spent
retrying, whichever comes first. `Backoff.Exponential`'s `max_restarts` and
`max_elapsed` set those two limits. The
[`Backoff`](/reference/supervisors/#enum-backoff) reference has the delays
and the defaults. When the supervisor gives up, `on_give_up` decides what
happens:

- `GiveUp.Report`, the default, reports the failure and carries on. The
  supervisor's other tasks keep running. This suits the welcome email.
- `GiveUp.Exit` ends the program. Every supervisor gets its shutdown
  timeout first, then the program exits with a non-zero status. This suits
  an outbox worker, where the program is no use if the queue isn't being
  processed.

`GiveUp.Exit` doesn't restart the program; whatever started the process
decides that. So `Exit` is a poor choice when nothing will restart the
process, such as a desktop app. Use `GiveUp.Report` there.

A task can retry its own `Err` values in a loop. It can't catch a trap in
its own code. The restart rule is the first thing that sees those.

To test restart schedules, use [`clock Clock.Virtual`](/testing/#virtual-time). The
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
ok a counter server / messages accumulate into state a caller can read back
test result: ok. 1 passed, 0 failed
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

[Databases](/databases/) is next. It queries SQLite from Nomi, the
database a background worker often drains.

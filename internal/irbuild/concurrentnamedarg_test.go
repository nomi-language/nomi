package irbuild

import "testing"

// Named arguments to the concurrency functions, in any order, run as the
// same call written positionally (irintrinsicnamed.go): `Sender.send`,
// `Receiver.receive`, `Context.with_value` and `Context.value` (whose type
// witness stays a name), `Task.spawn`, `Task.await`, `Task.await_all`,
// `Supervisor.spawn` and `Supervisor.spawn_all`. Each declined with "a named
// argument". The operands run in written order, as any call's named
// arguments do: `value` prints before `sender`.
func TestConcurrentNamedArguments_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import {
    std/channels.{Channel, Receiver, Sender}
    std/io
    std/supervisors.Supervisor
    std/tasks.Task
}

type TraceId String

struct App {
    context: Context
    work: Supervisor
}

fn boot() -> App {
    App{context: Context.root(), work: Supervisor.new(1)}
}

fn noisy<T>(label: String, v: T) -> T {
    io.print(label)
    v
}

fn main() {
    ch = Channel.buffered<Int>(2)
    sent = Sender.send(value: noisy("value", 1), sender: noisy("sender", ch.sender))
    io.print(Result.ok?(sent))
    io.inspect(Receiver.receive(receiver: ch.receiver))
    c = Context.with_value(value: TraceId("t"), c: Context.root())
    io.inspect(Context.value(value_type: TraceId, c: c))
    n = concurrent {
        t = Task.spawn(body: fn -> 3)
        Task.await(task: t)
    }
    io.inspect(n)
    one = Supervisor.spawn(body: fn -> io.print("job"), supervisor: App.work)
    _ = Task.await_all(tasks: [one])
    hs = Supervisor.spawn_all(f: fn x: Int -> io.inspect(x), source: [1], supervisor: App.work)
    _ = Task.await_all(hs)
}
`, "value\nsender\nTrue\nSome(1)\nSome(TraceId(\"t\"))\n3\njob\n1\n")
}

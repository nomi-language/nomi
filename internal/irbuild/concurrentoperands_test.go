package irbuild

import "testing"

// `Task.spawn` and `Supervisor.spawn` take any function value of their
// type, not only a lambda literal: a named function and a bound lambda.
// `Supervisor.spawn` takes its supervisor from any operand, a struct field
// and a conditional included. `Task.spawn_all` and `Supervisor.spawn_all`
// take any Iter, as std declares: a Vector, a Set, a Range and a lazy Iter,
// each viewed as the sequence rt reads. Each declined before.
func TestConcurrentOperands_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import {
    std/io
    std/supervisors.Supervisor
    std/tasks.Task
}

struct App {
    context: Context
    work: Supervisor
}

struct Holder {
    sup: Supervisor
}

fn boot() -> App {
    App{context: Context.root(), work: Supervisor.new(1)}
}

fn seven() -> Int {
    7
}

fn job() -> Unit {
    io.print("job")
}

fn double(n: Int) -> Int {
    n * 2
}

fn main() {
    f = fn -> 8
    n = concurrent {
        a = Task.spawn(seven)
        b = Task.spawn(f)
        Task.await(a) + Task.await(b)
    }
    io.inspect(n)
    h = Holder{sup: App.work}
    _ = Task.await_all([Supervisor.spawn(h.sup, job)])
    _ = Task.await_all([Supervisor.spawn(if n > 0 { App.work } else { h.sup }, job)])
    xs = concurrent {
        from_vector = Task.spawn_all(#[1, 2], double, max_running: 2)
        from_set = Task.spawn_all(#{3}, double, max_running: 1)
        from_range = Task.spawn_all(1..=2, double, max_running: 1)
        from_iter = Task.spawn_all([4] |> Iter.map(fn x: Int -> x + 1), double, max_running: 1)
        [from_vector, from_set, from_range, from_iter] |> Iter.map(Task.await_all) |> Iter.to_list()
    }
    io.inspect(xs)
    _ = Task.await_all(Supervisor.spawn_all(App.work, #[1], fn x: Int -> io.inspect(x)))
    _ = Task.await_all(Supervisor.spawn_all(App.work, [2] |> Iter.map(fn x: Int -> x + 1), fn x: Int -> io.inspect(x)))
    _ = Task.await_all(Supervisor.spawn_all(App.work, 5..=5, fn x: Int -> io.inspect(x)))
}
`, "15\njob\njob\n[[2, 4], [6], [2, 4], [10]]\n1\n3\n5\n")
}

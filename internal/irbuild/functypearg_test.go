package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// A function type is a type argument like any other: an actor whose
// message is a function (`Actor<(Int) -> Int>`), a channel, sender, task,
// Maybe, Result, List or Map of functions lowers and runs, in boot, in main
// and in a test group's boot.

const funcTypeArgActorImports = `import
    std::actors.Actor
    std::channels.{Channel, Receiver, Sender}
    std::io
    std::supervisors.{Restart, Supervisor}
end
`

const funcTypeArgChannelImports = `import
    std::channels.{Channel, Receiver, Sender}
    std::io
end
`

// funcTypeArgRead casts a message that sends the actor's state back, and
// waits for it.
const funcTypeArgRead = `
fn read(actor: Actor<(Int) -> Int>) -> Maybe<Int>
    back = Channel.buffered<Int>(1)
    _ = Actor.cast(actor, fn n ->
        _ = Sender.send(back.sender, n)
        n
    end)
    Receiver.receive(back.receiver)
end
`

const funcTypeArgApp = `
pub struct App
    context: Context
    workers: Supervisor
    counter: Actor<(Int) -> Int>
end

pub fn boot() -> Result<App, String>
    workers = Supervisor.new(max_running: 4, restart: Restart.Transient)
    counter: Actor<(Int) -> Int> = Actor.start(workers, 0, fn s, f -> f(s))
    _ = Actor.cast(counter, fn n -> n + 1)
    Ok(App{context: Context.root(), workers:, counter:})
end
`

func TestIRFuncTypeArg_EveryHolderRuns(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"actor started in boot", funcTypeArgActorImports + funcTypeArgApp + funcTypeArgRead + `
fn main()
    _ = Actor.cast(App.counter, fn n -> n + 2)
    io.print("${read(App.counter)}")
end
`, "Some(3)\n"},
		{"actor started in main", funcTypeArgActorImports + funcTypeArgApp + funcTypeArgRead + `
fn main()
    local: Actor<(Int) -> Int> = Actor.start(App.workers, 10, fn s, f -> f(s))
    _ = Actor.cast(local, fn n -> n * 3)
    io.print("${read(local)}")
end
`, "Some(30)\n"},
		{"channel", funcTypeArgChannelImports + `
fn main()
    ch = Channel.buffered<(Int) -> Int>(2)
    _ = Sender.send(ch.sender, fn n -> n + 5)
    io.print("${Receiver.receive(ch.receiver) |> Maybe.map(fn f -> f(1))}")
end
`, "Some(6)\n"},
		{"channel in a struct field", funcTypeArgChannelImports + `
struct Box
    inbox: Channel<(Int) -> Int>
end

fn main()
    b = Box{inbox: Channel.buffered<(Int) -> Int>(1)}
    _ = Sender.send(b.inbox.sender, fn n -> n - 1)
    io.print("${Receiver.receive(b.inbox.receiver) |> Maybe.map(fn f -> f(10))}")
end
`, "Some(9)\n"},
		{"sender in a variant", funcTypeArgChannelImports + `
enum Msg
    Run (Int) -> Int
    Back {reply: Sender<(Int) -> Int>}
end

fn answer(m: Msg) -> Unit
    case m
        .Back{reply:} ->
            _ = Sender.send(reply, fn n -> n * 100)

        .Run(_) -> Unit
    end
end

fn main()
    replies = Channel.buffered<(Int) -> Int>(1)
    answer(Msg.Back{reply: replies.sender})
    io.print("${Receiver.receive(replies.receiver) |> Maybe.map(fn f -> f(2))}")
end
`, "Some(200)\n"},
		{"task", `import
    std::io
    std::tasks.Task
end
fn main()
    g = concurrent
        t = Task.spawn(fn -> fn n: Int -> n + 9)
        Task.await(t)
    end
    io.print("${g(1)}")
end
`, "10\n"},
		{"maybe, result, list and map", `import std::io
fn main()
    mf: Maybe<(Int) -> Int> = Some(fn n -> n + 7)
    rf: Result<(Int) -> Int, String> = Ok(fn n -> n + 8)
    lf: List<(Int) -> Int> = [fn n -> n + 1, fn n -> n * 2]
    mp: Map<String, (Int) -> Int> = {"inc" => fn n: Int -> n + 1}
    io.print("${mf |> Maybe.map(fn f -> f(1))}")
    io.print("${rf |> Result.map(fn f -> f(1))}")
    io.print("${lf |> Iter.map(fn f -> f(5)) |> Iter.to_list()}")
    io.print("${Map.get(mp, "inc") |> Maybe.map(fn f -> f(1))}")
end
`, "Some(8)\nOk(9)\n[6, 10]\nSome(2)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireFuncTypeArgLowers(t, tc.body)
			verifyLambdaProgram(t, tc.body, tc.want)
		})
	}
}

// A handler whose message parameter is only called needs no annotation: the
// call makes the actor's message type `(Int) -> Int` (and the other
// callbacks' open parameters likewise), and the program runs.
func TestIRFuncTypeArg_ACalledParameterSolvesTheType(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"an actor started without an annotation", funcTypeArgActorImports + funcTypeArgApp + funcTypeArgRead + `
fn main()
    local = Actor.start(App.workers, 10, fn s, f -> f(s))
    _ = Actor.cast(local, fn n -> n * 3)
    io.print("${read(local)}")
end
`, "Some(30)\n"},
		{"a later argument and several arguments", `import std::io

fn run<M>(h: (Int, M) -> Int, m: M) -> Int
    h(1, m)
end

fn fold<S, M>(state: S, h: (S, M) -> S, m: M) -> S
    h(state, m)
end

fn main()
    xs: List<(Int) -> String> = [fn n -> "${n}", fn n -> "#${n}"]
    io.print("${Iter.map(xs, fn g -> g(1)) |> Iter.to_list()}")
    io.print("${run(fn s, f -> f(s), fn n -> n * 2)}")
    io.print(fold("a", fn s, f -> f(s, 3), fn s, n -> "${s}${n}"))
end
`, "[1, #1]\n2\na3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireFuncTypeArgLowers(t, tc.body)
			verifyLambdaProgram(t, tc.body, tc.want)
		})
	}
}

// requireFuncTypeArgLowers fails when the front end rejects src, or when
// the builder declines any of its bodies.
func requireFuncTypeArgLowers(t *testing.T, src string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	p.Declines.Observed = func(fn, reason string) { declined[fn] = reason }
	if _, _, err := GenerateIR(p); err != nil {
		t.Fatal(err)
	}
	if len(declined) != 0 {
		t.Fatalf("the builder declined: %v", declined)
	}
}

// The Todo-Backend shape: a test group boots an app holding an actor of
// function messages, and its case casts to it and reads the state back.
func TestIRFuncTypeArg_ATestGroupBootsTheActor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counter_test.nomi")
	src := `import
    std::actors.Actor
    std::channels.{Channel, Receiver, Sender}
    std::supervisors.{Restart, Supervisor}
end
` + funcTypeArgApp + funcTypeArgRead + `
fn main() end

group "counter"
    boot boot()

    test "applies each function"
        _ = Actor.cast(App.counter, fn n -> n * 5)
        assert read(App.counter) == Some(5)
    end
end
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	p.Declines.Observed = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(declined) != 0 {
		t.Fatalf("the builder declined: %v", declined)
	}
	for _, m := range res.IR {
		if m.Name() != path {
			continue
		}
		if len(m.Tests()) != 1 {
			t.Fatalf("retained %d case(s); want 1", len(m.Tests()))
		}
		var out bytes.Buffer
		exit, reason := vm.NewProgram(m, res.IRModules(), &out).RunTests(path)
		if reason != "" {
			t.Fatalf("the VM could not run the case: %s", reason)
		}
		want := "ok " + path + " :: counter / applies each function\ntest result: ok. 1 passed, 0 failed\n"
		if exit != 0 || out.String() != want {
			t.Fatalf("VM (exit %d): %q; want exit 0 and %q", exit, out.String(), want)
		}
		return
	}
	t.Fatal("the fixture retained nothing")
}

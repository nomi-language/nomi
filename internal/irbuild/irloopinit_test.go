package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// An `Iter.loop` lowers in every body the builder retains, not only in a
// named function's: a `once` initializer, a program boot, a test group's boot
// and setup, a default another file's call fills, and a lambda inside any of
// them. Its callback is a lambda, which the spec makes a boundary of its own,
// so `break v`, `continue` and `return v` there are the loop's wherever the
// loop sits.

// The spec's own example: a `once` whose initializer is a loop.
func TestIRLoopInInitializer_OnceSpecProgram(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

once total = Iter.loop(fn i = 0 -> {
    if i > 3 { break i * 10 }
    i + 1
})

fn main() {
    io.inspect(total)
}
`, "40\n")
}

// A `once` block initializer whose loop uses all three exits, read first by
// four tasks at once: the initializer prints once, before any task's answer,
// and every reader sees the same value. A `once` holding a lambda with a
// loop runs the loop on each call.
func TestIRLoopInInitializer_OnceRunsOnceUnderTasks(t *testing.T) {
	verifyLambdaProgram(t, `import std/channels.{Channel, Receiver, Sender}
import std/io
import std/tasks.Task

once total = {
    io.print("forcing total")
    ch = Channel.buffered<Int>(10)
    Iter.each([1, 0, 2, 7, 4, 9, 6], fn n -> {
        _ = Sender.send(ch.sender, n)
    })
    Sender.close(ch.sender)
    Iter.loop(fn acc = 0 -> {
        n = case Receiver.receive(ch.receiver) {
            Some(n) -> n
            None -> break acc
        }
        if n == 0 { continue }
        if n == 7 { return acc + 100 }
        case n {
            9 -> break acc * 2
            _ -> acc + n
        }
    })
}

once scale = fn k: Int -> Iter.loop(fn n = 1 -> {
    if n == 2 { return 5 }
    if n >= k { break n }
    n * 2
})

fn main() {
    io.print("before")
    got = concurrent {
        tasks = Iter.map([1, 2, 3, 4], fn i -> Task.spawn(fn -> total + i)) |> Iter.to_list()
        Iter.map(tasks, fn t -> Task.await(t)) |> Iter.to_list()
    }
    io.inspect(got)
    io.inspect(total)
    io.inspect(scale(10))
    io.inspect(scale(50))
}
`, "before\nforcing total\n[215, 216, 217, 218]\n214\n10\n80\n")
}

// A program boot's body, and a lambda it calls, each lower a loop with all
// three exits. A default another file fills (a parameter default reading an
// earlier parameter, and a field default) lowers its loop in its accessor.
func TestIRLoopInInitializer_BootAndOtherFileDefaults(t *testing.T) {
	verifyLambdaProgram(t, `import std/channels.{Channel, Receiver, Sender}
import std/io
import lib

pub struct App {
    context: Context
    total: Int
    stepped: Int
}

fn boot() -> Result<App, String> {
    ch = Channel.buffered<Int>(10)
    Iter.each([1, 0, 2, 7, 4, 9, 6], fn n -> {
        _ = Sender.send(ch.sender, n)
    })
    Sender.close(ch.sender)
    total = Iter.loop(fn acc = 0 -> {
        n = case Receiver.receive(ch.receiver) {
            Some(n) -> n
            None -> break acc
        }
        if n == 0 { continue }
        if n == 7 { return acc + 100 }
        if n == 9 { break acc * 2 }
        acc + n
    })
    steps = fn k: Int -> Iter.loop(fn n = 0 -> {
        if n == 3 { return 4 }
        if n >= k { break n }
        n + 3
    })
    Ok(#App{context: Context.root(), total: total, stepped: steps(10)})
}

fn main() {
    io.inspect(App.total)
    io.inspect(App.stepped)
    io.inspect(lib.pick())
    io.inspect(lib.pick(3))
    io.inspect(#lib.Cfg{}.n)
}
`, "214\n10\n32\n24\n12\n", map[string]string{"lib.nomi": `pub struct Cfg {
    n: Int = Iter.loop(fn i = 0 -> {
        if i >= 4 { break i * 3 }
        i + 1
    })
}

pub fn pick(a: Int = 1, k: Int = Iter.loop(fn i = a -> {
    if i > 20 { break i }
    i * 2
})) -> Int {
    k
}
`})
}

// Defaults in the caller's own file lower at the call, in the caller's body.
func TestIRLoopInInitializer_SameFileDefaults(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Cfg {
    n: Int = Iter.loop(fn i = 0 -> {
        if i == 1 { return 3 }
        if i >= 4 { break i }
        i + 1
    })
}

fn pick(k: Int = Iter.loop(fn i = 1 -> {
    if i > 20 { break i }
    i * 2
})) -> Int {
    k
}

fn main() {
    io.inspect(#Cfg{}.n)
    io.inspect(pick())
}
`, "4\n32\n")
}

// A test group's boot and setup each lower a loop, and so does a lambda the
// setup builds.
func TestIRLoopInInitializer_GroupBootAndSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop_test.nomi")
	src := `import std/io

pub struct App {
    context: Context
    base: Int
}

pub fn boot() -> Result<App, String> {
    base = Iter.loop(fn i = 0 -> {
        if i == 1 { return 6 }
        if i > 5 { break i * 10 }
        i + 1
    })
    Ok(#App{context: Context.root(), base: base})
}

fn main() {}

struct Fix {
    n: Int
    f: (Int) -> Int
}

group "loops in boot and setup" {
    boot boot()
    setup {
        n = Iter.loop(fn i = 0 -> {
            if i == 2 { return 7 }
            if i > 10 { break i }
            i + 1
        })
        io.print("setup ${n}")
        #Fix{n: n, f: fn k: Int -> Iter.loop(fn m = 0 -> {
            if m == 4 { return 6 }
            if m >= k { break m }
            m + 4
        })}
    }

    test "reads both", fix {
        assert App.base == 60
        assert fix.n == 11
        assert fix.f(9) == 10
    }
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	prev := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prev
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IR {
		if m.Name() != path {
			continue
		}
		if len(m.Tests()) != 1 {
			t.Fatalf("retained %d case(s); want 1 (declines: %v)", len(m.Tests()), declined)
		}
		var out bytes.Buffer
		exit, reason := vm.NewProgram(m, res.IRModules(), &out).RunTests(path)
		if reason != "" {
			t.Fatalf("the VM could not run the case: %s (declines: %v)", reason, declined)
		}
		want := "ok " + path + " :: loops in boot and setup / reads both\n  setup 11\ntest result: ok. 1 passed, 0 failed\n"
		if exit != 0 || out.String() != want {
			t.Fatalf("VM (exit %d): %q; want exit 0 and %q", exit, out.String(), want)
		}
		return
	}
	t.Fatalf("the fixture retained nothing (declines: %v)", declined)
}

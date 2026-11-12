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
	verifyLambdaProgram(t, `import std::io

once total = Iter.loop(0, fn i ->
    if i > 3 then break i * 10
    i + 1
end)

fn main()
    io.inspect(total)
end
`, "40\n")
}

// A `once` block initializer whose loop uses all three exits, read first by
// four tasks at once: the initializer prints once, before any task's answer,
// and every reader sees the same value. A `once` holding a lambda with a
// loop runs the loop on each call.
func TestIRLoopInInitializer_OnceRunsOnceUnderTasks(t *testing.T) {
	verifyLambdaProgram(t, `import std::channels.{Channel, Receiver, Sender}
import std::io
import std::tasks.Task

once total = begin
    io.print("forcing total")
    ch = Channel.buffered<Int>(10)
    Iter.each([1, 0, 2, 7, 4, 9, 6], fn n ->
        _ = Sender.send(ch.sender, n)
    end)
    Sender.close(ch.sender)
    Iter.loop(0, fn acc ->
        n = case Receiver.receive(ch.receiver)
            Some(n) -> n
            None -> break acc
        end
        if n == 0 then continue
        if n == 7 then return acc + 100
        case n
            9 -> break acc * 2
            _ -> acc + n
        end
    end)
end

once scale = fn k: Int -> Iter.loop(1, fn n ->
    if n == 2 then return 5
    if n >= k then break n
    n * 2
end)

fn main()
    io.print("before")
    got = concurrent
        tasks = Iter.map([1, 2, 3, 4], fn i -> Task.spawn(fn -> total + i)) |> Iter.to_list()
        Iter.map(tasks, fn t -> Task.await(t)) |> Iter.to_list()
    end
    io.inspect(got)
    io.inspect(total)
    io.inspect(scale(10))
    io.inspect(scale(50))
end
`, "before\nforcing total\n[215, 216, 217, 218]\n214\n10\n80\n")
}

// A program boot's body, and a lambda it calls, each lower a loop with all
// three exits. A default another file fills (a parameter default reading an
// earlier parameter, and a field default) lowers its loop in its accessor.
func TestIRLoopInInitializer_BootAndOtherFileDefaults(t *testing.T) {
	verifyLambdaProgram(t, `import std::channels.{Channel, Receiver, Sender}
import std::io
import lib

pub struct App
    context: Context
    total: Int
    stepped: Int
end

fn boot() -> Result<App, String>
    ch = Channel.buffered<Int>(10)
    Iter.each([1, 0, 2, 7, 4, 9, 6], fn n ->
        _ = Sender.send(ch.sender, n)
    end)
    Sender.close(ch.sender)
    total = Iter.loop(0, fn acc ->
        n = case Receiver.receive(ch.receiver)
            Some(n) -> n
            None -> break acc
        end
        if n == 0 then continue
        if n == 7 then return acc + 100
        if n == 9 then break acc * 2
        acc + n
    end)
    steps = fn k: Int -> Iter.loop(0, fn n ->
        if n == 3 then return 4
        if n >= k then break n
        n + 3
    end)
    Ok(App{context: Context.root(), total:, stepped: steps(10)})
end

fn main()
    io.inspect(App.total)
    io.inspect(App.stepped)
    io.inspect(lib.pick())
    io.inspect(lib.pick(3))
    io.inspect(lib.Cfg{}.n)
end
`, "214\n10\n32\n24\n12\n", map[string]string{"lib.nomi": `pub struct Cfg
    n: Int = Iter.loop(0, fn i ->
        if i >= 4 then break i * 3
        i + 1
    end)
end

pub fn pick(a: Int = 1, k: Int = Iter.loop(a, fn i ->
    if i > 20 then break i
    i * 2
end)) -> Int
    k
end
`})
}

// Defaults in the caller's own file lower at the call, in the caller's body.
func TestIRLoopInInitializer_SameFileDefaults(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Cfg
    n: Int = Iter.loop(0, fn i ->
        if i == 1 then return 3
        if i >= 4 then break i
        i + 1
    end)
end

fn pick(k: Int = Iter.loop(1, fn i ->
    if i > 20 then break i
    i * 2
end)) -> Int
    k
end

fn main()
    io.inspect(Cfg{}.n)
    io.inspect(pick())
end
`, "4\n32\n")
}

// A test group's boot and setup each lower a loop, and so does a lambda the
// setup builds.
func TestIRLoopInInitializer_GroupBootAndSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop_test.nomi")
	src := `import std::io

pub struct App
    context: Context
    base: Int
end

pub fn boot() -> Result<App, String>
    base = Iter.loop(0, fn i ->
        if i == 1 then return 6
        if i > 5 then break i * 10
        i + 1
    end)
    Ok(App{context: Context.root(), base:})
end

fn main() end

struct Fix
    n: Int
    f: (Int) -> Int
end

group "loops in boot and setup"
    boot boot()
    setup begin
        n = Iter.loop(0, fn i ->
            if i == 2 then return 7
            if i > 10 then break i
            i + 1
        end)
        io.print("setup ${n}")
        Fix{n:, f: fn k: Int -> Iter.loop(0, fn m ->
            if m == 4 then return 6
            if m >= k then break m
            m + 4
        end)}
    end

    test "reads both", fix
        assert App.base == 60
        assert fix.n == 11
        assert fix.f(9) == 10
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

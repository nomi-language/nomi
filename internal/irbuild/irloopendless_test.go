package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// An `Iter.loop` with no `break` never reaches its exit. The builder makes it
// the tail of its block, lowers nothing after it, and ends the exit in a jump
// to itself. These programs leave such a loop by a trap, which the VM and
// `nomi run` report at the trapping line after the exact output before it.

// verifyEndlessTrap runs src's main on the VM and through the `nomi run`
// command, and requires both to print output and then stop with fault.
func verifyEndlessTrap(t *testing.T, src, fault, output string) {
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
	prev := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prev
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, mod := range res.IR {
		for _, f := range mod.Funcs() {
			if f.Name() == "main" {
				entry = mod
			}
		}
	}
	if entry == nil {
		t.Fatalf("main was not retained (declines: %v)", declined)
	}
	var out bytes.Buffer
	booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
	if err != nil {
		t.Fatal(err)
	}
	_, err = booted.Run("main")
	if err == nil || !strings.Contains(err.Error(), fault) || out.String() != output {
		t.Fatalf("VM: error %v, output %q; want %q then %q (declines: %v)", err, out.String(), output, fault, declined)
	}
	if got := vmReference(path); got.exit == 0 || got.stdout != output || !strings.Contains(got.stderr, fault) {
		t.Fatalf("vm command: %s; want output %q then %q", got, output, fault)
	}
}

func TestIRLoopEndless_WorkerLoopEndsByATrap(t *testing.T) {
	// The tour's worker shape: a stateless loop that takes the next job and
	// goes round, as `fn main`'s last statement.
	verifyEndlessTrap(t, `import {
    std/channels.{Channel, Receiver, Sender}
    std/io
}

fn work(n: Int) {
    io.print("job ${n}")
    io.print(10 / (3 - n))
}

fn main() {
    jobs: Channel<Int> = Channel.buffered(4)
    Iter.each([0, 1, 2, 3], fn n -> {
        _ = Sender.send(jobs.sender, n)
    })
    io.print("start")
    Iter.loop(fn -> {
        case Receiver.receive(jobs.receiver) {
            Some(n) -> work(n)
            None -> Unit
        }
    })
}
`, "line 8: division by zero", "start\njob 0\n3\njob 1\n5\njob 2\n10\njob 3\n")
}

func TestIRLoopEndless_ReturnAndContinueGoRound(t *testing.T) {
	// `return v` in the callback is the next state and `continue` keeps it,
	// so neither leaves the loop.
	verifyEndlessTrap(t, `import std/io

fn main() {
    _ = Iter.loop(fn n = 0 -> {
        io.print("n ${n}")
        if n < 2 { return n + 1 }
        if n == 2 {
            io.print("two")
            return 5
        }
        io.print(10 / (n - 5))
        continue
    })
}
`, "line 11: division by zero", "n 0\nn 1\nn 2\ntwo\nn 5\n")
}

func TestIRLoopEndless_ValuePositions(t *testing.T) {
	// The loop as a function's Int tail, as a binding followed by statements
	// that never run, as an operand after one evaluated first, as a `case`
	// arm and an `if` branch, as the tail of an outer loop's body, and as a
	// `concurrent` body's tail. Arms that do not loop still answer.
	for name, call := range map[string]struct{ main, fault, output string }{
		"tail": {"io.print(arm(4))\n    io.print(branch(True))\n    io.print(count(5) + bound())",
			"line 6: division by zero", "40\n7\ncount 0\n2\ncount 1\n2\ncount 2\n3\ncount 3\n5\ncount 4\n10\ncount 5\n"},
		"bound":      {"io.print(bound())", "line 14: division by zero", "bound 1\n10\nbound 2\n"},
		"operand":    {"io.print(operand())", "line 28: division by zero", "first\n"},
		"arm":        {"io.print(arm(0))", "line 36: division by zero", ""},
		"branch":     {"io.print(branch(False))", "line 48: division by zero", ""},
		"nested":     {"nested()", "line 60: division by zero", "outer 0\nouter 1\ninner 1\n5\ninner 2\n10\ninner 3\n"},
		"concurrent": {"io.print(conc())", "line 70: division by zero", "conc\n5\n10\n"},
	} {
		t.Run(name, func(t *testing.T) {
			verifyEndlessTrap(t, endlessPositions(call.main), call.fault, call.output)
		})
	}
}

// endlessPositions is TestIRLoopEndless_ValuePositions's program with main
// running only body.
func endlessPositions(body string) string {
	return `import std/io

fn count(limit: Int) -> Int {
    Iter.loop(fn n = 0 -> {
        io.print("count ${n}")
        io.print(10 / (limit - n))
        n + 1
    })
}

fn bound() -> Int {
    x = Iter.loop(fn n = 1 -> {
        io.print("bound ${n}")
        io.print(10 / (2 - n))
        n + 1
    })
    io.print("never")
    x + 1
}

fn noisy(label: String) -> Int {
    io.print(label)
    1
}

fn operand() -> Int {
    noisy("first") + Iter.loop(fn n = 0 -> {
        io.print(10 / n)
        n
    })
}

fn arm(x: Int) -> Int {
    case x {
        0 -> Iter.loop(fn n = 0 -> {
            io.print(10 / n)
            n
        })
        _ -> x * 10
    }
}

fn branch(flag: Bool) -> Int {
    if flag {
        return 7
    } else {
        Iter.loop(fn n = 0 -> {
            io.print(10 / n)
            n
        })
    }
}

fn nested() {
    _ = Iter.loop(fn i = 0 -> {
        io.print("outer ${i}")
        if i < 1 { return i + 1 }
        Iter.loop(fn j = i -> {
            io.print("inner ${j}")
            io.print(10 / (3 - j))
            j + 1
        })
    })
}

fn conc() -> Int {
    concurrent {
        io.print("conc")
        Iter.loop(fn n = 2 -> {
            io.print(10 / n)
            n - 1
        })
    }
}

fn main() {
    ` + body + `
}
`
}

func TestIRLoopEndless_InALambda(t *testing.T) {
	// A lambda whose body ends in the loop, one that discards it, and one
	// whose earlier `return` settles its result.
	verifyEndlessTrap(t, `import std/io

fn main() {
    spin = fn limit: Int -> Iter.loop(fn n = 0 -> {
        io.print("spin ${n}")
        if n == limit { io.print(1 / 0) }
        n + 1
    })
    drop = fn label: String -> {
        io.print(label)
        _ = Iter.loop(fn n = 0 -> {
            io.print(10 / (1 - n))
            n + 1
        })
    }
    early = fn x: Int -> {
        if x > 0 { return x * 2 }
        Iter.loop(fn n = 0 -> { io.print(1 / n)  n })
    }
    io.print(early(4))
    f: (String) -> Unit = drop
    _ = f
    io.print(spin(2))
}
`, "line 6: division by zero", "8\nspin 0\nspin 1\nspin 2\n")
}

func TestIRLoopEndless_TestBodies(t *testing.T) {
	// A case ending in the loop, one binding it with statements after, and
	// one running it in a `case` arm: each fails at its trap after the
	// output before it, and nothing after the loop runs.
	path := filepath.Join(t.TempDir(), "endless_test.nomi")
	src := `import std/io

test "a worker loop" {
    assert 1 == 1
    Iter.loop(fn n = 0 -> {
        io.print("worker ${n}")
        if n < 2 { return n + 1 }
        io.print(10 / (2 - n))
        n
    })
}

test "a bound loop" {
    assert 2 == 2
    x = Iter.loop(fn -> {
        io.print("bound")
        io.print(1 / 0)
    })
    io.print("never")
    io.inspect(x)
}

test "a loop in an arm" {
    assert 3 == 3
    case 1 {
        1 -> Iter.loop(fn -> {
            io.print("arm")
            io.print(1 / 0)
        })
        _ -> io.print("other")
    }
    io.print("never")
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
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != 3 {
		t.Fatalf("not every case was retained (declines: %v)", declined)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	report := out.String()
	if exit == 0 || strings.Contains(report, "never") || strings.Contains(report, "other") {
		t.Fatalf("exit %d, report:\n%s", exit, report)
	}
	for _, want := range []string{
		"  worker 0\n  worker 1\n  worker 2\n", "line 8: division by zero",
		"  bound\n", "line 17: division by zero",
		"  arm\n", "line 28: division by zero",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
}

package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// An `Iter.loop` inside a lambda lowers in the lambda's own body, with the
// semantics it has in a named function: `break v` leaves the innermost loop
// with v, `continue` goes round again with the state unchanged, `return v`
// makes v the next state, and none of them leaves the enclosing lambda. The
// channel feeds each iteration a new input, so a `continue` makes progress.
func TestIRLoopInLambda_RunsAsInANamedFunction(t *testing.T) {
	verifyLambdaProgram(t, `import std/channels.{Channel, Receiver, Sender}
import std/io
import std/tasks.Task

fn feed(items: List<String>) -> Receiver<String> {
    ch = Channel.buffered<String>(10)
    Iter.each(items, fn s -> {
        _ = Sender.send(ch.sender, s)
    })
    Sender.close(ch.sender)
    ch.receiver
}

fn main() {
    // A lambda bound to a name. Its code after the loop runs.
    count = fn input: Receiver<String> -> {
        n = Iter.loop(fn total = 0 -> {
            t = case Receiver.receive(input) {
                Some(t) -> t
                None -> break total
            }
            if t == "skip" { continue }
            if t == "jump" { return total + 100 }
            case t {
                "stop" -> break total * 10
                "again" -> continue
                "leap" -> return total + 1000
                _ -> total + String.length(t)
            }
        })
        io.print("after the loop: ${n}")
        n + 1
    }
    io.print(count(feed(["ab", "skip", "jump", "c", "again", "leap"])))
    io.print(count(feed(["ab", "stop", "zzz"])))
    // An Iter.map callback: its own 'continue' skips the element, and the
    // one inside the loop is the loop's.
    lens = Iter.map(["a", "skip", "bcd"], fn w -> {
        if w == "skip" { continue }
        input = feed([w, "x", w])
        m = Iter.loop(fn acc = 0 -> {
            case Receiver.receive(input) {
                Some("x") -> continue
                Some(t) -> acc + String.length(t)
                None -> break acc
            }
        })
        m * 10
    }) |> Iter.to_list()
    io.inspect(lens)
    // A loop as the whole Iter.map callback.
    io.inspect(Iter.map([1, 2, 3], fn k -> Iter.loop(fn n = 0 -> {
        if n >= k { break n * 10 }
        n + 1
    })) |> Iter.to_list())
    // An Iter.each callback.
    Iter.each([3, 5], fn k -> {
        s = Iter.loop(fn n = 0 -> {
            if n == 1 { return 4 }
            if n >= k { break n }
            n + 1
        })
        io.print("each ${k}: ${s}")
    })
    // A loop in a loop's callback, in a lambda: the inner exits are the
    // inner loop's, and the outer 'return' after it is the outer loop's.
    nested = fn rows: Int -> Iter.loop(fn outer = #(0, 0) -> {
        #(i, sum) = outer
        if i >= rows { break outer }
        inner = Iter.loop(fn j = 0 -> {
            if j == 1 { return 5 }
            if j > i + 5 { break j }
            j + 1
        })
        if inner == 7 { return #(i + 1, sum) }
        #(i + 1, sum + inner)
    })
    io.inspect(nested(3))
    // A lambda that captures outer bindings the loop reads.
    base = 7
    step = 2
    f = fn -> Iter.loop(fn n = base -> {
        if n > 12 { break n + step }
        n + step
    })
    io.print(f())
    // A concurrent block's task lambda, and a loop in the block itself.
    got = concurrent {
        task = Task.spawn(fn -> Iter.loop(fn n = 1 -> {
            if n > 50 { break n }
            n * 3
        }))
        Task.await(task)
    }
    io.print(got)
    direct = concurrent {
        Iter.loop(fn n = 1 -> {
            if n > 10 { break n }
            n * 2
        })
    }
    io.print(direct)
}
`, "after the loop: 1103\n1104\nafter the loop: 20\n21\n[20, 60]\n[10, 20, 30]\n"+
		"each 3: 4\neach 5: 5\n#(3, 14)\n15\n81\n16\n")
}

// A lambda in a test body: the loop lowers in the lambda, and the lambda's
// code after the loop runs.
func TestIRLoopInLambda_TestBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loop_test.nomi")
	src := `import std/io

test "a loop in a lambda in a test" {
    f = fn k: Int -> {
        n = Iter.loop(fn i = 0 -> {
            if i == 2 { return 10 }
            if i > k { break i }
            i + 1
        })
        io.print("after the loop: ${n}")
        n * 2
    }
    assert f(3) == 20
    assert f(12) == 26
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
			t.Fatalf("the VM could not run the case: %s", reason)
		}
		want := "ok " + path + " :: a loop in a lambda in a test\n  after the loop: 10\n  after the loop: 13\ntest result: ok. 1 passed, 0 failed\n"
		if exit != 0 || out.String() != want {
			t.Fatalf("VM (exit %d): %q; want exit 0 and %q", exit, out.String(), want)
		}
		return
	}
	t.Fatalf("the fixture retained nothing (declines: %v)", declined)
}

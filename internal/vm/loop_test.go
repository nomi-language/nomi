package vm_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// Self tail calls as jumps (loop.go): each test runs a program with them
// and with every self tail call compiled as opTail, requires the same
// transcript byte for byte, and requires that the run with them compiled
// the loops it names to opLoop, so the comparison is of a loop that ran
// as a jump.

// loopRun is one run of src's main: what it printed, then its failure.
func loopRun(t *testing.T, src string, loops []string, jumps bool) string {
	t.Helper()
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
	}
	for _, fn := range loops {
		listing, err := p.Disassemble(fn)
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		if got := opcodesOf(listing)["loop"]; got != jumps {
			t.Fatalf("%s compiles its self tail call to opLoop: %v, want %v:\n%s", fn, got, jumps, listing)
		}
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	if _, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("the VM refused the program: %v", err)
	}
	if err != nil {
		fmt.Fprintf(&out, "failure: %v\n", err)
	}
	return out.String()
}

// loopBothWays runs src with self tail calls as jumps and as opTail,
// requires the two transcripts to be the same, and answers it.
func loopBothWays(t *testing.T, src string, loops []string) string {
	t.Helper()
	with := loopRun(t, src, loops, true)
	t.Setenv(vm.NoLoopJumpEnv, "1")
	without := loopRun(t, src, loops, false)
	os.Unsetenv(vm.NoLoopJumpEnv)
	if with != without {
		t.Fatalf("as jumps:\n%s\nas tail calls:\n%s", with, without)
	}
	return with
}

// A self tail call moves only the operands that are not already in their
// parameter's register: scan's loop moves the counter and the sum, and
// neither the bound, the string nor the list, whatever their banks.
func TestLoop_ASelfTailCallMovesOnlyTheOperandsThatChange(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std::io

fn scan(i: Int, n: Int, acc: Int, s: String, xs: List<Int>) -> Int
    if i == n then acc + String.length(s) + List.length(xs) else scan(i + 1, n, acc + i, s, xs)
end

fn main()
    io.print("${scan(0, 10, 0, "ab", [1])}")
end
`)
	if err != nil {
		t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
	}
	listing, err := p.Disassemble("scan")
	if err != nil {
		t.Fatal(err)
	}
	plan := regexp.MustCompile(`(?m)^\s*\d+\s+loop\s+\d+\s+; (.*)$`).FindStringSubmatch(listing)
	if plan == nil {
		t.Fatalf("scan's self tail call is no opLoop:\n%s", listing)
	}
	if moves := strings.Fields(plan[1]); len(moves) != 2 || !strings.HasPrefix(moves[0], "w0<-w") || !strings.HasPrefix(moves[1], "w2<-w") {
		t.Fatalf("scan's loop moves %q, want the counter and the sum (w0 and w2) alone:\n%s", plan[1], listing)
	}
}

// Every kind of operand reaches its parameter through a jump as through the
// tail call: Ints past int64, strings, references, a swap and a rotation
// (cycles of moves, through the scratch register), and Floats. A function
// with a deferred call keeps opTail, which runs it at each transfer.
func TestLoop_EveryOperandReachesItsParameter(t *testing.T) {
	got := loopBothWays(t, `import std::io

fn grow(n: Int, x: Int) -> Int
    if n == 0 then x else grow(n - 1, x * 1000000007)
end

fn swap(n: Int, a: String, b: String, xs: List<String>) -> String
    if n == 0 then "${a}${b}${xs}" else swap(n - 1, b, a, [a, ..xs])
end

fn rotate(n: Int, a: Int, b: Int, c: Int, d: Int, e: Int, f: Int, g: Int, h: Int, i: Int) -> Int
    if n == 0 then a * 1 + b * 10 + c * 100 + d * 1000 else rotate(n - 1, i, a, b, c, d, e, f, g, h)
end

fn floats(n: Int, x: Float, y: Float) -> Float
    if n == 0 then x - y else floats(n - 1, y, x + 1.5)
end

fn bigs(n: Int, a: Int, b: Int) -> Int
    if n == 0 then a - b else bigs(n - 1, b, a * 1000000007 * 1000000007)
end

fn main()
    io.print("${grow(5, 3)}")
    io.print(swap(3, "a", "b", []))
    io.print("${rotate(3, 1, 2, 3, 4, 5, 6, 7, 8, 9)}")
    io.print("${floats(5, 0.0, 0.0)}")
    io.print("${bigs(5, 1, 2)}")
end
`, []string{"grow", "swap", "rotate", "floats", "bigs"})
	want := "3000000105000001470000010290000036015000050421\nba[a, b, a]\n1987\n-1.5\n" +
		"-1000000042000000733000006804000035427000098098000112847\n"
	if got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
	deferred := loopRun(t, `import std::io

fn deferred(n: Int) -> Int
    defer io.print("left ${n}")
    if n == 0 then 0 else deferred(n - 1)
end

fn main()
    io.print("${deferred(2)}")
end
`, []string{"deferred"}, false)
	if deferred != "left 0\nleft 1\nleft 2\n0\n" {
		t.Fatalf("a loop with a deferred call printed %q", deferred)
	}
}

// A trap in a loop's body is reported at its own line, a `?` leaves the
// loop with its error, and a loop of more iterations than the call depth
// allows runs to its end: the jump counts no depth.
func TestLoop_TrapsAndEarlyExitsLeaveTheLoopAsTheTailCallDoes(t *testing.T) {
	const src = `import std::io

fn first_odd(i: Int, n: Int, xs: Vector<Int>) -> Result<Int, String>
    if i == n then
        Err("none")
    else
        x = Vector.get(xs, i) |> Maybe.to_result("short")?
        if x % 2 == 1 then Ok(x) else first_odd(i + 1, n, xs)
    end
end

fn long(i: Int, n: Int, acc: Int) -> Int
    if i == n then acc else long(i + 1, n, acc + i % 3)
end

fn pick(i: Int, acc: Int, v: Vector<Int>) -> Int
    if i == 5 then acc else pick(i + 1, acc + Vector.get(v, i)!, v)
end

fn main()
    io.print("${first_odd(0, 3, Vector[2, 4, 5])} ${first_odd(0, 3, Vector[2, 4])} ${first_odd(0, 2, Vector[2, 4])}")
    io.print("${long(0, 300000, 0)}")
    io.print("${pick(0, 0, Vector[1, 2, 3])}")
end
`
	got := loopBothWays(t, src, []string{"first_odd", "long", "pick"})
	trap := fmt.Sprintf("line %d", strings.Count(src[:strings.Index(src, "Vector.get(v, i)!")], "\n")+1)
	if !strings.HasPrefix(got, "Ok(5) Err(short) Err(none)\n300000\nfailure: ") || !strings.Contains(got, trap) {
		t.Fatalf("transcript %q, want Ok(5) Err(short) Err(none), 300000 and a trap at %s", got, trap)
	}
}

// An update of a value the loop owns and hands round itself is written in
// place on every iteration through a jump, as through the tail call: the
// loop allocates nothing per iteration either way.
func TestLoop_AnOwnedValueIsWrittenInPlaceRoundTheLoop(t *testing.T) {
	const src = `import std::io

struct Acc
    n: Int
    v: Vector<Int>
end

fn fill(i: Int, n: Int, a: Acc) -> Acc
    if i == n then a else fill(i + 1, n, {..a, n: a.n + i, v: Vector.set(a.v, i %% 8, i)!})
end

fn main()
    a = fill(0, %d, Acc{n: 0, v: Vector[0, 0, 0, 0, 0, 0, 0, 0]})
    io.print("${a.n} ${a.v}")
end
`
	listing, err := depthProgram(t, fmt.Sprintf(src, 1)).Disassemble("fill")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "update!") || !regexp.MustCompile(`loop .*owned`).MatchString(listing) {
		t.Fatalf("fill neither updates in place nor hands its Acc round the loop, so this tests nothing:\n%s", listing)
	}
	with := allocsPerActivation(t, src)
	t.Setenv(vm.NoLoopJumpEnv, "1")
	without := allocsPerActivation(t, src)
	os.Unsetenv(vm.NoLoopJumpEnv)
	if with > 0.1 || without > 0.1 {
		t.Fatalf("one iteration allocates %.2f times as a jump and %.2f as a tail call, want 0", with, without)
	}
	loopBothWays(t, fmt.Sprintf(src, 20), []string{"fill"})
}

// A test case stopped spinning in a loop shows the stack the tail call
// shows: the loop's frame at the loop's line, then its caller's.
func TestLoop_AStoppedLoopShowsTheSameStack(t *testing.T) {
	src := `fn spin(n: Int, s: String) -> Int
    spin(n + 1, s)
end

fn wrap(n: Int) -> Int
    spin(n, "x") + 1
end

test "spins in a loop"
    assert wrap(0) == 1
end
`
	stack := func(jumps bool) string {
		p, err := vmhost.Load(writeTempProgram(t, src))
		if err != nil {
			t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
		}
		if listing, err := p.Disassemble("spin"); err != nil || opcodesOf(listing)["loop"] != jumps {
			t.Fatalf("spin compiles to opLoop: %v, want %v (%v):\n%s", !jumps, jumps, err, listing)
		}
		p.SetTestTimeout(100*time.Millisecond, "")
		var b strings.Builder
		for _, r := range p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}) {
			var timedOut *vmhost.TimedOut
			if !errors.As(r.Err, &timedOut) {
				t.Fatalf("%s: want a TimedOut failure, got %v", r.Name, r.Err)
			}
			fmt.Fprintf(&b, "line %d:", timedOut.Line)
			for _, f := range timedOut.Stack {
				fmt.Fprintf(&b, " %s@%d", f.Name, f.Pos.Line())
			}
		}
		return b.String()
	}
	with := stack(true)
	t.Setenv(vm.NoLoopJumpEnv, "1")
	without := stack(false)
	if with != without {
		t.Fatalf("as a jump: %s\nas a tail call: %s", with, without)
	}
	if !strings.Contains(with, "spin@2 wrap@6") {
		t.Fatalf("the stack lacks spin under wrap: %s", with)
	}
}

// A task spinning in a loop stops at the jump when it is cancelled, and its
// deferred call runs: the jump is a safe point, as the tail call is.
func TestLoop_ACancelledTaskStopsInItsLoop(t *testing.T) {
	got := loopBothWays(t, `import
    std::channels.Channel
    std::channels.Receiver
    std::channels.Sender
    std::io
    std::tasks.Task
end

fn spin(i: Int, n: Int) -> Int
    if i == n then i else spin(i + 1, n)
end

fn main()
    started = Channel.buffered<Int>(1)
    outcome = concurrent
        t = Task.spawn(fn ->
            defer io.print("cleanup ran")
            _ = Sender.send(started.sender, 1)
            n = spin(0, 1000000000000)
            io.print("finished")
            n
        end)
        _ = Receiver.receive(started.receiver)
        Task.cancel(t)
        Task.outcome(t)
    end
    dbg outcome
end
`, []string{"spin"})
	if strings.Contains(got, "finished") || !strings.Contains(got, "cleanup ran") || !strings.Contains(got, "outcome = Cancelled") {
		t.Fatalf("the cancelled loop did not stop at its jump: %q", got)
	}
}

// A debug or profiling machine compiles no jump, so its stops and samples
// are the tail call's, and neither does a machine WithoutLoopJumps.
func TestLoop_HookedMachinesCompileTheTailCall(t *testing.T) {
	mod := dbgLoweredModule(t, `import std::io

fn count(i: Int, n: Int) -> Int
    if i == n then i else count(i + 1, n)
end

fn main()
    io.print("${count(0, 3)}")
end
`)
	listing := func(m *vm.Machine) string {
		l, err := m.Disassemble("count")
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if l := listing(vm.New(mod, io.Discard)); !opcodesOf(l)["loop"] {
		t.Fatalf("a plain machine compiles no jump, so this tests nothing:\n%s", l)
	}
	for name, m := range map[string]*vm.Machine{
		"debug":     vm.New(mod, io.Discard).WithDebugger(vm.NewDebugger()),
		"profiling": vm.New(mod, io.Discard).WithProfiler(vm.NewProfiler(time.Millisecond)),
		"without":   vm.New(mod, io.Discard).WithoutLoopJumps(),
	} {
		if l := listing(m); opcodesOf(l)["loop"] || !opcodesOf(l)["tail"] {
			t.Errorf("a %s machine compiles a jump:\n%s", name, l)
		}
	}
}

// A redirected loop runs the replacement from its next call, an earlier
// caller's included: the jump stays inside the activation that made it,
// and every call reads the function's table entry.
func TestLoop_ARedirectedLoopRunsTheReplacement(t *testing.T) {
	mod := dbgLoweredModule(t, `import std::io

fn ones(i: Int, n: Int, acc: Int) -> Int
    if i == n then acc else ones(i + 1, n, acc + 1)
end

fn twos(i: Int, n: Int, acc: Int) -> Int
    if i == n then acc else twos(i + 1, n, acc + 2)
end

fn count(n: Int) -> Int
    ones(0, n, 0) * 1
end

fn main()
    io.print("${count(5)} ${twos(0, 5, 0)}")
end
`)
	m := vm.New(mod, io.Discard)
	run := func() any {
		v, err := m.Run("count", int64(5))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if l, err := m.Disassemble("ones"); err != nil || !opcodesOf(l)["loop"] {
		t.Fatalf("ones compiles no jump (%v), so this tests nothing:\n%s", err, l)
	}
	before := run()
	m.Redirect(entriesFunc(t, mod, "ones"), entriesFunc(t, mod, "twos"))
	after := run()
	if fmt.Sprint(before) != "5" || fmt.Sprint(after) != "10" {
		t.Fatalf("count answers %v, then %v after the redirect, want 5 and 10", before, after)
	}
}

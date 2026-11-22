package vm_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// Calls compiled into their callers (inline.go): each test runs a program
// with them and without them, requires the same transcript byte for byte,
// and requires that the run with them compiled the calls it names in, so
// the comparison is of a body that ran as a view.

// inlineRun is one run of a program: what it printed, then its failure.
func inlineRun(t *testing.T, src string, compiledIn map[string]bool) string {
	t.Helper()
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
	}
	for fn, want := range compiledIn {
		listing, err := p.Disassemble(fn)
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		if got := strings.Contains(listing, "compiled in"); got != want {
			t.Fatalf("%s compiles a call into itself: %v, want %v:\n%s", fn, got, want, listing)
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

// inlineBothWays runs src with calls compiled in, requiring each function
// of compiledIn to have one compiled into it, and with none compiled in,
// requires the two transcripts to be the same, and answers it.
func inlineBothWays(t *testing.T, src string, compiledIn []string) string {
	t.Helper()
	in := map[string]bool{}
	for _, fn := range compiledIn {
		in[fn] = true
	}
	with := inlineRun(t, src, in)
	t.Setenv(vm.NoInlineEnv, "1")
	none := map[string]bool{}
	for fn := range in {
		none[fn] = false
	}
	without := inlineRun(t, src, none)
	os.Unsetenv(vm.NoInlineEnv)
	if with != without {
		t.Fatalf("compiled in:\n%s\nas calls:\n%s", with, without)
	}
	return with
}

// A caller that goes on, after a body compiled into it returns, to a tail
// call into a function with exit hooks (a `defer`) runs it with them, as
// it does when nothing was compiled in: the machine does not refuse the
// program.
func TestInline_ATailCallAfterACompiledInBodyGetsItsExitHooks(t *testing.T) {
	got := inlineBothWays(t, `import
    std::fs.{self, Dir}
    std::io
end

struct Held
    dir: Maybe<Dir>
end

fn guarded(held: Held, name: String) -> Result<Held, String>
    defer on error io.print("failed")
    if name == "" then return Err("no name")
    Ok(held)
end

fn outer(name: String) -> Result<Held, String>
    if name == "x" then return outer("y")
    temp = fs.open_dir(fs.temp_dir()) |> Result.to_maybe()
    guarded(Held{dir: temp}, name)
end

fn main() -> Result<Unit, String>
    held = outer("a")?
    defer io.print("done")
    io.print("${Maybe.is_some(held.dir)} ${Result.is_ok(outer(""))}")
    Ok(Unit)
end
`, []string{"outer"})
	if got != "failed\nTrue False\ndone\n" {
		t.Fatalf("transcript:\n%s", got)
	}
}

// A trap in a body compiled into its caller, two views deep, is the trap
// the calls would have reported: its own line, its own words.
func TestInline_ATrapInACompiledInBodyIsTheCallsTrap(t *testing.T) {
	got := inlineBothWays(t, `import std::io

fn third(v: Vector<Int>) -> Int
    Vector.get(v, 2)!
end

fn total(v: Vector<Int>) -> Int
    third(v) + 1
end

fn main()
    io.print("${total(Vector[1, 2, 3]) * 2}")
    io.print("${total(Vector[1]) * 2}")
end
`, []string{"main", "total"})
	if !strings.HasPrefix(got, "8\nfailure: line 4: ") {
		t.Fatalf("want 8, then the trap at line 4:\n%s", got)
	}
}

// A `?` in a body compiled into a test leaves that body, not the test:
// the call's answer is the None it propagates, as a call's is.
func TestInline_AQuestionMarkLeavesTheCompiledInBody(t *testing.T) {
	src := `import std::io

fn second(v: Vector<Int>) -> Maybe<Int>
    x = Vector.get(v, 1)?
    Some(x * 10)
end

fn main()
    io.print("${second(Vector[1]) == None} ${second(Vector[1, 2])}")
end
`
	if got := inlineBothWays(t, src, []string{"main"}); got != "True Some(20)\n" {
		t.Fatalf("transcript:\n%s", got)
	}

	cases := src + `
test "a ? in a body compiled into a test"
    assert second(Vector[1]) == None
    assert second(Vector[1, 2]) == Some(20)
end
`
	report := func() string {
		p, err := vmhost.LoadSource("main", cases)
		if err != nil {
			t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
		}
		var b strings.Builder
		for _, r := range p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}) {
			fmt.Fprintf(&b, "%s: %v %v\n", r.Name, r.Err, r.Blocked)
		}
		return b.String()
	}
	with := report()
	t.Setenv(vm.NoInlineEnv, "1")
	without := report()
	if with != without || with != "a ? in a body compiled into a test: <nil> []\n" {
		t.Fatalf("compiled in:\n%s\nas calls:\n%s", with, without)
	}
}

// An assertion whose operand is a call compiled in reports the operand's
// value as the call's, and a `dbg` in a compiled-in body prints its own
// line.
func TestInline_AnAssertionAndADbgSeeTheCompiledInBodyAsACall(t *testing.T) {
	src := `fn double(n: Int) -> Int
    dbg n
    n * 2
end

fn quad(n: Int) -> Int
    double(n) * 2
end

test "fails"
    assert quad(1) == 5
end
`
	report := func() string {
		p, err := vmhost.LoadSource("main", src)
		if err != nil {
			t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
		}
		listing, err := p.Disassemble("quad")
		if err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		p.SetErrorOutput(&errOut)
		var b strings.Builder
		for _, r := range p.Cases(&out, vmhost.TestOptions{}) {
			fmt.Fprintf(&b, "%s: %s\n", r.Name, vmhost.FormatFailure(r.Err))
		}
		return b.String() + out.String() + errOut.String() + fmt.Sprint(strings.Contains(listing, "compiled in"))
	}
	with := report()
	t.Setenv(vm.NoInlineEnv, "1")
	without := report()
	if with != strings.TrimSuffix(without, "false")+"true" {
		t.Fatalf("compiled in:\n%s\nas calls:\n%s", with, without)
	}
	if !strings.Contains(with, "line 11: assertion failed") || !strings.Contains(with, "= 4") || !strings.Contains(with, "1") {
		t.Fatalf("the report does not show the failure and double's value:\n%s", with)
	}
}

// The call depth counts a body compiled in as the activation it stands
// for, so recursion through one reaches the limit at the same call.
func TestInline_TheCallDepthCountsACompiledInBody(t *testing.T) {
	got := inlineBothWays(t, `import std::io

fn down(n: Int) -> Int
    1 + step(n)
end

fn step(n: Int) -> Int
    down(n + 1) * 1
end

fn main()
    down(0) |> io.print()
end
`, []string{"down"})
	if want := "failure: " + rt.CallDepthError(4).Error() + "\n"; got != want {
		t.Fatalf("transcript %q, want %q", got, want)
	}
}

// A body compiled in reads the app fields in force at its call, as the
// callee's activation does, a `with` in the caller's block included.
func TestInline_ACompiledInBodyReadsTheCallersAppFields(t *testing.T) {
	got := inlineBothWays(t, `import std::io

struct App
    greeting: String
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{greeting: "hi"})
end

fn greet(name: String) -> String
    "${App.greeting}, ${name}"
end

fn main()
    io.print("${greet("Ada")}!")
    with App.greeting = "hey"
    io.print("${greet("Ada")}!")
end
`, []string{"main"})
	if got != "hi, Ada!\nhey, Ada!\n" {
		t.Fatalf("transcript:\n%s", got)
	}
}

// A value handed to a body compiled in is shared as the call would share
// it: the body's update copies what the caller still reads, and one the
// caller hands over at its last use is written in place.
func TestInline_ACompiledInUpdateCopiesWhatTheCallerKeeps(t *testing.T) {
	got := inlineBothWays(t, `import std::io

struct P
    x: Int
    y: Int
end

fn bump(p: P) -> P
    {..p, x: p.x + 1}
end

fn main()
    p = P{x: 1, y: 2}
    q = bump(p)
    r = bump(bump(q))
    io.print("${p.x} ${q.x} ${r.x} ${p.y}")
end
`, []string{"main"})
	if got != "1 2 4 2\n" {
		t.Fatalf("transcript:\n%s", got)
	}
	p, err := vmhost.LoadSource("main", fmt.Sprintf(ownedStep, 10))
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.Disassemble("run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "step, compiled in") {
		t.Fatalf("run does not compile step in, so TestInPlace_AnOwnedParameterIsUpdatedInPlace "+
			"does not cover an update in place in a compiled-in body:\n%s", listing)
	}
}

// A debug machine compiles no call into its caller, so a breakpoint in the
// callee stops there and every step is the function's own.
func TestInline_ADebugMachineCompilesNoCallIn(t *testing.T) {
	src := `import std::io

fn plus(a: Int, b: Int) -> Int
    a + b
end

fn main()
    io.print("${plus(1, 2) * 2}")
end
`
	plain, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	debug, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	debug.SetDebugger(vm.NewDebugger())
	want, err := plain.Disassemble("main")
	if err != nil {
		t.Fatal(err)
	}
	got, err := debug.Disassemble("main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(want, "plus, compiled in") {
		t.Fatalf("a plain machine does not compile plus in, so this tests nothing:\n%s", want)
	}
	if strings.Contains(got, "compiled in") {
		t.Fatalf("a debug machine compiled a call in:\n%s", got)
	}
}

// A test case stopped inside a body compiled into its caller shows the
// stack the calls would have: the callee's frame, then the caller's.
func TestInline_AStoppedCaseShowsTheCompiledInFunction(t *testing.T) {
	src := `fn spin(n: Int) -> Int
    spin(n + 1)
end

fn wrap(n: Int) -> Int
    spin(n) + 1
end

fn mid(n: Int) -> Int
    spin(n)
end

fn outer(n: Int) -> Int
    mid(n) + 1
end

test "spins through a body compiled into the test"
    assert wrap(0) == 1
end

test "spins through a tail call compiled in"
    assert outer(0) == 1
end
`
	stack := func() string {
		p, err := vmhost.Load(writeTempProgram(t, src))
		if err != nil {
			t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
		}
		p.SetTestTimeout(100*time.Millisecond, "")
		var b strings.Builder
		if listing, err := p.Disassemble("outer"); err != nil || !strings.Contains(listing, "switch") {
			if os.Getenv(vm.NoInlineEnv) == "" {
				t.Fatalf("outer does not compile mid's tail call in (%v):\n%s", err, listing)
			}
		}
		for _, r := range p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}) {
			var timedOut *vmhost.TimedOut
			if !errors.As(r.Err, &timedOut) {
				t.Fatalf("%s: want a TimedOut failure, got %v", r.Name, r.Err)
			}
			fmt.Fprintf(&b, "line %d:", timedOut.Line)
			for _, f := range timedOut.Stack {
				fmt.Fprintf(&b, " %s@%d", f.Name, f.Pos.Line())
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	with := stack()
	t.Setenv(vm.NoInlineEnv, "1")
	without := stack()
	if with != without {
		t.Fatalf("compiled in: %s\nas calls:    %s", with, without)
	}
	if !strings.Contains(with, "spin@2 wrap@6 ") || !strings.Contains(with, "spin@2 outer@14 ") {
		t.Fatalf("the stack lacks spin under wrap: %s", with)
	}
}

// writeTempProgram writes src to main.nomi in a directory of its own.
func writeTempProgram(t *testing.T, src string) string {
	t.Helper()
	path := t.TempDir() + "/main.nomi"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// orNeg takes apart a Maybe it receives; down hands it the one a crossing
// answers, so with orNeg compiled in the Maybe fuses in down and no Some
// is built, one activation of down after another.
const orNegLoop = `import std::io

fn or_neg(m: Maybe<Int>) -> Int
    case m
        Some(c) -> c
        None    -> -1
    end
end

fn down(n: Int, data: Bytes) -> Int
    if n < 1 then 0 else down(n - 1, data) + or_neg(Bytes.int8_at(data, 0))
end

fn main()
    down(%d, Bytes.from_list([4, 0])) |> io.print()
end
`

// A Maybe handed to a body compiled in that only takes it apart is kept in
// the caller's registers: the Some is never built.
func TestInline_AMaybeACompiledInBodyTakesApartIsNotBuilt(t *testing.T) {
	p, err := vmhost.LoadSource("main", fmt.Sprintf(orNegLoop, 3))
	if err != nil {
		t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
	}
	if listing, err := p.Disassemble("down"); err != nil || !strings.Contains(listing, "or_neg, compiled in") ||
		!strings.Contains(listing, "hostm") {
		t.Fatalf("down does not compile or_neg in over a fused crossing (%v):\n%s", err, listing)
	}
	if per := allocsPerActivation(t, orNegLoop); per > 0.1 {
		t.Errorf("a Maybe taken apart in a compiled-in body allocates %.2f times an activation, want 0", per)
	}
	t.Setenv(vm.NoInlineEnv, "1")
	if per := allocsPerActivation(t, orNegLoop); per < 0.9 {
		t.Errorf("handed to a call, the Maybe allocates %.2f times an activation, want 1 or more", per)
	}
}

// first and down each call an intrinsic that is handed the machine
// (Map.put, Map.size): first as a body compiled into down, so the
// activation crosses at the view's depth and then at its own, every
// activation.
const crossingsAtTwoDepths = `import std::io

fn first(m: Map<Int, Int>) -> Int
    Map.size(Map.put(m, 3, 3))
end

fn down(n: Int, m: Map<Int, Int>) -> Int
    if n < 1 then 0 else first(m) + Map.size(Map.put(m, 4, 4)) + down(n - 1, m) * 0
end

fn main()
    down(%d, {1 => 1, 2 => 2}) |> io.print()
end
`

// An intrinsic a body compiled in calls is handed the machine the stack
// keeps for crossings at the view's depth, the one the callee's own
// activation would be handed (boundAt): crossing at the view's depth and
// at the caller's in turn copies no machine onto the heap.
func TestInline_CrossingsInACompiledInBodyAllocateAsTheCallsWould(t *testing.T) {
	p, err := vmhost.LoadSource("main", fmt.Sprintf(crossingsAtTwoDepths, 3))
	if err != nil {
		t.Fatalf("the front end rejects this, so it tests nothing: %v", err)
	}
	if listing, err := p.Disassemble("down"); err != nil || !strings.Contains(listing, "first, compiled in") {
		t.Fatalf("down does not compile first in (%v):\n%s", err, listing)
	}
	with := bytesPerActivation(t, crossingsAtTwoDepths)
	t.Setenv(vm.NoInlineEnv, "1")
	without := bytesPerActivation(t, crossingsAtTwoDepths)
	// The two Map.put results are the same either way; a copy of the
	// machine is hundreds of bytes.
	if with > without+32 {
		t.Errorf("an activation allocates %.0f bytes with first compiled in and %.0f as calls", with, without)
	}
}

// bytesPerActivation is allocsPerActivation in bytes: the bytes one
// activation of down allocates in src, a program whose main calls down(N),
// from the difference between two sizes.
func bytesPerActivation(t *testing.T, src string) float64 {
	t.Helper()
	const low, high, runs = 50, 200, 20
	measure := func(n int) float64 {
		p := depthProgram(t, fmt.Sprintf(src, n))
		var out bytes.Buffer
		run := func() {
			out.Reset()
			if err := p.Run(context.Background(), &out, nil, false); err != nil {
				t.Fatalf("down(%d): %v", n, err)
			}
		}
		run()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range runs {
			run()
		}
		runtime.ReadMemStats(&after)
		return float64(after.TotalAlloc-before.TotalAlloc) / runs
	}
	return (measure(high) - measure(low)) / (high - low)
}

// pick takes apart the Maybe second answers, so the body compiled into
// pick returns it in parts and leaves the machine's loop, and pick goes on
// to a tail call of report, a function whose `defer` needs exit hooks.
// main calls pick through a value, so pick runs as an activation of its
// own rather than compiled into main.
const tailIntoExitHooksAfterAView = `import std::io

fn second(v: Vector<Int>) -> Maybe<Int>
    x = Vector.get(v, 1)?
    Some(x * 10)
end

fn report(n: Int) -> Int
    defer io.print("reported")
    n
end

fn pick(v: Vector<Int>) -> Int
    case second(v)
        Some(x) -> report(x)
        None    -> report(-1)
    end
end

fn main()
    f = pick
    io.print("${f(Vector[1])} ${f(Vector[1, 2])}")
end
`

// A tail call the caller makes after a body compiled into it left the
// machine's loop enters a function that needs exit hooks with them
// installed, as it does after a call.
func TestInline_ATailCallAfterACompiledInBodyInstallsExitHooks(t *testing.T) {
	got := inlineBothWays(t, tailIntoExitHooksAfterAView, []string{"pick"})
	if got != "reported\nreported\n-1 20\n" {
		t.Fatalf("transcript:\n%s", got)
	}
}

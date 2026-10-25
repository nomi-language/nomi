package vmhost_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// timeoutProgram's cases never finish, each in its own way, except the last.
const timeoutProgram = `import {
    std/duration.Duration
    std/tasks.Task
    std/testing.Clock
    std/timer
}

fn spin(n: Int) -> Int {
    spin(n + 1)
}

fn never_negative() -> Maybe<Int> {
    Iter.iterate(0, &(&1 + 1)) |> Iter.find(&(&1 < 0))
}

test "spins" {
    assert spin(0) == 1
}

test "loops inside the standard library" {
    assert never_negative() == None
}

test "tasks spin" {
    total = concurrent {
        a = Task.spawn(fn -> spin(0))
        b = Task.spawn(fn -> spin(1))
        Task.await(a) + Task.await(b)
    }
    assert total == 0
}

test "sleeps" {
    timer.sleep(Duration.seconds(60))
    assert 1 == 1
}

group "virtual" {
    clock Clock.Virtual

    test "spins in a bubble" {
        assert spin(0) == 1
    }
}

test "runs after" {
    assert 1 == 1
}

fn through_std() -> Maybe<Int> {
    Maybe.map(Some(0), &spin(&1))
}

test "loops through a standard library call" {
    assert through_std() == None
}
`

// A case that does not finish within its limit is stopped and fails with
// where it was running: the innermost line of the case's own file outside
// the stdlib, then the stack. Spinning tasks stop with it, a case blocked in
// a wait wakes, a case in a virtual-time bubble stops in real time, and the
// next case runs as usual.
func TestTestTimeout_StopsEachCaseThatDoesNotFinishAndRunsTheRest(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, timeoutProgram))
	if err != nil {
		t.Fatalf("the front end rejects this, so it cannot test the limit: %v", err)
	}
	p.SetTestTimeout(200*time.Millisecond, "the hint")
	before := runtime.NumGoroutine()
	results := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{})

	want := map[string]struct {
		line  int
		inner string // the innermost frame's function
	}{
		"spins":                                 {9, "spin"},
		"loops inside the standard library":     {13, "never_negative"},
		"tasks spin":                            {9, "spin"},
		"sleeps":                                {34, "sleeps"},
		"virtual / spins in a bubble":           {9, "spin"},
		"loops through a standard library call": {9, "spin"},
	}
	for _, r := range results {
		if r.Blocked != nil {
			t.Fatalf("%s: blocked, not timed out: %v", r.Name, r.Blocked)
		}
		if r.Name == "runs after" {
			if r.Err != nil {
				t.Errorf("the case after the stopped ones failed: %v", r.Err)
			}
			continue
		}
		w, ok := want[r.Name]
		if !ok {
			t.Fatalf("unexpected case %q", r.Name)
		}
		var timedOut *vmhost.TimedOut
		if !errors.As(r.Err, &timedOut) {
			t.Errorf("%s: want a TimedOut failure, got %v", r.Name, r.Err)
			continue
		}
		if timedOut.Limit != 200*time.Millisecond || timedOut.Line != w.line {
			t.Errorf("%s: limit %s at line %d, want 200ms at line %d", r.Name, timedOut.Limit, timedOut.Line, w.line)
		}
		if len(timedOut.Stack) == 0 || timedOut.Stack[0].Name != w.inner {
			t.Errorf("%s: want the stack to start in %s, got %+v", r.Name, w.inner, timedOut.Stack)
		}
		if outer := timedOut.Stack[len(timedOut.Stack)-1]; !strings.HasSuffix(r.Name, outer.Name) {
			t.Errorf("%s: want the stack to end in the case's body, got %s", r.Name, outer.Name)
		}
		if r.Name == "loops through a standard library call" {
			// spin, the capture, Maybe.map in the stdlib, then through_std's
			// tail call into it, whose activation Maybe.map replaced.
			if len(timedOut.Stack) != 5 || !timedOut.Stack[2].Pos.Stdlib() ||
				timedOut.Stack[3].Name != "" || timedOut.Stack[3].Pos.Line() != 51 {
				t.Errorf("%s: stack %+v", r.Name, timedOut.Stack)
			}
		}
		msg := r.Err.Error()
		head := fmt.Sprintf("line %d: did not finish within 200ms, so it was stopped\nit was running:\n", w.line)
		if !strings.HasPrefix(msg, head) || !strings.HasSuffix(msg, "\nhelp: the hint") {
			t.Errorf("%s: message\n%s", r.Name, msg)
		}
	}
	if len(results) != len(want)+1 {
		t.Fatalf("got %d results, want %d", len(results), len(want)+1)
	}

	// The spinning tasks and the sleeping case left nothing running.
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines still running after the cases, %d before", n, before)
	}
}

// The JSON record of a timed-out case is a failure at its line, so an
// editor shows it as it shows a failed assertion.
func TestTestTimeout_TheJSONRecordIsAFailureAtTheLine(t *testing.T) {
	path := writeProgram(t, timeoutProgram)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("the front end rejects this: %v", err)
	}
	p.SetTestTimeout(100*time.Millisecond, "")
	var out bytes.Buffer
	rep := vmhost.NewTestReportFormat(&out, vmhost.TestFormatJSON)
	p.Test(&bytes.Buffer{}, rep, path, vmhost.TestOptions{Line: 16, LineSet: true}, func(name string) string { return name })
	var rec struct {
		Status    string `json:"status"`
		Message   string `json:"message"`
		ErrorLine int    `json:"error_line"`
		Line      int    `json:"line"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(out.String(), "\n", 2)[0]), &rec); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if rec.Status != "failed" || rec.Line != 16 || rec.ErrorLine != 9 ||
		!strings.HasPrefix(rec.Message, "  line 9: did not finish within 100ms, so it was stopped\n  it was running:\n    ") {
		t.Errorf("record: %+v", rec)
	}
}

// Zero is no limit, and so is a program never given one: a case that sleeps
// past what a limit would allow passes.
func TestTestTimeout_ZeroIsNoLimit(t *testing.T) {
	src := `import {
    std/duration.Duration
    std/timer
}

test "naps" {
    timer.sleep(Duration.milliseconds(150))
    assert 1 == 1
}
`
	for _, limit := range []time.Duration{-1, 0} {
		p, err := vmhost.Load(writeProgram(t, src))
		if err != nil {
			t.Fatalf("the front end rejects this: %v", err)
		}
		if limit >= 0 {
			p.SetTestTimeout(limit, "")
		}
		for _, r := range p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}) {
			if r.Err != nil || r.Blocked != nil {
				t.Errorf("limit %s: %s: %v %v", limit, r.Name, r.Err, r.Blocked)
			}
		}
	}
	// The same case under a limit it outlives fails.
	p, err := vmhost.Load(writeProgram(t, src))
	if err != nil {
		t.Fatal(err)
	}
	p.SetTestTimeout(50*time.Millisecond, "")
	var timedOut *vmhost.TimedOut
	if r := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}); !errors.As(r[0].Err, &timedOut) {
		t.Errorf("under 50ms: %v", r[0].Err)
	}
}

// A program with a debugger runs its cases with no limit, since a paused
// case must not time out (`nomi dap`).
func TestTestTimeout_ADebuggerRunsWithoutALimit(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `import {
    std/duration.Duration
    std/timer
}

test "naps" {
    timer.sleep(Duration.milliseconds(150))
    assert 1 == 1
}
`))
	if err != nil {
		t.Fatalf("the front end rejects this: %v", err)
	}
	p.SetDebugger(vm.NewDebugger())
	p.SetTestTimeout(20*time.Millisecond, "")
	for _, r := range p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}) {
		if r.Err != nil || r.Blocked != nil {
			t.Errorf("%s: %v %v", r.Name, r.Err, r.Blocked)
		}
	}
}

// What a limit costs a case that finishes is a fixed amount per case (its
// timer, its context and its machine view), never an amount per step: the
// stack is built only by a stop's unwind, and a step reads one flag. So the
// allocations a limit adds are the same for a case that takes a thousand
// steps and one that takes a hundred thousand.
func TestTestTimeout_ALimitCostsACaseThatFinishesNothingPerStep(t *testing.T) {
	added := func(steps int) float64 {
		src := fmt.Sprintf(`fn count(n: Int) -> Int {
    if n == 0 {
        0
    } else {
        count(n - 1)
    }
}

test "counts" {
    assert count(%d) == 0
}
`, steps)
		allocs := func(limit time.Duration) float64 {
			p, err := vmhost.Load(writeProgram(t, src))
			if err != nil {
				t.Fatalf("the front end rejects this: %v", err)
			}
			p.SetTestTimeout(limit, "")
			return testing.AllocsPerRun(20, func() {
				if r := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{}); r[0].Err != nil {
					t.Fatal(r[0].Err)
				}
			})
		}
		return allocs(time.Minute) - allocs(0)
	}
	small, large := added(1000), added(100000)
	t.Logf("added %.0f and %.0f", small, large)
	if large-small > 2 || small-large > 2 {
		t.Errorf("a limit adds %.0f allocations to a case of 1000 steps and %.0f to one of 100000", small, large)
	}
}

// A deep stack shows its eight innermost frames and its two outermost, with
// a count of the rest between them. down(0)'s tail call replaced its frame
// with spin's, so there are 22: spin, twenty downs and the test.
func TestTestTimeout_ALongStackShowsItsEnds(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, `fn spin(n: Int) -> Int {
    spin(n + 1)
}

fn down(n: Int) -> Int {
    if n == 0 {
        spin(0)
    } else {
        1 + down(n - 1)
    }
}

test "goes down" {
    assert down(20) == 0
}
`))
	if err != nil {
		t.Fatalf("the front end rejects this: %v", err)
	}
	p.SetTestTimeout(100*time.Millisecond, "")
	r := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{})[0]
	var timedOut *vmhost.TimedOut
	if !errors.As(r.Err, &timedOut) || len(timedOut.Stack) != 22 {
		t.Fatalf("want 22 frames, got %v", r.Err)
	}
	lines := strings.Split(r.Err.Error(), "\n")
	if len(lines) != 2+8+1+2 || lines[10] != "  ... 12 more" ||
		!strings.HasSuffix(lines[11], ":9 in down") || !strings.HasSuffix(lines[12], ":14 in goes down") {
		t.Errorf("message:\n%s", r.Err)
	}
}

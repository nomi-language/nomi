package vmhost_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// These tests hold what an embedder's host table can do through the public
// API alone (vmhost.WithHosts, hostadapt.Env and rt.Frame): call back into a
// Nomi function value it was passed, read the calling task's deadline and
// cancellation, answer a sibling file's `host fn` by its key, and answer one
// whose defaulted parameter Nomi filled. Each also checks how a host's list
// names the crossing: `host:<key>`.

// loadRefused loads the program at path with opts and answers the
// *EffectError the load refuses it with. The load must refuse.
func loadRefused(t *testing.T, path string, opts ...vmhost.Option) *vmhost.EffectError {
	t.Helper()
	_, err := vmhost.Load(path, opts...)
	if err == nil {
		t.Fatalf("the load admits a program its list does not allow: %s", path)
	}
	var ce *vmhost.EffectError
	if !errors.As(err, &ce) {
		t.Fatalf("the load fails with %T, not a *EffectError: %v", err, err)
	}
	return ce
}

// relativeFirstLine is the first line of err's text with the directory of
// entry left out of its paths.
func relativeFirstLine(err error, entry string) string {
	return filepath.ToSlash(strings.ReplaceAll(firstLine(err), filepath.Dir(entry)+string(filepath.Separator), ""))
}

// writeHostProject writes files under a fresh directory with a nomi.toml,
// and answers main.nomi's path.
func writeHostProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["nomi.toml"] = "[module]\nname = \"app\"\n"
	writeFiles(t, dir, files)
	return filepath.Join(dir, "main.nomi")
}

const callbackProgram = `host fn apply_twice(s: String, f: (String) -> Result<String, String>) -> Result<String, String>

pub fn exclaim(s: String) -> Result<String, String>
    apply_twice(s, fn x -> Ok("${x}!"))
end

pub fn refuse_second(s: String) -> Result<String, String>
    apply_twice(s, fn x -> if String.ends_with(x, "!") then Err("once is enough") else Ok("${x}!"))
end

pub fn parse_twice(s: String) -> Result<String, String>
    apply_twice(s, fn x -> Ok(Int.to_string(String.to_int(x)! + 1)))
end
`

// callbackTable answers apply_twice: it calls the function value it was
// passed through env.Call, on the frame of its own call, and calls it again
// with the first answer unless that answer is an Err. calls counts the
// callbacks, and failed holds the error a failed one returned.
type callbackTable struct {
	mu     sync.Mutex
	calls  int
	failed error
}

func (c *callbackTable) table(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
	return map[string]hostadapt.Func{
		"apply_twice": func(fr *rt.Frame, args []rt.Value) (rt.Value, error) {
			s, f := args[0], args[1]
			var last rt.Value
			for range 2 {
				c.mu.Lock()
				c.calls++
				c.mu.Unlock()
				res, err := env.Call(fr, f, []rt.Value{s})
				if err != nil {
					c.mu.Lock()
					c.failed = err
					c.mu.Unlock()
					return nil, err
				}
				r, tag, err := hostadapt.Variant(res, "Result")
				if err != nil {
					return nil, err
				}
				if tag == "Err" {
					return res, nil
				}
				last, s = res, hostadapt.Payload(r)
			}
			return last, nil
		},
	}, nil
}

// A host function calls back into the Nomi closure it was passed, on its own
// call's frame, and answers with what the closure answered. An Err the
// closure answers is data, not a failure: the host reads it and answers it.
func TestHosts_ACallbackRunsTheClosureItWasPassed(t *testing.T) {
	cb := &callbackTable{}
	p, err := vmhost.LoadSource("main", callbackProgram, vmhost.WithHosts(cb.table))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, err := p.Call(ctx, "exclaim", "ha")
	if err != nil {
		t.Fatal(err)
	}
	if text := rt.DisplayText(got); text != `Ok("ha!!")` {
		t.Errorf("exclaim(\"ha\") = %s, want Ok(\"ha!!\")", text)
	}
	if cb.calls != 2 {
		t.Errorf("the host called back %d times, want 2", cb.calls)
	}

	cb.calls = 0
	got, err = p.Call(ctx, "refuse_second", "ha")
	if err != nil {
		t.Fatal(err)
	}
	if text := rt.DisplayText(got); text != `Err("once is enough")` {
		t.Errorf("refuse_second(\"ha\") = %s, want Err(\"once is enough\")", text)
	}
	if cb.calls != 2 || cb.failed != nil {
		t.Errorf("the host called back %d times and saw failure %v, want 2 and none", cb.calls, cb.failed)
	}
}

// A closure that traps fails the callback: env.Call answers the trap as its
// error, and the host function that returns it fails the call that crossed
// into it, as a trap in Nomi code would. The host calls back no more.
func TestHosts_ACallbackThatTrapsFailsTheHostCall(t *testing.T) {
	cb := &callbackTable{}
	p, err := vmhost.LoadSource("main", callbackProgram, vmhost.WithHosts(cb.table))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(context.Background(), "parse_twice", "41")
	if err != nil || rt.DisplayText(got) != `Ok("43")` {
		t.Fatalf("parse_twice(\"41\") = %v, %v; want Ok(\"43\")", got, err)
	}

	cb.calls, cb.failed = 0, nil
	got, err = p.Call(context.Background(), "parse_twice", "forty")
	if err == nil {
		t.Fatalf("parse_twice(\"forty\") answered %s: the closure's trap did not fail the call", rt.DisplayText(got))
	}
	if cb.failed == nil || cb.calls != 1 {
		t.Fatalf("the host called back %d times and saw failure %v, want one failed callback", cb.calls, cb.failed)
	}
	const trap = "line 12: `String.to_int(x)` found nothing"
	if cb.failed.Error() != trap {
		t.Errorf("env.Call failed with %q, want the closure's trap %q", cb.failed, trap)
	}
	if err.Error() != trap {
		t.Errorf("the call failed with %q, want the closure's trap %q", err, trap)
	}
}

// The crossing is named host:apply_twice in a host's list. The closure's
// own effects count where the closure is made, though only the host calls
// it.
func TestHosts_ACallbackIsHeldToTheHostsList(t *testing.T) {
	cb := &callbackTable{}
	if _, err := vmhost.LoadSource("main", callbackProgram, vmhost.WithHosts(cb.table), vmhost.WithEffects("host:apply_twice")); err != nil {
		t.Fatalf("the host allows its function, and the load refuses: %v", err)
	}
	ce := refusedByHost(t, callbackProgram, vmhost.WithHosts(cb.table), vmhost.WithEffects())
	if got, want := firstLine(ce), "main.nomi:1:9: this program has the effect host:apply_twice, and the host allows no effects"; got != want {
		t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, want)
	}

	printing := "import std::io\n\n" + strings.Replace(callbackProgram, `fn x -> Ok("${x}!")`, "fn x ->\n        io.print(x)\n        Ok(\"${x}!\")\n    end", 1)
	if _, err := vmhost.LoadSource("main", printing, vmhost.WithHosts(cb.table), vmhost.WithEffects("host:apply_twice", "std::io")); err != nil {
		t.Fatalf("the host allows the closure's effect too, and the load refuses: %v", err)
	}
	ce = refusedByHost(t, printing, vmhost.WithHosts(cb.table), vmhost.WithEffects("host:apply_twice"))
	if got, want := firstLine(ce), "main.nomi:7:9: this program has the effect std::io, and the host allows only host:apply_twice"; got != want {
		t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, want)
	}
	if !strings.Contains(ce.Error(), "`exclaim` makes a lambda, which counts whether or not it is called") {
		t.Errorf("the refusal does not say the lambda counts where it is made:\n%s", ce)
	}
}

const contextProgram = `import
    std::duration.Duration
    std::tasks.{Outcome, Task}
end

host fn has_deadline() -> Bool

host fn wait_for_cancel(limit_ms: Int) -> String

host fn wait_or_stop(limit_ms: Int) -> String

host fn await_waiting() -> Unit

pub struct App
    context: Context
end

pub fn boot() -> Result<App, String>
    Ok(App{context: Context.root()})
end

fn main() end

pub fn unbounded(limit_ms: Int) -> (Bool, String)
    (has_deadline(), wait_for_cancel(limit_ms))
end

pub fn bounded(limit_ms: Int) -> (Bool, String)
    with App.context = Context.with_timeout(App.context, Duration.milliseconds(50))
    (has_deadline(), wait_for_cancel(limit_ms))
end

pub fn stopped(limit_ms: Int) -> String
    wait_or_stop(limit_ms)
end

pub fn cancel_waiting() -> (String, String)
    concurrent
        returns = Task.spawn(fn -> wait_for_cancel(60000))
        await_waiting()
        Task.cancel(returns)
        stops = Task.spawn(fn -> wait_or_stop(60000))
        await_waiting()
        Task.cancel(stops)
        (ending(Task.outcome(returns)), ending(Task.outcome(stops)))
    end
end

fn ending(outcome: Outcome<String>) -> String
    case outcome
        Outcome.Completed(text) -> "completed: ${text}"
        Outcome.Cancelled       -> "cancelled"
        Outcome.Failed(_)       -> "failed"
    end
end
`

// contextTable answers the context program's host functions from the
// calling frame's Go context (rt.Frame.Context). waiting receives once per
// wait that has started, and ended the context error each wait saw, or nil
// when its limit passed first. wait_for_cancel answers the context's end as
// a value; wait_or_stop stops its task as the context ends
// (rt.CancelIfDone), as std's own waits do.
type contextTable struct {
	waiting chan struct{}
	ended   chan error
}

func newContextTable() *contextTable {
	return &contextTable{waiting: make(chan struct{}, 4), ended: make(chan error, 4)}
}

func (c *contextTable) table(*hostadapt.Env) (map[string]hostadapt.Func, error) {
	return map[string]hostadapt.Func{
		"has_deadline": func(fr *rt.Frame, _ []rt.Value) (rt.Value, error) {
			_, ok := fr.Context().Deadline()
			return ok, nil
		},
		"wait_for_cancel": func(fr *rt.Frame, args []rt.Value) (rt.Value, error) {
			ctx := fr.Context()
			c.waiting <- struct{}{}
			select {
			case <-ctx.Done():
				c.ended <- ctx.Err()
				return ctx.Err().Error(), nil
			case <-time.After(time.Duration(args[0].(int64)) * time.Millisecond):
				c.ended <- nil
				return "the context did not end", nil
			}
		},
		"wait_or_stop": func(fr *rt.Frame, args []rt.Value) (rt.Value, error) {
			c.waiting <- struct{}{}
			if rt.SleepCtx(fr.Context(), rt.Duration(args[0].(int64))*rt.Duration(time.Millisecond)) {
				c.ended <- nil
				return "the context did not end", nil
			}
			c.ended <- fr.Context().Err()
			rt.CancelIfDone(fr)
			return "the task went on", nil
		},
		"await_waiting": func(fr *rt.Frame, _ []rt.Value) (rt.Value, error) {
			select {
			case <-c.waiting:
				return rt.Unit{}, nil
			case <-fr.Context().Done():
				return nil, fr.Context().Err()
			}
		},
	}, nil
}

// A host function reads the calling task's context off its frame: no
// deadline outside a bounded region, the deadline of the Context in force
// inside `with App.context = Context.with_timeout(...)`, which ends a wait.
func TestHosts_TheTasksDeadlineReachesTheHost(t *testing.T) {
	c := newContextTable()
	p, err := vmhost.LoadSource("main", contextProgram, vmhost.WithHosts(c.table))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, err := p.Call(ctx, "unbounded", 10)
	if err != nil || rt.DisplayText(got) != `(False, "the context did not end")` {
		t.Fatalf("unbounded(10) = %v, %v", got, err)
	}
	<-c.waiting
	if e := <-c.ended; e != nil {
		t.Errorf("an unbounded wait ended with %v", e)
	}
	got, err = p.Call(ctx, "bounded", 60000)
	if err != nil || rt.DisplayText(got) != `(True, "context deadline exceeded")` {
		t.Fatalf("bounded(60000) = %v, %v", got, err)
	}
	<-c.waiting
	if e := <-c.ended; !errors.Is(e, context.DeadlineExceeded) {
		t.Errorf("a bounded wait ended with %v, want the deadline", e)
	}
}

// A host function that waits on its frame's context sees it end when its
// task is cancelled, by Task.cancel in Nomi or by the context the embedder
// passed to Call. What the task does then is the host function's choice: one
// that returns a value lets the task complete with it, and one that calls
// rt.CancelIfDone stops the task as a cancelled std wait does, which
// Task.outcome reads as Cancelled and which fails a Call.
func TestHosts_ACancelledTaskEndsTheHostsWait(t *testing.T) {
	c := newContextTable()
	p, err := vmhost.LoadSource("main", contextProgram, vmhost.WithHosts(c.table))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(context.Background(), "cancel_waiting")
	if err != nil {
		t.Fatal(err)
	}
	if text, want := rt.DisplayText(got), `("completed: context canceled", "cancelled")`; text != want {
		t.Errorf("cancel_waiting() = %s, want %s", text, want)
	}
	for range 2 {
		if e := <-c.ended; !errors.Is(e, context.Canceled) {
			t.Errorf("a cancelled task's wait ended with %v, want cancellation", e)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-c.waiting
		cancel()
	}()
	got, err = p.Call(ctx, "unbounded", 60000)
	if text, want := rt.DisplayText(got), `(False, "context canceled")`; err != nil || text != want {
		t.Errorf("unbounded(60000) under a cancelled Call = %s, %v; want %s", text, err, want)
	}
	if e := <-c.ended; !errors.Is(e, context.Canceled) {
		t.Errorf("the wait under a cancelled Call ended with %v, want cancellation", e)
	}
	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		<-c.waiting
		cancel()
	}()
	if got, err = p.Call(ctx, "stopped", 60000); !errors.Is(err, context.Canceled) {
		t.Errorf("stopped(60000) under a cancelled Call = %s, %v; want context.Canceled", rt.DisplayText(got), err)
	}
	if e := <-c.ended; !errors.Is(e, context.Canceled) {
		t.Errorf("the wait under a cancelled Call ended with %v, want cancellation", e)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got, err = p.Call(ctx, "stopped", 60000); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("stopped(60000) under a Call's deadline = %s, %v; want context.DeadlineExceeded", rt.DisplayText(got), err)
	}
	<-c.waiting
	if e := <-c.ended; !errors.Is(e, context.DeadlineExceeded) {
		t.Errorf("the wait under a Call's deadline ended with %v, want the deadline", e)
	}
}

// A Call whose context ends while the program waits in std's timer.sleep
// answers the context's error, cancelled or past its deadline, even under
// a deadline of the program's own that has not run out.
func TestHosts_ACancelledCallEndsATimersWait(t *testing.T) {
	const src = `import
    std::duration.Duration
    std::timer
end

pub struct App
    context: Context
end

pub fn boot() -> Result<App, String>
    Ok(App{context: Context.root()})
end

fn main() end

pub fn sleeps(limit_ms: Int) -> Unit
    timer.sleep(Duration.milliseconds(limit_ms))
end

pub fn sleeps_bounded(limit_ms: Int) -> Unit
    with App.context = Context.with_timeout(App.context, Duration.seconds(60))
    timer.sleep(Duration.milliseconds(limit_ms))
end
`
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sleeps", "sleeps_bounded"} {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(20*time.Millisecond, cancel)
		if got, err := p.Call(ctx, name, 60000); !errors.Is(err, context.Canceled) {
			t.Errorf("%s(60000) under a cancelled Call = %v, %v; want context.Canceled", name, got, err)
		}
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
		if got, err := p.Call(ctx, name, 60000); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s(60000) under a Call's deadline = %v, %v; want context.DeadlineExceeded", name, got, err)
		}
		cancel()
	}
	if got, err := p.Call(context.Background(), "sleeps", 1); err != nil {
		t.Errorf("sleeps(1) after the stopped calls = %v, %v; want it to run", got, err)
	}
}

// Each of the context program's crossings is named host: and its key in a
// host's list, beside the std modules the program uses to bound and cancel
// its tasks.
func TestHosts_ContextHostsAreHeldToTheHostsList(t *testing.T) {
	c := newContextTable()
	hosts := []string{"host:has_deadline", "host:wait_for_cancel", "host:wait_or_stop", "host:await_waiting"}
	std := []string{"std::context", "std::tasks"}
	if _, err := vmhost.LoadSource("main", contextProgram, vmhost.WithHosts(c.table), vmhost.WithEffects(append(hosts, std...)...)); err != nil {
		t.Fatalf("the host allows every crossing, and the load refuses: %v", err)
	}
	ce := refusedByHost(t, contextProgram, vmhost.WithHosts(c.table), vmhost.WithEffects(append([]string{"host:has_deadline", "host:wait_or_stop", "host:await_waiting"}, std...)...))
	want := "main.nomi:8:9: this program has the effect host:wait_for_cancel, and the host allows only host:has_deadline, host:wait_or_stop, host:await_waiting, std::context, std::tasks"
	if got := firstLine(ce); got != want {
		t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, want)
	}
}

// A `host fn` in a file below the entry's directory is answered by the
// table under `<path>.<name>`, the file's path within its module without
// `.nomi`, and named host:<path>.<name> in a host's list; the entry's own
// is answered by its bare name.
func TestHosts_ASiblingFilesHostFnIsAnsweredByItsKey(t *testing.T) {
	entry := writeHostProject(t, map[string]string{
		"main.nomi": `import helpers::util

host fn stamp(s: String) -> String

pub fn greet(name: String) -> String
    stamp(util.greet(name))
end
`,
		"helpers/util.nomi": "pub host fn greet(name: String) -> String\n",
	})
	table := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{
			"helpers/util.greet": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				return "hello, " + args[0].(string), nil
			},
			"stamp": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				return "[" + args[0].(string) + "]", nil
			},
		}, nil
	}
	p, err := vmhost.Load(entry, vmhost.WithHosts(table), vmhost.WithEffects("host:helpers/util.greet", "host:stamp"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(context.Background(), "greet", "Ada")
	if err != nil || got != "[hello, Ada]" {
		t.Fatalf("greet(\"Ada\") = %v, %v", got, err)
	}

	ce := loadRefused(t, entry, vmhost.WithHosts(table), vmhost.WithEffects("host:stamp"))
	if got, want := relativeFirstLine(ce, entry), "helpers/util.nomi:1:13: this program has the effect host:helpers/util.greet, and the host allows only host:stamp"; got != want {
		t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, want)
	}
}

// Two files with one base name, a/util.nomi and b/util.nomi, each declare
// `host fn greet`: their keys are a/util.greet and b/util.greet, so a table
// answers each with its own function. A dependency's file is keyed under
// the dependency's name in the program: lib/sub/thing.nomi of the
// dependency lib is lib/sub/thing.ping.
func TestHosts_FilesWithOneBaseNameHaveTheirOwnKeys(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\n\n[dependencies]\nlib = { dir = \"../lib\" }\n",
		"app/main.nomi": `import
    a::util as au
    b::util as bu
    lib::sub::thing
end

pub fn both(name: String) -> String
    "${au.greet(name)} ${bu.greet(name)} ${thing.ping()}"
end
`,
		"app/a/util.nomi":    "pub host fn greet(name: String) -> String\n",
		"app/b/util.nomi":    "pub host fn greet(name: String) -> String\n",
		"lib/nomi.toml":      "[module]\nname = \"lib\"\n",
		"lib/sub/thing.nomi": "pub host fn ping() -> String\n",
	})
	answer := func(s string) hostadapt.Func {
		return func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			if len(args) == 0 {
				return s, nil
			}
			return s + args[0].(string), nil
		}
	}
	table := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{
			"a/util.greet":       answer("a:"),
			"b/util.greet":       answer("b:"),
			"lib/sub/thing.ping": answer("pong"),
		}, nil
	}
	entry := filepath.Join(dir, "app", "main.nomi")
	p, err := vmhost.Load(entry, vmhost.WithHosts(table))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(context.Background(), "both", "Ada")
	if err != nil || got != "a:Ada b:Ada pong" {
		t.Fatalf("both(\"Ada\") = %v, %v; want each file's own function", got, err)
	}

	// A table keyed by the base name answers neither file.
	_, err = vmhost.Load(entry, vmhost.WithHosts(func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{"util.greet": answer("?"), "lib/sub/thing.ping": answer("pong")}, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "a/util.greet") || !strings.Contains(err.Error(), "b/util.greet") {
		t.Fatalf("a table keyed by the base name: %v; want both keys unanswered", err)
	}
}

// A `host fn`'s defaulted parameter is filled in Nomi before the call
// crosses, so the table always receives every argument: a call that leaves
// it out, one that passes it, one that names it, in the declaring file and
// through an import, and a default that reads an earlier parameter.
func TestHosts_ADefaultedParameterIsFilledBeforeTheCrossing(t *testing.T) {
	entry := writeHostProject(t, map[string]string{
		"main.nomi": `import
    text.{self, pad}
end

host fn repeat(s: String, n: Int = 2) -> String

pub fn all() -> List<String>
    [repeat("ab"), repeat("ab", 3), repeat(n: 1, s: "z"), text.pad("x"), pad("xy", width: 5)]
end
`,
		"text.nomi": "pub host fn pad(s: String, width: Int = String.length(s) + 2) -> String\n",
	})
	var mu sync.Mutex
	var seen []int64
	table := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{
			"repeat": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				if len(args) != 2 {
					return nil, hostadapt.Arity("repeat", 2, len(args))
				}
				n := args[1].(int64)
				mu.Lock()
				seen = append(seen, n)
				mu.Unlock()
				return strings.Repeat(args[0].(string), int(n)), nil
			},
			"text.pad": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				if len(args) != 2 {
					return nil, hostadapt.Arity("text.pad", 2, len(args))
				}
				w := args[1].(int64)
				mu.Lock()
				seen = append(seen, w)
				mu.Unlock()
				s := args[0].(string)
				return s + strings.Repeat(".", int(w)-len(s)), nil
			},
		}, nil
	}
	p, err := vmhost.Load(entry, vmhost.WithHosts(table), vmhost.WithEffects("host:repeat", "host:text.pad"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(context.Background(), "all")
	if err != nil {
		t.Fatal(err)
	}
	if text, want := rt.DisplayText(got), `["abab", "ababab", "z", "x..", "xy..."]`; text != want {
		t.Errorf("all() = %s, want %s", text, want)
	}
	if want := []int64{2, 3, 1, 3, 5}; !equalInts(seen, want) {
		t.Errorf("the table received %v, want %v", seen, want)
	}

	ce := loadRefused(t, entry, vmhost.WithHosts(table), vmhost.WithEffects("host:repeat"))
	if got, want := relativeFirstLine(ce, entry), "text.nomi:1:13: this program has the effect host:text.pad, and the host allows only host:repeat"; got != want {
		t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, want)
	}
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// closureAppFieldsHeader is the app and the helpers the programs below
// share: a field to replace, functions that read it, a deadline and a task.
const closureAppFieldsHeader = `import
    std::duration.Duration
    std::io
    std::supervisors.Supervisor
    std::tasks.Task
end

struct App
    label:   String
    context: Context
    workers: Supervisor
end

fn boot() -> Result<App, String>
    Ok(App{label: "base", context: Context.root(), workers: Supervisor.new(max_running: 1)})
end

fn label() -> String
    App.label
end

fn tagged(prefix: String) -> String
    "${prefix} ${App.label}"
end

fn has_deadline() -> Bool
    Context.deadline(App.context) is Some(_)
end

fn bounded() -> Context
    Context.with_timeout(App.context, Duration.seconds(30))
end

fn in_task(body: () -> String) -> String
    concurrent
        Task.await(Task.spawn(body))
    end
end
`

// A function value keeps the app fields in force where it is made and runs
// under them wherever it is called (spec §15 *Closures and tasks*). Each
// program prints what a call reads; under the rule this replaced, a closure
// read the caller's fields, and each want below would read "base" or the
// caller's replacement instead.
func TestClosureAppFields_AFunctionValueKeepsTheFieldsWhereItIsMade(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"the spec's example", `
fn greeter() -> () -> String
    with App.label = "psst"
    io.print(label())
    fn -> label()
end

fn main()
    later = greeter()
    io.print(later())
end
`, "psst\npsst\n"},
		{"made under an override, called after it and under another", `
fn made() -> () -> String
    with App.label = "made"
    fn -> label()
end

fn main()
    read = made()
    io.print(read())
    with App.label = "later"
    io.print(read())
    io.print(label())
end
`, "made\nmade\nlater\n"},
		{"made with no override, called under one", `
fn main()
    read = fn -> label()
    with App.label = "caller"
    io.print(read())
    io.print(label())
end
`, "base\ncaller\n"},
		{"a named function used as a value", `
fn named() -> () -> String
    with App.label = "named"
    label
end

fn main()
    io.print(named()())
end
`, "named\n"},
		{"an Iter pipeline built under an override and consumed after it", `
fn piped() -> Iter<String>
    with App.label = "piped"
    ["a", "b"] |> Iter.map(fn x -> "${x} ${label()}")
end

fn main()
    io.print(piped() |> Iter.to_list())
end
`, "[a piped, b piped]\n"},
		{"a partial and a capture", `
fn partial() -> (String) -> String
    with App.label = "partial"
    &tagged(&1)
end

fn capture() -> (Int) -> String
    with App.label = "capture"
    &"${&1} ${label()}"
end

fn main()
    io.print(partial()("p"))
    io.print(capture()(7))
end
`, "p partial\n7 capture\n"},
		{"a nested fn used as a value", `
fn nested() -> () -> String
    with App.label = "nested"

    fn inner() -> String
        label()
    end

    inner
end

fn main()
    io.print(nested()())
end
`, "nested\n"},
		{"a closure called in tail position", `
fn made() -> () -> String
    with App.label = "made"
    fn -> label()
end

fn call(read: () -> String) -> String
    read()
end

fn main()
    read = made()
    with App.label = "caller"
    io.print(call(read))
end
`, "made\n"},
		{"a task spawned under an override", `
fn main()
    got = begin
        with App.label = "spawned"
        in_task(fn -> label())
    end
    io.print(got)
end
`, "spawned\n"},
		{"a closure passed to a task", `
fn made() -> () -> String
    with App.label = "made"
    fn -> label()
end

fn main()
    body = made()
    with App.label = "spawner"
    io.print(in_task(body))
end
`, "made\n"},
		{"the Context field and its deadline", `
fn bounded_reader() -> () -> Bool
    with App.context = bounded()
    fn -> has_deadline()
end

fn main()
    read = bounded_reader()
    io.print(read())
    io.print(has_deadline())
end
`, "True\nFalse\n"},
		{"a deadline in force at the call still bounds the closure", `
fn main()
    read = fn -> has_deadline()
    with App.context = bounded()
    io.print(read())
end
`, "True\n"},
		{"a defer in a with block calls a closure", `
fn made() -> () -> String
    with App.label = "made"
    fn -> label()
end

fn main()
    read = made()
    begin
        with App.label = "deferring"
        defer io.print("deferred ${read()} ${label()}")
        io.print("body ${read()}")
    end
end
`, "body made\ndeferred made deferring\n"},
		{"a supervised task drops the deadline its body kept", `
fn main()
    with App.context = bounded()
    task = Supervisor.spawn(App.workers, fn -> io.print("supervised ${has_deadline()} ${label()}"))
    Task.await(task)
    io.print("main ${has_deadline()}")
end
`, "supervised False base\nmain True\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runOutput(t, closureAppFieldsHeader+tc.body); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// Where no application exists yet there is nothing to keep, so the body
// runs under the fields where it is called: a closure made while boot runs
// or while a `once` initializer runs. A type's own `impl Iter` is no
// closure: the code consuming the value calls it and passes its own fields.
func TestClosureAppFields_WhatKeepsNothing(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a closure made in boot", `import std::io

struct App
    label: String
    read:  () -> String
end

fn label() -> String
    App.label
end

fn boot() -> Result<App, String>
    Ok(App{label: "base", read: fn -> label()})
end

fn main()
    with App.label = "caller"
    io.print(App.read())
end
`, "caller\n"},
		{"a closure made in a once initializer", `import std::io

struct App
    label: String
end

fn label() -> String
    App.label
end

fn boot() -> Result<App, String>
    Ok(App{label: "base"})
end

once read = fn -> label()

fn main()
    with App.label = "caller"
    io.print(read())
end
`, "caller\n"},
		{"a type's own impl Iter", `import std::io

struct App
    label: String
end

fn boot() -> Result<App, String>
    Ok(App{label: "base"})
end

struct Labels
    items: List<String>
end

impl Iter<String> for Labels
    fn each_while(labels: Labels, yield: (String) -> Bool) -> Bool
        labels.items |> Iter.each_while(fn i -> yield("${i} ${App.label}"))
    end
end

fn source() -> Iter<String>
    with App.label = "source"
    Labels{items: ["a", "b"]}
end

fn main()
    io.print(source() |> Iter.to_list())
end
`, "[a base, b base]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runOutput(t, tc.src); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// A closure a group's setup makes keeps the fields the setup ran under, and
// a test's own `with` does not reach into it.
func TestClosureAppFields_ASetupClosureKeepsTheSetupsFields(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "setup_test.nomi"
	p, err := vmhost.LoadSource(name, `struct App
    label: String
end

fn boot() -> Result<App, String>
    Ok(App{label: "base"})
end

fn label() -> String
    App.label
end

fn main()
end

group "setup closures"
    boot boot()

    setup begin
        fn -> label()
    end

    test "the test's with does not reach the setup's closure", read
        with App.label = "test"
        assert label() == "test"
        assert read() == "base"
    end
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	if out := buf.String(); !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("want the one case to pass:\n%s", out)
	}
}

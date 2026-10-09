package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// runCaptureProgram loads src with input as its standard input and runs main,
// answering its output and its error.
func runCaptureProgram(t *testing.T, src, input string) (string, error) {
	t.Helper()
	p, err := vmhost.LoadSource("main.nomi", src, vmhost.WithInput(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out bytes.Buffer
	err = p.Run(context.Background(), &out, nil, false)
	return out.String(), err
}

// Outside a capture, output goes to Run's writer and `io.read_line` reads the
// program's input; inside, both are the capture's, and the program's input
// is left where it was. `dbg` inside a capture still reaches Run's writer.
func TestCapture_RedirectsOnlyInsideTheCall(t *testing.T) {
	src := `import std/io

fn echo(): Int {
    case io.read_line() {
        Ok(line) -> io.print("got " + line)
        Err(e) -> io.print("err " + e)
    }
    _ = dbg 5
    7
}

fn main() {
    io.print("before")
    run = io.capture("inner\n", || {
        a = echo()
        b = echo()
        a + b
    })
    io.print("value ${run.value}")
    io.write("output [${run.output}]\n")
    _ = echo()
}
`
	got, err := runCaptureProgram(t, src, "outer\n")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "before\n" +
		"dbg line 8: 5 = 5\ndbg line 8: 5 = 5\n" +
		"value 14\n" +
		"output [got inner\nerr eof\n]\n" +
		"got outer\n" +
		"dbg line 8: 5 = 5\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A trap inside the captured call propagates as the program's failure, and
// what the call printed before it is dropped. In a task, the trap settles the
// task and the spawner's output is its own again.
func TestCapture_TrapPropagatesAndRestoresOutput(t *testing.T) {
	const boom = `fn boom(): Int {
    io.print("partial")
    1 / 0
}

`
	got, err := runCaptureProgram(t, "import std/io\n\n"+boom+`fn main() {
    io.print("before")
    _ = io.capture("", boom)
    io.print("unreachable")
}
`, "")
	if err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("err = %v, want the division trap", err)
	}
	if got != "before\n" {
		t.Fatalf("output = %q, want only %q", got, "before\n")
	}
	got, err = runCaptureProgram(t, "import std/io\nimport std/tasks.Task\n\n"+boom+`fn main() {
    outcome = concurrent {
        t = Task.spawn(|| io.capture("", boom).value)
        Task.outcome(t)
    }
    io.print("after")
    io.inspect(outcome)
}
`, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "after\nFailed(Errored(\"line 6: division by zero\"))\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// `import std/io.capture` brings the generic function in bare, and a bare
// call lowers as the qualified one does.
func TestCapture_SelectiveImport(t *testing.T) {
	got, err := runCaptureProgram(t, `import std/io.{capture, print}

fn main() {
    c = capture("", || {
        print("in")
        42
    })
    print(c.output + "${c.value}")
}
`, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "in\n42\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// Call's WithOutput writer receives what a called function prints outside a
// capture and nothing it prints inside one.
func TestCapture_WithOutputStillReceivesNormalOutput(t *testing.T) {
	var out bytes.Buffer
	p, err := vmhost.LoadSource("main.nomi", `import std/io

pub fn shout(): String {
    io.print("outside")
    io.capture("", || io.print("inside")).output
}
`, vmhost.WithOutput(&out))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, err := p.Call(context.Background(), "shout")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if v != "inside\n" {
		t.Fatalf("value = %#v, want \"inside\\n\"", v)
	}
	if out.String() != "outside\n" {
		t.Fatalf("WithOutput received %q, want %q", out.String(), "outside\n")
	}
}

// A Captured renders through Debug, directly, under `dbg` and inside a
// container, at a scalar, a declared struct and Unit; and a function may
// name `Captured<T>` as a parameter type. Captured is a generic std struct
// interned once per program, so the instance `io.capture` builds is the one
// the program's annotation and std's Debug instance name.
func TestCapture_DebugAndAnnotation(t *testing.T) {
	got, err := runCaptureProgram(t, `import std/io
import std/io.Captured

struct Point {
    x: Int
}

fn show(c: Captured<Point>): String {
    "${c.value.x}|${c.output}"
}

fn main() {
    run = io.capture("", || {
        io.write("hi")
        42
    })
    io.inspect(run)
    _ = dbg run
    io.print(Debug.inspect([run]))
    p = io.capture("", || Point{x: 3})
    io.inspect(p)
    io.print(show(p))
    u = io.capture("", || io.write("u"))
    io.inspect((u, run))
}
`, "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Captured{value: 42, output: \"hi\", transcript: \"  hi\"}\n" +
		"dbg line 18: run = Captured{value: 42, output: \"hi\", transcript: \"  hi\"}\n" +
		"[Captured{value: 42, output: \"hi\", transcript: \"  hi\"}]\n" +
		"Captured{value: Point{x: 3}, output: \"\", transcript: \"\"}\n" +
		"3|\n" +
		"(Captured{value: Unit, output: \"u\", transcript: \"  u\"}, Captured{value: 42, output: \"hi\", transcript: \"  hi\"})\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

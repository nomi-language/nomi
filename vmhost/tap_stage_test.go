package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// runTapProgram loads src and runs main, answering its output.
func runTapProgram(t *testing.T, src string) string {
	t.Helper()
	p, err := vmhost.LoadSource("main.nomi", src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\noutput:\n%s", err, out.String())
	}
	return out.String()
}

// A `tap` stage runs its lambda once, at its place in the pipeline, with the
// value the stage before it answered, and passes that value on unchanged.
// The value is computed once: `next` prints once per call.
func TestTapStage_RunsInOrderAndPassesTheValueOn(t *testing.T) {
	src := `import std/io

fn next(n: Int): Int {
    io.print("next ${n}")
    n + 1
}

fn main() {
    total =
        1
        |> next()
        |> tap |n| io.print("tap ${n}")
        |> next()
        |> tap |n| {
            io.print("braced ${n}")
            io.print("again ${n}")
        }
        |> then |n| n * 10
        |> tap |n| io.print("last ${n}")

    io.print("total ${total}")
    words =
        "go north"
        |> String.words()
        |> tap |ws| io.print("got ${Iter.count(ws)} words")
        |> Iter.map(String.to_upper)
        |> Iter.to_list()

    io.print("${words}")
}
`
	want := "next 1\ntap 2\nnext 2\nbraced 3\nagain 3\nlast 30\ntotal 30\ngot 2 words\n[GO, NORTH]\n"
	if got := runTapProgram(t, src); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A `tap` inside a captured call prints into the capture, in order with the
// rest of the call's output, and the call's value is the tapped value.
func TestTapStage_InsideACapture(t *testing.T) {
	src := `import std/io

fn main() {
    run = io.capture("", || {
        io.print("start")
        7
        |> tap |n| io.print("saw ${n}")
        |> then |n| n + 1
    })
    io.print("value ${run.value}")
    io.write("output [${run.output}]\n")
}
`
	want := "value 8\noutput [start\nsaw 7\n]\n"
	if got := runTapProgram(t, src); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A body ending in `dbg` is a Unit body: the observation prints and the
// piped value, not the dbg operand, goes on.
func TestTapStage_DbgTail(t *testing.T) {
	src := `import std/io

fn main() {
    n = 4 |> tap |v| dbg v * 2
    io.print("n ${n}")
}
`
	want := "dbg line 4: v * 2 = 8\nn 4\n"
	if got := runTapProgram(t, src); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A generic function can tap a value of its type parameter, and a lambda's
// body can hold a `tap` stage.
func TestTapStage_GenericAndInLambda(t *testing.T) {
	src := `import std/io

fn logged<T>(x: T, label: String): T {
    x |> tap |v| io.print("${label}: ${Debug.inspect(v)}")
}

fn main() {
    a = logged(5, "five")
    b = logged("s", "str")
    xs = Iter.map([1, 2], |x| x |> tap |y| io.print("in ${y}")) |> Iter.to_list()
    io.print("${a} ${b} ${xs}")
}
`
	want := "five: 5\nstr: \"s\"\nin 1\nin 2\n5 s [1, 2]\n"
	if got := runTapProgram(t, src); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

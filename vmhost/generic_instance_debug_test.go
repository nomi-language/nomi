package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// runSourceOutput loads src as main.nomi and runs main, failing t on a
// rejection or a run error, and answers the output.
func runSourceOutput(t *testing.T, src string) string {
	t.Helper()
	p, err := vmhost.LoadSource("main.nomi", src)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\noutput so far:\n%s", err, out.String())
	}
	return out.String()
}

// Two instances of one generic type in one Debug rendering each render
// through their own instance's body. They share a runtime type name, so the
// renderer selects the body by the instance key the value's descriptor
// carries; before, the second rendered through the first's body and the VM
// stopped with "Box.inspect: t5 holds Bool and received string". The
// payloads here are scalars of two slot types, two declared structs (one
// slot type, so only the instance key tells them apart), and a generic enum.
func TestGenericInstanceDebug_EachInstanceRendersThroughItsOwnBody(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Box<T> {
    v: T
}

struct A {
    n: Int
}

struct B {
    s: String
}

enum Tree<T> {
    Leaf(T)
    Empty
}

fn main() {
    io.inspect((Box{v: True}, Box{v: ""}))
    io.inspect([(Box{v: A{n: 1}}, Box{v: B{s: "x"}})])
    pair = (Box{v: A{n: 2}}, Box{v: B{s: "y"}})
    _ = dbg pair
    e: Tree<A> = Tree.Empty
    io.inspect((Tree.Leaf(1), Tree.Leaf("a"), e))
    io.print(Debug.inspect({left: Box{v: 3}, right: Box{v: [B{s: "z"}]}}))
}
`)
	want := "(Box{v: True}, Box{v: \"\"})\n" +
		"[(Box{v: A{n: 1}}, Box{v: B{s: \"x\"}})]\n" +
		"dbg line 24: pair = (Box{v: A{n: 2}}, Box{v: B{s: \"y\"}})\n" +
		"(Leaf(1), Leaf(\"a\"), Empty)\n" +
		"{left: Box{v: 3}, right: Box{v: [B{s: \"z\"}]}}\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

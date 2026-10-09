package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A distinct over a distinct over a scalar (`type Wrapped Meters`) lives in
// the scalar's register, and its record holds the inner distinct boxed. A
// value of it read out of its register into a record field, a list or an
// enum payload is boxed one level at a time; the VM panicked with "a word
// register holds a main.Wrapped(main.Meters(Int))" before. Found by
// FuzzLoweredProgramsRun's rotating set.
func TestNestedDistinct_BoxesOutOfItsRegister(t *testing.T) {
	path := writeProgram(t, `import std/io

type Meters Int

type Wrapped Meters

type Name String

type Label Name

enum Holder {
    Has Wrapped
    Empty
}

fn main() {
    io.inspect(Holder.Has(Wrapped(Meters(3))))
    io.inspect([Wrapped(Meters(4))])
    io.inspect(Some(Label(Name("x"))))
    Wrapped(Meters(m)) = Wrapped(Meters(5))
    io.print(m)
}
`)
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Has(Wrapped(Meters(3)))\n[Wrapped(Meters(4))]\nSome(Label(Name(\"x\")))\n5\n"
	if out.String() != want {
		t.Fatalf("output %q, want %q", out.String(), want)
	}
}

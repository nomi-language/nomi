package irbuild

import (
	"testing"
)

// A list literal of a marker is built (backlog-types): the VM boxes each
// element as a record over the marker's descriptor.
func TestIRMarker_ContainerRetains(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `type Ready
fn main() { _ = [Ready] }
`)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range got.IR {
		for _, f := range mod.Funcs() {
			if f.Name() == "main" {
				return
			}
		}
	}
	t.Fatal("main, which builds a list of a marker, is not retained")
}

// A variant whose positional payload is a marker is built and matched: the
// payload is stored nowhere and read back from its type (irSlotlessPayload,
// irSlotlessValue). A generic payload instantiated at a marker or at Unit
// goes the same way.
func TestIRMarker_ZeroSizedEnumPayloadRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

type Ready

enum State {
    Present(Ready)
    Missing
}

enum Box<T> {
    Full(T)
    Empty
}

fn describe(s: State): String {
    case s {
        State.Present(r) -> "present ${Debug.inspect(r)}"
        State.Missing -> "missing"
    }
}

fn main() {
    s = State.Present(Ready)
    io.inspect(s)
    [s, State.Missing] |> Iter.each(|x| io.print(describe(x)))
    b: Box<Ready> = Box.Full(Ready)
    u: Box<Unit> = Box.Full(Unit)
    io.inspect((b, u))
}
`
	irRunSource(t, src, "Present(Ready)\npresent Ready\nmissing\n(Full(Ready), Full(Unit))\n")
}

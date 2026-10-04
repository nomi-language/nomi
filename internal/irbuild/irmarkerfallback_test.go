package irbuild

import (
	"testing"
)

// A list literal of a marker is built (backlog-types): the VM boxes each
// element as a record over the marker's descriptor.
func TestIRMarker_ContainerRetains(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `type Ready
fn main(): List<Ready> { [Ready] }
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

func TestIRMarker_ZeroSizedEnumPayloadPreservesFallback(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `type Ready
enum State { Present Ready; Missing }
fn main(): State { State.Present(Ready) }
`)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range got.IR {
		for _, f := range mod.Funcs() {
			if f.Name() == "main" {
				t.Fatal("zero-sized enum payload retained without native slot delivery")
			}
		}
	}
}

package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A type argument that names no type is the usual unknown-type error at
// the name, on a stdlib generic and on a user one; before, it was dropped
// and `ident<Nope>(1)` ran as `ident(1)`. A type declared in the body is a
// type there.
func TestTurbofishChecker_UnknownTypeArgument_Rejected(t *testing.T) {
	errs := checkWithStdlib(`
fn ident<T>(x: T): T {
  x
}

fn main() {
  _ = Map.size(Map.empty<String, Nope>())
  _ = ident<Nope>(1)
  type Meters Int
  _ = ident<Meters>(Meters(2))
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		`7:34 unknown type "Nope"`,
		`8:13 unknown type "Nope"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Turbofish pins a return-only type param: `Channel.buffered<Int>(…)` produces a
// `Channel<Int>`, so assigning it to a `Channel<String>`-annotated binding
// conflicts. (Distinguishing test: without turbofish the RHS is `Channel<?>`
// and would unify `? = String` with no error.)
func TestTurbofishChecker_PinsReturnParam_ConflictRejected(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
fn main(): Unit {
  ch: Channel<String> = Channel.buffered<Int>(4)
}`
	expectErrorContaining(t, checkWithStdlib(src), "type mismatch")
}

// Turbofish consistent with the binding annotation is clean (guard).
func TestTurbofishChecker_ConsistentUsage_Clean(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
fn main(): Unit {
  ch: Channel<Int> = Channel.buffered<Int>(4)
}`
	expectClean(t, checkWithStdlib(src))
}

// Too many type args for the callee's declared params is rejected.
func TestTurbofishChecker_ArityMismatch_Rejected(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
fn main(): Unit {
  ch = Channel.buffered<Int, String>(4)
  r = Sender.send(ch.sender, 5)
}`
	expectErrorContaining(t, checkWithStdlib(src), "type argument")
}

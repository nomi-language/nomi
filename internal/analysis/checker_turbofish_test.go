package analysis_test

import (
	"testing"
)

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

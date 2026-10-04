package analysis_test

import (
	"testing"
)

// A binding whose RHS type still has an unsolved type param (return-only,
// nothing on the line pins it) is rejected — it isn't locally determined.
func TestLocallyDetermined_UnannotatedChannelNew_Rejected(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
fn main(): Unit {
  ch = Channel.buffered(4)
}`
	expectErrorContaining(t, checkWithStdlib(src), "not locally determined")
}

// An empty-list binding with no annotation is undetermined → rejected.
func TestLocallyDetermined_EmptyList_Rejected(t *testing.T) {
	src := `fn main(): Unit {
  xs = []
}`
	expectErrorContaining(t, checkWithStdlib(src), "not locally determined")
}

// Forward use no longer rescues a binding: even though `send` later pins
// the element type, the binding line itself isn't locally determined.
func TestLocallyDetermined_ForwardUseDoesNotRescue(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
fn main(): Unit {
  ch = Channel.buffered(4)
  r = Sender.send(ch.sender, 5)
}`
	expectErrorContaining(t, checkWithStdlib(src), "not locally determined")
}

// A turbofish on the RHS satisfies the rule (pins the type on the line).
func TestLocallyDetermined_TurbofishSatisfies_Clean(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
fn main(): Unit {
  ch = Channel.buffered<Int>(4)
  r = Sender.send(ch.sender, 5)
}`
	expectClean(t, checkWithStdlib(src))
}

// An LHS annotation satisfies the rule.
func TestLocallyDetermined_AnnotationSatisfies_Clean(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
fn main(): Unit {
  ch: Channel<Int> = Channel.buffered(4)
  r = Sender.send(ch.sender, 5)
}`
	expectClean(t, checkWithStdlib(src))
}

// Self-determining RHSs need no annotation: literals, pinned generics.
func TestLocallyDetermined_ConcreteRHS_Clean(t *testing.T) {
	src := `fn main(): Unit {
  x = 5
  xs = [1, 2, 3]
  s = "hi"
}`
	expectClean(t, checkWithStdlib(src))
}

// once bindings obey the rule too.
func TestLocallyDetermined_OnceUndetermined_Rejected(t *testing.T) {
	src := `import {
  std/channels.{Channel, Sender}
}
once shared = Channel.buffered(1)
fn main(): Unit {
}`
	expectErrorContaining(t, checkWithStdlib(src), "not locally determined")
}

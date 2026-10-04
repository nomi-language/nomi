package irbuild

// The `iter` class: every `Iter.` operation over a sequence lowers to an
// `ir.Iter` node. The node records which operation it is, which
// representation family the source operand is in, the operands as
// temporaries, whether the callback answers a control signal
// (`ir.Iter.Signalling()`), and the position.
//
// The push protocol (`each_while`, `run`, `yield`) is below the instruction
// set: a pull protocol would produce the same instructions, so the protocol
// is invisible at this level. See ir/iter.go.
//
// `Iter.loop` is not an `ir.Iter`: `pub host fn loop<S>(f: (S) -> S): S`
// never mentions `Iter` and has no source or driver. It lowers to branches
// and jumps over blocks (irloop.go).

import (
	"github.com/nomi-language/nomi/internal/ir"
)

// irIterObserved is a test-only hook, nil in production, for an iteration
// node and the instruction cursor its call lands on. A digest of the emitted
// graph cannot see a node's position, so a test that checks positions or the
// order a pipeline's stages were built in needs a hook like this.
var irIterObserved func(n *ir.Iter, cursor int)

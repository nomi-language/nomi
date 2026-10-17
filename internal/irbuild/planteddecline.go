package irbuild

import (
	"sync/atomic"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A planted decline makes the builder decline every call to one named
// function, so a test about how a blocked program is reported (`nomi run`'s
// diagnostic, `nomi test`'s BLOCKED, the REPL, `vmhost.Check`, the language
// server, the debug adapter) has a program that blocks without depending on
// a real lowering gap, which a later fix would close.
//
// The call declines in `lowerExact`, the funnel every expression decline
// passes through, so the body that holds it is declined, censused, located
// and reported exactly as a real decline at that call would be; the reason
// is the planted one.

// declinePlant is one plant: the callee whose calls decline, and the reason.
type declinePlant struct{ callee, reason string }

// plantedDecline is the plant a test installed, or nil. It is atomic because
// a language server test's background lowering may still run when the test
// that planted ends.
var plantedDecline atomic.Pointer[declinePlant]

// PlantDeclineForTest makes the builder decline each call `callee(...)`,
// with reason as the builder's own reason, until restore is called. It
// panics outside a test binary. The plant is process-wide, so a test that
// plants must not run in parallel with another that lowers a call to callee.
// callee should be a name no stdlib function has: the stdlib's shared
// lowering is cached per process, and a plant changes only programs lowered
// while it is in place.
func PlantDeclineForTest(callee, reason string) (restore func()) {
	if !testing.Testing() {
		panic("irbuild.PlantDeclineForTest called outside a test")
	}
	if callee == "" || reason == "" {
		panic("irbuild.PlantDeclineForTest needs a callee and a reason")
	}
	prev := plantedDecline.Swap(&declinePlant{callee, reason})
	return func() { plantedDecline.Store(prev) }
}

// plantedDeclineAt is the planted reason when n is a call to the planted
// callee, else "".
func plantedDeclineAt(n ast.Node) string {
	p := plantedDecline.Load()
	if p == nil {
		return ""
	}
	if c, ok := n.(*ast.Call); ok {
		if id, ok := c.Func.(*ast.Ident); ok && id.Name == p.callee {
			return p.reason
		}
	}
	return ""
}

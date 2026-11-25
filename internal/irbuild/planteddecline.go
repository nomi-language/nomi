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

// PlantDeclineForTest makes the builder decline each call `callee(...)`, or
// `Owner.name(...)` for a callee written `Owner.name`, with reason as the builder's own reason, until restore is called. It
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

// DeclinesPlanted reports whether a test has planted a decline
// (PlantDeclineForTest, PlantTypeDeclineForTest,
// PlantMissingSubtypeCheckForTest), which no kept program holds.
func DeclinesPlanted() bool {
	return plantedDecline.Load() != nil || plantedTypeDecline.Load() != nil || plantedMissingSubtypeCheck.Load() != nil
}

// plantedTypeDecline is the struct a test made the builder refuse, or nil:
// callee is the struct's name.
var plantedTypeDecline atomic.Pointer[declinePlant]

// PlantTypeDeclineForTest makes the builder refuse the declaration of every
// struct named typeName, with reason as the refusal's construct, until
// restore is called: a function whose signature names it, and a type that
// holds it, are refused as for a struct the builder cannot represent, so a
// test about how a refused signature is reported needs no real gap. It
// panics outside a test binary, and is process-wide as PlantDeclineForTest
// is.
func PlantTypeDeclineForTest(typeName, reason string) (restore func()) {
	if !testing.Testing() {
		panic("irbuild.PlantTypeDeclineForTest called outside a test")
	}
	if typeName == "" || reason == "" {
		panic("irbuild.PlantTypeDeclineForTest needs a type name and a reason")
	}
	prev := plantedTypeDecline.Swap(&declinePlant{typeName, reason})
	return func() { plantedTypeDecline.Store(prev) }
}

// plantedRefusal is the planted refusal of the struct named name, or "".
func plantedRefusal(name string) string {
	if p := plantedTypeDecline.Load(); p != nil && p.callee == name {
		return p.reason
	}
	return ""
}

// plantedMissingSubtypeCheck is the subtype whose literals' checks a
// test made the builder fail to find, or nil: callee is its name.
var plantedMissingSubtypeCheck atomic.Pointer[declinePlant]

// PlantMissingSubtypeCheckForTest makes the builder find no `try_from`
// for the subtype named typeName when it lists a literal's checks
// (subtypeSites), as a linkage name built wrong would, until restore is
// called: a test that such a literal is an internal error at the literal,
// not let through unchecked, needs no real naming bug. It panics outside a
// test binary, and is process-wide as PlantDeclineForTest is.
func PlantMissingSubtypeCheckForTest(typeName string) (restore func()) {
	if !testing.Testing() {
		panic("irbuild.PlantMissingSubtypeCheckForTest called outside a test")
	}
	if typeName == "" {
		panic("irbuild.PlantMissingSubtypeCheckForTest needs a type name")
	}
	prev := plantedMissingSubtypeCheck.Swap(&declinePlant{callee: typeName})
	return func() { plantedMissingSubtypeCheck.Store(prev) }
}

// plantedMissingCheck reports whether a test planted a missing check for
// the subtype named typeName.
func plantedMissingCheck(typeName string) bool {
	p := plantedMissingSubtypeCheck.Load()
	return p != nil && p.callee == typeName
}

// plantedDeclineAt is the planted reason when n is a call to the planted
// callee, else "".
func plantedDeclineAt(n ast.Node) string {
	p := plantedDecline.Load()
	if p == nil {
		return ""
	}
	if c, ok := n.(*ast.Call); ok {
		switch f := c.Func.(type) {
		case *ast.Ident:
			if f.Name == p.callee {
				return p.reason
			}
		case *ast.FieldAccess:
			// A callee planted as `Owner.name` matches a qualified call.
			if owner, ok := f.Object.(*ast.TypeIdent); ok && f.Field != nil && owner.Name+"."+f.Field.Name == p.callee {
				return p.reason
			}
		}
	}
	return ""
}

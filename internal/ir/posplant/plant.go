//go:build irposplant

// Package posplant is not compiled. It is the planted program that
// internal/ir's TestPosition_OmittingAPositionDoesNotCompile builds with
// `-tags irposplant` in order to observe the compiler rejecting it.
//
// "This does not compile" is not assertable from inside a normal test, so it
// is asserted from outside: the test runs `go build -tags irposplant` on this
// package and requires an error on each marked line and on no other line.
//
// The build tag keeps it out of every ordinary build. `go build ./...` and
// `go vet ./internal/...` do not see it, because the go tool skips a package
// whose files are all excluded by build constraints.
//
// EVERY LINE NUMBER HERE IS LOAD-BEARING. The test names them.
package posplant

import "github.com/nomi-language/nomi/internal/ir"

// Keep the cases in order and keep each one to a single line, so a diff that
// moves them fails the test rather than silently measuring nothing. The line
// numbers named below are the lines this file's cases are actually on.

// CASE A, line 26: a keyed composite literal cannot name an unexported field,
// so a node's position cannot be written from outside the package.
var _ = ir.Const{pos: ir.At("a.nomi", 1, 1)}

// CASE B, line 29: neither can a position's own line.
var _ = ir.Pos{line: 7}

// CASE C, line 32: omitting the position argument to a const constructor.
var _ = ir.NewInt(1, 42)

// CASE D, line 35: omitting it to an arithmetic constructor.
var _ = ir.NewArith(1, ir.OpAdd, ir.IntArith(ir.OverflowFaults), 2, 3)

// CASE E, line 38: omitting it to a terminator.
var _ = ir.NewReturn(1)

// CASE F, line 41: omitting it to a function.
var _ = ir.NewFunc("total")

// CASE G, line 46: Instr is sealed by an unexported method, so a type outside
// this package cannot be an instruction at all — which is what makes "every
// instruction has a position" a property of the type and not of a convention.
var _ ir.Instr = positionlessInstr{}

type positionlessInstr struct{}

func (positionlessInstr) Pos() ir.Pos                        { return ir.Pos{} }
func (positionlessInstr) String() string                     { return "positionless" }
func (positionlessInstr) Dst() ir.Temp                       { return ir.NoTemp }
func (positionlessInstr) AppendUses(dst []ir.Temp) []ir.Temp { return dst }

// THE POSITIVE CONTROL, line 59. A correct construction. The test requires NO
// error on this line: without it, a file the compiler rejected for some
// unrelated reason — a typo, a renamed constructor — would look exactly like
// the enforcement working.
var _ = ir.NewInt(ir.At("a.nomi", 1, 1), 1, 42)

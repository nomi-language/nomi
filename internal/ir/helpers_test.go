package ir_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// moduleDir is the repository root, the directory holding go.mod. A test's working
// directory is its own package directory, so this is two levels up — checked
// rather than assumed, because a silently wrong directory would make
// `go build` fail for a reason that reads like the thing under test.
func moduleDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("no go.mod in %s: %v", dir, err)
	}
	return dir
}

// rtDir is the rt module's source directory, a separate Go module nested at
// the repository root.
func rtDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "rt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "arith.go")); err != nil {
		t.Fatalf("no arith.go in %s: %v", dir, err)
	}
	return dir
}

const helperFile = "helper.nomi"

// buildShortCircuitFunc builds `fn f(): Bool { a and b }` where the right
// operand needs a block: it contains a `once` read, which FORCES, and a
// materialized constant.
//
// The shape is the one BeginShortCircuit and Finish produce, so this is also
// the fixture the logic tests assert against.
func buildShortCircuitFunc(t *testing.T) *ir.Func {
	t.Helper()
	var (
		fnPos    = ir.At(helperFile, 1, 1)
		aPos     = ir.At(helperFile, 2, 3)
		opPos    = ir.At(helperFile, 2, 5)
		bPos     = ir.At(helperFile, 2, 9)
		oncePos  = ir.At(helperFile, 3, 3)
		tailPos  = ir.At(helperFile, 4, 1)
		symA     = ir.NewSymbol("a")
		symB     = ir.NewSymbol("b")
		symReady = ir.NewSymbol("ready")
	)

	f := ir.NewFunc(fnPos, "f")
	entry := f.NewBlock(fnPos, "entry")

	lhs := f.NewTemp()
	entry.Append(ir.NewRefLocal(aPos, lhs, symA))

	answer := f.NewTemp()
	sc := ir.BeginShortCircuit(f.Region, entry, ir.LogicAnd, opPos, aPos, bPos, answer, lhs)

	// The right operand's statements. The `once` read is the one that makes
	// this a block rather than an operand: it may RUN the cell's initializer,
	// so when it happens is observable.
	forced := f.NewTemp()
	sc.Rhs().Append(ir.NewRefOnce(oncePos, forced, symReady))
	rhs := f.NewTemp()
	sc.Rhs().Append(ir.NewRefLocal(bPos, rhs, symB))

	join := sc.Finish(sc.Rhs(), bPos, rhs)
	join.SetTerm(ir.NewReturn(tailPos, answer))
	return f
}

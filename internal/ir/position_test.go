package ir_test

// Enforcement of the per-node position rule (point 2 of ir.go's package
// header). Its two halves are unequal.
//
//   - OMITTING a position is a COMPILE ERROR. Every constructor takes one as
//     its first parameter, every field holding one is unexported, and Instr and
//     Term are sealed by an unexported method so no outside type can be a node
//     at all. TestPosition_OmittingAPositionDoesNotCompile demonstrates this by
//     building a planted program rather than describing it, because "this does
//     not compile" is not assertable from inside a test.
//
//   - FABRICATING an invalid one is a PANIC AT CONSTRUCTION. `ir.Pos{}` is a
//     legal composite literal outside the package even though every field is
//     unexported — Go permits an empty keyless literal — so the type system
//     cannot close that hole and something else has to. Every constructor
//     rejects it, and so do Block.Append and Block.SetTerm, which is the route
//     a zero-valued node could otherwise take into a function.
//
// Neither half is claimed to be the other. Construction without a position is
// a compile error rather than a lint, and the compile error covers omission
// exactly, while fabrication is caught one layer later.

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// TestPosition_OmittingAPositionDoesNotCompile builds internal/ir/posplant,
// which is excluded from every ordinary build by `//go:build irposplant`, and
// requires the compiler to reject each planted line and no other line.
func TestPosition_OmittingAPositionDoesNotCompile(t *testing.T) {
	// Each case is a line of posplant/plant.go and a fragment of the error it
	// must produce. The fragments are deliberately the compiler's own words:
	// if a future Go changes them, this test says so rather than passing on a
	// weaker match.
	cases := []struct {
		line int
		want string
		why  string
	}{
		{26, "cannot refer to unexported field pos", "a node's position cannot be written from outside"},
		{29, "cannot refer to unexported field line", "a Pos's line cannot be written from outside"},
		{32, "not enough arguments in call to ir.NewInt", "a const constructor demands a position"},
		{35, "not enough arguments in call to ir.NewArith", "an arith constructor demands a position"},
		{38, "not enough arguments in call to ir.NewReturn", "a terminator constructor demands a position"},
		{41, "not enough arguments in call to ir.NewFunc", "a function constructor demands a position"},
		{46, "missing method irInstr", "Instr is sealed, so no outside type can be an instruction"},
	}
	// THE POSITIVE CONTROL. A correct construction on this line. An error here
	// would mean the build failed for a reason that has nothing to do with
	// positions, and every assertion above would be passing for free.
	const controlLine = 59

	// -gcflags=-e disables the type checker's ten-error cap. Without it the
	// later cases could be silently unreported and this test would fail for a
	// reason that reads like the enforcement being absent.
	cmd := exec.Command("go", "build", "-tags", "irposplant", "-gcflags=-e",
		"./internal/ir/posplant")
	cmd.Dir = moduleDir(t)
	out, err := cmd.CombinedOutput()
	text := string(out)
	t.Logf("go build -tags irposplant ./internal/ir/posplant:\n%s", text)
	if err == nil {
		t.Fatalf("the planted program COMPILED. Every case in it constructs a node without a "+
			"position or implements Instr from outside the package, so a successful build "+
			"means the enforcement described in this file is gone.\noutput:\n%s", text)
	}
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("could not run `go build`: %v\noutput:\n%s", err, text)
	}

	for _, c := range cases {
		prefix := "plant.go:" + strconv.Itoa(c.line) + ":"
		found := ""
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, prefix) {
				found = line
				break
			}
		}
		if found == "" {
			t.Errorf("no compiler error on plant.go:%d, where %s. Either the enforcement is "+
				"gone or the case moved; the line numbers in plant.go's comments are the ones "+
				"this table names", c.line, c.why)
			continue
		}
		if !strings.Contains(found, c.want) {
			t.Errorf("plant.go:%d reported %q; want it to contain %q (%s)",
				c.line, strings.TrimSpace(found), c.want, c.why)
		}
	}

	if strings.Contains(text, "plant.go:"+strconv.Itoa(controlLine)+":") {
		t.Errorf("the compiler reported an error on plant.go:%d, which is the CORRECT "+
			"construction. The build is failing for a reason unrelated to positions, so the "+
			"assertions above prove nothing", controlLine)
	}
}

// TestPosition_FabricatingAnInvalidPositionPanics covers the hole the type
// system leaves: `ir.Pos{}` compiles.
func TestPosition_FabricatingAnInvalidPositionPanics(t *testing.T) {
	var zero ir.Pos
	if zero.IsValid() {
		t.Fatal("the zero Pos reports itself valid, so nothing below can distinguish a " +
			"fabricated position from a real one")
	}
	// PLANT A POSITIVE: a real position must be valid, or `IsValid` could be
	// `return false` and every case below would pass.
	if real := ir.At("a.nomi", 12, 3); !real.IsValid() {
		t.Fatal("a position built by At reports itself invalid")
	}

	for _, c := range []struct {
		name string
		call func()
	}{
		{"NewUnit", func() { ir.NewUnit(zero, 1) }},
		{"NewInt", func() { ir.NewInt(zero, 1, 7) }},
		{"NewString", func() { ir.NewString(zero, 1, "x") }},
		{"NewRefLocal", func() { ir.NewRefLocal(zero, 1, ir.NewSymbol("x")) }},
		{"NewCopy", func() { ir.NewCopy(zero, 1, 2) }},
		{"NewArith", func() {
			ir.NewArith(zero, 1, ir.OpAdd, ir.IntArith(ir.OverflowFaults), 2, 3)
		}},
		{"NewUnaryArith", func() {
			ir.NewUnaryArith(zero, 1, ir.OpNeg, ir.FloatArith(), 2)
		}},
		{"NewJump", func() { ir.NewJump(zero, 0) }},
		{"NewBranch", func() { ir.NewBranch(zero, 1, 0, 1) }},
		{"NewReturn", func() { ir.NewReturn(zero, 1) }},
		{"NewReturnUnit", func() { ir.NewReturnUnit(zero) }},
		{"NewFunc", func() { ir.NewFunc(zero, "f") }},
		{"Func.NewBlock", func() { ir.NewFunc(ir.At("a.nomi", 1, 1), "f").NewBlock(zero, "b") }},
	} {
		if got := recovered(c.call); got == "" {
			t.Errorf("%s accepted the zero Pos without panicking; a node with no position is "+
				"the defect this package exists to remove", c.name)
		} else if !strings.Contains(got, "position") {
			t.Errorf("%s panicked with %q, which does not name the problem", c.name, got)
		}
	}

	// At itself rejects a line below 1, so there is no route to a Pos that
	// reports itself valid while naming no line.
	if got := recovered(func() { ir.At("a.nomi", 0, 1) }); got == "" {
		t.Error("At accepted line 0")
	}
	if got := recovered(func() { ir.AtSynthesized("a.nomi", -1, 1) }); got == "" {
		t.Error("AtSynthesized accepted a negative line")
	}
}

// TestPosition_AZeroValuedNodeCannotEnterAFunction closes the last route: a
// node built as `&ir.Const{}` rather than through a constructor.
func TestPosition_AZeroValuedNodeCannotEnterAFunction(t *testing.T) {
	f := ir.NewFunc(ir.At("a.nomi", 1, 1), "f")
	b := f.NewBlock(ir.At("a.nomi", 1, 1), "entry")

	// PLANT A POSITIVE: a properly constructed node must be accepted, or
	// Append could reject everything and the assertions below would be free.
	b.Append(ir.NewInt(ir.At("a.nomi", 2, 3), f.NewTemp(), 1))
	if len(b.Instrs()) != 1 {
		t.Fatalf("Append rejected a well-formed instruction; %d in the block", len(b.Instrs()))
	}

	if got := recovered(func() { b.Append(&ir.Const{}) }); !strings.Contains(got, "position") {
		t.Errorf("Block.Append accepted a zero-valued Const; panic was %q. `ir.Const{}` is a "+
			"legal composite literal outside the package even with every field unexported, so "+
			"this is the one hole the type system leaves and Append is what closes it", got)
	}
	if got := recovered(func() { b.SetTerm(&ir.Return{}) }); !strings.Contains(got, "position") {
		t.Errorf("Block.SetTerm accepted a zero-valued Return; panic was %q", got)
	}
	if got := recovered(func() { b.Append(nil) }); got == "" {
		t.Error("Block.Append(nil) did not panic")
	}
}

// TestPosition_ASynthesizedPositionCarriesItsOrigin covers the second
// inheritance path into the same defect class as the loop exit.
//
// `gen.at` (native.go:1642) IGNORES a synthesized line — `derive` synthesis
// allocates from a band around 2^30 and `//line f.nomi:1077657600` is not a
// legal Go directive — and leaves the cursor wherever the caller last set it.
// Ignoring is the right call for a `//line` directive and the wrong shape for
// an IR node, because it means the node has no position of its own. Here a
// synthesized position is a position: it carries the origin the programmer
// wrote, and says that it is one.
func TestPosition_ASynthesizedPositionCarriesItsOrigin(t *testing.T) {
	read := ir.At("a.nomi", 10, 1)
	if read.Synthesized() {
		t.Error("a position read from source reports itself synthesized")
	}

	synth := ir.AtSynthesized("a.nomi", 10, 1)
	if !synth.Synthesized() {
		t.Error("a synthesized position does not report itself synthesized")
	}
	if !synth.IsValid() || synth.Line() != 10 {
		t.Errorf("a synthesized position must still name its origin; got %s", synth)
	}
	if got := synth.String(); !strings.Contains(got, "synthesized") {
		t.Errorf("Pos.String() = %q; a synthesized position must be distinguishable in a "+
			"diagnostic, or a reader cannot tell derived code from written code", got)
	}
	if read.String() == synth.String() {
		t.Error("a read position and a synthesized one at the same line render identically")
	}
}

// TestPosition_EveryNodeInAFunctionHasOne walks a fully built function and
// requires a position on every instruction, every terminator, every block and
// the function itself. It is the assertion a consumer relies on when it emits
// debug information without an ambient cursor.
func TestPosition_EveryNodeInAFunctionHasOne(t *testing.T) {
	f := buildShortCircuitFunc(t)

	// PLANT A POSITIVE on the walk: it must actually visit something. A walk
	// over an empty function would report "every node has a position" too.
	nodes := 0
	for _, b := range f.Blocks() {
		if !b.Pos().IsValid() {
			t.Errorf("%s has no position", b.ID())
		}
		nodes++
		for _, in := range b.Instrs() {
			if !in.Pos().IsValid() {
				t.Errorf("%s in %s has no position", in, b.ID())
			}
			nodes++
		}
		if b.Term() == nil {
			t.Errorf("%s is unterminated", b.ID())
			continue
		}
		if !b.Term().Pos().IsValid() {
			t.Errorf("terminator %s of %s has no position", b.Term(), b.ID())
		}
		nodes++
	}
	if !f.Pos().IsValid() {
		t.Error("the function has no position")
	}
	if nodes < 10 {
		t.Fatalf("the walk visited %d nodes, which is too few for its answer to mean "+
			"anything", nodes)
	}
	t.Logf("%d nodes walked, every one positioned", nodes)
}

func recovered(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = strings.TrimSpace(strings.TrimPrefix(toString(r), "ir: "))
		}
	}()
	f()
	return ""
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return "non-string panic"
}

package irbuild

// The planted positive behind the empty HOST buckets of both retained lists.

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

var v11pos = ir.At("v11.nomi", 1, 1)

// TestVMCoverage_TheHostClassifierCatchesItsPlant is the planted positive for
// the empty HOST bucket in testdata/expectations/vm-retained.txt and
// vm-std-retained.txt.
//
// No retained body in either population lands in the HOST bucket, and a zero
// nothing validates is indistinguishable from a classifier that stopped
// recognising the failure. The plant is a marked crossing whose name the
// machine binds nothing for. The control is the same graph with `io.print`,
// which the machine binds, so it also shows the crossing path itself works
// rather than only that the classifier fires.
func TestVMCoverage_TheHostClassifierCatchesItsPlant(t *testing.T) {
	build := func(name string) error {
		f := ir.NewFunc(v11pos, "crosser")
		p := f.AddParam(ir.NewSymbol("s"), ir.ValUnknown)
		b := f.NewBlock(v11pos, "entry")
		// A HOST call, which is the marker `callInstr` branches on. The
		// operand is a String because `io.print`'s binding requires the
		// rendered text; the control must reach the binding rather than
		// fail inside it.
		c := ir.NewHostCall(v11pos, f.NewTemp(), ir.OrdinaryCall, ir.NewSymbol(name), p)
		b.Append(c)
		b.SetTerm(ir.NewReturn(v11pos, c.Dst()))

		mod := ir.NewModule("hostplant")
		mod.AddFunc(f)
		_, err := vmRunV(vm.New(mod, io.Discard), "crosser", "x")
		return err
	}

	if err := build("io.print"); err != nil {
		t.Fatalf("the control failed: a crossing this machine binds must run, or "+
			"the plant below proves nothing about the binding and only something about "+
			"the map lookup: %v", err)
	}
	err := build("duration.Duration.seconds")
	if err == nil {
		t.Fatal("a marked crossing the machine binds no implementation for ran, which " +
			"would make the HOST bucket unreachable and both zeros vacuous")
	}
	if got := vmClassify(err); got != "HOST: crosses into Go with no binding" {
		t.Errorf("vmClassify answered %q for %v; both retained lists record that exact string, "+
			"so an empty bucket in either would be one nothing can reach", got, err)
	}
	// It must be a hard failure, because `vmHardFailure` is what stops the
	// population tests trying the next argument shape. A HOST failure that read
	// as soft would be retried per shape and reported under whatever the last
	// shape produced, which is a misclassification the count cannot see.
	if !vmHardFailure(err) {
		t.Errorf("a HOST failure must be hard; vmHardFailure said false for %v", err)
	}
}

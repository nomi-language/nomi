package ir_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// iterOps is every operation the model declares, derived by walking the
// constant range rather than listed — a hand-written list is exactly what a new
// operation gets left out of.
//
// The walk is bounded by asking the model itself: an op with no spec answers
// the empty name, and the first two consecutive misses past the last known
// member end the walk. Two rather than one because a gap would otherwise
// truncate silently, and TestIRIter_EveryOpHasASpec is what makes a gap fail
// instead of shortening this list.
func iterOps() []ir.IterOp {
	var out []ir.IterOp
	misses := 0
	for v := 1; v < 256 && misses < 2; v++ {
		op := ir.IterOp(v)
		if op.Name() == "" || strings.HasPrefix(op.Name(), "IterOp(") {
			misses++
			continue
		}
		misses = 0
		out = append(out, op)
	}
	return out
}

func TestIRIter_EveryOpHasASpec(t *testing.T) {
	ops := iterOps()
	// THE PLANT: the walk must find the operations the file plainly declares,
	// or every per-op assertion below runs over an empty list and passes.
	if len(ops) < 35 {
		t.Fatalf("the op walk found %d operations and the class has more than that; every "+
			"check below would be vacuous", len(ops))
	}
	for _, op := range ops {
		if op.Form() == 0 {
			t.Errorf("%v has no form, so a consumer cannot tell a stage from a terminal", op)
		}
		if op.MinArity() < 1 || op.MaxArity() < op.MinArity() {
			t.Errorf("%v brackets its arity at [%d, %d], which admits no operand count",
				op, op.MinArity(), op.MaxArity())
		}
		admits := 0
		for _, d := range []ir.IterDomain{
			ir.IterOverNothing, ir.IterOverSeq, ir.IterOverList, ir.IterOverEmptyList,
			ir.IterOverString, ir.IterOverMap, ir.IterOverSet, ir.IterOverVector,
			ir.IterOverRange, ir.IterOverBytes, ir.IterOverUserImpl,
		} {
			if op.Admits(d) {
				admits++
			}
		}
		if admits == 0 {
			t.Errorf("%v admits no source family, so no node of it can be built", op)
		}
	}
	// One operation is beyond the enum: a value nobody declared must not be
	// silently well formed.
	if ir.IterOp(200).Form() != 0 || ir.IterOp(200).Admits(ir.IterOverSeq) {
		t.Error("an undeclared operation answers a form or admits a domain, so the spec " +
			"table is not the gate it is supposed to be")
	}
	t.Logf("%d operations, all specified", len(ops))
}

func TestIRIter_TheThreeFormsPartitionTheOpSet(t *testing.T) {
	count := map[ir.IterForm]int{}
	for _, op := range iterOps() {
		count[op.Form()]++
	}
	for _, f := range []ir.IterForm{ir.IterFormSource, ir.IterFormStage, ir.IterFormTerminal} {
		if count[f] == 0 {
			t.Errorf("no operation has form %v, so the partition has an empty part", f)
		}
	}
	// THE FOURTH POPULATION A READER WOULD NAME IS NOT A FORM. `to_list` and
	// `count` are both terminals; one answers a container and one a scalar,
	// and that difference is the RESULT TYPE, which this node does not carry.
	if ir.IterToList.Form() != ir.IterCount.Form() {
		t.Error("Iter.to_list and Iter.count have different forms. A materializer is a " +
			"terminal whose result is a container, and the result type is the consumer's " +
			"answer — splitting them would put a type in the opcode")
	}
	t.Logf("source %d, stage %d, terminal %d",
		count[ir.IterFormSource], count[ir.IterFormStage], count[ir.IterFormTerminal])
}

// mustPanic requires f to panic, naming what was being built when it did not.
// `recovered` is position_test.go's, so the two share one reader.
func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	if got := recovered(f); got == "" {
		t.Errorf("%s did not panic; the guard it is meant to exercise is not there", what)
	}
}

func TestIRIter_EveryConstructorREQUIRESAPosition(t *testing.T) {
	// Position is mandatory per node: a node with no position is a producer bug, and the zero Pos
	// is the one invalid value a composite literal outside this package can
	// build.
	mustPanic(t, "NewIter with the zero position", func() {
		ir.NewIter(ir.Pos{}, 1, ir.IterToList, ir.IterOverSeq, 2)
	})
	mustPanic(t, "NewIterSignalling with the zero position", func() {
		ir.NewIterSignalling(ir.Pos{}, 1, ir.IterMap, ir.IterOverSeq, 2, 3)
	})
	// THE PLANT: a real position must NOT panic, or the two above prove only
	// that the constructor panics on everything.
	ir.NewIter(ir.At("a.nomi", 3, 1), 1, ir.IterToList, ir.IterOverSeq, 2)
}

func TestIRIter_TheArityCheckHasPower(t *testing.T) {
	at := ir.At("a.nomi", 4, 2)
	// Too few, too many, and — for the one operation with a range — outside
	// the bracket on both sides.
	mustPanic(t, "to_list with no operand", func() {
		ir.NewIter(at, 1, ir.IterToList, ir.IterOverSeq)
	})
	mustPanic(t, "to_list with two operands", func() {
		ir.NewIter(at, 1, ir.IterToList, ir.IterOverSeq, 2, 3)
	})
	mustPanic(t, "reduce with one operand", func() {
		ir.NewIter(at, 1, ir.IterReduce, ir.IterOverSeq, 2)
	})
	mustPanic(t, "reduce with four operands", func() {
		ir.NewIter(at, 1, ir.IterReduce, ir.IterOverSeq, 2, 3, 4, 5)
	})
	// THE PLANT: both ends of reduce's bracket must be accepted, because a
	// check that rejected everything would pass all four assertions above.
	ir.NewIter(at, 1, ir.IterReduce, ir.IterOverSeq, 2, 3)
	ir.NewIter(at, 1, ir.IterReduce, ir.IterOverSeq, 2, 3, 4)
}

func TestIRIter_ADomainIsCheckedAgainstTheOperation(t *testing.T) {
	at := ir.At("a.nomi", 5, 1)
	// A STAGE RUNS ON A SEQUENCE, structurally. After the source view the
	// domain is erased, and a stage carrying a collection family would be a
	// node claiming the view had not happened.
	mustPanic(t, "map over a List", func() {
		ir.NewIter(at, 1, ir.IterMap, ir.IterOverList, 2, 3)
	})
	// A VIEW OF A SEQUENCE IS UNREPRESENTABLE. A value that already is a
	// sequence has no view to take, and the builder answers it unchanged with
	// no instruction at all — so a node saying otherwise would be an
	// instruction for something that does nothing.
	mustPanic(t, "a view of a Seq", func() {
		ir.NewIter(at, 1, ir.IterView, ir.IterOverSeq, 2)
	})
	// A CONSTRUCTOR HAS NO SOURCE.
	mustPanic(t, "from over a Seq", func() {
		ir.NewIter(at, 1, ir.IterFrom, ir.IterOverSeq, 2)
	})
	mustPanic(t, "count over a String", func() {
		ir.NewIter(at, 1, ir.IterCount, ir.IterOverString, 2)
	})
	// THE PLANT: the three operations that DO read a domain must accept the
	// families std declares an override for, or the four above would pass
	// against a constructor that rejected every domain.
	ir.NewIter(at, 1, ir.IterView, ir.IterOverList, 2)
	ir.NewIter(at, 1, ir.IterCount, ir.IterOverMap, 2)
	ir.NewIter(at, 1, ir.IterKnownCount, ir.IterOverString, 2)
	ir.NewIter(at, 1, ir.IterFrom, ir.IterOverNothing, 2)
}

func TestIRIter_SignallingIsRefusedWhereTheFrontEndHasNoSlot(t *testing.T) {
	at := ir.At("a.nomi", 6, 1)
	// The set is `analysis/iter_sensitive.go`'s, not this file's. An
	// operation with no callback slot a `break` can reach cannot claim the
	// widened convention.
	for _, op := range []ir.IterOp{ir.IterToList, ir.IterDropWhile, ir.IterFind, ir.IterZip} {
		mustPanic(t, "signalling "+op.Name(), func() {
			ir.NewIterSignalling(at, 1, op, ir.IterOverSeq, 2, 3)
		})
	}
	// THE PLANT: the six the front end does sanction must be accepted.
	signalling := 0
	for _, op := range iterOps() {
		if op.HasSignallingForm() {
			signalling++
		}
	}
	if signalling != 6 {
		t.Fatalf("%d operations declare a signalling form and the front end's "+
			"iterCallbackSlots sanctions six of this class's; the guard above is "+
			"measuring the wrong set", signalling)
	}
	ir.NewIterSignalling(at, 1, ir.IterMap, ir.IterOverSeq, 2, 3)
	ir.NewIterSignalling(at, 1, ir.IterReduce, ir.IterOverSeq, 2, 3, 4)
	if !ir.NewIterSignalling(at, 1, ir.IterEach, ir.IterOverSeq, 2, 3).Signalling() {
		t.Error("a signalling node does not report itself as one")
	}
	if ir.NewIter(at, 1, ir.IterEach, ir.IterOverSeq, 2, 3).Signalling() {
		t.Error("an ordinary node reports itself as signalling, so the predicate is " +
			"constant and cannot distinguish the two forms")
	}
}

func TestIRIter_APipelineIsStraightLine(t *testing.T) {
	// `xs |> Iter.map(f) |> Iter.filter(g) |> Iter.to_list()` as the model has
	// it. Seven instructions in one block, no branch and no terminator but the
	// return — which is the whole answer to whether a PUSH protocol
	// linearizes.
	f := ir.NewFunc(ir.At("p.nomi", 1, 1), "pipeline")
	b := f.NewBlock(ir.At("p.nomi", 2, 1), "entry")
	xs, fn, gn := f.NewTemp(), f.NewTemp(), f.NewTemp()
	view, mapped, filtered, out := f.NewTemp(), f.NewTemp(), f.NewTemp(), f.NewTemp()
	b.Append(ir.NewRefLocal(ir.At("p.nomi", 2, 1), xs, ir.NewSymbol("xs")))
	b.Append(ir.NewIter(ir.At("p.nomi", 2, 8), view, ir.IterView, ir.IterOverList, xs))
	b.Append(ir.NewRefLocal(ir.At("p.nomi", 2, 20), fn, ir.NewSymbol("f")))
	b.Append(ir.NewIter(ir.At("p.nomi", 2, 20), mapped, ir.IterMap, ir.IterOverSeq, view, fn))
	b.Append(ir.NewRefLocal(ir.At("p.nomi", 2, 40), gn, ir.NewSymbol("g")))
	b.Append(ir.NewIter(ir.At("p.nomi", 2, 40), filtered, ir.IterFilter, ir.IterOverSeq, mapped, gn))
	b.Append(ir.NewIter(ir.At("p.nomi", 2, 60), out, ir.IterToList, ir.IterOverSeq, filtered))
	b.SetTerm(ir.NewReturn(ir.At("p.nomi", 2, 60), out))

	if len(f.Blocks()) != 1 {
		t.Fatalf("a three-stage pipeline needed %d blocks; the push protocol would then "+
			"be control flow rather than a callee's obligation", len(f.Blocks()))
	}
	got := make([]string, 0, len(b.Instrs()))
	for _, in := range b.Instrs() {
		got = append(got, in.String())
	}
	want := []string{
		"t1 = local xs",
		"t4 = iter (source view)(list) t1",
		"t2 = local f",
		"t5 = iter map t4, t2",
		"t3 = local g",
		"t6 = iter filter t5, t3",
		"t7 = iter to_list t6",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the pipeline's instruction sequence is\n%s\nwant\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// EVERY STAGE READS EXACTLY ITS PREDECESSOR, which is what makes the chain
	// a chain rather than a tree the builder happened to flatten.
	uses := b.Instrs()[6].AppendUses(nil)
	if len(uses) != 1 || uses[0] != filtered {
		t.Errorf("the terminal reads %v; a linear pipeline's terminal reads exactly the "+
			"last stage", uses)
	}
}

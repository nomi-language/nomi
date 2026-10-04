package irbuild

// Is the recorded parameter shape the right one? The VM as an independent
// oracle.
//
// `irParamShape` records each parameter's shape from the builder's kind.
// `vmShaper` derives the shape of each argument from the `ir.Proj`
// instructions that read it, a different fact from the same graph. So two
// readings of one parameter's shape exist and this file compares them.
//
// Two checks, because each is blind to half the population:
//
//  1. One argument vector, chosen by the recorded shape, and the machine must
//     still run. `vmArgShapes` tries every leaf and counts a function runnable
//     if any completes. This runs each function with the leaf the declaration
//     names, so a wrong scalar shape feeds, say, a String where the body adds,
//     the machine refuses, and the count falls below vmWantRunnable.
//
//  2. The structure the shaper derived must agree with the shape the
//     declaration records. Check 1 cannot see a wrong composite shape:
//     `argFor` builds a struct because a `ProjField` reads the parameter and
//     ignores the leaf when it does, so recording `ValTuple` for a struct
//     parameter changes no argument and the run still passes. This compares
//     the two answers directly.
//
// Check 2 is vacuous for a parameter nothing projects off, because the shaper
// then answers the leaf it was handed. Check 1 is vacuous for a parameter
// nothing constrains, because every leaf works. The counts are logged so a
// reader can see how much of the population each covers.

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// v12LeafFor is the scalar rt value a recorded shape names, and false where
// the declaration states no scalar.
//
// `ValStruct`, `ValTuple`, `ValVariant` and `ValDistinct` answer false on
// purpose: those are structures, and the leaf inside one is a `Proj` result
// type the parameter's shape does not state. `vmShaper` builds the structure
// from the graph and this supplies only what it cannot.
func v12LeafFor(s ir.ValShape) (any, bool) {
	switch s {
	case ir.ValBool:
		return true, true
	case ir.ValInt:
		return int64(1), true
	case ir.ValFloat:
		return 1.5, true
	case ir.ValDecimal:
		return rt.Decimal{}, true
	case ir.ValString:
		return "s", true
	case ir.ValUnit:
		return rt.Unit{}, true
	}
	return nil, false
}

// v12ShapeOfValue is a probe argument's shape in `ir.ValShape`'s vocabulary,
// or `ir.ValUnknown` where this file has no reading for it.
//
// The composites are what it is for. A struct that `vmShaper` built because a
// `ProjField` reads the parameter is the graph's own statement that the
// parameter is a struct, arrived at without consulting the declaration.
func v12ShapeOfValue(v any) ir.ValShape {
	switch n := v.(type) {
	case *probeLeaf:
		return v12ShapeOfValue(n.v)
	case *probeStruct:
		return ir.ValStruct
	case *probeList, *rt.List[any]:
		return ir.ValContainer
	case *probeTuple:
		return ir.ValTuple
	case *probeDistinct:
		return ir.ValDistinct
	case *probeVariant:
		if n.enum == "Bool" || n.enum == "bool.Bool" {
			return ir.ValBool
		}
		return ir.ValVariant
	case *rt.Record:
		switch n.Desc.Kind {
		case rt.KindStruct, rt.KindAnon:
			return ir.ValStruct
		case rt.KindTuple:
			return ir.ValTuple
		case rt.KindDistinct:
			return ir.ValDistinct
		case rt.KindEnum:
			return ir.ValVariant
		}
	case bool:
		return ir.ValBool
	case int64:
		return ir.ValInt
	case float64:
		return ir.ValFloat
	case rt.Decimal:
		return ir.ValDecimal
	case string:
		return ir.ValString
	case rt.Unit:
		return ir.ValUnit
	}
	return ir.ValUnknown
}

// v12IsLeaf reports whether the shaper handed back the leaf it was given: the
// leaf itself, or a scalar equal to it.
func v12IsLeaf(arg any, leaf *probeLeaf) bool {
	if l, isLeaf := arg.(*probeLeaf); isLeaf {
		return l == leaf
	}
	switch arg.(type) {
	case bool, int64, float64, string, rt.Byte, rt.Bytes, rt.Unit:
		return arg == leaf.v
	}
	return false
}

// v12RunWithRecordedShapes is check 1 and check 2 over one population.
//
// The parameter's own shape is read, and the leaf inside a structure is still
// searched. A field read off a struct parameter holds the projection's result
// type, which the parameter's recorded shape does not state, so fixing the
// inner leaf as well would fail bodies for a reason unrelated to the
// recording. A parameter whose declaration names a scalar gets that scalar in
// every vector, which is what makes a wrong scalar shape fail all of them.
//
// Each boot and each run is made inside a virtual-time bubble, as
// TestIRRetainedPopulationRuns makes them (see vmInBubble).
func v12RunWithRecordedShapes(t *testing.T, label string, entries []vmProbeEntry,
	wantRan int, kinds map[*ir.Func][]kind) {
	t.Helper()
	vmPrepareBubbles()

	// leaves is `vmArgShapes`'s own list, in its own order, so a declined
	// parameter and a structure's interior get the same search they get there.
	leaves := []any{
		int64(1),
		"s",
		true,
		1.5,
		rt.Decimal{},
		rt.Byte(65),
		rt.Bytes("A"),
		// vmArgShapes' Maybe leaf, for a Maybe only a host call consumes.
		&probeVariant{enum: "maybe.Maybe", variant: "Some", payload: int64(1)},
	}
	if re := vmRegexHandle(); re != nil {
		leaves = append(leaves, re)
	}
	leaves = append(leaves, vmCalendarValues()...)
	leaves = append(leaves, vmChannelHalves()...)
	leaves = append(leaves, vmHandleLeaves()...)

	// Every retained function runs on made-up arguments, and a String argument
	// is "s": a corpus body that writes a file (15-app-and-defer's
	// `write_all(path, tasks)`) writes one named "s". The calls run from a
	// temporary directory so a relative path lands there, not in this package.
	t.Chdir(t.TempDir())

	total, ran := 0, 0
	var failed []string
	// declined is a parameter whose kind states no shape; scalar and structural
	// are the two halves of the ones that do.
	declined, scalar, structural := 0, 0, 0
	// agreed and disagreed are check 2 over the parameters where the shaper
	// derived a structure, so the two readings are both non-vacuous.
	agreed := 0
	var disagreed []string
	counted := map[*ir.Func]bool{}
	// run makes the machine and calls fn in a bubble.
	run := func(e vmProbeEntry, args []any) error {
		err, answered, bubbleErr := vmInBubble(func() error {
			_, err := vmRunSymV(vmProgram(e.mod, e.links), e.fn.Sym(), args...)
			return err
		})
		if !answered {
			return vmBlockedError(e.fn, bubbleErr)
		}
		return err
	}
	for _, e := range entries {
		mod, fn := e.mod, e.fn
		total++
		// A boot starts on a boot frame, as TestIRRetainedPopulationRuns does;
		// its two parameters are the startup snapshot and a root Context,
		// not leaves.
		type started struct {
			isBoot bool
			err    error
		}
		boot, answered, bubbleErr := vmInBubble(func() started {
			isBoot, err := vmStartBoot(mod, e.links, fn)
			return started{isBoot, err}
		})
		if !answered {
			boot = started{true, vmBlockedError(fn, bubbleErr)}
		}
		if boot.isBoot {
			counted[fn] = true
			if boot.err == nil {
				ran++
			} else {
				failed = append(failed, fn.Name()+": "+boot.err.Error())
			}
			continue
		}
		okAny := false
		var lastErr error
		for _, search := range leaves {
			sh := &vmShaper{mod: mod, links: e.links, projs: map[*ir.Func]map[ir.Temp][]*ir.Proj{},
				fwd: map[*ir.Func]map[ir.Temp][]vmFwd{}}
			args := make([]any, 0, len(fn.Params()))
			for _, p := range fn.Params() {
				scalarLeaf, isScalar := v12LeafFor(p.Shape)
				leaf := &probeLeaf{v: scalarLeaf}
				if !isScalar {
					// A decline, or a structure whose interior is a Proj
					// result type. Either way the declaration does not
					// name a leaf and the search supplies one.
					leaf = &probeLeaf{v: search}
				}
				if !counted[fn] {
					switch {
					case p.Shape == ir.ValUnknown:
						declined++
					case isScalar:
						scalar++
					default:
						structural++
					}
				}
				arg := sh.argFor(fn, p.Temp, leaf, 0)

				// Check 2. Only where the shaper derived something: if it
				// handed back the leaf it was given it has read no
				// projection and has no independent answer. Counted on the
				// first vector only, because every vector reads one graph.
				if got := v12ShapeOfValue(arg); !counted[fn] && !v12IsLeaf(arg, leaf) &&
					got != ir.ValUnknown && p.Shape != ir.ValUnknown {
					if got == p.Shape {
						agreed++
					} else {
						disagreed = append(disagreed, fn.Name()+"("+p.Sym.Name()+
							"): the declaration records "+p.Shape.String()+
							" and the projections that read it say "+got.String())
					}
				}
				args = append(args, arg)
			}
			counted[fn] = true
			if err := run(e, args); vmRan(err) {
				okAny = true
				break
			} else {
				lastErr = err
				if vmHardFailure(err) {
					break
				}
			}
		}
		// A body that hands its parameters straight to a host reads no
		// projection the leaves could follow; its declared kinds give one
		// argument per parameter.
		// A blocked run waits on the same channel whatever it is handed.
		if !okAny && (lastErr == nil || !strings.Contains(lastErr.Error(), vmBlockedText)) {
			// A std body's own struct parameters are sampled field by
			// field under the module's run-time qualifier, as
			// TestIRRetainedStdPopulationRuns samples them.
			qual := ""
			if label == "std" {
				qual = irModuleQualifier(mod.Name())
			}
			if args, ok := vmArgsForKindsIn(kinds[fn], qual); ok && len(kinds[fn]) == len(fn.Params()) {
				if err := run(e, args); vmRan(err) {
					okAny = true
				} else {
					lastErr = err
				}
			}
		}
		if okAny {
			ran++
			continue
		}
		failed = append(failed, fn.Name()+": "+lastErr.Error())
	}

	if total == 0 {
		t.Fatalf("%s: no retained function was collected, so every count below is vacuous",
			label)
	}
	t.Logf("%s: %d retained functions, %d executed with every parameter's own leaf taken "+
		"from its recorded shape (the unconstrained search reaches %d)",
		label, total, ran, wantRan)
	t.Logf("%s: parameters — %d state a scalar shape, %d state a structural one, %d declined",
		label, scalar, structural, declined)
	t.Logf("%s: check 2 — %d parameter(s) where the projections and the declaration both "+
		"answer, %d disagreement(s)", label, agreed, len(disagreed))
	for _, d := range disagreed {
		t.Errorf("%s: %s", label, d)
	}
	if ran != wantRan {
		for _, f := range failed {
			t.Logf("%s:   FAILED %s", label, f)
		}
		t.Errorf("%s: fixing each parameter's leaf from its recorded shape ran %d "+
			"functions where the free search runs %d. A recorded scalar shape that "+
			"names the wrong leaf is the way this falls, and the failures are logged "+
			"above", label, ran, wantRan)
	}
	if agreed == 0 {
		t.Errorf("%s: check 2 compared nothing, so a wrong composite shape would pass "+
			"this test silently", label)
	}
	if scalar == 0 {
		t.Errorf("%s: no parameter states a scalar shape, so check 1 is vacuous", label)
	}
}

// TestIRParamShapeAgreesWithTheVM is the oracle for the corpus, over the
// population TestIRRetainedPopulationRuns probes.
func TestIRParamShapeAgreesWithTheVM(t *testing.T) {
	entries, _, _ := vmCorpusPopulation(t)
	v12RunWithRecordedShapes(t, "corpus", entries, vmWantRunnable, nil)
}

// TestIRParamShapeAgreesWithTheVMForStd is the same oracle for `std`,
// which is the larger of the two populations and the one whose parameters are
// dominated by destructured distinct types — `fn add(Duration(a), Duration(b))`
// — so it is where a wrong `ValDistinct` would show up.
//
// Cached modules supply the same cross-file links as
// TestIRRetainedStdPopulationRuns.
func TestIRParamShapeAgreesWithTheVMForStd(t *testing.T) {
	// Warmed first: `buildStdlibIndex` is not idempotent from a fresh
	// process, and only the warm reading is reproducible from inside a shared
	// test binary.
	buildStdlibIndex()

	idx := buildStdlibIndex()
	var mods []*ir.Module
	for _, mod := range idx.irModules {
		mods = append(mods, mod)
	}
	var entries []vmProbeEntry
	for _, mod := range mods {
		for _, fn := range mod.Funcs() {
			entries = append(entries, vmProbeEntry{mod: mod, fn: fn, rel: mod.Name(), links: mods})
		}
	}
	kinds := map[*ir.Func][]kind{}
	for _, f := range idx.byKey {
		if f.irBody != nil {
			kinds[f.irBody] = f.params
		}
		for arity, body := range f.irArity {
			kinds[body] = f.params[:arity]
		}
		for slot, body := range f.irSlot {
			kinds[body] = f.params[:slot]
		}
	}
	v12RunWithRecordedShapes(t, "std", entries, vmStdWantRunnable, kinds)
}

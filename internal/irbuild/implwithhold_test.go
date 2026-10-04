package irbuild

import (
	"sort"
	"strings"
	"testing"
)

// A WITHHELD METHOD IS ALWAYS ONE NOTHING CAN DISPATCH TO — over the whole
// corpus, not over a fixture.
//
// # The property, and why it is the one worth pinning
//
// `implDef.mayWithhold` is the safety condition of the whole narrowing: a block
// may lower without supplying a method only when no route can select that
// method. If a withheld method's dispatch table were live, `tableCall` would
// emit `m.table.Get(recv)(…)` for a receiver that binds no entry — a RUN-TIME
// TRAP on a program the checker accepted, which is strictly worse than
// refusing the block.
//
// # THE GRANULARITY THIS IS ASSERTED AT, stated because it is the point
//
// The unit is `(impl block, method name)`. A test that asked the FILE's blocker
// set instead would not change if a dispatchable method were withheld, because
// a sibling refusal renders under the same key at the coarser unit. So the
// reading walks `d.gaps` name by name and asks `d.iface.methods[name].dispatchable()` for
// each, which is the same question `mayWithhold` asks and the only one that
// separates a safe withholding from an unsafe one.
//
// # NOT VACUOUS, AND THE COUNT SAYS SO
//
// A run in which nothing is ever withheld would pass while measuring nothing,
// so the total is asserted positive. That is this package's own rule about a
// zero-member category: without it, deleting every `noteGap` call passes green.
func TestIRImplWithhold_EveryWithheldMethodIsUndispatchable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	_, all := corpusAnalysis(t)

	withheld := 0
	var bad []string
	for _, f := range all {
		if f.AnalyzeErr != nil || f.Prog == nil {
			continue
		}
		for _, g := range implWithholdGensOf(f.Prog) {
			for _, d := range implWithholdAllImplDefs(g) {
				names := make([]string, 0, len(d.gaps))
				for name := range d.gaps {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					withheld++
					if d.iface == nil {
						continue
					}
					m := d.iface.methods[name]
					if m != nil && m.dispatchable() {
						bad = append(bad, f.Rel+": "+d.label()+"."+name+
							" (why "+m.why+")")
					}
				}
			}
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d withheld method(s) are still DISPATCHABLE, so an erased call would "+
			"emit `table.Get(recv)` for a receiver that binds no entry — a run-time trap "+
			"where the block should refuse:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
	if withheld == 0 {
		t.Fatalf("no method was withheld anywhere in the corpus, so this reading measured "+
			"nothing. Deleting every `noteGap` call passes a run like this; the corpus "+
			"contains `interface_defaults_test.nomi`, whose `Ranked.prefer` and "+
			"`Chooser.choose` are both withheld, so a zero here means the walk is broken "+
			"rather than that the builder changed. Files walked: %d", len(all))
	}
	t.Logf("withheld methods across the corpus: %d, all undispatchable", withheld)
}

// `implItem.lowerable` HAS NO WRITERS, AND THIS IS THE TEST THAT SAYS SO.
//
// A withheld method is ABSENT from `d.items` rather than present-and-refused,
// because most readers of `d.items[name]` consult only `!= nil`, not
// `lowerable`: `bindImpl`, `interfaceCall`, `typeQualifiedCall`,
// `foreignIfaceCall`, `equalCall`, `to_string`, `assertable.go`,
// `genericimpl.go`, `operimpl.go`, `stdenum.go`, `tail.go` and one of
// `literal.go`'s readers among them. They would take the item's `goName`,
// naming a function `emitImpl` never wrote.
//
// So the hook is a trap: writing `false` into it looks like the smaller change
// and is the larger one. This test is that sentence with a failure mode. It
// fails the moment somebody sets it, and its message is where they have to look
// first.
func TestIRImplWithhold_WithheldItemsAreAbsentRatherThanUnlowerable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	_, all := corpusAnalysis(t)

	items := 0
	var bad []string
	for _, f := range all {
		if f.AnalyzeErr != nil || f.Prog == nil {
			continue
		}
		for _, g := range implWithholdGensOf(f.Prog) {
			for _, d := range implWithholdAllImplDefs(g) {
				for _, it := range d.order {
					items++
					if !it.lowerable {
						bad = append(bad, f.Rel+": "+d.label()+"."+it.name)
					}
				}
			}
		}
	}
	if len(bad) > 0 {
		t.Errorf("%d impl item(s) carry lowerable=false. Most readers of "+
			"`d.items[name]` never consult that field and will call the item's goName for a "+
			"function `emitImpl` skipped. If this is deliberate, those readers are the "+
			"audit — start with impl.go's `bindImpl`, `interfaceCall`, `typeQualifiedCall` "+
			"and `foreignIfaceCall`:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
	if items == 0 {
		t.Fatalf("no impl items were walked at all, so this reading is vacuous over %d files", len(all))
	}
}

// implWithholdGensOf is every gen of a lowered program, or nothing when the program was
// not lowered far enough to have one.
//
// A gen is built here rather than reused from the shared corpus analysis
// because `GenerateIR` returns a `*Result` and not its gens; re-running the
// builder over the same checked program is the only way to reach the defs. The
// shared analysis is still what supplies the checked programs, so no second
// FRONT-END pass is paid.
func implWithholdGensOf(p *Program) []*gen {
	var out []*gen
	for i := range p.Modules {
		m := &p.Modules[i]
		g := newGen(m, "nomimod0", nil, nil, -1, nil)
		func() {
			defer func() { _ = recover() }()
			g.declareTypes()
			g.declareFuncs()
			g.emitModule(m)
			// Test bodies are built after the module walk, and a body is what
			// instantiates a generic impl block.
			g.irRetryWalkOnlyTestBodies()
		}()
		out = append(out, g)
	}
	return out
}

// implWithholdAllImplDefs is every impl def a gen built, through BOTH registration routes.
//
// `g.implOrder` holds the ordinary blocks and `g.genericImplQueue` holds those
// registered against a generic INSTANCE, which `registerImplAt` deliberately
// keeps out of `implOrder`. A walk over one route only would report a vacuous
// zero for generic instances, so both are read and the
// result is deduplicated by pointer.
func implWithholdAllImplDefs(g *gen) []*implDef {
	seen := map[*implDef]bool{}
	out := make([]*implDef, 0, len(g.implOrder)+len(g.genericImplQueue))
	for _, d := range g.implOrder {
		if d != nil && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, e := range g.genericImplQueue {
		if e.impl != nil && !seen[e.impl] {
			seen[e.impl] = true
			out = append(out, e.impl)
		}
	}
	return out
}

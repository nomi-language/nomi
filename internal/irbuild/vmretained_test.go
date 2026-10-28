package irbuild

// The VM against the builder's whole retained corpus population.
//
// Every function the builder retains over the front-end-accepted corpus runs
// on the VM with arguments synthesized from its graph. A function either runs
// or fails in exactly one classified bucket, and each function's outcome is
// recorded in a committed list (vmRetainedList). The
// retention count alone cannot see a body the machine cannot execute, because
// the builder retains the same graph either way; this test is where that shows.

import (
	"errors"
	"fmt"
	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"

	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/rt/vclock"
)

// THE POPULATION IS A COMMITTED LIST, testdata/expectations/vm-retained.txt:
// one line per retained function, `<declaring file>:<name>\t<outcome>`,
// where the outcome is `ran` or the failure bucket. A function added,
// dropped or moved between buckets shows as a diff of that file and names
// itself; regenerate with vmRetainedRegenerate and name the cause in the
// commit message. One declaration can retain several bodies (a generic
// function's instantiations), so a line may repeat.
//
// A function that ran in the committed list and no longer runs is a VM
// regression, and the test reports it by name before any other difference.
//
// WHAT "RAN" MEANS is one rule the machine observes (vmRan): the machine
// entered the function's body (vm.Machine.Entries, counted at pc 0 of every
// body compiled WithEntryCounts) and the run did not end in a machine
// refusal (vmRefused). Whatever else ends the run is the body's answer on
// the probe's made-up arguments: a trap, an `Err`, a `todo`, the step or
// call-depth limit, waiting on a channel nothing answers, Supervisor.new
// outside boot, a boot that returns `Err`, or a fault on a field the probe
// could not build. The bytecode compiler never fails (a construct it cannot
// run compiles to a refusal raised on arrival), so entry alone is not
// enough: a refusal shows only when execution reaches its instruction.
//
// A dispatched call whose receiver's run-time type has no retained impl
// (vm.DispatchMiss) is LINKING, since the impl may be one the builder did
// not retain, unless the receiver is not a value of the type the call
// declares for that operand: then the probe made it up and the miss is the
// body's answer (vmProbeReceiver). The machine can decide that only for a
// concrete declared type, and every dispatched operand the builder makes is
// an interface value, so today every dispatch miss is LINKING. The probe
// hands an interface-typed temporary a value of a type the program retains
// an impl of (vmShaper.ifaceArg), so a miss is not its own made-up receiver;
// an interface with no retained impl keeps the trial leaf, and the test logs
// each function that takes one.
//
// A failure is one of the refusal buckets, or PROBE (vmNotEnteredClass): the
// machine did not enter the body on any synthesized argument vector, because
// a parameter refused the argument (a counting machine also refuses a
// scalar of the wrong kind) or the argument could not be built.
//
// Two buckets must stay empty whatever the list says. VOCABULARY: the builder
// put an instruction into a retained body that the machine cannot run.
// IDENTITY: a retained graph reads a local its own parameter list does not
// declare, which `ir.Lint`'s RuleLocalDeclared also reports (internal/ir's
// TestLintLocalDeclared_CatchesItsPlant builds that graph, and
// TestLintLocalDeclared_TheLocalControlIsClean its clean control).
//
// The other buckets are expected and their members are in the list. LINKING
// and HOST count bodies that call out of their module to a callee with no VM
// body or binding; they move when the builder admits a caller before its
// callee, which is not a machine gap (planted positives:
// TestVMCoverage_TheLinkingClassifierCatchesItsPlant and
// TestVMCoverage_TheHostClassifierCatchesItsPlant). UNLINKED is a host fn
// that crosses into a project's Go binding this test binary does not link
// (vmUnlinkedClass). NEVER takes an Infallible parameter, so nothing can call
// it. PROBE is an artifact of argument synthesis, not of the machine; the
// corpus runs each member with real values.
//
// std's population has the same list, vm-std-retained.txt
// (TestIRRetainedStdPopulationRuns, vmstdretained_test.go).

// vmRetainedList is one committed population list: its file under
// testdata/expectations, the command that rewrites it, and what it covers.
type vmRetainedList struct {
	file       string
	regenerate string
	// population names what the list covers in its header, and test the
	// test that probes it.
	population, test string
}

// vmRetainedCorpus is the corpus's list, TestIRRetainedPopulationRuns's.
var vmRetainedCorpus = vmRetainedList{
	file: "vm-retained.txt",
	regenerate: "NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild " +
		"-run '^TestIRRetainedPopulationRuns$' -count=1",
	population: "the corpus",
	test:       "TestIRRetainedPopulationRuns",
}

// vmRetainedStd is std's list, TestIRRetainedStdPopulationRuns's.
var vmRetainedStd = vmRetainedList{
	file: "vm-std-retained.txt",
	regenerate: "NOMI_REGENERATE_EXPECTATIONS=1 go test ./internal/irbuild " +
		"-run '^TestIRRetainedStdPopulationRuns$' -count=1",
	population: "the cached std modules",
	test:       "TestIRRetainedStdPopulationRuns",
}

// committed is the committed list's lines, sorted.
func (l vmRetainedList) committed(t *testing.T) []string {
	t.Helper()
	dir, err := expectation.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, l.file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v; create it with\n  %s", path, err, l.regenerate)
	}
	var want []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			want = append(want, line)
		}
	}
	sort.Strings(want)
	return want
}

// ranCount is how many functions the committed list says the VM runs, the
// number TestIRParamShapeAgreesWithTheVM and its std counterpart hold their
// readings to.
func (l vmRetainedList) ranCount(t *testing.T) int {
	t.Helper()
	n := 0
	for _, line := range l.committed(t) {
		if strings.HasSuffix(line, "\tran") {
			n++
		}
	}
	return n
}

// check compares the population's lines with the committed list, or rewrites
// the list under NOMI_REGENERATE_EXPECTATIONS=1.
func (l vmRetainedList) check(t *testing.T, lines []string) {
	t.Helper()
	sort.Strings(lines)
	dir, err := expectation.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, l.file)
	if expectation.RegenerateRequested() {
		var b strings.Builder
		fmt.Fprintf(&b, "# Every function the builder retains over %s, and whether the VM\n", l.population)
		b.WriteString("# ran it on synthesized arguments (internal/irbuild's\n")
		fmt.Fprintf(&b, "# %s). A line that leaves `ran` is a regression;\n", l.test)
		b.WriteString("# name the cause of every moved line. Regenerate with:\n")
		fmt.Fprintf(&b, "#   %s\n", l.regenerate)
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("REWROTE %s with %d function(s) because NOMI_REGENERATE_EXPECTATIONS=1", path, len(lines))
		return
	}
	want := l.committed(t)

	// RAN BEFORE, DOES NOT RUN NOW: per function key, the committed list's
	// `ran` count against this run's.
	ranCount := func(ls []string) (map[string]int, map[string][]string) {
		ran, other := map[string]int{}, map[string][]string{}
		for _, line := range ls {
			key, outcome, _ := strings.Cut(line, "\t")
			if outcome == "ran" {
				ran[key]++
			} else {
				other[key] = append(other[key], outcome)
			}
		}
		return ran, other
	}
	wantRan, _ := ranCount(want)
	gotRan, gotOther := ranCount(lines)
	var regressed []string
	for key, n := range wantRan {
		if gotRan[key] < n {
			now := strings.Join(gotOther[key], "; ")
			if now == "" {
				now = "no longer retained"
			}
			regressed = append(regressed, fmt.Sprintf("    %s: %s", key, now))
		}
	}
	sort.Strings(regressed)
	if len(regressed) > 0 {
		t.Errorf("%d function(s) the VM ran before no longer run. This is the regression "+
			"the retention count cannot see:\n%s", len(regressed), strings.Join(regressed, "\n"))
	}

	// EVERY OTHER MOVE: a multiset difference of the two lists.
	count := func(ls []string) map[string]int {
		m := map[string]int{}
		for _, line := range ls {
			m[line]++
		}
		return m
	}
	wantN, gotN := count(want), count(lines)
	var diff []string
	for line, n := range wantN {
		for i := gotN[line]; i < n; i++ {
			diff = append(diff, "  - "+line)
		}
	}
	for line, n := range gotN {
		for i := wantN[line]; i < n; i++ {
			diff = append(diff, "  + "+line)
		}
	}
	sort.Slice(diff, func(i, j int) bool { return diff[i][4:] < diff[j][4:] })
	if len(diff) > 0 {
		t.Errorf("the retained population differs from %s (- committed, + this run). If the "+
			"move is intended, regenerate with\n  %s\nand name each moved function's cause.\n%s",
			l.file, l.regenerate, strings.Join(diff, "\n"))
	}
}

// vmNotEnteredClass is a function the machine entered on none of the
// synthesized argument vectors, with no refusal to name: a parameter refused
// the argument at entry, or the argument could not be built. An artifact of
// argument synthesis, not of the machine.
const vmNotEnteredClass = "PROBE: the machine entered the body on no synthesized argument"

// vmNeverParamClass is a function with a parameter of Infallible, which has
// no values, so no call can pass one an argument (`impl Display for
// Infallible`). The probe does not run it: any argument it made up would be a
// value of another type.
const vmNeverParamClass = "NEVER: a parameter is Infallible, so nothing can call it"

// vmNeverParam reports whether fn takes a parameter of Infallible.
func vmNeverParam(fn *ir.Func) bool {
	for _, p := range fn.Params() {
		if t := fn.TempType(p.Temp); t != nil && t.Kind() == ir.KindNever {
			return true
		}
	}
	return false
}

// vmArgShapes is one argument vector per scalar shape the retained grammar
// admits, with a composite built wherever the graph projects out of a
// parameter.
//
// Every scalar shape is tried and a function counts as runnable if it runs
// on any (vmRan). That cannot manufacture a pass for the VOCABULARY pin: a "this
// machine runs no X" refusal is a fact about the instruction, and no argument
// choice produces or avoids it, which is why the loop stops on one rather than
// trying the rest.
//
// The derivation is interprocedural. In `bump(bump(c))` nothing in the
// caller's own graph projects off `c`; the demand on `c` is stated by the
// callee's graph, so a call argument forwards the callee parameter's demand
// back to the caller's temporary.
//
// A call is followed only where the machine would follow it: a
// `CalleeDirect`, non-crossing call resolved by symbol identity in the entry or
// linked modules, as `callInstr` does. A LINKING failure therefore still
// reports as LINKING, because there is no callee graph to read a demand out of.
//
// The derivation is bounded by depth, because a recursive function would
// otherwise derive its own argument forever. A demand deeper than four call
// edges is not derived and the function is fed a leaf, which fails at the read
// rather than passing quietly.
func vmArgShapes(mod *ir.Module, f *ir.Func, links ...*ir.Module) [][]any {
	shapes := []any{
		int64(1),
		"s",
		true,
		1.5,
		rt.Decimal{},
		rt.Byte(65),
		rt.Bytes("A"),
		// A Maybe a host call consumes, such as Maybe.to_result's receiver,
		// has no Match or projection in the graph to state its shape.
		&probeVariant{enum: "maybe.Maybe", variant: "Some", payload: int64(1)},
	}
	// A host handle is a leaf no literal builds: std's Regex bodies receive
	// the handle Regex.compile answers, through the same extern table the
	// machine calls.
	if re := vmRegexHandle(); re != nil {
		shapes = append(shapes, re)
	}
	shapes = append(shapes, vmCalendarValues()...)
	shapes = append(shapes, vmChannelHalves()...)
	shapes = append(shapes, vmHandleLeaves()...)
	sh := &vmShaper{mod: mod, links: links, projs: map[*ir.Func]map[ir.Temp][]*ir.Proj{},
		fwd: map[*ir.Func]map[ir.Temp][]vmFwd{}}
	out := make([][]any, 0, len(shapes))
	for _, shape := range shapes {
		leaf := &probeLeaf{v: shape}
		args := make([]any, 0, len(f.Params()))
		for _, p := range f.Params() {
			args = append(args, sh.argFor(f, p.Temp, leaf, 0))
		}
		out = append(out, vmOperands(args))
	}
	return out
}

// vmParamAliases maps the destination of every `RefLocal` that names a
// parameter to that parameter's own temporary.
//
// A destructuring parameter's projection reads the parameter's temporary
// directly, but a destructuring binding written in the body (`Email(s) = e`)
// reads `e` through an `ir.Ref` first, so the projection's subject is the Ref's
// destination. Without this hop such a parameter is fed a bare scalar where
// the graph asks for a distinct. A call argument is a local read too, so the
// forward edge `vmShaper.index` builds resolves through the same hop.
//
// Resolved by the symbol's identity, which is the question `ir.Lint`'s
// RuleLocalDeclared asks: a `RefLocal` names a local this function declares,
// compared by identity and not by printed name.
func vmParamAliases(f *ir.Func) map[ir.Temp]ir.Temp {
	byParam := map[*ir.Symbol]ir.Temp{}
	for _, p := range f.Params() {
		byParam[p.Sym] = p.Temp
	}
	out := map[ir.Temp]ir.Temp{}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			r, isRef := in.(*ir.Ref)
			if !isRef || r.Kind() != ir.RefLocal {
				continue
			}
			if t, isParam := byParam[r.Sym()]; isParam {
				out[r.Dst()] = t
			}
		}
	}
	return out
}

// vmFwd is "this temporary is passed as parameter `t` of `fn`", so whatever
// `fn`'s graph demands of that parameter is demanded of the temporary.
type vmFwd struct {
	fn *ir.Func
	t  ir.Temp
}

// vmShaper derives the value a temporary must hold, from the projections that
// read it in its own function and from those that read it in a callee.
type vmShaper struct {
	mod   *ir.Module
	links []*ir.Module
	projs map[*ir.Func]map[ir.Temp][]*ir.Proj
	fwd   map[*ir.Func]map[ir.Temp][]vmFwd
	// built is builtTypes' answer, computed once.
	built map[string]bool
}

// callee follows the same identity and ambiguity rules as vm.NewProgram.
// Projections in a cached stdlib body constrain its callers' arguments too.
func (s *vmShaper) callee(sym *ir.Symbol) *ir.Func {
	if f := s.mod.FuncFor(sym); f != nil {
		return f
	}
	var found *ir.Func
	for _, mod := range s.links {
		if mod == nil || mod == s.mod {
			continue
		}
		if f := mod.FuncFor(sym); f != nil {
			if found != nil {
				return nil
			}
			found = f
		}
	}
	return found
}

// index builds both maps for one function, with a `RefLocal` of a parameter
// resolved to the parameter and a pattern binding resolved to the value it
// binds. See vmParamAliases. The binding hop is what carries a demand from
// `Shape.Circle(c) -> c.r`, or from an enum's derived Debug passing an
// embedded struct to its own Debug impl, back to the payload projection.
func (s *vmShaper) index(f *ir.Func) {
	if s.projs[f] != nil {
		return
	}
	alias := vmParamAliases(f)
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if bind, isBind := in.(*ir.Bind); isBind {
				alias[bind.Dst()] = bind.Src()
			}
		}
	}
	resolve := func(t ir.Temp) ir.Temp {
		for {
			a, aliased := alias[t]
			if !aliased || a == t {
				return t
			}
			t = a
		}
	}
	byProj := map[ir.Temp][]*ir.Proj{}
	byFwd := map[ir.Temp][]vmFwd{}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Proj:
				subj := resolve(n.Subject())
				byProj[subj] = append(byProj[subj], n)
			case *ir.Call:
				if n.Form() != ir.CalleeDirect || n.Crosses() {
					continue
				}
				callee := s.callee(n.Callee())
				if callee == nil || len(callee.Params()) != n.NumArgs() {
					continue
				}
				for i := range n.NumArgs() {
					a := resolve(n.Arg(i))
					byFwd[a] = append(byFwd[a], vmFwd{fn: callee, t: callee.Params()[i].Temp})
				}
			}
		}
	}
	// A slot several paths copy into (a binding's `else` joins the payload
	// it read with the fallback it built) demands of each source what is
	// read off the slot, so `Some(#(Pair(x, _), y)) = m else { ... }`
	// demands a tuple of m's payload. Repeated while a copy into a copied
	// slot still carries a demand on.
	for carried := true; carried; {
		carried = false
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				c, isCopy := in.(*ir.Copy)
				if !isCopy {
					continue
				}
				dst, src := resolve(c.Dst()), resolve(c.Src())
				for _, p := range byProj[dst] {
					if dst != src && !slices.Contains(byProj[src], p) {
						byProj[src] = append(byProj[src], p)
						carried = true
					}
				}
			}
		}
	}
	s.projs[f], s.fwd[f] = byProj, byFwd
}

// argFor is the value t must hold for every projection that reads it — here
// or in a callee — to answer. `leaf` where nothing reads it structurally.
func (s *vmShaper) argFor(f *ir.Func, t ir.Temp, leaf *probeLeaf, depth int) any {
	if depth > 4 {
		return leaf
	}
	// A temporary whose stored type is a scalar takes that scalar's leaf.
	// The VM keeps a scalar in a typed register and refuses a value of
	// another kind at the write, so a struct built with one trial leaf in
	// every field (an Int field holding "s") does not run; the type says
	// which leaf the field holds.
	if v, ok := vmLeafForType(f.TempType(t)); ok {
		return v
	}
	// An interface value takes a value of a type the program implements
	// the interface for.
	if v, ok := s.ifaceArg(f, t, leaf, depth); ok {
		return v
	}
	// An indirect callee demands a callable, not one of the scalar leaves.
	// Build it through the VM's real closure instruction so the probe does
	// not introduce a second callable implementation. Its result is the same
	// trial leaf used for the other arguments.
	aliases := vmParamAliases(f)
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			call, ok := in.(*ir.Call)
			if !ok || call.Form() != ir.CalleeIndirect {
				continue
			}
			callee := call.Fn()
			if alias, ok := aliases[callee]; ok {
				callee = alias
			}
			if callee == t {
				return vmProbeCallback(call.NumArgs(), leaf)
			}
		}
	}
	s.index(f)
	reads := s.projs[f][t]
	if len(reads) == 0 {
		// Tag-only enums and list-length-only cases supply their shape through
		// Match rather than a projection. Scalar trial leaves are invalid here.
		for _, block := range f.Blocks() {
			for _, instruction := range block.Instrs() {
				if iter, ok := instruction.(*ir.Iter); ok && iter.Over() == ir.IterOverList {
					source := iter.Arg(0)
					if alias, ok := aliases[source]; ok {
						source = alias
					}
					if source == t {
						return &probeList{}
					}
				}
				// A container host states its container operands: Map.get's
				// map and List.concat's lists, which derived FromJson bodies
				// and std/json's shape_error_prepend pass straight through.
				if call, ok := instruction.(*ir.Call); ok && call.Crosses() {
					for i := range call.NumArgs() {
						operand := call.Arg(i)
						if alias, ok := aliases[operand]; ok {
							operand = alias
						}
						if src, bound := vmBindSources(f)[operand]; bound {
							operand = src
						}
						if operand != t {
							continue
						}
						switch name := call.Callee().Name(); {
						case strings.HasPrefix(name, "Map.") && (i == 0 || name == "Map.merge"):
							return vmEmptyMap()
						case name == "List.concat":
							return &probeList{}
						}
					}
				}
				// A functional update's base is a struct even when nothing
				// projects out of it; `Struct.update(app, #{...})` states no
				// field it reads. The machine requires the base to carry every
				// field the update replaces.
				if m, ok := instruction.(*ir.Make); ok && m.Kind() == ir.MakeUpdate {
					base := m.Operand(0)
					if alias, ok := aliases[base]; ok {
						base = alias
					}
					if base == t {
						sv := &probeStruct{name: "Probe", fields: map[string]any{}}
						for _, name := range m.Names() {
							sv.fields[name] = leaf
						}
						return sv
					}
				}
				if cmp, ok := instruction.(*ir.Compare); ok && !cmp.Ranked() {
					for _, operand := range []ir.Temp{cmp.Lhs(), cmp.Rhs()} {
						if alias, ok := aliases[operand]; ok {
							operand = alias
						}
						if operand != t {
							continue
						}
						if cmp.Shape() == ir.ValContainer {
							return &probeList{}
						}
						// A scalar comparison states its operands' leaf, which
						// a struct field read by a derived body needs when its
						// siblings hold other leaves.
						if v, ok := v12LeafFor(cmp.Shape()); ok {
							return v
						}
					}
				}
				match, ok := instruction.(*ir.Match)
				if !ok || (match.Kind() != ir.MatchVariant && match.Kind() != ir.MatchListLen && match.Kind() != ir.MatchListMin) {
					continue
				}
				subject := match.Subject()
				if alias, ok := aliases[subject]; ok {
					subject = alias
				}
				if subject == t {
					if match.Kind() == ir.MatchListLen || match.Kind() == ir.MatchListMin {
						items := make([]any, match.Arity())
						for i := range items {
							items[i] = leaf
						}
						return &probeList{items: items}
					}
					return &probeVariant{enum: match.Sym().Name(), variant: match.Variant()}
				}
			}
		}
		// No local demand, so the demand is a callee's if there is one. The
		// FIRST non-leaf answer wins: two callees demanding two different
		// shapes of one argument is a graph the checker would have refused,
		// so there is nothing to reconcile.
		//
		// EXCEPT FIELD DEMANDS, which compose: `NaiveDateTime.of` forwards
		// one Date to `Date.year` and `Date.month`, and each reads its own
		// field of the same struct. Their fields are merged into one value.
		var found any = leaf
		for _, fw := range s.fwd[f][t] {
			v := s.argFor(fw.fn, fw.t, leaf, depth+1)
			if v12IsLeaf(v, leaf) {
				continue
			}
			if v12IsLeaf(found, leaf) {
				found = v
				continue
			}
			into, isStruct := found.(*probeStruct)
			more, alsoStruct := v.(*probeStruct)
			if !isStruct || !alsoStruct {
				break
			}
			for name, fv := range more.fields {
				if _, has := into.fields[name]; !has {
					into.fields[name] = fv
				}
			}
		}
		return found
	}
	switch reads[0].Kind() {
	case ir.ProjField, ir.ProjRecordField:
		sv := &probeStruct{name: "Probe", fields: map[string]any{}}
		for _, p := range reads {
			sv.fields[p.Name()] = s.argFor(f, p.Dst(), leaf, depth)
		}
		return sv
	case ir.ProjSlot:
		width := 0
		for _, p := range reads {
			if p.Index()+1 > width {
				width = p.Index() + 1
			}
		}
		tv := &probeTuple{items: make([]any, width)}
		for i := range tv.items {
			tv.items[i] = leaf
		}
		for _, p := range reads {
			tv.items[p.Index()] = s.argFor(f, p.Dst(), leaf, depth)
		}
		return tv
	case ir.ProjElem, ir.ProjSuffix:
		width := 0
		for _, p := range reads {
			n := p.Index()
			if p.Kind() == ir.ProjElem {
				n++
			}
			if n > width {
				width = n
			}
		}
		items := make([]any, width)
		for i := range items {
			items[i] = leaf
		}
		for _, p := range reads {
			if p.Kind() == ir.ProjElem {
				items[p.Index()] = s.argFor(f, p.Dst(), leaf, depth+1)
			}
		}
		return &probeList{items: items}
	case ir.ProjPayload:
		p := reads[0]
		if p.PayloadField() != "" {
			// A struct-shaped variant's fields are the variant's own; every
			// field this variant's reads name is filled.
			fields := map[string]any{}
			for _, r := range reads {
				if r.Kind() == ir.ProjPayload && r.Name() == p.Name() && r.PayloadField() != "" {
					fields[r.PayloadField()] = s.argFor(f, r.Dst(), leaf, depth)
				}
			}
			return &probeVariant{enum: p.Sym().Name(), variant: p.Name(), fields: fields}
		}
		return &probeVariant{enum: p.Sym().Name(), variant: p.Name(),
			payload: s.argFor(f, p.Dst(), leaf, depth)}
	case ir.ProjInner:
		p := reads[0]
		return &probeDistinct{name: p.Sym().Name(),
			inner: s.argFor(f, p.Dst(), leaf, depth)}
	}
	return leaf
}

// ifaceArg is the value an interface-typed temporary t of f holds: a value
// of a type the program implements the interface for, as a caller would pass
// one, so a dispatch on it reaches a retained impl and a dispatch miss is a
// builder gap rather than the probe's made-up receiver.
//
// The interface's methods are the dispatched calls whose receiver is t, in f
// or, when f only passes t on, in the callees it passes t to
// (vmShaper.ifaceMethods). The implementing type is the one the program's
// retained impls (ir.Module.Impls, the table the machine dispatches by)
// cover those methods for, preferring a type the program builds a value of
// (vmShaper.builtTypes). Its value is shaped by what the impls' bodies read
// off their receiver, as any struct argument is (argFor). A struct, enum,
// distinct or scalar is passed as itself, since the machine dispatches on
// its run-time type name; any other type is passed in the box
// ir.MakeExistential builds for it (rt.Box under the impl's type name).
//
// It answers false for a temporary that is not an interface value, and for
// an interface the program retains no impl of, which keeps the leaf.
func (s *vmShaper) ifaceArg(f *ir.Func, t ir.Temp, leaf *probeLeaf, depth int) (any, bool) {
	ty := f.TempType(t)
	if ty == nil || ty.Kind() != ir.KindIface || ty.Sym() == nil {
		return nil, false
	}
	methods := s.ifaceMethods(f, t, depth)
	if len(methods) == 0 {
		// Nothing dispatches on it within reach: every method of the
		// interface, by the name its method symbols carry
		// (`Conn.close`, `Gets<String>.got`).
		name := ty.Sym().Name()
		for _, impl := range s.impls() {
			m := impl.Method.Name()
			if strings.HasPrefix(m, name+".") || strings.HasPrefix(m, name+"<") {
				methods[impl.Method] = true
			}
		}
	}
	// Each implementing type, with the impls it brings for these methods.
	byType := map[string][]ir.Impl{}
	var order []string
	for _, impl := range s.impls() {
		if !methods[impl.Method] {
			continue
		}
		name := impl.Type.Name()
		if byType[name] == nil {
			order = append(order, name)
		}
		byType[name] = append(byType[name], impl)
	}
	if len(order) == 0 {
		return nil, false
	}
	built := s.builtTypes()
	best := ""
	for _, name := range order {
		if best == "" || vmBetterImplType(byType[name], built[name], byType[best], built[best]) {
			best = name
		}
	}
	// The receiver's value, with what every chosen impl body reads off it.
	var v any = leaf
	var self *ir.ValType
	for _, impl := range byType[best] {
		body := s.callee(impl.Func)
		if body == nil || len(body.Params()) == 0 || depth > 4 {
			continue
		}
		p := body.Params()[0].Temp
		if self == nil {
			self = body.TempType(p)
		}
		got := s.argFor(body, p, leaf, depth+1)
		if v12IsLeaf(v, leaf) {
			v = got
			continue
		}
		into, isStruct := v.(*probeStruct)
		more, alsoStruct := got.(*probeStruct)
		if isStruct && alsoStruct {
			for name, fv := range more.fields {
				if _, has := into.fields[name]; !has {
					into.fields[name] = fv
				}
			}
		}
	}
	if self == nil {
		return nil, false
	}
	if scalar, ok := vmLeafForType(self); ok {
		return scalar, true
	}
	switch self.Kind() {
	case ir.KindStruct:
		sv, isStruct := v.(*probeStruct)
		if !isStruct {
			sv = &probeStruct{fields: map[string]any{}}
		}
		sv.name = best
		return sv, true
	case ir.KindEnum, ir.KindDistinct, ir.KindTuple:
		if v12IsLeaf(v, leaf) {
			return nil, false
		}
		return v, true
	}
	// A container or other value with no run-time type name of its own is
	// an interface value only in a box that names its static type.
	return rt.Box(rt.BoxDesc(best), vmOperand(v)), true
}

// vmUnimplementedIfaces names fn's interface-typed parameters whose
// interface the program retains no impl of, so the probe hands them the
// trial leaf (ifaceArg answers false).
func vmUnimplementedIfaces(mod *ir.Module, fn *ir.Func, links []*ir.Module) []string {
	sh := &vmShaper{mod: mod, links: links, projs: map[*ir.Func]map[ir.Temp][]*ir.Proj{},
		fwd: map[*ir.Func]map[ir.Temp][]vmFwd{}}
	var out []string
	for _, p := range fn.Params() {
		ty := fn.TempType(p.Temp)
		if ty == nil || ty.Kind() != ir.KindIface {
			continue
		}
		if _, ok := sh.ifaceArg(fn, p.Temp, &probeLeaf{v: int64(1)}, 0); !ok {
			out = append(out, ty.String())
		}
	}
	return out
}

// vmBetterImplType reports whether the type with impls a (built says the
// program builds a value of it) is a better receiver than the one with b:
// it covers more of the methods, or as many and the program builds it.
func vmBetterImplType(a []ir.Impl, aBuilt bool, b []ir.Impl, bBuilt bool) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return aBuilt && !bBuilt
}

// ifaceMethods are the interface methods dispatched on temporary t of f:
// each dispatched call whose receiver operand is t, and, followed up to the
// probe's depth, each one on the parameter of a callee t is passed to.
func (s *vmShaper) ifaceMethods(f *ir.Func, t ir.Temp, depth int) map[*ir.Symbol]bool {
	out := map[*ir.Symbol]bool{}
	aliases := vmParamAliases(f)
	binds := vmBindSources(f)
	resolve := func(x ir.Temp) ir.Temp {
		if a, ok := aliases[x]; ok {
			x = a
		}
		if b, ok := binds[x]; ok {
			x = b
		}
		return x
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			call, isCall := in.(*ir.Call)
			if !isCall || call.Form() != ir.CalleeDispatched {
				continue
			}
			if k := call.KeyAt(); k < call.NumArgs() && resolve(call.Arg(k)) == t {
				out[call.Callee()] = true
			}
		}
	}
	if len(out) > 0 || depth > 4 {
		return out
	}
	s.index(f)
	for _, fw := range s.fwd[f][t] {
		for m := range s.ifaceMethods(fw.fn, fw.t, depth+1) {
			out[m] = true
		}
	}
	return out
}

// impls are the dispatch entries of the shaper's module and its links.
func (s *vmShaper) impls() []ir.Impl {
	var out []ir.Impl
	seen := map[*ir.Module]bool{}
	for _, mod := range append([]*ir.Module{s.mod}, s.links...) {
		if mod == nil || seen[mod] {
			continue
		}
		seen[mod] = true
		out = append(out, mod.Impls()...)
	}
	return out
}

// builtTypes are the names of the types the program builds a value of, by
// the stored type of each Make's destination.
func (s *vmShaper) builtTypes() map[string]bool {
	if s.built != nil {
		return s.built
	}
	s.built = map[string]bool{}
	seen := map[*ir.Module]bool{}
	for _, mod := range append([]*ir.Module{s.mod}, s.links...) {
		if mod == nil || seen[mod] {
			continue
		}
		seen[mod] = true
		for _, f := range mod.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if mk, isMake := in.(*ir.Make); isMake {
						if ty := f.TempType(mk.Dst()); ty != nil && ty.Sym() != nil {
							s.built[ty.Sym().Name()] = true
						}
					}
				}
			}
		}
	}
	return s.built
}

// vmBindSources maps each Bind's destination to the value it binds, so a
// pattern binding's use states a demand on the projection it names.
func vmBindSources(f *ir.Func) map[ir.Temp]ir.Temp {
	out := map[ir.Temp]ir.Temp{}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if bind, isBind := in.(*ir.Bind); isBind {
				out[bind.Dst()] = bind.Src()
			}
		}
	}
	return out
}

func vmProbeCallback(arity int, result any) any {
	at := ir.At("coverage-callback.nomi", 1, 1)
	body := ir.NewFunc(at, "callback")
	for i := 0; i < arity; i++ {
		body.AddParam(ir.NewSymbol(fmt.Sprintf("arg%d", i)), ir.ValUnknown)
	}
	captured := body.AddParam(ir.NewSymbol("result"), ir.ValUnknown)
	body.NewBlock(at, "entry").SetTerm(ir.NewReturn(at, captured))
	maker := ir.NewFunc(at, "maker")
	arg := maker.AddParam(ir.NewSymbol("result"), ir.ValUnknown)
	b := maker.NewBlock(at, "entry")
	closure := ir.NewFuncValue(at, maker.NewTemp(), body, arg)
	b.Append(closure)
	b.SetTerm(ir.NewReturn(at, closure.Dst()))
	mod := ir.NewModule("coverage-callback.nomi")
	mod.AddFunc(maker)
	v, err := vmRunV(vm.New(mod, io.Discard), "maker", result)
	if err != nil {
		panic(err)
	}
	return v
}

// vmProbeEntry is one member of the retained corpus population: a retained
// function, the module that holds it, and the program it is probed in.
type vmProbeEntry struct {
	mod *ir.Module
	fn  *ir.Func
	// rel names the program, which is what a failure is reported under.
	rel string
	// decl is the corpus-relative file that declares fn, or rel when the
	// declaration lies outside the corpus. It keys the committed list, so a
	// helper module's function keeps its line when a new program that
	// imports it sorts first.
	decl     string
	links    []*ir.Module
	unlinked map[string]bool
}

// vmCorpusPopulation lowers every front-end-accepted corpus program and lists
// each retained function once.
//
// A file that is a unit of several programs is lowered once per program: an
// entry file beside its test file (message_loop/counter_app.nomi is its own
// program and a unit of message_loop_test.nomi's), or an FFI binding file
// that main.nomi and main_test.nomi both import. Each lowering mints its own
// `ir.Func`, so one declaration arrives once per program, and probing the
// copy runs the same body again. A function is therefore a member once, under
// vmProbeKey, in the first program in corpus order that retains it. A
// declaration the builder lowered to a different graph in another program
// keys differently and stays a member of its own.
//
// It also answers the number of modules, and how many retained functions were
// copies of a member already listed.
func vmCorpusPopulation(t *testing.T) (entries []vmProbeEntry, modules, copies int) {
	t.Helper()
	root, files := corpusAnalysis(t)
	seen := map[string]bool{}
	for _, f := range files {
		if f.Prog == nil {
			continue
		}
		res, _, err := GenerateIR(f.Prog)
		if err != nil {
			t.Fatalf("%s: %v", f.Rel, err)
		}
		for _, m := range res.IR {
			modules++
			for _, fn := range m.Funcs() {
				key := vmProbeKey(fn)
				if seen[key] {
					copies++
					continue
				}
				seen[key] = true
				decl := f.Rel
				if name, isStd := std.ImageName(fn.Pos().File()); isStd {
					// A std module lowered with the program
					// (programstd.go) is declared in std, not in the
					// corpus file that reached it.
					decl = name
				} else if rel, err := filepath.Rel(root, fn.Pos().File()); err == nil &&
					!strings.HasPrefix(rel, "..") && filepath.IsAbs(fn.Pos().File()) {
					decl = filepath.ToSlash(rel)
				}
				entries = append(entries, vmProbeEntry{mod: m, fn: fn, rel: f.Rel, decl: decl,
					links: res.IRModules(), unlinked: res.irHostKeys})
			}
		}
	}
	return entries, modules, copies
}

// vmProbeKey is a retained function's identity across programs: where it is
// declared and under what name, plus its graph's temporary types and
// instructions. Symbols are interned per lowering, so a `*ir.Symbol` cannot
// match two programs' copies; the declaration's position can. The graph is in
// the key because one declaration lowers to several bodies (a generic
// function's instantiations share a position and a name, as
// generic_inherent_impl's two Span.ends do), and because a program-dependent
// lowering of one declaration would be a different body that must run on its
// own.
//
// A derived body's position is synthesized: its line is allocated from a band
// per lowering (ir.AtSynthesized), so counter_app's derived Config.inspect sits
// at one line in counter_app.nomi's program and another in
// message_loop_test.nomi's. Its file, name and graph identify it.
func vmProbeKey(fn *ir.Func) string {
	var b strings.Builder
	where := fn.Pos().String()
	if fn.Pos().Synthesized() {
		where = fn.Pos().File() + " (synthesized)"
	}
	fmt.Fprintf(&b, "%s %s(", where, fn.Name())
	for _, p := range fn.Params() {
		fmt.Fprintf(&b, "%d,", p.Temp)
	}
	b.WriteString(")")
	for i := range fn.NumTemps() {
		fmt.Fprintf(&b, " %s", fn.TempType(ir.Temp(i)))
	}
	for _, blk := range fn.Blocks() {
		for _, in := range blk.Instrs() {
			fmt.Fprintf(&b, "; %s", vmShapeOf(in))
		}
		fmt.Fprintf(&b, "; %T", blk.Term())
	}
	return b.String()
}

// vmProbeResult is one member's outcome.
type vmProbeResult struct {
	ran bool
	// err is the last failure when the member did not run.
	err error
}

// vmProbeWatch is the machine a probe calls its function on, kept where a
// probe that never answers (vmInBubble) still lets the caller ask whether
// the machine entered the body. base is the function's entry count when the
// probe started calling it, so entries a boot made do not count.
type vmProbeWatch struct {
	m    atomic.Pointer[vm.Machine]
	base atomic.Uint64
}

// watch records m as the machine fn is called on from now on.
func (w *vmProbeWatch) watch(m *vm.Machine, fn *ir.Func) {
	if w == nil {
		return
	}
	w.base.Store(m.Entries(fn))
	w.m.Store(m)
}

// entered reports whether the watched machine entered fn's body since watch.
func (w *vmProbeWatch) entered(fn *ir.Func) bool {
	m := w.m.Load()
	return m != nil && m.Entries(fn) > w.base.Load()
}

// vmProbe runs one member: a boot the way a program or a test group starts
// it, on a boot frame where Supervisor.new is legal, and any other function on
// each synthesized argument vector until one runs. w sees the machine.
func vmProbe(e vmProbeEntry, w *vmProbeWatch) vmProbeResult {
	if isBoot, entered, err := vmStartBoot(e.mod, e.links, e.fn, w); isBoot {
		return vmProbeResult{ran: vmRan(entered, err), err: err}
	}
	// Made-up arguments can send a loop that the program's own callers
	// bound around forever (all-your-base's digits_of in base 1), so each
	// call has a step budget. A spinning goroutine never blocks, so the
	// bubble's virtual clock cannot end it.
	m := vmProgram(e.mod, e.links).WithLimits(vm.Limits{Steps: vmProbeSteps})
	w.watch(m, e.fn)
	var lastErr error
	for _, args := range vmArgShapes(e.mod, e.fn, e.links...) {
		entered, err := vmCall(m, e.fn, args)
		if vmRan(entered, err) {
			return vmProbeResult{ran: true}
		}
		lastErr = err
		if vmHardFailure(err) {
			break
		}
	}
	return vmProbeResult{err: lastErr}
}

// TestIRRetainedPopulationRuns runs every function the builder retains over
// the corpus on the VM and holds each outcome bucket to its pin.
func TestIRRetainedPopulationRuns(t *testing.T) {
	entries, modules, copies := vmCorpusPopulation(t)
	vmPrepareBubbles()

	// Every retained function runs on made-up arguments, and a String argument
	// is "s": a corpus body that writes a file (15-app-and-defer's
	// `write_all(path, tasks)`) writes one named "s". The calls run from a
	// temporary directory so a relative path lands there, not in this package.
	t.Chdir(t.TempDir())

	shapes := map[string]int{}
	failures := map[string][]string{}
	var lines []string
	total, ran := 0, 0
	var unimplemented []string
	for _, e := range entries {
		fn := e.fn
		total++
		for _, b := range fn.Blocks() {
			for _, in := range b.Instrs() {
				shapes[vmShapeOf(in)]++
			}
			if b.Term() != nil {
				shapes[fmt.Sprintf("%T", b.Term())]++
			}
		}

		key := e.decl + ":" + fn.Name()
		if vmNeverParam(fn) {
			failures[vmNeverParamClass] = append(failures[vmNeverParamClass], e.rel+":"+fn.Name())
			lines = append(lines, key+"\t"+vmNeverParamClass)
			continue
		}
		if missing := vmUnimplementedIfaces(e.mod, fn, e.links); len(missing) > 0 {
			unimplemented = append(unimplemented, fmt.Sprintf("%s:%s (%s)", e.rel, fn.Name(), strings.Join(missing, ", ")))
		}
		w := &vmProbeWatch{}
		res, answered, bubbleErr := vmInBubble(func() vmProbeResult { return vmProbe(e, w) })
		if !answered {
			// Waiting on a channel nothing in isolation answers is the
			// body's answer, when the machine entered it.
			res = vmProbeResult{ran: w.entered(fn), err: vmBlockedError(fn, bubbleErr)}
		} else if bubbleErr != nil {
			t.Logf("  %s:%s answered, and then its bubble reported: %v", e.rel, fn.Name(), bubbleErr)
		}
		if res.ran {
			ran++
			lines = append(lines, key+"\tran")
			continue
		}
		class := vmFailureClass(res.err, e.unlinked)
		t.Logf("  %s:%s did not run: %v%s", e.rel, fn.Name(), res.err, vmDispatchNote(res.err))
		failures[class] = append(failures[class], e.rel+":"+fn.Name())
		lines = append(lines, key+"\t"+class)
	}

	if total == 0 {
		t.Fatal("no function was retained over the corpus, so every count below is vacuous")
	}

	keys := make([]string, 0, len(shapes))
	for k := range shapes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  built %-46s %d", k, shapes[k])
	}
	fkeys := make([]string, 0, len(failures))
	for k := range failures {
		fkeys = append(fkeys, k)
	}
	sort.Strings(fkeys)
	for _, k := range fkeys {
		sort.Strings(failures[k])
		t.Logf("  FAILS %-46s %d  %s", k, len(failures[k]), strings.Join(failures[k], ", "))
	}
	t.Logf("%d modules, %d retained functions, %d executed by the VM; %d copies of a "+
		"function another program already lowered were not probed again", modules, total, ran, copies)
	t.Logf("%d function(s) take an interface value the program retains no impl of, and get "+
		"the trial leaf for it: %s", len(unimplemented), strings.Join(unimplemented, "; "))

	vmRetainedCorpus.check(t, lines)

	vmCheckBuckets(t, failures)
}

// vmFailureClass is the bucket of a member that did not run, from the last
// error its probe saw: the refusal's class, UNLINKED for a refused crossing
// into one of the program's own Go bindings (unlinked), and otherwise PROBE,
// since a member the machine entered without a refusal ran.
func vmFailureClass(err error, unlinked map[string]bool) string {
	if !vmRefused(err) {
		return vmNotEnteredClass
	}
	if vmUnlinkedBinding(err, unlinked) {
		return vmUnlinkedClass
	}
	return vmClassify(err)
}

// vmCheckBuckets holds a population's failure buckets to what no list can
// excuse: the two that must stay empty, and any bucket outside the classified
// ones.
func vmCheckBuckets(t *testing.T, failures map[string][]string) {
	t.Helper()
	// THE TWO BUCKETS THAT MUST STAY EMPTY, whatever the list holds.
	for class, members := range failures {
		if strings.HasPrefix(class, "VOCABULARY") || strings.HasPrefix(class, "IDENTITY") {
			t.Errorf("%d retained function(s) in %q, which must stay empty: the builder "+
				"retained a body the VM cannot execute: %s", len(members), class,
				strings.Join(members, ", "))
		}
	}
	// A failure outside the classified buckets (a compiler panic) is a
	// misclassification the list would record as an opaque reason; name it
	// instead.
	known := map[string]bool{
		"LINKING: a declaration this module did not retain": true,
		"HOST: crosses into Go with no binding":             true,
		vmUnlinkedClass:                                     true,
		vmNotEnteredClass:                                   true,
		vmNeverParamClass:                                   true,
	}
	for class, members := range failures {
		if !known[class] && !strings.HasPrefix(class, "VOCABULARY") && !strings.HasPrefix(class, "IDENTITY") {
			t.Errorf("%d retained function(s) failed in the UNCLASSIFIED bucket %q: %s",
				len(members), class, strings.Join(members, ", "))
		}
	}
}

// vmShapeOf names the instruction and its discriminating kind, for the log of
// what the retained population is built from.
func vmShapeOf(in ir.Instr) string {
	key := fmt.Sprintf("%T", in)
	switch n := in.(type) {
	case *ir.Const:
		return key + " " + n.Kind().String()
	case *ir.Ref:
		return key + " " + n.Kind().String()
	case *ir.Match:
		return fmt.Sprintf("%s %s answers=%v", key, n.Kind(), n.Answers())
	case *ir.Render:
		return key + " " + n.Kind().String()
	case *ir.Call:
		return fmt.Sprintf("%s %s crosses=%v", key, n.Form(), n.Crosses())
	case *ir.Arith:
		return key + " " + n.Op().String()
	case *ir.Iter:
		return key + " " + n.Op().String()
	}
	return key
}

// vmProgram opens a counting machine over mod's program and boots it when the
// program's boot is retained, so a body reading an app field finds the app
// published. A test file with no program boot boots its first `group` boot
// instead, which is the app that group's cases call the file's functions in.
// An unretained boot, or one that answers `Err`, leaves the unbooted machine.
func vmProgram(mod *ir.Module, links []*ir.Module) *vm.Machine {
	m := vm.NewProgram(mod, links, io.Discard).WithHosts(testHosts).WithEntryCounts()
	if mod.Boot() == nil {
		// A test file's module, or an entry file a test file imports (whose
		// boot the test file's group `boot` line calls): boot the first
		// group boot the program records.
		for _, cand := range append([]*ir.Module{mod}, links...) {
			if cand == nil || len(cand.TestBoots()) == 0 {
				continue
			}
			if group, ok := vmFirstGroupBoot(cand); ok {
				if booted, err := m.BootTest(group); err == nil {
					return booted
				}
			}
			return m
		}
	}
	if booted, err := m.Boot(); err == nil {
		return booted
	}
	return m
}

// vmStartBoot starts fn when it is a boot a program or a retained test case
// starts, on a counting machine w watches, and reports whether it was one,
// whether the machine entered fn's body and how the start ended. A boot no
// retained case starts has no Startup to call it with, and is probed as any
// other function.
func vmStartBoot(mod *ir.Module, links []*ir.Module, fn *ir.Func, w *vmProbeWatch) (isBoot, entered bool, err error) {
	m := vm.NewProgram(mod, links, io.Discard).WithHosts(testHosts).WithEntryCounts()
	if fn.Sym() == mod.Boot() {
		w.watch(m, fn)
		_, err := m.Boot()
		return true, m.Entries(fn) > 0, err
	}
	// An entry boot a test file's group `boot` line calls lives in the entry
	// file's module, and the test file's module records it.
	for _, cand := range append([]*ir.Module{mod}, links...) {
		if cand == nil {
			continue
		}
		for _, c := range cand.Tests() {
			if group := c.Group(); group.Boot != nil && fn.Sym() == group.Boot {
				w.watch(m, fn)
				_, err := m.BootTest(group)
				return true, m.Entries(fn) > 0, err
			}
		}
	}
	return false, false, nil
}

// vmFirstGroupBoot is the group of mod's first retained case that boots.
func vmFirstGroupBoot(mod *ir.Module) (ir.TestGroup, bool) {
	for _, c := range mod.Tests() {
		if group := c.Group(); group.Boot != nil {
			return group, true
		}
	}
	return ir.TestGroup{}, false
}

// vmProbeSteps is a probe call's step budget: far more than any corpus body
// takes on its made-up arguments, and a moment of CPU.
const vmProbeSteps = 20_000_000

// vmCall calls fn on m with args and reports whether the machine entered
// fn's body during the call, by its entry count. m counts entries
// (vm.Machine.WithEntryCounts).
func vmCall(m *vm.Machine, fn *ir.Func, args []any) (entered bool, err error) {
	before := m.Entries(fn)
	_, err = vmRunSymV(m, fn.Sym(), args...)
	return m.Entries(fn) > before, err
}

// vmRan is the one rule for a probe's outcome: the machine entered the body
// and the run did not end in a refusal. Every other ending is the body's
// answer on the probe's arguments.
func vmRan(entered bool, err error) bool {
	return entered && !vmRefused(err)
}

// vmRefused reports whether a run ended in a machine refusal: a construct
// this machine runs no code for (VOCABULARY), a local the frame does not
// declare (IDENTITY), a callee no module retained (LINKING) or a crossing
// with no binding (HOST). A compiler panic counts too, so it never counts as
// a run; vmClassify leaves it unclassified and the test fails on it.
func vmRefused(err error) bool {
	if err == nil {
		return false
	}
	if _, isPanic := vm.AsCompilePanic(err); isPanic {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "this machine runs no") ||
		strings.Contains(s, "does not declare") ||
		vmMissingCallee(err) ||
		strings.Contains(s, "binds no implementation")
}

// vmMissingCallee reports a callee the machine finds in no retained module:
// resolveFunc's refusal of a symbol, in either spelling, or a dispatched
// call whose receiver's run-time type has no retained impl
// (vm.DispatchMiss) unless the receiver is not a value of the type the call
// declares for that operand (vmProbeReceiver).
func vmMissingCallee(err error) bool {
	s := err.Error()
	if strings.Contains(s, "names a declaration this module did not retain") ||
		strings.Contains(s, "names a declaration this program's modules did not retain") {
		return true
	}
	if !strings.Contains(s, "did not retain an implementation") {
		return false
	}
	return !vmProbeReceiver(err)
}

// vmDispatchNote says, for a dispatch miss, the operand's declared type and
// whether the receiver was a value of it, so the log shows why the miss
// counts as LINKING.
func vmDispatchNote(err error) string {
	var miss *vm.DispatchMiss
	if err == nil || !errors.As(err, &miss) {
		return ""
	}
	of, decided := miss.ReceiverOfDeclaredType()
	switch {
	case !decided:
		return fmt.Sprintf(" (dispatch miss; declared %v, undecidable)", miss.Declared)
	case of:
		return fmt.Sprintf(" (dispatch miss; receiver is its declared %v)", miss.Declared)
	}
	return fmt.Sprintf(" (dispatch miss; receiver is not its declared %v)", miss.Declared)
}

// vmProbeReceiver reports a dispatch miss on a receiver that is not a value
// of the operand's declared type: the probe synthesized it, so the miss is
// a fault on a made-up argument and the body's answer. A miss on a receiver
// of its declared type is a builder gap, the impl not retained, and so is
// one the machine cannot decide (an interface or untyped operand, or a
// miss whose typed error a crossing turned into text).
func vmProbeReceiver(err error) bool {
	var miss *vm.DispatchMiss
	if !errors.As(err, &miss) {
		return false
	}
	of, decided := miss.ReceiverOfDeclaredType()
	return decided && !of
}

// vmHardFailure is a failure no other argument vector changes: a refusal,
// or a wait on a channel nothing in isolation answers.
func vmHardFailure(err error) bool {
	return vmRefused(err) || (err != nil && strings.Contains(err.Error(), vmBlockedText))
}

// vmBlockedText marks a probe vmInBubble gave up on.
const vmBlockedText = "gave no answer within the probe's virtual-time bound"

// vmProbeBound is how much virtual time a probe may take. Far longer than any
// sleep in the corpus (supervisors' slow_work sleeps thirty seconds), and
// free, since virtual time passes only while every goroutine in the bubble is
// blocked.
const vmProbeBound = 24 * time.Hour

// vmBlockedError is the failure of a probe of fn that vmInBubble reports
// unanswered.
func vmBlockedError(fn *ir.Func, bubbleErr error) error {
	return fmt.Errorf("vm: %s %s: %v", fn.Name(), vmBlockedText, bubbleErr)
}

// vmInBubble runs probe inside a virtual-time bubble (`rt/vclock.RunCase`, the
// bubble a `clock Clock.Virtual` test group runs in) and answers its value, or
// answered=false when the bubble ended first. bubbleErr is what the bubble
// reported, if anything.
//
// The machine, its boot and the calls are all made inside the bubble, so every
// goroutine and timer they start is the bubble's. That buys two things.
//
// A sleep or timeout spends virtual time, so supervisors' `slow_work` (thirty
// seconds) and concurrent_runtime_test's `long_sleep` (five) cost no real
// time.
//
// A run that waits on a channel nothing will ever answer costs none either:
// message_loop's `get` sends its request to the closed inbox the probe
// supplies and then waits for a reply. The probe runs on its own goroutine and
// the bubble's root waits for it for vmProbeBound of virtual time. Once every
// goroutine in the bubble is durably blocked, the clock jumps to the bound and
// the root gives up at once. The blocked goroutine stays blocked, and synctest
// reports it when the bubble closes. A blocked run is a hard failure, since no
// other argument vector changes what the body waits for.
//
// The root rather than synctest's own deadlock report ends a blocked probe
// because the bubble's cleanup must still run: rt.ResetSupervisors, as for a
// test case, drains the supervisors a boot started and empties rt's
// supervisor registry. A registry left holding one bubble's supervisors is
// unusable from the next bubble.
func vmInBubble[T any](probe func() T) (v T, answered bool, bubbleErr error) {
	got := make(chan T, 1)
	bubbleErr = vclock.RunCase(func() error {
		done := make(chan T, 1)
		go func() { done <- probe() }()
		select {
		case v := <-done:
			got <- v
		case <-time.After(vmProbeBound):
		}
		return nil
	}, rt.ResetSupervisors)
	select {
	case v = <-got:
		return v, true, bubbleErr
	default:
		if bubbleErr == nil {
			bubbleErr = errors.New("the bubble ended without an error and without an answer")
		}
		return v, false, bubbleErr
	}
}

// vmPrepareBubbles readies the process for a sweep of vmInBubble probes.
//
// It builds the probe's once-per-process leaves outside any bubble. Built
// inside the first probe's bubble, a later probe would use them from another
// bubble, which synctest refuses.
//
// It drains and empties rt's supervisor registry on the real clock. Other
// tests in this package boot programs and leave their supervisors there, and
// the first bubble's cleanup would otherwise drain them from inside the
// bubble, where a wait on a channel the bubble did not create never lets the
// virtual clock advance.
func vmPrepareBubbles() {
	vmRegexHandle()
	vmCalendarValues()
	rt.ResetSupervisors()
}

func vmClassify(err error) string {
	if err == nil {
		return "<nil>"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "this machine runs no"):
		i := strings.Index(s, "this machine runs no")
		return "VOCABULARY: " + vmFirstWords(s[i:], 6)
	case strings.Contains(s, "does not declare"):
		return "IDENTITY: a local the frame does not declare"
	case vmMissingCallee(err):
		return "LINKING: a declaration this module did not retain"
	case strings.Contains(s, "binds no implementation"):
		return "HOST: crosses into Go with no binding"
	}
	return "OTHER: " + vmFirstWords(s, 10)
}

// vmUnlinkedClass is a crossing into a project's own Go-bound `host fn`. That
// binding is reachable only inside ffirun's wrapper binary, which links the
// project's Go package; this test binary is in another Go module
// and cannot. It is neither a VM gap nor a missing std binding.
const vmUnlinkedClass = "UNLINKED: a project Go binding this process does not link"

// vmUnlinkedBinding reports whether err is a crossing into one of keys with no
// binding.
func vmUnlinkedBinding(err error, keys map[string]bool) bool {
	if err == nil || !strings.Contains(err.Error(), "binds no implementation") {
		return false
	}
	for key := range keys {
		if strings.Contains(err.Error(), " "+key+" crosses into Go") {
			return true
		}
	}
	return false
}

func vmFirstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// vmHandleLeaves are values no literal in a retained body's parameters
// builds: an idle Supervisor, which std's flush bodies receive, an empty
// Map, which a generic body handing its parameters to Map.get receives as
// both the map and the key, and a root Context, which std's deadline bodies
// hand to Context.deadline, an empty Reader and standard output, which
// std/io's stream bodies hand to their host calls, and a File that is not
// open, which File's bodies hand to theirs (each answers Closed). A `Type<T>` witness is
// typed per parameter (vmLeafForType), because it shares signatures with a
// Context.
//
// Built per call rather than once: the supervisor joins rt's supervisor
// registry, and a probe in a virtual-time bubble (vmInBubble) must hold one
// its own bubble created and drains.
func vmHandleLeaves() []any {
	return []any{vm.IdleSupervisor(), vmEmptyMap(), rt.ReaderFromText(""), rt.IOStdout(), rt.File{}, rt.ContextRoot()}
}

// vmChannelHalves are a closed channel's Sender and Receiver, handles no
// literal builds: a send answers Err and a receive None without blocking.
func vmChannelHalves() []any {
	ch, ok := vm.ClosedChannel().(*rt.Record)
	if !ok {
		return nil
	}
	sender, _ := ch.FieldNamed("sender")
	receiver, _ := ch.FieldNamed("receiver")
	return []any{sender, receiver}
}

var vmRegexHandle = sync.OnceValue(func() any {
	hosts, err := stdlibadapters.Bind(&hostadapt.Env{})
	if err != nil {
		return nil
	}
	compiled, err := hosts["Regex.compile"](nil, []rt.Value{"a"})
	r, isRecord := compiled.(*rt.Record)
	if err != nil || !isRecord || r.Variant().Name != "Ok" {
		return nil
	}
	return r.Field(0)
})

// vmCalendarValues are one value of each calendar type, parsed by calendar's
// own hosts, for the bodies that hand a parameter straight to a host and so
// read no field the shaper could follow.
var vmCalendarValues = sync.OnceValue(func() []any {
	var out []any
	hosts, err := stdlibadapters.Bind(&hostadapt.Env{})
	if err != nil {
		return nil
	}
	for host, text := range map[string]string{
		"calendar.date_parse_raw":   "2026-06-15",
		"calendar.time_parse_raw":   "12:30:00",
		"calendar.naive_parse_raw":  "2026-06-15T12:30:00",
		"calendar.offset_parse_raw": "2026-06-15T12:30:00-04:00",
		"calendar.zoned_parse_raw":  "2026-06-15T12:30:00-04:00[America/New_York]",
	} {
		attempt, err := hosts[host](nil, []rt.Value{text})
		if r, ok := attempt.(*rt.Record); err == nil && ok {
			if v, has := r.FieldNamed("value"); has && v != nil {
				out = append(out, v)
			}
		}
	}
	return out
})

// vmLeafForType is the trial leaf of a scalar stored type, or false for a
// type the leaves do not answer by kind.
func vmLeafForType(ty *ir.ValType) (any, bool) {
	if ty == nil {
		return nil, false
	}
	switch ty.Kind() {
	case ir.KindInt:
		return int64(1), true
	case ir.KindFloat:
		return 1.5, true
	case ir.KindBool:
		return true, true
	case ir.KindByte:
		return rt.Byte(65), true
	case ir.KindString:
		return "s", true
	case ir.KindBytes:
		return rt.Bytes("A"), true
	case ir.KindHandle:
		// A `Type<T>` witness beside a Context in one signature
		// (`Context.value(c, witness)`) cannot share the Context's trial
		// leaf. Any witness runs such a body.
		if ty.Sym().Name() == "type.Type" {
			return rt.Type[any]{TID: &rt.TIDInt}, true
		}
	}
	return nil, false
}

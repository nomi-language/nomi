package irbuild

// The VM against the builder's whole retained corpus population.
//
// Every function the builder retains over the front-end-accepted corpus runs
// on the VM with arguments synthesized from its graph. A function either runs
// or fails in exactly one classified bucket, and each bucket is pinned. The
// retention count alone cannot see a body the machine cannot execute, because
// the builder retains the same graph either way; this test is where that shows.

import (
	"errors"
	"fmt"
	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"

	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/rt/vclock"
)

// The pins partition the retained corpus population. Every retained function
// either runs or lands in exactly one failure bucket, and the test checks that
// the buckets exhaust the population, so a failure in an unclassified bucket
// cannot hide behind unchanged totals.
//
// A fall in vmWantRunnable is a VM regression. VOCABULARY must stay zero: a
// rise means the builder put an instruction into a retained body that the
// machine cannot run. IDENTITY must stay zero: a rise means a retained graph
// reads a local its own parameter list does not declare, which `ir.Lint`'s
// RuleLocalDeclared also reports (internal/ir's
// TestLintLocalDeclared_CatchesItsPlant builds that graph, and
// TestLintLocalDeclared_TheLocalControlIsClean its clean control). LINKING
// and HOST count bodies that call out of their module to a callee with no VM
// body or binding; they move when the builder admits a caller before its
// callee, which is not a machine gap. Their zeros are validated by planted
// positives: TestVMCoverage_TheLinkingClassifierCatchesItsPlant and
// TestVMCoverage_TheHostClassifierCatchesItsPlant.
//
// Re-derive a pin from the run rather than bumping it, and name the bodies
// that moved and the cause in the commit message.
const (
	// vmWantRetained counts the retained module functions and the declared or
	// inherited impl bodies over every front-end-accepted corpus file, each
	// once: a file that is a unit of several programs (an entry file beside
	// its test file, a helper module its importers share) is lowered once per
	// program, and vmCorpusPopulation keeps one copy of each body. It includes
	// the field-default accessors a declaring file builds for another file's
	// literals (foreignfielddefault.go): 7 of them, 4 in
	// 15-app-and-defer/effects/app.nomi and 3 in variant_field_defaults. A
	// group whose boot takes no parameter has no startup function, so the
	// corpus's five `boot x.boot()` lines add none.
	vmWantRetained = 947
	// vmWantRunnable counts the retained functions the VM executes to a
	// Return, or to a Nomi fault, on some synthesized argument vector.
	vmWantRunnable           = 908
	vmWantIdentityFailures   = 0
	vmWantVocabularyFailures = 0
	// message_loop's synthesized Config.inspect calls Counter.inspect, which
	// is not retained (Debug over a Channel field). The corpus never reaches
	// Config's Debug; a program that did would be BLOCKED by Unretained.
	vmWantLinkingFailures = 1
	vmWantHostFailures    = 0
	// The host fn bodies that cross into a project's Go binding, and the FFI
	// `main`s that call them: each crossing names its binding's extern key and
	// this test binary does not link the project's Go package. See
	// vmUnlinkedClass.
	vmWantUnlinkedBindingFailures = 20
	// message_loop's get, which sends its request to the closed inbox the
	// probe supplies and then waits for a reply nothing sends. See
	// vmInBubble.
	vmWantBlockedFailures = 1
	// message_loop's start, whose Supervisor.new is legal only while boot
	// runs; the entry boot that calls it runs.
	vmWantBootFailures = 1
	// todo_test's `unfinished`, whose body is a `todo` on the path the
	// probe's argument takes; the corpus test calls it only to show that a
	// `todo` on an untaken path does not trap.
	vmWantTodoFailures = 1
	// file_store_pattern's FileStore.save puts the probe's TodoTask, which
	// carries only the `id` save reads, into a map that `serialize` reads
	// every field of through Map.values. The probe cannot see a field demand
	// across a container; the corpus runs the body with a real TodoTask.
	// generic_inherent_impl's Span.ends, at Span<Int> and Span<String>, reads
	// `stop` itself and hands the value to Span.first, which reads `start`;
	// the probe builds the argument from the body's own read alone. The
	// corpus runs both with real Spans.
	vmWantProbeShapeFailures = 3
	// Bodies whose synthesized argument is not a value of the declared type:
	// interface_dispatch's `announce(s: Speech)`, whose existential parameter
	// holds a value of no implementing type; type_argument_inference's
	// Chooser.top over Pair<Priority>, whose callee reads `rank` off a leaf;
	// iter_test's Tree.each_while and Tree.inspect, whose variant test reads
	// a leaf where a Tree belongs; and iter_values_test's total and first_of,
	// which iterate a leaf where an Iter<T> belongs, as vectors_test's collect
	// (at Int and at String) and strings_test's slash_joined do; recursive_structs'
	// length, which reads `next` off the leaf the probe supplies for a Link,
	// a struct that holds itself; and generic_debug's Tree<Int>.inspect and
	// Tree<String>.inspect, whose variant test reads a leaf where a Tree
	// belongs, as iter_test's Tree.inspect does. The corpus runs each with
	// real values.
	vmWantProbeArgFailures = 12
)

// vmProbeArgClass is a function the probe's synthesized arguments cannot
// satisfy: an existential parameter with no implementing value, or a field
// read in a callee off a leaf the probe supplied. An artifact of argument
// synthesis, not of the machine.
const vmProbeArgClass = "PROBE: a synthesized argument is not a value of the declared type"

// vmProbeShapeClass is a probe argument that lacks a field a callee reads
// after the value crossed a container: an artifact of argument synthesis,
// not of the machine.
const vmProbeShapeClass = "PROBE: a synthesized struct lacks a field read through a container"

// vmArgShapes is one argument vector per scalar shape the retained grammar
// admits, with a composite built wherever the graph projects out of a
// parameter.
//
// Every scalar shape is tried and a function counts as runnable if any
// completes. That cannot manufacture a pass for the VOCABULARY pin: a "this
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
		// A Maybe a host call consumes, such as Result.from_maybe's receiver,
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
// directly, but a distinct's unwrap written in the body (`String(e)`) reads
// `e` through an `ir.Ref` first, so the projection's subject is the Ref's
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
				// projects out of it; `Struct.update(app, {...})` states no
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
		// EXCEPT FIELD DEMANDS, which compose: `NaiveDateTime.at` forwards
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
	case ir.ProjField, ir.ProjRecordField, ir.ProjIfaceField:
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
	rel      string
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
	_, files := corpusAnalysis(t)
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
				entries = append(entries, vmProbeEntry{mod: m, fn: fn, rel: f.Rel,
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
	// boot reports a boot, which is started rather than called.
	boot bool
}

// vmProbe runs one member: a boot the way a program or a test group starts
// it, on a boot frame where Supervisor.new is legal, and any other function on
// each synthesized argument vector until one runs.
func vmProbe(e vmProbeEntry) vmProbeResult {
	if isBoot, err := vmStartBoot(e.mod, e.links, e.fn); isBoot {
		return vmProbeResult{ran: err == nil, err: err, boot: true}
	}
	m := vmProgram(e.mod, e.links)
	var lastErr error
	for _, args := range vmArgShapes(e.mod, e.fn, e.links...) {
		_, err := vmRunSymV(m, e.fn.Sym(), args...)
		if vmRan(err) {
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
	total, ran := 0, 0
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

		res, answered, bubbleErr := vmInBubble(func() vmProbeResult { return vmProbe(e) })
		if !answered {
			res = vmProbeResult{err: vmBlockedError(fn, bubbleErr)}
		} else if bubbleErr != nil {
			t.Logf("  %s:%s answered, and then its bubble reported: %v", e.rel, fn.Name(), bubbleErr)
		}
		if res.ran {
			ran++
			continue
		}
		class := vmClassify(res.err)
		if res.boot {
			failures[class] = append(failures[class], e.rel+":"+fn.Name())
			t.Logf("  %s:%s did not start: %v", e.rel, fn.Name(), res.err)
			continue
		}
		if vmUnlinkedBinding(res.err, e.unlinked) {
			class = vmUnlinkedClass
		}
		if vmProbeArgFailure(fn, res.err) {
			class = vmProbeArgClass
		}
		failures[class] = append(failures[class], e.rel+":"+fn.Name())
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

	if total != vmWantRetained {
		t.Errorf("the corpus retained %d functions, pinned at %d; re-derive rather than "+
			"bumping; this population includes module functions and declared or inherited impl bodies", total, vmWantRetained)
	}
	if ran != vmWantRunnable {
		t.Errorf("the VM executed %d retained functions, pinned at %d. A fall is a VM "+
			"regression the retention count cannot see", ran, vmWantRunnable)
	}
	if got := len(failures["IDENTITY: a local the frame does not declare"]); got != vmWantIdentityFailures {
		t.Errorf("%d retained functions read a local their own parameter list does not "+
			"declare, pinned at %d. A rise means the builder retained incomplete graphs",
			got, vmWantIdentityFailures)
	}
	vocab := 0
	for k, v := range failures {
		if strings.HasPrefix(k, "VOCABULARY") {
			vocab += len(v)
		}
	}
	if vocab != vmWantVocabularyFailures {
		t.Errorf("%d retained functions need an instruction this machine does not run, "+
			"pinned at %d: the builder put an instruction into a retained body that the "+
			"VM cannot execute",
			vocab, vmWantVocabularyFailures)
	}
	link := len(failures["LINKING: a declaration this module did not retain"])
	if link != vmWantLinkingFailures {
		t.Errorf("%d retained functions call a declaration this module does not hold, "+
			"pinned at %d. This is module closure rather than a machine gap: the "+
			"caller was retained and its callee was not", link, vmWantLinkingFailures)
	}
	host := len(failures["HOST: crosses into Go with no binding"])
	if host != vmWantHostFailures {
		t.Errorf("%d retained functions call into Go with no VM binding, pinned at %d. "+
			"The call instruction runs; the crossing has no host binding",
			host, vmWantHostFailures)
	}
	unbound := len(failures[vmUnlinkedClass])
	if unbound != vmWantUnlinkedBindingFailures {
		t.Errorf("%d retained functions reach a project's Go-bound `host fn`, whose Go "+
			"package this test binary does not link, pinned at %d. The crossing and its "+
			"key are right: TestIRHostFn_GoBoundProgramsRunOnTheVM runs the same "+
			"programs with the bindings registered", unbound, vmWantUnlinkedBindingFailures)
	}
	// A count cannot see a misclassification that leaves the total unchanged,
	// and a partition can. Every retained function either ran or failed in
	// exactly one classified bucket, so the pins must exhaust the population.
	blocked := len(failures["BLOCKED: waits on a channel nothing in isolation answers"])
	if blocked != vmWantBlockedFailures {
		t.Errorf("%d retained functions gave no answer within the probe's virtual-time bound, pinned at %d",
			blocked, vmWantBlockedFailures)
	}
	boot := len(failures["BOOT: creatable only while boot runs"])
	if boot != vmWantBootFailures {
		t.Errorf("%d retained functions create a supervisor outside boot, pinned at %d",
			boot, vmWantBootFailures)
	}
	probe := len(failures[vmProbeShapeClass])
	if probe != vmWantProbeShapeFailures {
		t.Errorf("%d retained functions read a field the probe's synthesized struct lacks, pinned at %d",
			probe, vmWantProbeShapeFailures)
	}
	todo := len(failures["TODO: the body is not written yet"])
	if todo != vmWantTodoFailures {
		t.Errorf("%d retained functions reach a `todo`, pinned at %d", todo, vmWantTodoFailures)
	}
	probeArg := len(failures[vmProbeArgClass])
	if probeArg != vmWantProbeArgFailures {
		t.Errorf("%d retained functions take a synthesized argument that is not a value of "+
			"the declared type, pinned at %d", probeArg, vmWantProbeArgFailures)
	}
	if ran+link+host+unbound+vocab+blocked+boot+todo+probe+probeArg+len(failures["IDENTITY: a local the frame does not declare"]) != total {
		t.Errorf("%d ran + %d LINKING + %d HOST + %d UNLINKED + %d VOCABULARY + %d BLOCKED + %d BOOT + %d TODO + %d PROBE + %d PROBE-ARG + %d IDENTITY does not "+
			"exhaust the %d retained functions, so some failure is in an UNCLASSIFIED "+
			"bucket that no pin above can see", ran, link, host, unbound, vocab, blocked, boot, todo, probe, probeArg,
			len(failures["IDENTITY: a local the frame does not declare"]), total)
	}
}

// vmProbeArgFailure reports a failure the probe's own arguments explain: a
// function with an existential parameter the probe filled with a value of no
// implementing type, or a field read off a leaf the probe supplied where the
// declared type is a struct.
func vmProbeArgFailure(fn *ir.Func, err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "reads a field off") || strings.Contains(err.Error(), "variant test reads rt.Context") ||
		strings.Contains(err.Error(), "iteration reads rt.Context") {
		// A field read, variant test or iteration on the leaf the probe
		// supplied for a parameter it cannot build (a nominal type, or an
		// `Iter<T>`).
		return true
	}
	for _, p := range fn.Params() {
		if t := fn.TempType(p.Temp); t != nil && t.Kind() == ir.KindIface {
			return true
		}
	}
	return false
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

// vmProgram opens a machine over mod's program and boots it when the program's
// boot is retained, so a body reading an app field finds the app published.
// A test file with no program boot boots its first `tests` group boot instead,
// which is the app that group's cases call the file's functions in. An
// unretained boot leaves the unbooted machine.
func vmProgram(mod *ir.Module, links []*ir.Module) *vm.Machine {
	m := vm.NewProgram(mod, links, io.Discard).WithHosts(testHosts)
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

// vmStartBoot starts fn when it is a boot mod records, program or test
// group, and reports whether it was one.
func vmStartBoot(mod *ir.Module, links []*ir.Module, fn *ir.Func) (bool, error) {
	m := vm.NewProgram(mod, links, io.Discard).WithHosts(testHosts)
	if fn.Sym() == mod.Boot() {
		_, err := m.Boot()
		return true, err
	}
	// An entry boot a test file's group `boot` line calls lives in the entry
	// file's module, and the test file's module records it.
	for _, cand := range append([]*ir.Module{mod}, links...) {
		if cand == nil {
			continue
		}
		for _, c := range cand.Tests() {
			if group := c.Group(); group.Boot != nil && fn.Sym() == group.Boot {
				_, err := m.BootTest(group)
				return true, err
			}
		}
		for _, sym := range cand.TestBoots() {
			if fn.Sym() == sym {
				// A recorded boot no retained case starts: there is no
				// Startup to call it with.
				return true, nil
			}
		}
	}
	return false, nil
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

// vmRan reports whether a probe's run executed the body, and it counts the
// call-depth fault as a run. A probe calls a recursive body on arguments it
// made up, and one that recurses past rt.MaxCallDepth on them was executed by
// the machine: the fault is that body's answer on those arguments, as a
// division by zero would be, and not a construct this machine cannot run.
func vmRan(err error) bool {
	if err == nil {
		return true
	}
	var fault *vm.Fault
	depthText := strings.TrimPrefix(rt.CallDepthError(0).Error(), "line 0")
	return errors.As(err, &fault) && strings.HasSuffix(err.Error(), depthText)
}

func vmHardFailure(err error) bool {
	s := err.Error()
	return strings.Contains(s, vmBlockedText) ||
		strings.Contains(s, "this machine runs no") ||
		strings.Contains(s, "does not declare") ||
		strings.Contains(s, "did not retain") ||
		strings.Contains(s, "binds no implementation") ||
		strings.Contains(s, "Create it while `boot` builds the app value")
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
	case strings.Contains(s, vmBlockedText):
		return "BLOCKED: waits on a channel nothing in isolation answers"
	case strings.Contains(s, "this machine runs no"):
		i := strings.Index(s, "this machine runs no")
		return "VOCABULARY: " + vmFirstWords(s[i:], 6)
	case strings.Contains(s, "does not declare"):
		return "IDENTITY: a local the frame does not declare"
	case strings.Contains(s, "did not retain"):
		return "LINKING: a declaration this module did not retain"
	case strings.Contains(s, "binds no implementation"):
		return "HOST: crosses into Go with no binding"
	case strings.Contains(s, "Create it while `boot` builds the app value"):
		return "BOOT: creatable only while boot runs"
	case strings.Contains(s, "todo reached at"):
		return "TODO: the body is not written yet"
	case strings.Contains(s, "names a field Probe"):
		return vmProbeShapeClass
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
// hand to Context.deadline. A `Type<T>` witness is typed per parameter
// (vmLeafForType), because it shares signatures with a Context.
//
// Built per call rather than once: the supervisor joins rt's supervisor
// registry, and a probe in a virtual-time bubble (vmInBubble) must hold one
// its own bubble created and drains.
func vmHandleLeaves() []any {
	return []any{vm.IdleSupervisor(), vmEmptyMap(), rt.ContextRoot()}
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

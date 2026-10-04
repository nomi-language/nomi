package irbuild

// The VM against std's retained population.
//
// Every std function the builder retains in the cached stdlib modules runs on
// the VM, with arguments from its declared parameter kinds where the index
// holds them and otherwise from the graph (vmArgShapes). Each body runs in a
// machine over its own module, linked against every other cached module.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// The std pins. vmStdWantRetained is irStdWantRetained (irstdbody_test.go)
// plus the arity-wrapper and slot-accessor bodies the cached modules also
// hold, which are declarations of their own. A fall in vmStdWantRunnable is a
// VM regression the retention count cannot see. Re-derive a pin from the run
// rather than bumping it, and name the bodies that moved in the commit message.
const (
	vmStdWantRetained = irStdWantRetained + vmStdWantArityWrappers + vmStdWantSlotAccessors
	vmStdWantRunnable = 389
	// The arity wrappers whose VM bodies the std modules retain.
	vmStdWantArityWrappers = 10
	// The `_DEFAULT<slot>` accessors whose VM bodies the std modules retain,
	// which a call whose named arguments skip a defaulted parameter reaches:
	// Supervisor.new's four, Supervisor.flush's one, Time.new's two,
	// NaiveDateTime.new's two and DateTime.in_zone's one.
	vmStdWantSlotAccessors = 10
	// BOOT is a body that makes a supervisor, which rt refuses outside the
	// boot phase by the language's rule, and this test runs no boot:
	// Supervisor.new and its four arity wrappers.
	vmStdWantBootFailures = 5
	// HOST is a call into Go the machine binds no implementation for; LINKING
	// is a call to a Nomi body no linked module holds. Their zeros are
	// validated by TestVMCoverage_TheHostClassifierCatchesItsPlant and
	// TestVMCoverage_TheLinkingClassifierCatchesItsPlant.
	vmStdWantHostFailures    = 0
	vmStdWantLinkingFailures = 0
)

// TestIRRetainedStdPopulationRuns runs every std function the builder
// retains, and classifies each failure as TestIRRetainedPopulationRuns does.
func TestIRRetainedStdPopulationRuns(t *testing.T) {
	// Warmed first: `buildStdlibIndex` is not idempotent from a fresh
	// process, and only the warm reading is reproducible from inside a shared
	// test binary.
	buildStdlibIndex()

	idx := buildStdlibIndex()
	var fns []*ir.Func
	var modules []*ir.Module
	names := map[*ir.Func]string{}
	owners := map[*ir.Func]*ir.Module{}
	pkgs := make([]string, 0, len(idx.irModules))
	for pkg := range idx.irModules {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	for _, pkg := range pkgs {
		mod := idx.irModules[pkg]
		modules = append(modules, mod)
		for _, fn := range mod.Funcs() {
			fns = append(fns, fn)
			names[fn], owners[fn] = fn.Name(), mod
		}
	}

	// A body's declared parameter kinds, when the index holds them, give one
	// argument per parameter: what a body handing its parameters straight to
	// a host needs, since it reads no field the shaper could follow.
	byBody := map[*ir.Func]*stdFunc{}
	for _, f := range idx.byKey {
		if f.irBody != nil {
			byBody[f.irBody] = f
		}
		// An arity wrapper takes the declaration's leading parameters.
		for arity, body := range f.irArity {
			view := *f
			view.params = f.params[:arity]
			byBody[body] = &view
		}
		// A `_DEFAULT<slot>` accessor takes the parameters before its slot.
		for slot, body := range f.irSlot {
			view := *f
			view.params = f.params[:slot]
			byBody[body] = &view
		}
	}

	if len(fns) == 0 {
		t.Fatal("the cached modules contain no retained std function, so every count below is " +
			"vacuous rather than low; see irStdWantRetained in irstdbody_test.go")
	}

	shapes := map[string]int{}
	failures := map[string][]string{}
	ran := 0
	for _, fn := range fns {
		for _, b := range fn.Blocks() {
			for _, in := range b.Instrs() {
				shapes[vmShapeOf(in)]++
			}
			if b.Term() != nil {
				shapes[fmt.Sprintf("%T", b.Term())]++
			}
		}

		mod := owners[fn]
		m := vm.NewProgram(mod, modules, io.Discard).WithHosts(testHosts)
		var lastErr error
		okAny := false
		vectors := vmArgShapes(mod, fn, modules...)
		if f := byBody[fn]; f != nil {
			if args, ok := vmArgsForKindsIn(f.params, irModuleQualifier(mod.Name())); ok {
				vectors = append([][]any{args}, vectors...)
			}
		}
		for _, args := range vectors {
			_, err := vmRunSymV(m, fn.Sym(), args...)
			if vmRan(err) {
				okAny = true
				break
			}
			lastErr = err
			if vmHardFailure(err) {
				break
			}
		}
		if okAny {
			ran++
			continue
		}
		failures[vmClassify(lastErr)] = append(failures[vmClassify(lastErr)], names[fn])
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
	t.Logf("%d retained std functions, %d executed by the VM", len(fns), ran)

	if len(fns) != vmStdWantRetained {
		t.Errorf("the producer retained %d std functions, pinned at %d; keep this equal "+
			"to irStdWantRetained", len(fns), vmStdWantRetained)
	}
	if ran != vmStdWantRunnable {
		t.Errorf("the VM executed %d retained std functions, pinned at %d. A FALL is a "+
			"regression the retention count cannot see: the builder retains the same graph "+
			"whether or not the machine can run it", ran, vmStdWantRunnable)
	}
	host := len(failures["HOST: crosses into Go with no binding"])
	if host != vmStdWantHostFailures {
		t.Errorf("%d retained std functions call into Go with no VM binding, pinned at "+
			"%d. The call instruction runs; the crossing has no host binding", host, vmStdWantHostFailures)
	}
	link := len(failures["LINKING: a declaration this module did not retain"])
	if link != vmStdWantLinkingFailures {
		t.Errorf("%d retained std functions call a declaration this module does not hold, "+
			"pinned at %d. This is module closure rather than a machine gap: the "+
			"caller was retained and its callee was not",
			link, vmStdWantLinkingFailures)
	}
	boot := len(failures["BOOT: creatable only while boot runs"])
	if boot != vmStdWantBootFailures {
		t.Errorf("%d retained std functions make a supervisor outside boot, pinned at %d",
			boot, vmStdWantBootFailures)
	}
	// The buckets must exhaust the population, so a failure in an
	// unclassified bucket cannot hide behind unchanged totals.
	if ran+host+link+boot != len(fns) {
		t.Errorf("%d ran + %d HOST + %d LINKING + %d BOOT does not exhaust the %d retained std "+
			"functions, so some failure is in an UNCLASSIFIED bucket that no pin above "+
			"can see", ran, host, link, boot, len(fns))
	}
}

// vmArgsForKindsIn is vmArgsForKinds for a body of the std module qual, whose
// own declared types carry that module's qualifier at run time.
func vmArgsForKindsIn(params []kind, qual string) ([]any, bool) {
	args := make([]any, len(params))
	for i, k := range params {
		v := vmSampleForIn(k, qual)
		if v == nil {
			return nil, false
		}
		args[i] = v
	}
	return args, true
}

// vmTypeNameIn is the run-time name of a declared type, as irTypeSymName
// spells it, for a body in the std module qual ("" outside one).
func vmTypeNameIn(d *typeDef, qual string) string {
	if i, ok := stdEnumDef(d); ok {
		return irModuleQualifier(stdEnumSpecs[i].origin) + "." + stdEnumSpecs[i].nomi
	}
	if i, ok := stdStructIndex(d); ok {
		return irModuleQualifier(stdStructSpecs[i].origin) + "." + stdStructSpecs[i].nomi
	}
	if d.preludeOf != nil {
		return irModuleQualifier(d.preludeOf.spec.origin) + "." + d.preludeOf.spec.nomi
	}
	if d.foreign != "" {
		if mod := irModuleQualifier(d.foreign); mod != "" {
			return mod + "." + d.nomi
		}
	}
	if qual != "" {
		return qual + "." + d.nomi
	}
	return d.nomi
}

func vmSampleForIn(k kind, qual string) any {
	vmSampleFor := func(k kind) any { return vmSampleForIn(k, qual) }
	switch k {
	case kindInt:
		return int64(1)
	case kindFloat:
		return 1.5
	case kindString:
		return "s"
	case kindBool:
		return true
	}
	if k.tag == tagList && qual != "" {
		return (*rt.List[any])(nil)
	}
	if k.tag != tagNamed || k.def == nil {
		return nil
	}
	if irHostHandleKind(k) {
		return vmRegexHandle()
	}
	if irSupervisorKind(k) {
		return vm.IdleSupervisor()
	}
	if irContextKind(k) {
		return rt.ContextRoot()
	}
	for _, v := range vmCalendarValues() {
		if s, ok := v.(*rt.Record); ok && vmCalendarTypeOf(s) == k.def.nomi {
			return v
		}
	}
	if k.def.isDistinct && k.def.inner != kindInvalid {
		if inner := vmSampleFor(k.def.inner); inner != nil {
			return &probeDistinct{name: k.def.nomi, inner: inner}
		}
		return nil
	}
	if k == irBytesKind() {
		return rt.Bytes("A")
	}
	if k == irByteKind() {
		return rt.Byte(65)
	}
	if k.def.isEnum {
		name := k.def.nomi
		if i, ok := stdEnumDef(k.def); ok {
			name = irModuleQualifier(stdEnumSpecs[i].origin) + "." + stdEnumSpecs[i].nomi
		} else if qual != "" {
			name = vmTypeNameIn(k.def, qual)
		}
		for _, v := range k.def.variants {
			if v.kind == "bare" {
				return &probeVariant{enum: name, variant: v.nomi}
			}
		}
		// A struct-shaped variant, such as Backoff.Exponential, over a
		// sample of each field.
		for _, v := range k.def.variants {
			if v.kind != "struct" {
				continue
			}
			fields := map[string]any{}
			for _, p := range v.payloads {
				if fields[p.nomi] = vmSampleFor(p.k); fields[p.nomi] == nil {
					return nil
				}
			}
			return &probeVariant{enum: name, variant: v.nomi, fields: fields}
		}
		return nil
	}
	// A std struct body's own struct parameter.
	if qual == "" || k.def.isDistinct || len(k.def.fields) == 0 {
		return nil
	}
	fields := map[string]any{}
	for _, f := range k.def.fields {
		if f.k.def == k.def {
			return nil
		}
		if fields[f.nomi] = vmSampleFor(f.k); fields[f.nomi] == nil {
			return nil
		}
	}
	return &probeStruct{name: vmTypeNameIn(k.def, qual), fields: fields}
}

// vmCalendarTypeOf names the calendar type a parsed sample is, by the fields
// calendar's hosts fill.
func vmCalendarTypeOf(s *rt.Record) string {
	_, zone := s.FieldNamed("zone")
	_, offset := s.FieldNamed("offset_seconds")
	_, year := s.FieldNamed("year")
	_, hour := s.FieldNamed("hour")
	switch {
	case zone:
		return "DateTime"
	case offset:
		return "OffsetDateTime"
	case year && hour:
		return "NaiveDateTime"
	case year:
		return "Date"
	case hour:
		return "Time"
	}
	return ""
}

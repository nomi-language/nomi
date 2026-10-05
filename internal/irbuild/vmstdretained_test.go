package irbuild

// The VM against std's retained population.
//
// Every std function the builder retains in the cached stdlib modules runs on
// the VM, with arguments from its declared parameter kinds where the index
// holds them and otherwise from the graph (vmArgShapes). Each body runs in a
// machine over its own module, linked against every other cached module.
//
// The population is a committed list, testdata/expectations/vm-std-retained.txt,
// one line per retained function, `<file under std/>:<name>\t<outcome>`, as
// vm-retained.txt is for the corpus (vmretained_test.go). A function that ran
// in the committed list and no longer runs is named before any other
// difference. The buckets std is expected to fill are BOOT (Supervisor.new and
// its arity wrappers make a supervisor, which rt refuses outside boot, and
// this test runs no boot); HOST and LINKING are expected to be empty, and
// their classifiers are validated by
// TestVMCoverage_TheHostClassifierCatchesItsPlant and
// TestVMCoverage_TheLinkingClassifierCatchesItsPlant.

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// TestIRRetainedStdPopulationRuns runs every std function the builder
// retains, classifies each failure as TestIRRetainedPopulationRuns does, and
// holds the outcomes to vm-std-retained.txt.
func TestIRRetainedStdPopulationRuns(t *testing.T) {
	// Warmed first: `buildStdlibIndex` is not idempotent from a fresh
	// process, and only the warm reading is reproducible from inside a shared
	// test binary.
	buildStdlibIndex()

	idx := buildStdlibIndex()
	var fns []*ir.Func
	var modules []*ir.Module
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
			owners[fn] = mod
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
		t.Fatal("the cached modules contain no retained std function, so the list " +
			"comparison below is vacuous; see irStdWantRetained in irstdbody_test.go")
	}

	shapes := map[string]int{}
	failures := map[string][]string{}
	var lines []string
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
		key := vmStdDeclKey(fn, mod, byBody[fn])
		if okAny {
			ran++
			lines = append(lines, key+"\tran")
			continue
		}
		class := vmClassify(lastErr)
		failures[class] = append(failures[class], key)
		lines = append(lines, key+"\t"+class)
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

	vmRetainedStd.check(t, lines)
	vmCheckBuckets(t, failures)
}

// vmStdDeclKey is a std function's key in vm-std-retained.txt: its declaring
// file under std/ and its name. A declaration's own body is named as the
// index keys it, with its receiver and interface instantiation
// (`Date.Add<Duration, Date>.add`), since calendar.nomi alone declares
// dozens of `add`s; an arity wrapper or a defaulted slot's accessor carries
// its own name (`supervisors.Supervisor.new arity 1`). A key may still repeat.
func vmStdDeclKey(fn *ir.Func, mod *ir.Module, f *stdFunc) string {
	name := fn.Name()
	if f != nil && f.irBody == fn {
		name = strings.TrimPrefix(f.key, f.module+".")
	}
	file := filepath.ToSlash(fn.Pos().File())
	if i := strings.LastIndex(file, "/std/"); i >= 0 {
		file = file[i+len("/std/"):]
	}
	if file == "" {
		file = mod.Name()
	}
	return file + ":" + name
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

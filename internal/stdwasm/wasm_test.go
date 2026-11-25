package stdwasm

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

func list[T any](xs ...T) *rt.List[T] { return rt.ListFromGo(xs, func(x T) T { return x }) }

func slice[T any](xs *rt.List[T]) []T {
	var out []T
	for c := xs; c != nil; c = c.Tail {
		out = append(out, c.Head)
	}
	return out
}

func mustCompile(t *testing.T, name string) rt.WasmModule {
	t.Helper()
	res := Compile(rt.Bytes(fixture(t, name)))
	if res.Tag != rt.TagOk {
		t.Fatalf("%s does not compile: %s", name, res.Err)
	}
	return res.Ok
}

func newInstance(t *testing.T, m rt.WasmModule, limit int64, imports map[string]Import) rt.WasmInstance {
	t.Helper()
	inst, fail := instantiateWith(m, limit, imports)
	if fail != nil {
		t.Fatalf("instantiate: %v", slice(fail))
	}
	t.Cleanup(func() { Drop(inst) })
	return inst
}

// instantiateWith instantiates m with imports, each `module.name` declared
// with the module's own types.
func instantiateWith(m rt.WasmModule, limit int64, imports map[string]Import) (rt.WasmInstance, *rt.List[string]) {
	return instantiateStopping(m, limit, imports, nil)
}

// instantiateStopping is instantiateWith, each import in stops answering
// those values (kind, bits each) while the calling task is stopping.
func instantiateStopping(m rt.WasmModule, limit int64, imports map[string]Import, stops map[string][]int64) (rt.WasmInstance, *rt.List[string]) {
	var mods, names []string
	var params, results, answers []*rt.List[int64]
	var fns []Import
	for _, imp := range moduleOf(m).info.imports {
		mod, name := imp.module, imp.name
		fn, ok := imports[mod+"."+name]
		if !ok {
			continue
		}
		mods, names, fns = append(mods, mod), append(names, name), append(fns, fn)
		params = append(params, list(kindsOf(imp.typ.params)...))
		results = append(results, list(kindsOf(imp.typ.results)...))
		if answer, ok := stops[mod+"."+name]; ok {
			answers = append(answers, list(append([]int64{1}, answer...)...))
		} else {
			answers = append(answers, nil)
		}
	}
	fr := rt.NewFrame(context.Background())
	res := Instantiate(fr, m, list(mods...), list(names...), list(params...), list(results...), list(answers...), list(fns...), rt.SmallInt(limit))
	if res.Tag != rt.TagOk {
		return rt.WasmInstance{}, res.Err
	}
	return res.Ok, nil
}

func call(inst rt.WasmInstance, export string, args ...int64) ([]int64, []string) {
	res := Call(rt.NewFrame(context.Background()), inst, export, list(args...))
	if res.Tag != rt.TagOk {
		return nil, slice(res.Err)
	}
	return slice(res.Ok), nil
}

// A value of each type crosses in and out: an integer as its signed value,
// a float as its bits, and a result of several values in order.
func TestCall_EachValueType(t *testing.T) {
	inst := newInstance(t, mustCompile(t, "values"), DefaultLimit, nil)
	for _, tc := range []struct {
		export string
		args   []int64
		want   []int64
	}{
		{"add_i32", []int64{kindI32, 2, kindI32, 3}, []int64{kindI32, 5}},
		{"add_i32", []int64{kindI32, math.MaxUint32, kindI32, 2}, []int64{kindI32, 1}},
		{"add_i32", []int64{kindI32, math.MaxInt32, kindI32, 1}, []int64{kindI32, math.MinInt32}},
		{"add_i64", []int64{kindI64, math.MaxInt64, kindI64, 1}, []int64{kindI64, math.MinInt64}},
		{"mul_f32", []int64{kindF32, int64(math.Float32bits(1.5)), kindF32, int64(math.Float32bits(2))}, []int64{kindF32, int64(math.Float32bits(3))}},
		{"mul_f64", []int64{kindF64, int64(math.Float64bits(1.5)), kindF64, int64(math.Float64bits(-2))}, []int64{kindF64, int64(math.Float64bits(-3))}},
		{"pair", nil, []int64{kindI32, 1, kindI64, 2}},
		{"ready", nil, []int64{kindI32, 42}},
	} {
		got, fail := call(inst, tc.export, tc.args...)
		if fail != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s(%v) = %v, %v; want %v", tc.export, tc.args, got, fail, tc.want)
		}
	}
}

// A NaN a call returns crosses with the bits the module made it with;
// std/wasm, which builds the Value, reads each as Float.nan().
func TestCall_NaNKeepsItsBits(t *testing.T) {
	inst := newInstance(t, mustCompile(t, "values"), DefaultLimit, nil)
	for export, want := range map[string][]int64{
		"nan_payload_f64":  {kindF64, 0x7FF4_0000_0000_0001},
		"negative_nan_f64": {kindF64, -0x8_0000_0000_0000},
		"negative_nan_f32": {kindF32, 0xFFC0_0001},
	} {
		if got, fail := call(inst, export); fail != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s() = %#x, %v; want %#x", export, got, fail, want)
		}
	}
}

// Arguments that are not the export's parameters, and an export that does
// not exist, fail without running anything and leave the instance usable.
func TestCall_ArgumentsAreChecked(t *testing.T) {
	inst := newInstance(t, mustCompile(t, "values"), DefaultLimit, nil)
	for _, tc := range []struct {
		export string
		args   []int64
		want   []string
	}{
		{"add_i32", []int64{kindI32, 1}, []string{"mismatch", "add_i32 takes [I32, I32], and the call passes [I32]"}},
		{"add_i32", []int64{kindI32, 1, kindF64, 2}, []string{"mismatch", "add_i32 takes [I32, I32], and the call passes [I32, F64]"}},
		{"sub", nil, []string{"no_export", "sub"}},
	} {
		if _, fail := call(inst, tc.export, tc.args...); !reflect.DeepEqual(fail, tc.want) {
			t.Errorf("%s(%v) fails %q, want %q", tc.export, tc.args, fail, tc.want)
		}
	}
	if got, fail := call(inst, "add_i32", kindI32, 1, kindI32, 1); fail != nil || got[1] != 2 {
		t.Errorf("after the refusals, add_i32(1, 1) = %v, %v", got, fail)
	}
}

// Each trap is the kind it names, and leaves the instance dead: the next
// call, read or size answers the trap it died of. An `other` trap reads as
// wazero writes it on every engine. A trap after the module asked to grow
// past its limit is memory_limit on an engine that sees the refusal; on
// one that does not (the host's, engine_js.go) it is the trap it is.
func TestCall_TrapsLeaveTheInstanceDead(t *testing.T) {
	m := mustCompile(t, "values")
	for _, tc := range []struct {
		export string
		args   []int64
		want   []string
	}{
		{"unreachable", nil, []string{"unreachable"}},
		{"out_of_bounds", nil, []string{"out_of_bounds"}},
		{"divide", []int64{kindI32, 1, kindI32, 0}, []string{"other", "integer divide by zero"}},
		{"divide", []int64{kindI32, math.MinInt32, kindI32, -1}, []string{"other", "integer overflow"}},
		{"grow_or_trap", []int64{kindI32, 2}, refusedGrowthTrap},
	} {
		t.Run(tc.export, func(t *testing.T) {
			inst := newInstance(t, m, 2*pageSize, nil)
			if _, fail := call(inst, tc.export, tc.args...); !reflect.DeepEqual(fail, append([]string{"trapped"}, tc.want...)) {
				t.Fatalf("%s fails %q, want trapped %q", tc.export, fail, tc.want)
			}
			dead := append([]string{"dead"}, tc.want...)
			if _, fail := call(inst, "ready"); !reflect.DeepEqual(fail, dead) {
				t.Errorf("a call after the trap fails %q, want %q", fail, dead)
			}
			if res := Size(inst); res.Tag == rt.TagOk || !reflect.DeepEqual(slice(res.Err), dead) {
				t.Errorf("size after the trap = %+v, want %q", res, dead)
			}
			if res := Read(inst, rt.SmallInt(0), rt.SmallInt(1)); res.Tag == rt.TagOk || !reflect.DeepEqual(slice(res.Err), dead) {
				t.Errorf("read after the trap = %+v, want %q", res, dead)
			}
		})
	}
}

// Memory grows to the limit in whole pages and no further: past it,
// memory.grow answers -1 inside the module. A module whose initial memory
// is over the limit is not instantiated.
func TestMemoryLimit(t *testing.T) {
	m := mustCompile(t, "values")
	inst := newInstance(t, m, 3*pageSize+100, nil)
	for _, tc := range []struct{ pages, want int64 }{{1, 1}, {1, 2}, {1, -1}, {0, 3}} {
		if got, fail := call(inst, "grow", kindI32, tc.pages); fail != nil || got[1] != tc.want {
			t.Errorf("grow(%d) = %v, %v; want %d", tc.pages, got, fail, tc.want)
		}
	}
	if res := Size(inst); res.Ok != 3*pageSize {
		t.Errorf("size = %+v, want 3 pages", res)
	}
	if _, fail := instantiateWith(m, pageSize-1, nil); !reflect.DeepEqual(slice(fail), []string{"trapped", "memory_limit"}) {
		t.Errorf("an instance whose one page is over its limit fails %q, want trapped memory_limit", slice(fail))
	}
}

// Reads and writes stay inside memory; a range past it is refused with the
// memory's size, and the instance stays usable.
func TestMemory_ReadAndWriteBounds(t *testing.T) {
	inst := newInstance(t, mustCompile(t, "values"), DefaultLimit, nil)
	if w := Write(inst, rt.SmallInt(pageSize-2), "ab"); w.Tag != rt.TagOk {
		t.Fatalf("write at the end = %+v", w)
	}
	if r := Read(inst, rt.SmallInt(pageSize-2), rt.SmallInt(2)); r.Tag != rt.TagOk || r.Ok != "ab" {
		t.Errorf("read at the end = %+v", r)
	}
	if r := Read(inst, rt.SmallInt(0), rt.SmallInt(0)); r.Tag != rt.TagOk || r.Ok != "" {
		t.Errorf("an empty read = %+v", r)
	}
	outside := []string{"out_of_range", fmt.Sprint(pageSize)}
	for _, r := range []rt.Result[rt.Bytes, *rt.List[string]]{
		Read(inst, rt.SmallInt(pageSize-1), rt.SmallInt(2)),
		Read(inst, rt.SmallInt(-1), rt.SmallInt(1)),
		Read(inst, rt.SmallInt(0), rt.SmallInt(-1)),
		Read(inst, rt.UintInt(math.MaxUint64), rt.SmallInt(1)),
	} {
		if r.Tag == rt.TagOk || !reflect.DeepEqual(slice(r.Err), outside) {
			t.Errorf("a read outside memory = %+v, want %q", r, outside)
		}
	}
	if w := Write(inst, rt.SmallInt(pageSize-1), "ab"); w.Tag == rt.TagOk || !reflect.DeepEqual(slice(w.Err), outside) {
		t.Errorf("a write past the end = %+v, want %q", w, outside)
	}
	if got, fail := call(inst, "ready"); fail != nil || got[1] != 42 {
		t.Errorf("after the refusals, ready() = %v, %v", got, fail)
	}
}

// A dropped instance answers Dropped to every operation, and a second drop
// does nothing.
func TestDrop(t *testing.T) {
	inst := newInstance(t, mustCompile(t, "values"), DefaultLimit, nil)
	Drop(inst)
	Drop(inst)
	if _, fail := call(inst, "ready"); !reflect.DeepEqual(fail, []string{"dropped"}) {
		t.Errorf("a call after drop fails %q", fail)
	}
	if w := Write(inst, rt.SmallInt(0), "x"); w.Tag == rt.TagOk || !reflect.DeepEqual(slice(w.Err), []string{"dropped"}) {
		t.Errorf("a write after drop = %+v", w)
	}
}

// An import's function reads and writes the calling instance's memory
// through the Memory it is handed, which expires when it returns; its
// results go back into the module.
func TestImports_ReadAndWriteTheCallersMemory(t *testing.T) {
	var kept rt.WasmMemory
	inst := newInstance(t, mustCompile(t, "imports"), DefaultLimit, map[string]Import{
		"env.shout": func(fr *rt.Frame, mem rt.WasmMemory, args *rt.List[int64]) rt.Result[*rt.List[int64], string] {
			a := slice(args)
			got := MemoryRead(mem, rt.SmallInt(a[1]), rt.SmallInt(a[3]))
			if got.Tag != rt.TagOk {
				return rt.Err[*rt.List[int64]](fmt.Sprint(slice(got.Err)))
			}
			MemoryWrite(mem, rt.SmallInt(a[1]), rt.Bytes(strings.ToUpper(string(got.Ok))))
			kept = mem
			return rt.Ok[*rt.List[int64], string](nil)
		},
		"env.twice": func(fr *rt.Frame, mem rt.WasmMemory, args *rt.List[int64]) rt.Result[*rt.List[int64], string] {
			return rt.Ok[*rt.List[int64], string](list(kindI64, slice(args)[1]*2))
		},
	})
	Write(inst, rt.SmallInt(10), "quiet")
	if _, fail := call(inst, "shout", kindI32, 10, kindI32, 5); fail != nil {
		t.Fatalf("shout fails %q", fail)
	}
	if r := Read(inst, rt.SmallInt(10), rt.SmallInt(5)); r.Ok != "QUIET" {
		t.Errorf("after shout, memory holds %q", r.Ok)
	}
	if r := MemoryRead(kept, rt.SmallInt(0), rt.SmallInt(1)); r.Tag == rt.TagOk || !reflect.DeepEqual(slice(r.Err), []string{"expired"}) {
		t.Errorf("a Memory after its import returned reads %+v, want expired", r)
	}
	if got, fail := call(inst, "twice", kindI64, 21); fail != nil || got[1] != 42 {
		t.Errorf("twice(21) = %v, %v", got, fail)
	}
}

// An import that answers Err, or results of other types than the module
// takes, traps the call as import_failed, naming the import.
func TestImports_AFailureTrapsTheCall(t *testing.T) {
	m := mustCompile(t, "imports")
	for _, tc := range []struct {
		name string
		fn   Import
		want []string
	}{
		{"an Err", func(*rt.Frame, rt.WasmMemory, *rt.List[int64]) rt.Result[*rt.List[int64], string] {
			return rt.Err[*rt.List[int64]]("no twice today")
		}, []string{"trapped", "import_failed", "env.twice", "no twice today"}},
		{"a result of another type", func(*rt.Frame, rt.WasmMemory, *rt.List[int64]) rt.Result[*rt.List[int64], string] {
			return rt.Ok[*rt.List[int64], string](list(kindF64, 0))
		}, []string{"trapped", "import_failed", "env.twice", "result 1 is a F64, and the module takes a I64"}},
		{"too few results", func(*rt.Frame, rt.WasmMemory, *rt.List[int64]) rt.Result[*rt.List[int64], string] {
			return rt.Ok[*rt.List[int64], string](nil)
		}, []string{"trapped", "import_failed", "env.twice", "it returned 0 values, and the module takes 1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := newInstance(t, m, DefaultLimit, map[string]Import{"env.shout": tc.fn, "env.twice": tc.fn})
			if _, fail := call(inst, "twice", kindI64, 1); !reflect.DeepEqual(fail, tc.want) {
				t.Errorf("twice fails %q, want %q", fail, tc.want)
			}
		})
	}
}

// A panic in an import's function (a Nomi trap unwinding) leaves the
// instance dead and goes on unwinding through the call that reached it.
func TestImports_APanicUnwindsThroughTheCall(t *testing.T) {
	type nomiTrap struct{ msg string }
	panicking := func(*rt.Frame, rt.WasmMemory, *rt.List[int64]) rt.Result[*rt.List[int64], string] {
		panic(nomiTrap{"boom"})
	}
	inst := newInstance(t, mustCompile(t, "imports"), DefaultLimit, map[string]Import{"env.shout": panicking, "env.twice": panicking})
	func() {
		defer func() {
			if r := recover(); r != (nomiTrap{"boom"}) {
				t.Errorf("the call unwound with %v, want the import's panic", r)
			}
		}()
		call(inst, "twice", kindI64, 1)
		t.Error("the call returned")
	}()
	want := []string{"dead", "import_failed", "env.twice", "its function trapped"}
	if _, fail := call(inst, "twice", kindI64, 1); !reflect.DeepEqual(fail, want) {
		t.Errorf("a call after the unwind fails %q, want %q", fail, want)
	}
}

// stoppingFrame is the frame of a task whose deadline has passed: it is
// stopping, and its next safe point would unwind it.
func stoppingFrame(t *testing.T) *rt.Frame {
	t.Helper()
	fr, release := rt.EnterDeadline(rt.NewFrame(context.Background()), rt.ContextWithDeadline(rt.ContextRoot(), rt.Instant(1)))
	t.Cleanup(release)
	if !fr.Stopping() {
		t.Fatal("a frame past its deadline is not stopping")
	}
	return fr
}

// While the calling task is stopping, an import that declares an answer
// for then answers it without running its function, and the instance stays
// usable; one that declares none runs to its end on a frame that does not
// stop, as a deferred call does.
func TestImports_AStoppingTaskGetsTheDeclaredAnswer(t *testing.T) {
	ran := 0
	var frames []*rt.Frame
	twice := func(fr *rt.Frame, _ rt.WasmMemory, args *rt.List[int64]) rt.Result[*rt.List[int64], string] {
		ran++
		frames = append(frames, fr)
		return rt.Ok[*rt.List[int64], string](list(kindI64, slice(args)[1]*2))
	}
	m := mustCompile(t, "imports")
	answering, fail := instantiateStopping(m, DefaultLimit, map[string]Import{"env.shout": twice, "env.twice": twice}, map[string][]int64{"env.twice": {kindI64, 7}})
	if fail != nil {
		t.Fatalf("instantiate: %q", slice(fail))
	}
	t.Cleanup(func() { Drop(answering) })
	stopped := stoppingFrame(t)
	if got := Call(stopped, answering, "twice", list(kindI64, 21)); got.Tag != rt.TagOk || !reflect.DeepEqual(slice(got.Ok), []int64{kindI64, 7}) || ran != 0 {
		t.Errorf("twice(21) while stopping = %+v after %d runs, want the declared 7 and no run", got, ran)
	}
	if got, fail := call(answering, "twice", kindI64, 21); fail != nil || got[1] != 42 || ran != 1 {
		t.Errorf("twice(21) after = %v, %q after %d runs, want 42 from one run", got, fail, ran)
	}

	running := newInstance(t, m, DefaultLimit, map[string]Import{"env.shout": twice, "env.twice": twice})
	if got := Call(stopped, running, "twice", list(kindI64, 4)); got.Tag != rt.TagOk || slice(got.Ok)[1] != 8 || ran != 2 {
		t.Errorf("twice(4) while stopping, with no answer declared = %+v after %d runs, want 8 from a run", got, ran)
	}
	if last := frames[len(frames)-1]; last.Stopping() {
		t.Error("an import ran while its task stopped on a frame that stops")
	}
}

// An answer for a stopping task must be values of the import's results.
func TestInstantiate_ChecksTheStoppingAnswer(t *testing.T) {
	noop := func(*rt.Frame, rt.WasmMemory, *rt.List[int64]) rt.Result[*rt.List[int64], string] {
		return rt.Ok[*rt.List[int64], string](nil)
	}
	_, fail := instantiateStopping(mustCompile(t, "imports"), DefaultLimit, map[string]Import{"env.shout": noop, "env.twice": noop}, map[string][]int64{"env.twice": {kindF64, 0}})
	want := []string{"mismatch", "the module's import env.twice returns [I64], and the Nomi function's answer while its task stops is [F64]"}
	if !reflect.DeepEqual(slice(fail), want) {
		t.Errorf("an F64 answer for an I64 result: %q, want %q", slice(fail), want)
	}
}

// instantiate checks each import the module names against the Nomi
// function given for it, by name and by type, and refuses a module whose
// imports it cannot serve.
func TestInstantiate_ChecksTheImports(t *testing.T) {
	m := mustCompile(t, "imports")
	noop := func(*rt.Frame, rt.WasmMemory, *rt.List[int64]) rt.Result[*rt.List[int64], string] {
		return rt.Ok[*rt.List[int64], string](nil)
	}
	if _, fail := instantiateWith(m, DefaultLimit, map[string]Import{"env.shout": noop}); !reflect.DeepEqual(slice(fail), []string{"missing_import", "env", "twice"}) {
		t.Errorf("without env.twice: %q", slice(fail))
	}
	fr := rt.NewFrame(context.Background())
	res := Instantiate(fr, m, list("env", "env"), list("shout", "twice"),
		list(list(kindI32, kindI32), list(kindI32)), list(list[int64](), list(kindI64)),
		list(list[int64](), list[int64]()), list[Import](noop, noop), rt.SmallInt(DefaultLimit))
	want := []string{"mismatch", "the module's import env.twice takes [I64], and the Nomi function declares [I32]"}
	if res.Tag == rt.TagOk || !reflect.DeepEqual(slice(res.Err), want) {
		t.Errorf("env.twice declared over an I32: %+v, want %q", res, want)
	}
}

// Instance.run calls a live instance by run's convention, once for each
// request, the instance's memory kept between them.
func TestRunOn(t *testing.T) {
	inst := newInstance(t, mustCompile(t, "upper"), DefaultLimit, nil)
	fr := rt.NewFrame(context.Background())
	for _, in := range []string{"one", "two, three"} {
		if got := RunOn(fr, inst, "upper", rt.Bytes(in)); got.Tag != rt.TagOk || string(got.Ok) != strings.ToUpper(in) {
			t.Errorf("run upper over %q = %+v", in, got)
		}
	}
	if got := RunOn(fr, inst, "lower", "x"); got.Tag == rt.TagOk || !reflect.DeepEqual(slice(got.Err), []string{"no_export", "lower"}) {
		t.Errorf("run lower = %+v", got)
	}
	Drop(inst)
	if got := RunOn(fr, inst, "upper", "x"); got.Tag == rt.TagOk || !reflect.DeepEqual(slice(got.Err), []string{"dropped"}) {
		t.Errorf("run on a dropped instance = %+v", got)
	}
}

// run calls a fresh instance by its convention, within the memory limit
// it is given, and refuses a module that does not meet it.
func TestRun(t *testing.T) {
	upper := mustCompile(t, "upper")
	limit := rt.SmallInt(DefaultLimit)
	if got := Run(upper, "upper", "hello, wasm", limit); got.Tag != rt.TagOk || got.Ok != "HELLO, WASM" {
		t.Errorf("run upper = %+v", got)
	}
	if got := Run(upper, "upper", "", limit); got.Tag != rt.TagOk || got.Ok != "" {
		t.Errorf("run upper over nothing = %+v", got)
	}
	if got := Run(upper, "upper", "x", rt.SmallInt(pageSize-1)); got.Tag == rt.TagOk || !reflect.DeepEqual(slice(got.Err), []string{"trapped", "memory_limit"}) {
		t.Errorf("run upper under a limit below its one page = %+v, want trapped memory_limit", got)
	}
	values := mustCompile(t, "values")
	for _, tc := range []struct {
		m      rt.WasmModule
		export string
		want   []string
	}{
		{upper, "lower", []string{"no_export", "lower"}},
		{values, "add_i32", []string{"no_export", "alloc"}},
		{mustCompile(t, "imports"), "shout", []string{"missing_import", "env", "shout"}},
	} {
		if got := Run(tc.m, tc.export, "x", limit); got.Tag == rt.TagOk || !reflect.DeepEqual(slice(got.Err), tc.want) {
			t.Errorf("run %s = %+v, want %q", tc.export, got, tc.want)
		}
	}
}

// scribble runs residue's scribble in a fresh instance and answers what it
// found an earlier call left, or how the run failed.
func scribble(m rt.WasmModule) string {
	got := Run(m, "scribble", "", rt.SmallInt(DefaultLimit))
	if got.Tag != rt.TagOk {
		return fmt.Sprintf("the run failed: %v", slice(got.Err))
	}
	return string(got.Ok)
}

// fresh is what scribble answers in a fresh instance: nothing left.
const fresh = "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"

// run is pure: calls on many tasks at once each see a fresh instance,
// with nothing another call wrote to its globals or its memory, the pages
// it grows included.
func TestRun_CallsAtOnceSeeNothingOfEachOther(t *testing.T) {
	m := mustCompile(t, "residue")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 25 {
				if got := scribble(m); got != fresh {
					t.Errorf("a call found %q left, want nothing", got)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestCompile_RefusesWhatIsNotAModule(t *testing.T) {
	for _, data := range []string{"", "not wasm", string(fixture(t, "values")[:20])} {
		if res := Compile(rt.Bytes(data)); res.Tag == rt.TagOk || res.Err == "" {
			t.Errorf("compile(%q) = %+v, want an error", data, res)
		}
	}
}

// The same bytes compile once a process.
func TestCompile_SameBytesOnce(t *testing.T) {
	a, b := mustCompile(t, "values"), mustCompile(t, "values")
	if a.Module() != b.Module() {
		t.Error("compiling the same bytes twice made two modules")
	}
}

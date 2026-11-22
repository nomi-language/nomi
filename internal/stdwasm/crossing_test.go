package stdwasm

import (
	"math"
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// The cases of this file and wasm_test.go run on the engine of the build:
// wazero natively, and the host's under GOOS=js GOARCH=wasm with Node
// (internal/stdwasm package doc, "Engines"). They are the same on both but
// for refusedGrowthTrap.

// crossingImports answers the crossing fixture's imports: float and echo
// give back what they are given, recording it in got, and shout writes
// "grown" where it is pointed, recording the memory's size.
func crossingImports(got *[]int64) map[string]Import {
	echo := func(_ *rt.Frame, _ rt.WasmMemory, args *rt.List[int64]) rt.Result[*rt.List[int64], string] {
		*got = append(*got, slice(args)...)
		return rt.Ok[*rt.List[int64], string](args)
	}
	return map[string]Import{
		"env.float": echo,
		"env.echo":  echo,
		"env.shout": func(_ *rt.Frame, mem rt.WasmMemory, args *rt.List[int64]) rt.Result[*rt.List[int64], string] {
			a := slice(args)
			*got = append(*got, MemorySize(mem).Ok)
			if w := MemoryWrite(mem, rt.SmallInt(a[1]), "grown"); w.Tag != rt.TagOk {
				return rt.Err[*rt.List[int64]]("the write failed")
			}
			return rt.Ok[*rt.List[int64], string](nil)
		},
	}
}

// An i64 at either end of its range, and the values whose halves are the
// ends of an i32's, cross into a call, out of it, and both ways through an
// import.
func TestCall_I64AtTheExtremes(t *testing.T) {
	values := newInstance(t, mustCompile(t, "values"), DefaultLimit, nil)
	var got []int64
	crossing := newInstance(t, mustCompile(t, "crossing"), DefaultLimit, crossingImports(&got))
	for _, n := range []int64{math.MinInt64, math.MaxInt64, -1, 0, 1, math.MaxUint32, math.MaxUint32 + 1, math.MinInt32, -math.MaxUint32} {
		if res, fail := call(values, "add_i64", kindI64, n, kindI64, 0); fail != nil || !reflect.DeepEqual(res, []int64{kindI64, n}) {
			t.Errorf("add_i64(%d, 0) = %v, %q", n, res, fail)
		}
		got = nil
		if res, fail := call(crossing, "echo", kindI64, n); fail != nil || !reflect.DeepEqual(res, []int64{kindI64, n}) || !reflect.DeepEqual(got, []int64{kindI64, n}) {
			t.Errorf("echo(%d) = %v, %q, the import given %v", n, res, fail, got)
		}
	}
	if res, fail := call(values, "add_i64", kindI64, math.MinInt64, kindI64, -1); fail != nil || res[1] != math.MaxInt64 {
		t.Errorf("add_i64(min, -1) = %v, %q; want it to wrap to max", res, fail)
	}
}

// A float crosses an import as its bits both ways, a NaN's payload and a
// negative zero's sign kept, and an import's two results come back in
// order.
func TestImports_FloatsKeepTheirBits(t *testing.T) {
	var got []int64
	inst := newInstance(t, mustCompile(t, "crossing"), DefaultLimit, crossingImports(&got))
	for _, tc := range [][2]int64{
		{0x7ff4_0000_0000_0001, 0xffc0_0001},
		{-0x8_0000_0000_0000, 0x7fa0_0000},
		{int64(math.Float64bits(math.Copysign(0, -1))), int64(math.Float32bits(float32(math.Inf(-1))))},
		{int64(math.Float64bits(1.5)), int64(math.Float32bits(-2.25))},
	} {
		got = nil
		want := []int64{kindF64, tc[0], kindF32, tc[1]}
		if res, fail := call(inst, "float", want...); fail != nil || !reflect.DeepEqual(res, want) || !reflect.DeepEqual(got, want) {
			t.Errorf("float(%#x, %#x) = %#x, %q, the import given %#x", tc[0], tc[1], res, fail, got)
		}
	}
}

// An import sees the memory as the call that reached it left it, grown,
// and what it writes there the instance reads after.
func TestImports_SeeTheMemoryTheCallGrew(t *testing.T) {
	var got []int64
	inst := newInstance(t, mustCompile(t, "crossing"), DefaultLimit, crossingImports(&got))
	if _, fail := call(inst, "grow_and_shout", kindI32, 1, kindI32, pageSize+10, kindI32, 5); fail != nil {
		t.Fatalf("grow_and_shout fails %q", fail)
	}
	if !reflect.DeepEqual(got, []int64{2 * pageSize}) {
		t.Errorf("the import saw a memory of %v bytes, want two pages", got)
	}
	if r := Read(inst, rt.SmallInt(pageSize+10), rt.SmallInt(5)); r.Tag != rt.TagOk || r.Ok != "grown" {
		t.Errorf("after the call, the new page holds %+v", r)
	}
	if s := Size(inst); s.Ok != 2*pageSize {
		t.Errorf("size = %+v, want two pages", s)
	}
}

// A memory that declares a maximum grows to the lower of it and the limit.
func TestMemoryLimit_UnderAModulesMaximum(t *testing.T) {
	m := mustCompile(t, "crossing")
	for _, tc := range []struct {
		limit, grow, want int64
	}{
		{3 * pageSize, 1, 2 * pageSize},
		{3 * pageSize, 5, pageSize},
		{20 * pageSize, 15, 16 * pageSize},
		{20 * pageSize, 16, pageSize},
	} {
		var got []int64
		inst := newInstance(t, m, tc.limit, crossingImports(&got))
		if _, fail := call(inst, "grow_and_shout", kindI32, tc.grow, kindI32, 0, kindI32, 5); fail != nil {
			t.Errorf("limit %d, grow %d fails %q", tc.limit, tc.grow, fail)
			continue
		}
		if s := Size(inst); s.Ok != tc.want {
			t.Errorf("limit %d pages, grow %d: size %+v, want %d", tc.limit/pageSize, tc.grow, s, tc.want)
		}
	}
}

// The memory of a module that exports nothing is read and written as any
// other's.
func TestMemory_AModuleThatExportsNothing(t *testing.T) {
	inst := newInstance(t, mustCompile(t, "bare"), DefaultLimit, nil)
	if s := Size(inst); s.Ok != 2*pageSize {
		t.Errorf("size = %+v, want two pages", s)
	}
	if w := Write(inst, rt.SmallInt(pageSize+1), "page two"); w.Tag != rt.TagOk {
		t.Fatalf("write = %+v", w)
	}
	if r := Read(inst, rt.SmallInt(pageSize+1), rt.SmallInt(8)); r.Ok != "page two" {
		t.Errorf("read = %+v", r)
	}
	if got, fail := call(inst, "anything"); fail == nil || !reflect.DeepEqual(fail, []string{"no_export", "anything"}) {
		t.Errorf("a call = %v, %q", got, fail)
	}
}

// A recursion that never ends overflows the stack, a trap of its own.
func TestCall_StackOverflow(t *testing.T) {
	var got []int64
	inst := newInstance(t, mustCompile(t, "crossing"), DefaultLimit, crossingImports(&got))
	want := []string{"trapped", "other", "stack overflow"}
	if _, fail := call(inst, "recurse"); !reflect.DeepEqual(fail, want) {
		t.Errorf("recurse fails %q, want %q", fail, want)
	}
}

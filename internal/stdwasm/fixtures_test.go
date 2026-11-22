package stdwasm

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The test modules are assembled here by hand, from the text in
// testdata/<name>.wat beside each, so the tests need no WebAssembly
// toolchain. TestFixturesAreTheirAssembly holds testdata/<name>.wasm to
// what this file assembles; NOMI_REGENERATE_EXPECTATIONS=1 writes them.

func sleb(n int64) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if (n == 0 && b&0x40 == 0) || (n == -1 && b&0x40 != 0) {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

func i32c(n int32) []byte { return append([]byte{opI32Const}, sleb(int64(n))...) }
func i64c(n int64) []byte { return append([]byte{opI64Const}, sleb(n)...) }
func f32c(bits uint32) []byte {
	return binary.LittleEndian.AppendUint32([]byte{opF32Const}, bits)
}
func f64c(bits uint64) []byte {
	return binary.LittleEndian.AppendUint64([]byte{opF64Const}, bits)
}
func get(i uint64) []byte { return append([]byte{opLocalGet}, uleb(i)...) }

// sig is a type section entry.
func sig(params, results []valType) []byte { return encFuncType(funcType{params, results}) }

// fn is one defined function: its type's index, its locals by type, its
// code without the final end, and the name it is exported as, if any.
type fn struct {
	typ    uint64
	locals []valType
	code   []byte
	export string
}

// mod is a module to assemble.
type mod struct {
	types   [][]byte
	imports [][]byte // each module, name and type index
	memory  []byte   // the memory section's one entry, or nil
	globals [][]byte
	funcs   []fn
	// hideMemory leaves the memory unexported.
	hideMemory bool
}

func (m mod) assemble() []byte {
	out := []byte("\x00asm\x01\x00\x00\x00")
	out = append(out, encSection(1, vec(m.types...))...)
	if len(m.imports) > 0 {
		out = append(out, encSection(2, vec(m.imports...))...)
	}
	var idx [][]byte
	var exports [][]byte
	var bodies [][]byte
	for i, f := range m.funcs {
		idx = append(idx, uleb(f.typ))
		if f.export != "" {
			exports = append(exports, cat(encName(f.export), []byte{0x00}, uleb(uint64(len(m.imports)+i))))
		}
		var locals [][]byte
		for _, t := range f.locals {
			locals = append(locals, []byte{1, byte(t)})
		}
		body := cat(vec(locals...), f.code, []byte{opEnd})
		bodies = append(bodies, cat(uleb(uint64(len(body))), body))
	}
	out = append(out, encSection(3, vec(idx...))...)
	if m.memory != nil {
		out = append(out, encSection(5, vec(m.memory))...)
		if !m.hideMemory {
			exports = append(exports, cat(encName("memory"), []byte{0x02, 0x00}))
		}
	}
	if len(m.globals) > 0 {
		out = append(out, encSection(6, vec(m.globals...))...)
	}
	if len(exports) > 0 {
		out = append(out, encSection(7, vec(exports...))...)
	}
	return append(out, encSection(10, vec(bodies...))...)
}

func importFunc(module, field string, typ uint64) []byte {
	return cat(encName(module), encName(field), []byte{0x00}, uleb(typ))
}

// mutableI32 is a mutable i32 global starting at n.
func mutableI32(n int32) []byte { return cat([]byte{byte(tI32), 0x01}, i32c(n), []byte{opEnd}) }

// fixtures is every test module, by its file name without the extension.
var fixtures = map[string]mod{
	// values.wat: a function of each value type, the traps, memory
	// growth, and an `_initialize` that sets the global `ready` reads.
	"values": {
		types: [][]byte{
			sig([]valType{tI32, tI32}, []valType{tI32}), // 0
			sig([]valType{tI64, tI64}, []valType{tI64}), // 1
			sig([]valType{tF32, tF32}, []valType{tF32}), // 2
			sig([]valType{tF64, tF64}, []valType{tF64}), // 3
			sig(nil, []valType{tF64}),                   // 4
			sig(nil, []valType{tF32}),                   // 5
			sig(nil, nil),                               // 6
			sig(nil, []valType{tI32}),                   // 7
			sig([]valType{tI32}, []valType{tI32}),       // 8
			sig([]valType{tI32}, nil),                   // 9
			sig(nil, []valType{tI32, tI64}),             // 10
		},
		memory:  []byte{0x00, 0x01}, // at least 1 page, no maximum
		globals: [][]byte{mutableI32(0)},
		funcs: []fn{
			{typ: 0, code: cat(get(0), get(1), []byte{opI32Add}), export: "add_i32"},
			{typ: 1, code: cat(get(0), get(1), []byte{opI64Add}), export: "add_i64"},
			{typ: 2, code: cat(get(0), get(1), []byte{opF32Mul}), export: "mul_f32"},
			{typ: 3, code: cat(get(0), get(1), []byte{opF64Mul}), export: "mul_f64"},
			{typ: 4, code: f64c(0x7ff4_0000_0000_0001), export: "nan_payload_f64"},
			{typ: 4, code: f64c(0xfff8_0000_0000_0000), export: "negative_nan_f64"},
			{typ: 5, code: f32c(0xffc0_0001), export: "negative_nan_f32"},
			{typ: 6, code: []byte{opUnreachable}, export: "unreachable"},
			{typ: 7, code: cat(i32c(-1), []byte{opI32Load, 0x02, 0x00}), export: "out_of_bounds"},
			{typ: 0, code: cat(get(0), get(1), []byte{opI32DivS}), export: "divide"},
			{typ: 8, code: cat(get(0), []byte{opMemoryGrow, 0x00}), export: "grow"},
			{typ: 9, code: cat(get(0), []byte{opMemoryGrow, 0x00}, i32c(-1), []byte{opI32Eq, opIf, blockEmpty, opUnreachable, opEnd}), export: "grow_or_trap"},
			{typ: 10, code: cat(i32c(1), i64c(2)), export: "pair"},
			{typ: 6, code: cat(i32c(42), []byte{opGlobalSet, 0x00}), export: "_initialize"},
			{typ: 7, code: []byte{opGlobalGet, 0x00}, export: "ready"},
		},
	},
	// imports.wat: two functions that call the imports they are named
	// for, one over memory and one over a value.
	"imports": {
		types: [][]byte{
			sig([]valType{tI32, tI32}, nil),       // 0
			sig([]valType{tI64}, []valType{tI64}), // 1
		},
		imports: [][]byte{importFunc("env", "shout", 0), importFunc("env", "twice", 1)},
		memory:  []byte{0x00, 0x01},
		funcs: []fn{
			{typ: 0, code: cat(get(0), get(1), []byte{opCall, 0x00}), export: "shout"},
			{typ: 1, code: cat(get(0), []byte{opCall, 0x01}), export: "twice"},
		},
	},
	// crossing.wat: imports of floats and of an i64 called through
	// exports, an import called after the memory grew, and a recursion
	// that overflows the stack, over a memory with a maximum that it does
	// not export.
	"crossing": {
		types: [][]byte{
			sig([]valType{tF64, tF32}, []valType{tF64, tF32}), // 0
			sig([]valType{tI32, tI32}, nil),                   // 1
			sig([]valType{tI32, tI32, tI32}, nil),             // 2
			sig(nil, nil),                                     // 3
			sig([]valType{tI64}, []valType{tI64}),             // 4
		},
		imports:    [][]byte{importFunc("env", "float", 0), importFunc("env", "shout", 1), importFunc("env", "echo", 4)},
		memory:     []byte{0x01, 0x01, 0x10}, // 1 page, at most 16
		hideMemory: true,
		funcs: []fn{
			{typ: 0, code: cat(get(0), get(1), []byte{opCall, 0x00}), export: "float"},
			{typ: 2, code: cat(get(0), []byte{opMemoryGrow, 0x00, opDrop}, get(1), get(2), []byte{opCall, 0x01}), export: "grow_and_shout"},
			{typ: 3, code: []byte{opCall, 0x05}, export: "recurse"},
			{typ: 4, code: cat(get(0), []byte{opCall, 0x02}), export: "echo"},
		},
	},
	// bare.wat: two pages of memory, and nothing exported.
	"bare": {
		memory:     []byte{0x00, 0x02},
		hideMemory: true,
	},
	// upper.wat: `wasm.run`'s calling convention, a bump allocator and a
	// function that uppercases ASCII letters in place.
	"upper": {
		types: [][]byte{
			sig([]valType{tI32}, []valType{tI32}),       // 0: alloc
			sig([]valType{tI32, tI32}, []valType{tI64}), // 1: upper
		},
		memory:  []byte{0x00, 0x01},
		globals: [][]byte{mutableI32(1024)},
		funcs: []fn{
			{typ: 0, locals: []valType{tI32}, export: "alloc", code: cat(
				[]byte{opGlobalGet, 0x00, opLocalSet, 0x01},
				[]byte{opGlobalGet, 0x00}, get(0), []byte{opI32Add, opGlobalSet, 0x00},
				get(1),
			)},
			{typ: 1, locals: []valType{tI32, tI32}, export: "upper", code: cat(
				[]byte{opBlock, blockEmpty, opLoop, blockEmpty},
				get(2), get(1), []byte{opI32GeU, opBrIf, 0x01},
				// the address, then the byte there less 32 when it is a-z
				get(0), get(2), []byte{opI32Add},
				get(0), get(2), []byte{opI32Add, opI32Load8U, 0x00, 0x00, opLocalTee, 0x03},
				get(3), i32c(97), []byte{opI32Sub}, i32c(26), []byte{opI32LtU}, i32c(32), []byte{opI32Mul, opI32Sub},
				[]byte{opI32Store8, 0x00, 0x00},
				get(2), i32c(1), []byte{opI32Add, opLocalSet, 0x02},
				[]byte{opBr, 0x00, opEnd, opEnd},
				get(0), []byte{opI64ExtendI32U}, i64c(32), []byte{opI64Shl},
				get(1), []byte{opI64ExtendI32U, opI64Or},
			)},
		},
	},
}

// fixture is the committed module name.
func fixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".wasm"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFixturesAreTheirAssembly(t *testing.T) {
	for n, m := range fixtures {
		want := m.assemble()
		path := filepath.Join("testdata", n+".wasm")
		if os.Getenv("NOMI_REGENERATE_EXPECTATIONS") == "1" {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not what fixtures_test.go assembles; rerun with NOMI_REGENERATE_EXPECTATIONS=1", path)
		}
		if _, err := os.Stat(filepath.Join("testdata", n+".wat")); err != nil {
			t.Errorf("%s has no .wat source beside it: %v", path, err)
		}
	}
}

package stdwasm

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// The parts of the WebAssembly binary format std::wasm reads itself: each
// function import's and export's type and the module's own memory, which
// the checks above the engine read (wasm.go) and the host engine's build
// rewrites (engine_js.go). The engine has validated the bytes first; a form
// this reader does not know, such as a type of the GC proposal, is refused
// as one std::wasm cannot run.

// valType is a value type as the binary format writes it.
type valType byte

// The value types std::wasm has a value for.
const (
	tI32 valType = 0x7f
	tI64 valType = 0x7e
	tF32 valType = 0x7d
	tF64 valType = 0x7c
)

// typeName is t as a message names it.
func typeName(t valType) string {
	switch t {
	case tI32:
		return "i32"
	case tI64:
		return "i64"
	case tF32:
		return "f32"
	case tF64:
		return "f64"
	case 0x7b:
		return "v128"
	case 0x70:
		return "funcref"
	case 0x6f:
		return "externref"
	}
	return "reference"
}

// funcType is a function's parameters and results.
type funcType struct{ params, results []valType }

// key is ft as a string, the same for the same types.
func (ft funcType) key() string {
	return string(toBytes(ft.params)) + "\x00" + string(toBytes(ft.results))
}

func toBytes(ts []valType) []byte {
	out := make([]byte, len(ts))
	for i, t := range ts {
		out[i] = byte(t)
	}
	return out
}

// funcImport is one function import.
type funcImport struct {
	module, name string
	typ          funcType
}

// limits is a memory's size in pages: its minimum, and its maximum when it
// declares one. flags is the binary format's (bit 0: a maximum, bit 1:
// shared, bit 2: 64-bit addresses).
type limits struct {
	flags    byte
	min, max uint64
}

func (l limits) hasMax() bool { return l.flags&1 != 0 }

// section is where one section's contents are in the module's bytes.
type section struct {
	id         byte
	start, end int
}

// moduleInfo is what std::wasm reads from a module's bytes.
type moduleInfo struct {
	// imports is the function imports, in order, and other the module and
	// name of its first import of anything else (a memory, a table, a
	// global, a tag), which instantiate never gives.
	imports []funcImport
	other   *[2]string
	// exports is each exported function's type, by its name.
	exports map[string]funcType
	// memories is the limits of each memory the module defines, and
	// memoryExport the name the first is exported as, or "".
	memories     []limits
	memoryExport string
	// sections is every section, in order.
	sections []section
}

// memory is the limits of the module's first memory, or false when it
// defines none.
func (m *moduleInfo) memory() (limits, bool) {
	if len(m.memories) == 0 {
		return limits{}, false
	}
	return m.memories[0], true
}

// reader reads the binary format.
type reader struct {
	data []byte
	at   int
}

var errShort = errors.New("the module ends early")

func (r *reader) byte() (byte, error) {
	if r.at >= len(r.data) {
		return 0, errShort
	}
	b := r.data[r.at]
	r.at++
	return b, nil
}

func (r *reader) uleb() (uint64, error) {
	var n uint64
	for shift := 0; shift < 64; shift += 7 {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		n |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return n, nil
		}
	}
	return 0, errors.New("an integer is too long")
}

// count is a vector's length, held to what the bytes left could hold.
func (r *reader) count() (int, error) {
	n, err := r.uleb()
	if err != nil {
		return 0, err
	}
	if n > uint64(len(r.data)-r.at) {
		return 0, errShort
	}
	return int(n), nil
}

func (r *reader) name() (string, error) {
	n, err := r.count()
	if err != nil {
		return "", err
	}
	s := string(r.data[r.at : r.at+n])
	r.at += n
	if !utf8.ValidString(s) {
		return "", errors.New("a name is not UTF-8")
	}
	return s, nil
}

// valType reads a value type; a reference to a heap type (0x63, 0x64) is
// read past its type index and kept as the reference it is.
func (r *reader) valType() (valType, error) {
	b, err := r.byte()
	if err != nil {
		return 0, err
	}
	if b == 0x63 || b == 0x64 {
		if _, err := r.uleb(); err != nil {
			return 0, err
		}
	}
	return valType(b), nil
}

func (r *reader) valTypes() ([]valType, error) {
	n, err := r.count()
	if err != nil {
		return nil, err
	}
	out := make([]valType, n)
	for i := range out {
		if out[i], err = r.valType(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *reader) limits() (limits, error) {
	flags, err := r.byte()
	if err != nil {
		return limits{}, err
	}
	if flags > 7 {
		return limits{}, fmt.Errorf("a memory's limits have the flags %#x", flags)
	}
	l := limits{flags: flags}
	if l.min, err = r.uleb(); err != nil {
		return limits{}, err
	}
	if l.hasMax() {
		if l.max, err = r.uleb(); err != nil {
			return limits{}, err
		}
	}
	return l, nil
}

// parseModule reads data's function imports and exports and its memories.
func parseModule(data []byte) (*moduleInfo, error) {
	if len(data) < 8 || string(data[:4]) != "\x00asm" || string(data[4:8]) != "\x01\x00\x00\x00" {
		return nil, errors.New("the bytes are not a WebAssembly module of version 1")
	}
	info := &moduleInfo{exports: map[string]funcType{}}
	var types []funcType
	var funcs []int // each function's type index, imports first
	r := &reader{data: data, at: 8}
	for r.at < len(data) {
		id, err := r.byte()
		if err != nil {
			return nil, err
		}
		size, err := r.count()
		if err != nil {
			return nil, err
		}
		s := section{id: id, start: r.at, end: r.at + size}
		info.sections = append(info.sections, s)
		body := &reader{data: data[:s.end], at: s.start}
		switch id {
		case 1:
			err = body.types(&types)
		case 2:
			err = body.imports(info, types, &funcs)
		case 3:
			err = body.functions(types, &funcs)
		case 5:
			err = body.memories(info)
		case 7:
			err = body.exports(info, types, funcs)
		}
		if err != nil {
			return nil, err
		}
		r.at = s.end
	}
	return info, nil
}

func (r *reader) types(types *[]funcType) error {
	n, err := r.count()
	if err != nil {
		return err
	}
	for range n {
		form, err := r.byte()
		if err != nil {
			return err
		}
		if form != 0x60 {
			return fmt.Errorf("the module declares a type of the form %#x, which std::wasm does not run", form)
		}
		var ft funcType
		if ft.params, err = r.valTypes(); err != nil {
			return err
		}
		if ft.results, err = r.valTypes(); err != nil {
			return err
		}
		*types = append(*types, ft)
	}
	return nil
}

func typeAt(types []funcType, i uint64) (funcType, error) {
	if i >= uint64(len(types)) {
		return funcType{}, fmt.Errorf("the type index %d is past the module's %d types", i, len(types))
	}
	return types[i], nil
}

func (r *reader) imports(info *moduleInfo, types []funcType, funcs *[]int) error {
	n, err := r.count()
	if err != nil {
		return err
	}
	for range n {
		mod, err := r.name()
		if err != nil {
			return err
		}
		name, err := r.name()
		if err != nil {
			return err
		}
		kind, err := r.byte()
		if err != nil {
			return err
		}
		switch kind {
		case 0x00:
			i, err := r.uleb()
			if err != nil {
				return err
			}
			ft, err := typeAt(types, i)
			if err != nil {
				return err
			}
			info.imports = append(info.imports, funcImport{module: mod, name: name, typ: ft})
			*funcs = append(*funcs, int(i))
			continue
		case 0x01:
			if _, err = r.valType(); err == nil {
				_, err = r.limits()
			}
		case 0x02:
			_, err = r.limits()
		case 0x03:
			if _, err = r.valType(); err == nil {
				_, err = r.byte()
			}
		case 0x04:
			if _, err = r.byte(); err == nil {
				_, err = r.uleb()
			}
		default:
			err = fmt.Errorf("the module imports %s.%s of the kind %#x", mod, name, kind)
		}
		if err != nil {
			return err
		}
		if info.other == nil {
			info.other = &[2]string{mod, name}
		}
	}
	return nil
}

func (r *reader) functions(types []funcType, funcs *[]int) error {
	n, err := r.count()
	if err != nil {
		return err
	}
	for range n {
		i, err := r.uleb()
		if err != nil {
			return err
		}
		if _, err := typeAt(types, i); err != nil {
			return err
		}
		*funcs = append(*funcs, int(i))
	}
	return nil
}

func (r *reader) memories(info *moduleInfo) error {
	n, err := r.count()
	if err != nil {
		return err
	}
	for range n {
		l, err := r.limits()
		if err != nil {
			return err
		}
		info.memories = append(info.memories, l)
	}
	return nil
}

func (r *reader) exports(info *moduleInfo, types []funcType, funcs []int) error {
	n, err := r.count()
	if err != nil {
		return err
	}
	for range n {
		name, err := r.name()
		if err != nil {
			return err
		}
		kind, err := r.byte()
		if err != nil {
			return err
		}
		i, err := r.uleb()
		if err != nil {
			return err
		}
		switch {
		case kind == 0x00:
			if i >= uint64(len(funcs)) {
				return fmt.Errorf("the export %s names the function %d, past the module's %d", name, i, len(funcs))
			}
			info.exports[name] = types[funcs[i]]
		case kind == 0x02 && i == 0 && info.memoryExport == "":
			info.memoryExport = name
		}
	}
	return nil
}

// The binary format written: the host engine's build assembles its
// trampolines and rewrites a module's memory and exports with these, and
// the tests their fixtures.

// The instructions std::wasm assembles (the trampolines, the test
// fixtures), by their opcodes, and the empty block type.
const (
	opUnreachable   = 0x00
	opBlock         = 0x02
	opLoop          = 0x03
	opIf            = 0x04
	opEnd           = 0x0b
	opBr            = 0x0c
	opBrIf          = 0x0d
	opCall          = 0x10
	opDrop          = 0x1a
	opLocalGet      = 0x20
	opLocalSet      = 0x21
	opLocalTee      = 0x22
	opGlobalGet     = 0x23
	opGlobalSet     = 0x24
	opI32Load       = 0x28
	opI32Load8U     = 0x2d
	opI32Store8     = 0x3a
	opMemoryGrow    = 0x40
	opI32Const      = 0x41
	opI64Const      = 0x42
	opF32Const      = 0x43
	opF64Const      = 0x44
	opI32Eq         = 0x46
	opI32LtU        = 0x49
	opI32GeU        = 0x4f
	opI32Add        = 0x6a
	opI32Sub        = 0x6b
	opI32Mul        = 0x6c
	opI32DivS       = 0x6d
	opI64Add        = 0x7c
	opI64Or         = 0x84
	opI64Shl        = 0x86
	opI64ShrU       = 0x88
	opF32Mul        = 0x94
	opF32Div        = 0x95
	opF64Mul        = 0xa2
	opF64Div        = 0xa3
	opI32WrapI64    = 0xa7
	opI64ExtendI32U = 0xad
	opI32ReinterpF  = 0xbc
	opI64ReinterpF  = 0xbd
	opF32ReinterpI  = 0xbe
	opF64ReinterpI  = 0xbf
	blockEmpty      = 0x40
)

func uleb(n uint64) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func encName(s string) []byte { return append(uleb(uint64(len(s))), s...) }

func vec(items ...[]byte) []byte {
	out := uleb(uint64(len(items)))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func encSection(id byte, body []byte) []byte {
	return cat([]byte{id}, uleb(uint64(len(body))), body)
}

func encLimits(l limits) []byte {
	out := append([]byte{l.flags}, uleb(l.min)...)
	if l.hasMax() {
		out = append(out, uleb(l.max)...)
	}
	return out
}

func encFuncType(ft funcType) []byte {
	return cat([]byte{0x60}, vec(each(ft.params)...), vec(each(ft.results)...))
}

func each(ts []valType) [][]byte {
	out := make([][]byte, len(ts))
	for i, t := range ts {
		out[i] = []byte{byte(t)}
	}
	return out
}

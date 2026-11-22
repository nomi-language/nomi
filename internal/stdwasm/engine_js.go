//go:build js

package stdwasm

import (
	"crypto/sha256"
	"errors"
	"strconv"
	"sync"
	"syscall/js"
)

// The host engine: the WebAssembly engine of the JavaScript host the
// program runs in (V8 in Node and Chrome, SpiderMonkey in Firefox,
// JavaScriptCore in Safari), through syscall/js. `new WebAssembly.Module`
// and `new WebAssembly.Instance` are synchronous, which a browser allows
// at any size only in a worker; the tour runs the VM in one.
//
// Every value crosses JavaScript as i32 Numbers: an i32 as itself, an f32
// as its bits, an i64 or an f64 as its low and high 32 bits. A trampoline
// module between Go and the module does the splitting and joining, in
// WebAssembly, so a NaN keeps its bits and no BigInt reaches Go (syscall/js
// never frees one). The import trampoline also stops the wasm when an
// import failed: a Go function called from JavaScript cannot throw, so it
// sets the trampoline's global `failed`, and the trampoline runs
// `unreachable`.
//
// A module's memory cannot be limited from outside it, so the module is
// compiled with each memory's maximum lowered to the instance's limit
// (rewrite), and `memory.grow` answers -1 inside the module past it, as on
// wazero. A module compiles once a limit it binds at, the default limit's
// at compile. The engine cannot see a refused growth, so a trap after one
// is its own kind here, where wazero names it memory_limit. The rewrite
// also exports the first memory, which JavaScript reaches only through an
// export.
//
// An import's Go function runs while JavaScript waits in the call into the
// module, so it must not wait for anything that needs JavaScript's event
// loop: in Node, a write to the process's output is one, and the program
// stops with "all goroutines are asleep". A channel between tasks and a
// timer's sleep are not.

var (
	wasmJS     = js.Global().Get("WebAssembly")
	uint8Array = js.Global().Get("Uint8Array")
	object     = js.Global().Get("Object")
)

// memoryExport is the name the rewrite exports a module's first memory as,
// when the module does not.
const memoryExport = "\x00memory"

type jsModule struct {
	data []byte
	info *moduleInfo
	// compiled is the module compiled for each limit, in pages, that
	// lowers a memory's maximum (0 for none that does).
	mu       sync.Mutex
	compiled map[uint64]js.Value
	// trampoline is the compiled import trampoline, once, and firsts the
	// first index of each import it serves, in its order.
	trampoline js.Value
	firsts     []int
	once       sync.Once
}

// jsError is the message of the JavaScript exception a call into the host
// threw, recovered as syscall/js's panic.
func jsError(r any) error {
	e, ok := r.(js.Error)
	if !ok {
		panic(r)
	}
	v := e.Value
	if v.Type() == js.TypeObject {
		if m := v.Get("message"); m.Type() == js.TypeString {
			return errors.New(m.String())
		}
	}
	return errors.New(js.Global().Get("String").Invoke(v).String())
}

// newModule compiles data, or answers the engine's message.
func hostModule(data []byte) (mod js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = jsError(r)
		}
	}()
	u8 := uint8Array.New(len(data))
	js.CopyBytesToJS(u8, data)
	return wasmJS.Get("Module").New(u8), nil
}

func hostInstance(mod, imports js.Value) (inst js.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = jsError(r)
		}
	}()
	return wasmJS.Get("Instance").New(mod, imports), nil
}

func compileEngine(data []byte, _ [sha256.Size]byte, info *moduleInfo) (engineModule, string) {
	m := &jsModule{data: data, info: info, compiled: map[uint64]js.Value{}}
	if info == nil {
		if _, err := hostModule(data); err != nil {
			return nil, err.Error()
		}
		return m, ""
	}
	if _, err := m.module(DefaultLimit); err != nil {
		return nil, err.Error()
	}
	return m, ""
}

func (m *jsModule) close() {}

// capAt is the limit in pages the module is compiled for under limit: 0
// when no memory's maximum is past it.
func (m *jsModule) capAt(limit uint64) uint64 {
	pages := limit / pageSize
	for _, l := range m.info.memories {
		if maxPages(l) > pages {
			return pages
		}
	}
	return 0
}

// maxPages is the most pages a memory of limits l grows to.
func maxPages(l limits) uint64 {
	switch {
	case l.hasMax():
		return l.max
	case l.flags&4 != 0:
		return 1 << 48
	}
	return 65536
}

// module is m compiled for limit.
func (m *jsModule) module(limit uint64) (js.Value, error) {
	key := m.capAt(limit)
	m.mu.Lock()
	defer m.mu.Unlock()
	if mod, ok := m.compiled[key]; ok {
		return mod, nil
	}
	mod, err := hostModule(m.rewrite(key))
	if err != nil {
		return js.Value{}, err
	}
	m.compiled[key] = mod
	return mod, nil
}

// rewrite is m's bytes with each memory's maximum lowered to pages (none
// when 0) and the first memory exported, when the module defines one and
// does not export it.
func (m *jsModule) rewrite(pages uint64) []byte {
	info := m.info
	exportMemory := len(info.memories) > 0 && info.memoryExport == ""
	extra := cat(encName(memoryExport), []byte{0x02}, uleb(0))
	out := append([]byte(nil), m.data[:8]...)
	exported := false
	for _, s := range info.sections {
		body := m.data[s.start:s.end]
		// The sections after the exports: start, element, data count, code
		// and data.
		if exportMemory && !exported && s.id >= 8 && s.id <= 12 {
			out = append(out, encSection(7, cat(uleb(1), extra))...)
			exported = true
		}
		switch {
		case s.id == 5 && pages > 0:
			var entries [][]byte
			for _, l := range info.memories {
				most := maxPages(l)
				l.flags |= 1
				l.max = min(most, max(pages, l.min))
				entries = append(entries, encLimits(l))
			}
			body = vec(entries...)
		case s.id == 7 && exportMemory:
			r := &reader{data: body}
			n, _ := r.uleb()
			body = cat(uleb(n+1), body[r.at:], extra)
			exported = true
		}
		out = append(out, encSection(s.id, body)...)
	}
	if exportMemory && !exported {
		out = append(out, encSection(7, cat(uleb(1), extra))...)
	}
	return out
}

// lowered is how many i32s ts cross JavaScript as.
func lowered(ts []valType) int {
	n := 0
	for _, t := range ts {
		n++
		if t == tI64 || t == tF64 {
			n++
		}
	}
	return n
}

func localGet(i int) []byte { return append([]byte{opLocalGet}, uleb(uint64(i))...) }
func localSet(i int) []byte { return append([]byte{opLocalSet}, uleb(uint64(i))...) }

// split pushes the value of type t in local i as the i32s it crosses as.
func split(t valType, i int) []byte {
	switch t {
	case tF32:
		return cat(localGet(i), []byte{opI32ReinterpF})
	case tI64, tF64:
		get := localGet(i)
		if t == tF64 {
			get = append(get, opI64ReinterpF)
		}
		return cat(get, []byte{opI32WrapI64}, get, []byte{opI64Const, 32, opI64ShrU, opI32WrapI64})
	}
	return localGet(i)
}

// join pushes the value of type t whose i32s are in locals from i.
func join(t valType, i int) []byte {
	switch t {
	case tF32:
		return cat(localGet(i), []byte{opF32ReinterpI})
	case tI64, tF64:
		code := cat(localGet(i), []byte{opI64ExtendI32U}, localGet(i+1), []byte{opI64ExtendI32U, opI64Const, 32, opI64Shl, opI64Or})
		if t == tF64 {
			code = append(code, opF64ReinterpI)
		}
		return code
	}
	return localGet(i)
}

// lowerSig is ft as it crosses JavaScript: i32s.
func lowerSig(ft funcType) funcType {
	i32s := func(n int) []valType {
		out := make([]valType, n)
		for i := range out {
			out[i] = tI32
		}
		return out
	}
	return funcType{i32s(lowered(ft.params)), i32s(lowered(ft.results))}
}

// trampolineBody is the code of trampoline function callee over a function
// of type ft. Inward, the trampoline has type ft and calls the lowered
// import: it splits the arguments, calls, runs check, and joins the
// results. Outward, it has the lowered type and calls an import of type
// ft: it joins the arguments, calls, and splits the results.
func trampolineBody(ft funcType, callee int, inward bool, check []byte) []byte {
	var code []byte
	var locals [][]byte
	np := len(ft.params)
	if inward {
		for i, t := range ft.params {
			code = append(code, split(t, i)...)
		}
	} else {
		np = 0
		for _, t := range ft.params {
			code = append(code, join(t, np)...)
			np += lowered([]valType{t})
		}
	}
	code = cat(code, []byte{opCall}, uleb(uint64(callee)), check)
	// The results, in locals after the parameters, the last first.
	results := ft.results
	if inward {
		results = lowerSig(ft).results
	}
	for j := len(results) - 1; j >= 0; j-- {
		code = append(code, localSet(np+j)...)
	}
	for _, t := range results {
		locals = append(locals, []byte{1, byte(t)})
	}
	at := np
	for _, t := range ft.results {
		if inward {
			code = append(code, join(t, at)...)
			at += lowered([]valType{t})
		} else {
			code = append(code, split(t, at)...)
			at++
		}
	}
	body := cat(vec(locals...), code, []byte{opEnd})
	return cat(uleb(uint64(len(body))), body)
}

// trampolines assembles a module whose function k imports "go"."k" and is
// exported as "k": of type ft[k] over a lowered import (inward), or
// lowered over an import of type ft[k]. An inward module has the global
// `failed`, which stops a call after its import returns.
func trampolines(fts []funcType, inward bool) []byte {
	var types, imports, funcs, exports, bodies [][]byte
	var check []byte
	if inward {
		check = []byte{opGlobalGet, 0, opIf, blockEmpty, opUnreachable, opEnd}
	}
	for k, ft := range fts {
		outer, inner := ft, lowerSig(ft)
		if !inward {
			outer, inner = inner, ft
		}
		types = append(types, encFuncType(outer), encFuncType(inner))
		imports = append(imports, cat(encName("go"), encName(strconv.Itoa(k)), []byte{0x00}, uleb(uint64(2*k+1))))
		funcs = append(funcs, uleb(uint64(2*k)))
		exports = append(exports, cat(encName(strconv.Itoa(k)), []byte{0x00}, uleb(uint64(len(fts)+k))))
		bodies = append(bodies, trampolineBody(ft, k, inward, check))
	}
	out := cat([]byte("\x00asm\x01\x00\x00\x00"), encSection(1, vec(types...)), encSection(2, vec(imports...)), encSection(3, vec(funcs...)))
	if inward {
		out = append(out, encSection(6, vec(cat([]byte{byte(tI32), 0x01, 0x41, 0x00, opEnd})))...)
		exports = append(exports, cat(encName("failed"), []byte{0x03, 0x00}))
	}
	return cat(out, encSection(7, vec(exports...)), encSection(10, vec(bodies...)))
}

// exportTrampolines is the compiled trampoline of each export type that
// has a value other than an i32, by the type's key.
var exportTrampolines sync.Map

func exportTrampoline(ft funcType) js.Value {
	if v, ok := exportTrampolines.Load(ft.key()); ok {
		return v.(js.Value)
	}
	mod, err := hostModule(trampolines([]funcType{ft}, false))
	if err != nil {
		panic("std::wasm: an export trampoline does not compile: " + err.Error())
	}
	v, _ := exportTrampolines.LoadOrStore(ft.key(), mod)
	return v.(js.Value)
}

// importTrampoline compiles, once, the trampoline of m's imports, one
// function for each module and name.
func (m *jsModule) importTrampoline() js.Value {
	m.once.Do(func() {
		seen := map[string]bool{}
		var fts []funcType
		for i, imp := range m.info.imports {
			if key := imp.module + "\x00" + imp.name; !seen[key] {
				seen[key] = true
				m.firsts = append(m.firsts, i)
				fts = append(fts, imp.typ)
			}
		}
		if len(fts) == 0 {
			return
		}
		mod, err := hostModule(trampolines(fts, true))
		if err != nil {
			panic("std::wasm: an import trampoline does not compile: " + err.Error())
		}
		m.trampoline = mod
	})
	return m.trampoline
}

// fromJS reads the i32s args holds from at into stack, by ts.
func fromJS(ts []valType, args func(int) js.Value, stack []uint64) {
	at := 0
	for j, t := range ts {
		lo := uint64(uint32(args(at).Int()))
		at++
		if t == tI64 || t == tF64 {
			lo |= uint64(uint32(args(at).Int())) << 32
			at++
		}
		stack[j] = lo
	}
}

// toJS is stack's values of types ts as the i32s they cross as.
func toJS(ts []valType, stack []uint64) []any {
	out := make([]any, 0, lowered(ts))
	for j, t := range ts {
		out = append(out, int(int32(uint32(stack[j]))))
		if t == tI64 || t == tF64 {
			out = append(out, int(int32(uint32(stack[j]>>32))))
		}
	}
	return out
}

// jsResult is values as a JavaScript function returns them: nothing, one,
// or an array.
func jsResult(values []any) any {
	switch len(values) {
	case 0:
		return nil
	case 1:
		return values[0]
	}
	return values
}

type jsInstance struct {
	exports js.Value
	mem     *jsMemory
	funcs   []js.Func
}

func (m *jsModule) instantiate(limit uint64, host hostFunc) (engineInstance, error) {
	mod, err := m.module(limit)
	if err != nil {
		return nil, err
	}
	inst := &jsInstance{}
	imports := object.New()
	if tramp := m.importTrampoline(); tramp.Truthy() {
		var failed js.Value
		goFns := object.New()
		for k, i := range m.firsts {
			ft := m.info.imports[i].typ
			i := i
			f := js.FuncOf(func(_ js.Value, args []js.Value) any {
				stack := make([]uint64, max(len(ft.params), len(ft.results)))
				fromJS(ft.params, func(a int) js.Value { return args[a] }, stack)
				var mem engineMemory
				if inst.mem != nil {
					inst.mem.stale = true
					mem = inst.mem
				}
				if !host(i, mem, stack) {
					failed.Set("value", 1)
					clear(stack)
				}
				return jsResult(toJS(ft.results, stack))
			})
			inst.funcs = append(inst.funcs, f)
			goFns.Set(strconv.Itoa(k), f)
		}
		trampInst, err := hostInstance(tramp, js.ValueOf(map[string]any{"go": goFns}))
		if err != nil {
			inst.close()
			return nil, err
		}
		served := trampInst.Get("exports")
		failed = served.Get("failed")
		for k, i := range m.firsts {
			imp := m.info.imports[i]
			ns := imports.Get(imp.module)
			if ns.IsUndefined() {
				ns = object.New()
				imports.Set(imp.module, ns)
			}
			ns.Set(imp.name, served.Get(strconv.Itoa(k)))
		}
	}
	live, err := hostInstance(mod, imports)
	if err != nil {
		inst.close()
		return nil, err
	}
	inst.exports = live.Get("exports")
	if len(m.info.memories) > 0 {
		name := m.info.memoryExport
		if name == "" {
			name = memoryExport
		}
		inst.mem = &jsMemory{mem: inst.exports.Get(name), stale: true}
	}
	return inst, nil
}

// jsFunc is an export as the engine calls it: the module's function, or
// the trampoline in front of it.
type jsFunc struct {
	fn  js.Value
	typ funcType
}

func (inst *jsInstance) function(name string, typ funcType) engineFunc {
	fn := inst.exports.Get(name)
	if !allI32(typ) {
		tramp, err := hostInstance(exportTrampoline(typ), js.ValueOf(map[string]any{"go": map[string]any{"0": fn}}))
		if err != nil {
			panic("std::wasm: an export trampoline does not instantiate: " + err.Error())
		}
		fn = tramp.Get("exports").Get("0")
	}
	return &jsFunc{fn: fn, typ: typ}
}

// allI32 reports whether every value of ft is an i32, which crosses
// JavaScript as itself.
func allI32(ft funcType) bool {
	for _, t := range append(append([]valType(nil), ft.params...), ft.results...) {
		if t != tI32 {
			return false
		}
	}
	return true
}

func (inst *jsInstance) call(fn engineFunc, stack []uint64) (err error) {
	f := fn.(*jsFunc)
	defer func() {
		if inst.mem != nil {
			inst.mem.stale = true
		}
		if r := recover(); r != nil {
			err = jsError(r)
		}
	}()
	res := f.fn.Invoke(toJS(f.typ.params, stack)...)
	if lowered(f.typ.results) > 1 {
		fromJS(f.typ.results, res.Index, stack)
	} else {
		fromJS(f.typ.results, func(int) js.Value { return res }, stack)
	}
	return nil
}

func (inst *jsInstance) memory() engineMemory {
	if inst.mem == nil {
		return nil
	}
	return inst.mem
}

func (inst *jsInstance) refused() bool { return false }

func (inst *jsInstance) close() {
	for _, f := range inst.funcs {
		f.Release()
	}
	inst.funcs = nil
	inst.exports, inst.mem = js.Value{}, nil
}

// jsMemory is an instance's memory through a Uint8Array over its buffer,
// made again after anything that may have grown it: a call into the
// instance, and the module calling an import.
type jsMemory struct {
	mem   js.Value
	view  js.Value
	size  uint32
	stale bool
}

func (m *jsMemory) current() js.Value {
	if m.stale {
		m.view = uint8Array.New(m.mem.Get("buffer"))
		m.size = uint32(m.view.Length())
		m.stale = false
	}
	return m.view
}

func (m *jsMemory) Size() uint32 {
	m.current()
	return m.size
}

func (m *jsMemory) Read(offset, count uint32) ([]byte, bool) {
	out := make([]byte, count)
	js.CopyBytesToGo(out, m.current().Call("subarray", offset, offset+count))
	return out, true
}

func (m *jsMemory) Write(offset uint32, data []byte) bool {
	js.CopyBytesToJS(m.current().Call("subarray", offset, offset+uint32(len(data))), data)
	return true
}

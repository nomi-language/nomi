package stdwasm

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// The kinds a value crosses with, beside its bits: std/wasm's ValueType,
// in declaration order.
const (
	kindI32 int64 = iota
	kindI64
	kindF32
	kindF64
)

// kindOf is t's kind, or false for a type std/wasm has no value for
// (v128, funcref, externref).
func kindOf(t valType) (int64, bool) {
	switch t {
	case tI32:
		return kindI32, true
	case tI64:
		return kindI64, true
	case tF32:
		return kindF32, true
	case tF64:
		return kindF64, true
	}
	return 0, false
}

// kindNames is each kind as std/wasm's ValueType writes it.
var kindNames = [...]string{"I32", "I64", "F32", "F64"}

// toStack is a value's bits as the wasm stack holds them: an i32 or an f32
// in the low 32 bits.
func toStack(kind, bits int64) uint64 {
	if kind == kindI32 || kind == kindF32 {
		return uint64(uint32(bits))
	}
	return uint64(bits)
}

// fromStack is the bits of a value of kind the wasm stack holds: an i32
// sign-extended, an f32 as its 32-bit pattern. A NaN keeps its bits here;
// std/wasm reads every NaN as Float.nan().
func fromStack(kind int64, raw uint64) int64 {
	switch kind {
	case kindI32:
		return int64(int32(uint32(raw)))
	case kindF32:
		return int64(uint32(raw))
	}
	return int64(raw)
}

// pageSize is a wasm page: memory grows and is limited in whole pages.
const pageSize = 65536

// --- Failures ------------------------------------------------------------

// A failure crosses as a list of strings, its kind first, which std/wasm
// reads into its Error: `trapped` or `dead` and the trap's own list,
// `dropped`, `expired`, `no_export` and the name, `missing_import`, the
// module and the name, `mismatch` and a message, and `out_of_range` and
// the memory's size. A trap's list is `unreachable`, `out_of_bounds`,
// `memory_limit`, `import_failed`, the import and its message, or
// `other` and the trap's text as wazero writes it.

// trap is how a call into an instance failed while it ran.
type trap struct{ kind, name, message string }

func (t trap) fields() []string {
	switch t.kind {
	case "import_failed":
		return []string{t.kind, t.name, t.message}
	case "other":
		return []string{t.kind, t.message}
	}
	return []string{t.kind}
}

func failure(fields ...string) *rt.List[string] {
	return rt.ListFromGo(fields, func(s string) string { return s })
}

func trapped(t trap) *rt.List[string] { return failure(append([]string{"trapped"}, t.fields()...)...) }

func mismatch(format string, args ...any) *rt.List[string] {
	return failure("mismatch", fmt.Sprintf(format, args...))
}

// engineTraps is the trap each engine's text for one is: wazero's, then the
// host engines' (V8 in Node, Chrome and Edge; SpiderMonkey in Firefox;
// JavaScriptCore in Safari). An `other` trap carries wazero's text for it,
// so a trap reads the same on every engine. Any text not here is `other`
// with the text. TestCall_TrapsLeaveTheInstanceDead provokes each kind on
// the engine of its build.
var engineTraps = map[string]trap{
	// wazero
	"unreachable":                 {kind: "unreachable"},
	"out of bounds memory access": {kind: "out_of_bounds"},
	"invalid table access":        {kind: "out_of_bounds"},
	// V8
	"memory access out of bounds":                  {kind: "out_of_bounds"},
	"table index is out of bounds":                 {kind: "out_of_bounds"},
	"divide by zero":                               otherTrap("integer divide by zero"),
	"remainder by zero":                            otherTrap("integer divide by zero"),
	"divide result unrepresentable":                otherTrap("integer overflow"),
	"float unrepresentable in integer range":       otherTrap("invalid conversion to integer"),
	"null function or function signature mismatch": otherTrap("indirect call type mismatch"),
	"Maximum call stack size exceeded":             otherTrap("stack overflow"),
	// SpiderMonkey
	"unreachable executed":             {kind: "unreachable"},
	"index out of bounds":              {kind: "out_of_bounds"},
	"too much recursion":               otherTrap("stack overflow"),
	"indirect call to null":            otherTrap("indirect call type mismatch"),
	"indirect call signature mismatch": otherTrap("indirect call type mismatch"),
	// JavaScriptCore
	"Unreachable code should not be executed":          {kind: "unreachable"},
	"Out of bounds memory access":                      {kind: "out_of_bounds"},
	"Out of bounds call_indirect":                      {kind: "out_of_bounds"},
	"Division by zero":                                 otherTrap("integer divide by zero"),
	"Integer overflow":                                 otherTrap("integer overflow"),
	"Out of bounds Trunc operation":                    otherTrap("invalid conversion to integer"),
	"call_indirect to a null table entry":              otherTrap("indirect call type mismatch"),
	"call_indirect to a signature that does not match": otherTrap("indirect call type mismatch"),
	"Maximum call stack size exceeded.":                otherTrap("stack overflow"),
}

func otherTrap(message string) trap { return trap{kind: "other", message: message} }

// trapOf is the trap err, from a call into an engine, is.
func trapOf(err error) trap {
	inner := err
	for {
		next := errors.Unwrap(inner)
		if next == nil {
			break
		}
		inner = next
	}
	if t, ok := engineTraps[inner.Error()]; ok {
		return t
	}
	return otherTrap(inner.Error())
}

// --- Modules -------------------------------------------------------------

// module is a compiled module: the engine's, and what std::wasm read from
// its bytes.
type module struct {
	eng  engineModule
	info *moduleInfo
	// link is why the module's imports cannot be served, found once, or
	// nil.
	link     sync.Once
	linkFail *rt.List[string]
}

// modules is every module this process compiled, by the SHA-256 of its
// bytes, so the same bytes compile once.
var modules sync.Map

// compiling is one compile of some bytes, which callers that ask for them
// together wait on.
type compiling struct {
	once sync.Once
	m    *module
	err  string
}

// Compile is `wasm.compile`: the module in data, compiled, or why it is not
// one. The same bytes compile once a process.
func Compile(data rt.Bytes) rt.Result[rt.WasmModule, string] {
	sum := sha256.Sum256([]byte(data))
	v, _ := modules.LoadOrStore(sum, &compiling{})
	c := v.(*compiling)
	c.once.Do(func() { c.m, c.err = compile([]byte(data), sum) })
	if c.m == nil {
		return rt.Err[rt.WasmModule](c.err)
	}
	return rt.Ok[rt.WasmModule, string](rt.NewWasmModule(c.m))
}

// compile has the engine check and compile data, and reads its imports,
// exports and memory.
func compile(data []byte, sum [sha256.Size]byte) (*module, string) {
	info, readErr := parseModule(data)
	eng, fail := compileEngine(data, sum, info)
	if fail != "" {
		return nil, fail
	}
	if readErr != nil {
		eng.close()
		return nil, "std::wasm cannot run this module: " + readErr.Error()
	}
	return &module{eng: eng, info: info}, ""
}

func moduleOf(h rt.WasmModule) *module {
	m, ok := h.Module().(*module)
	if !ok {
		rt.Trap("wasm: the value is not a compiled module")
	}
	return m
}

// linkImports answers why m's imports cannot be served, or nil: an import
// of anything but a function, a function imported twice with two
// signatures, or one of a type std::wasm has no value for.
func (m *module) linkImports() *rt.List[string] {
	m.link.Do(func() {
		if o := m.info.other; o != nil {
			m.linkFail = failure("missing_import", o[0], o[1])
			return
		}
		seen := map[string]funcType{}
		for _, imp := range m.info.imports {
			key := imp.module + "\x00" + imp.name
			if prev, ok := seen[key]; ok {
				if prev.key() != imp.typ.key() {
					m.linkFail = mismatch("the module imports %s.%s twice, with two signatures", imp.module, imp.name)
					return
				}
				continue
			}
			seen[key] = imp.typ
			for _, t := range append(append([]valType(nil), imp.typ.params...), imp.typ.results...) {
				if _, ok := kindOf(t); !ok {
					m.linkFail = mismatch("the import %s.%s has a %s, which std::wasm has no value for", imp.module, imp.name, typeName(t))
					return
				}
			}
		}
	})
	return m.linkFail
}

// --- Instances -----------------------------------------------------------

// Import is the Nomi function that answers one import: the calling
// instance's memory and the arguments, two Int64s each (kind, bits), and
// the results the same way, or a message the call traps with.
type Import = func(fr *rt.Frame, mem rt.WasmMemory, args *rt.List[int64]) rt.Result[*rt.List[int64], string]

// instance is one instance of a module. mu serializes the operations on it,
// a call holding it for the call's length, so an import must not call its
// own instance. A trap leaves it dead, and drop dropped; either closes the
// engine's instance.
type instance struct {
	mu      sync.Mutex
	module  *module
	eng     engineInstance // nil once dead or dropped
	dead    *trap
	dropped bool
	// imports answers the module's function imports, index for index, and
	// stopping holds, for an import that declares them, the values it
	// answers without running while the calling task is stopping (nil for
	// one that runs then too).
	imports  []Import
	stopping [][]uint64
	// exports is each export a call named, as the engine answered it once.
	exports map[string]*export
	// limit is the instance's memory limit in bytes.
	limit uint64
	// fr is the frame of the call running, which its imports run on, and
	// unwind a Nomi failure an import's function unwound with, which the
	// call goes on unwinding once the engine has stopped. failed is an
	// import's Err, the trap the call answers.
	fr     *rt.Frame
	unwind any
	failed *trap
}

// serve answers import i over stack, with mem the calling instance's
// memory (nil while the start function runs on an engine that has none to
// give yet): it calls the Nomi function the instance was given for it, and
// answers false when that failed, which stops the wasm. A call into an
// instance is one call into the host, which a stop does not interrupt, so
// while the calling task is stopping the function runs to its end, as a
// deferred call does (rt.Unstoppable), and the task stops once the
// instance's call returns; an import that declares an answer for that time
// answers it instead, without running.
func (inst *instance) serve(i int, mem engineMemory, stack []uint64) bool {
	imp := inst.module.info.imports[i]
	full := imp.module + "." + imp.name
	fr := inst.fr
	if fr.Ended() {
		if answer := inst.stopping[i]; answer != nil {
			copy(stack, answer)
			return true
		}
		fr = rt.Unstoppable(fr)
	}
	var args *rt.List[int64]
	for j := len(imp.typ.params) - 1; j >= 0; j-- {
		k, _ := kindOf(imp.typ.params[j])
		args = rt.Cons(k, rt.Cons(fromStack(k, stack[j]), args))
	}
	view := &memory{mem: mem, live: true}
	var res rt.Result[*rt.List[int64], string]
	func() {
		defer func() {
			view.mem, view.live = nil, false
			if r := recover(); r != nil {
				inst.unwind = r
			}
		}()
		res = inst.imports[i](fr, rt.NewWasmMemory(view), args)
	}()
	if inst.unwind != nil {
		// The trap a later operation reports the instance died of.
		inst.failed = &trap{kind: "import_failed", name: full, message: "its function trapped"}
		return false
	}
	if res.Tag != rt.TagOk {
		inst.failed = &trap{kind: "import_failed", name: full, message: res.Err}
		return false
	}
	var got []int64
	for c := res.Ok; c != nil; c = c.Tail {
		got = append(got, c.Head)
	}
	if len(got) != 2*len(imp.typ.results) {
		inst.failed = &trap{kind: "import_failed", name: full, message: fmt.Sprintf("it returned %d values, and the module takes %d", len(got)/2, len(imp.typ.results))}
		return false
	}
	for j, t := range imp.typ.results {
		k, _ := kindOf(t)
		if got[2*j] != k {
			inst.failed = &trap{kind: "import_failed", name: full, message: fmt.Sprintf("result %d is a %s, and the module takes a %s", j+1, kindNames[got[2*j]&3], kindNames[k])}
			return false
		}
		stack[j] = toStack(k, got[2*j+1])
	}
	return true
}

// Instantiate is `wasm.instantiate`: an instance of m whose function
// imports are answered by fns, each by the import that names its module
// and name and declares its params and results by kind, with memory
// limited to limit bytes, in whole pages. stops holds, for each import,
// what it answers while the calling task is stopping: nothing for one that
// runs then too, else 1 and the values, two Int64s each. An import the
// module does not name is unused. The module's `_initialize`, if it
// exports one, runs first, on fr, as a reactor's toolchain expects;
// `_start` does not.
func Instantiate(fr *rt.Frame, h rt.WasmModule, modules, names *rt.List[string], params, results, stops *rt.List[*rt.List[int64]], fns *rt.List[Import], limit rt.Int) rt.Result[rt.WasmInstance, *rt.List[string]] {
	m := moduleOf(h)
	type given struct {
		params, results, stop []int64
		fn                    Import
	}
	byName := map[string]given{}
	ns, ps, rs, ss, fs := names, params, results, stops, fns
	for c := modules; c != nil && ns != nil && ps != nil && rs != nil && ss != nil && fs != nil; c, ns, ps, rs, ss, fs = c.Tail, ns.Tail, ps.Tail, rs.Tail, ss.Tail, fs.Tail {
		byName[c.Head+"\x00"+ns.Head] = given{goInts(ps.Head), goInts(rs.Head), goInts(ss.Head), fs.Head}
	}
	imports := m.info.imports
	inst := &instance{module: m, imports: make([]Import, len(imports)), stopping: make([][]uint64, len(imports)), exports: map[string]*export{}}
	for i, imp := range imports {
		mod, name := imp.module, imp.name
		g, ok := byName[mod+"\x00"+name]
		if !ok {
			return rt.Err[rt.WasmInstance](failure("missing_import", mod, name))
		}
		if want, got := kindsOf(imp.typ.params), g.params; !sameKinds(want, got) {
			return rt.Err[rt.WasmInstance](mismatch("the module's import %s.%s takes %s, and the Nomi function declares %s", mod, name, kindList(want), kindList(got)))
		}
		if want, got := kindsOf(imp.typ.results), g.results; !sameKinds(want, got) {
			return rt.Err[rt.WasmInstance](mismatch("the module's import %s.%s returns %s, and the Nomi function declares %s", mod, name, kindList(want), kindList(got)))
		}
		inst.imports[i] = g.fn
		if len(g.stop) > 0 {
			var kinds []int64
			answer := make([]uint64, 0, len(g.stop)/2)
			for j := 1; j+1 < len(g.stop); j += 2 {
				kinds = append(kinds, g.stop[j])
				answer = append(answer, toStack(g.stop[j], g.stop[j+1]))
			}
			if want := kindsOf(imp.typ.results); !sameKinds(want, kinds) {
				return rt.Err[rt.WasmInstance](mismatch("the module's import %s.%s returns %s, and the Nomi function's answer while its task stops is %s", mod, name, kindList(want), kindList(kinds)))
			}
			inst.stopping[i] = answer
		}
	}
	inst.limit = pagesOf(limit)
	if fail := inst.instantiate(fr); fail != nil {
		return rt.Err[rt.WasmInstance](fail)
	}
	return rt.Ok[rt.WasmInstance, *rt.List[string]](rt.NewWasmInstance(inst))
}

// instantiate makes the engine's instance of inst and runs its
// `_initialize`, or answers why the module cannot be served or its initial
// memory is over the limit.
func (inst *instance) instantiate(fr *rt.Frame) *rt.List[string] {
	if fail := inst.module.linkImports(); fail != nil {
		return fail
	}
	if mem, ok := inst.module.info.memory(); ok && mem.min > inst.limit/pageSize {
		return trapped(trap{kind: "memory_limit"})
	}
	eng, err := inst.module.eng.instantiate(inst.limit, inst.serve)
	if err != nil {
		return mismatch("the module cannot be instantiated: %v", err)
	}
	inst.eng = eng
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if init := inst.exported("_initialize"); init != nil && init.typed {
		if _, fail := inst.run(fr, init, nil); fail != nil {
			return fail
		}
	}
	return nil
}

func instanceOf(h rt.WasmInstance) *instance {
	inst, ok := h.Instance().(*instance)
	if !ok {
		rt.Trap("wasm: the value is not an instance")
	}
	return inst
}

// usable is why inst takes no operation, or nil. inst.mu is held.
func (inst *instance) usable() *rt.List[string] {
	switch {
	case inst.dropped:
		return failure("dropped")
	case inst.dead != nil:
		return failure(append([]string{"dead"}, inst.dead.fields()...)...)
	}
	return nil
}

// run calls e with stack, on fr, and answers the stack after, or the
// failure, leaving inst dead on a trap. An import's Nomi failure goes on
// unwinding once inst is dead. inst.mu is held.
func (inst *instance) run(fr *rt.Frame, e *export, stack []uint64) ([]uint64, *rt.List[string]) {
	inst.fr, inst.unwind, inst.failed = fr, nil, nil
	defer func() { inst.fr = nil }()
	if n := max(len(e.params), len(e.results)); len(stack) < n {
		stack = append(stack, make([]uint64, n-len(stack))...)
	}
	err := inst.eng.call(e.fn, stack)
	if err == nil {
		return stack, nil
	}
	t := trapOf(err)
	switch {
	case inst.failed != nil:
		t = *inst.failed
	case inst.eng.refused():
		t = trap{kind: "memory_limit"}
	}
	inst.dead = &t
	inst.eng.close()
	inst.eng = nil
	if r := inst.unwind; r != nil {
		inst.unwind = nil
		panic(r)
	}
	return nil, trapped(t)
}

// export is a function an instance exports, as calls reach it: the
// engine's function, looked up once an instance, its parameters' and
// results' kinds, and whether std::wasm has a value for every one of their
// types.
type export struct {
	fn              engineFunc
	params, results []int64
	typed           bool
}

// exported is inst's export name, or nil when it exports no function by
// that name. inst.mu is held.
func (inst *instance) exported(name string) *export {
	if e, ok := inst.exports[name]; ok {
		return e
	}
	typ, ok := inst.module.info.exports[name]
	if !ok {
		return nil
	}
	e := &export{params: kindsOf(typ.params), results: kindsOf(typ.results)}
	e.typed = len(e.params) == len(typ.params) && len(e.results) == len(typ.results)
	if e.typed {
		e.fn = inst.eng.function(name, typ)
	}
	inst.exports[name] = e
	return e
}

// Call is `Instance.call`: the export of inst called with args, two Int64s
// each (kind, bits), answering its results the same way.
func Call(fr *rt.Frame, h rt.WasmInstance, name string, args *rt.List[int64]) rt.Result[*rt.List[int64], *rt.List[string]] {
	inst := instanceOf(h)
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if fail := inst.usable(); fail != nil {
		return rt.Err[*rt.List[int64]](fail)
	}
	e := inst.exported(name)
	if e == nil {
		return rt.Err[*rt.List[int64]](failure("no_export", name))
	}
	if !e.typed {
		return rt.Err[*rt.List[int64]](mismatch("%s takes or returns a type std::wasm has no value for", name))
	}
	stack := make([]uint64, max(len(e.params), len(e.results)))
	n, fits := 0, true
	for c := args; c != nil && c.Tail != nil; c = c.Tail.Tail {
		if n >= len(e.params) || c.Head != e.params[n] {
			fits = false
		} else {
			stack[n] = toStack(c.Head, c.Tail.Head)
		}
		n++
	}
	if !fits || n != len(e.params) {
		var got []int64
		for c := args; c != nil && c.Tail != nil; c = c.Tail.Tail {
			got = append(got, c.Head)
		}
		return rt.Err[*rt.List[int64]](mismatch("%s takes %s, and the call passes %s", name, kindList(e.params), kindList(got)))
	}
	out, fail := inst.run(fr, e, stack)
	if fail != nil {
		return rt.Err[*rt.List[int64]](fail)
	}
	var results *rt.List[int64]
	for j := len(e.results) - 1; j >= 0; j-- {
		k := e.results[j]
		results = rt.Cons(k, rt.Cons(fromStack(k, out[j]), results))
	}
	return rt.Ok[*rt.List[int64], *rt.List[string]](results)
}

// Drop is `Instance.drop`: inst's memory freed, and every later operation
// on it Err(Dropped). A second drop does nothing.
func Drop(h rt.WasmInstance) rt.Unit {
	inst := instanceOf(h)
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.eng != nil {
		inst.eng.close()
		inst.eng = nil
	}
	inst.dropped = true
	return rt.Unit{}
}

// --- Memory --------------------------------------------------------------

// memory is the memory an import's function is handed: the calling
// instance's, until the import returns (live). mem is nil for an instance
// with no memory, or while a start function runs on an engine that has
// none to give yet.
type memory struct {
	mem  engineMemory
	live bool
}

func memoryOf(h rt.WasmMemory) *memory {
	m, ok := h.Memory().(*memory)
	if !ok {
		rt.Trap("wasm: the value is not an instance's memory")
	}
	return m
}

// span is at and length as a range of mem, or the failure.
func span(mem engineMemory, at, length rt.Int) (uint32, uint32, *rt.List[string]) {
	size := uint64(size(mem))
	a, okA := at.Int64()
	n, okN := length.Int64()
	if !okA || !okN || a < 0 || n < 0 || uint64(a)+uint64(n) > size {
		return 0, 0, failure("out_of_range", strconv.FormatUint(size, 10))
	}
	return uint32(a), uint32(n), nil
}

func read(mem engineMemory, at, length rt.Int) rt.Result[rt.Bytes, *rt.List[string]] {
	a, n, fail := span(mem, at, length)
	if fail != nil {
		return rt.Err[rt.Bytes](fail)
	}
	if n == 0 {
		return rt.Ok[rt.Bytes, *rt.List[string]]("")
	}
	view, _ := mem.Read(a, n)
	return rt.Ok[rt.Bytes, *rt.List[string]](rt.Bytes(view))
}

func write(mem engineMemory, at rt.Int, data rt.Bytes) rt.Result[rt.Unit, *rt.List[string]] {
	a, _, fail := span(mem, at, rt.SmallInt(int64(len(data))))
	if fail != nil {
		return rt.Err[rt.Unit](fail)
	}
	if len(data) > 0 {
		mem.Write(a, []byte(data))
	}
	return rt.Ok[rt.Unit, *rt.List[string]](rt.Unit{})
}

func size(mem engineMemory) int64 {
	if mem == nil {
		return 0
	}
	return int64(mem.Size())
}

// withMemory runs f over inst's memory with inst.mu held, or answers why
// inst takes no operation.
func withMemory[T any](h rt.WasmInstance, f func(engineMemory) rt.Result[T, *rt.List[string]]) rt.Result[T, *rt.List[string]] {
	inst := instanceOf(h)
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if fail := inst.usable(); fail != nil {
		return rt.Err[T](fail)
	}
	return f(inst.eng.memory())
}

// Read is `Instance.read`: length bytes of inst's memory from at.
func Read(h rt.WasmInstance, at, length rt.Int) rt.Result[rt.Bytes, *rt.List[string]] {
	return withMemory(h, func(mem engineMemory) rt.Result[rt.Bytes, *rt.List[string]] { return read(mem, at, length) })
}

// Write is `Instance.write`: data into inst's memory from at.
func Write(h rt.WasmInstance, at rt.Int, data rt.Bytes) rt.Result[rt.Unit, *rt.List[string]] {
	return withMemory(h, func(mem engineMemory) rt.Result[rt.Unit, *rt.List[string]] { return write(mem, at, data) })
}

// Size is `Instance.size`: the size of inst's memory in bytes.
func Size(h rt.WasmInstance) rt.Result[int64, *rt.List[string]] {
	return withMemory(h, func(mem engineMemory) rt.Result[int64, *rt.List[string]] {
		return rt.Ok[int64, *rt.List[string]](size(mem))
	})
}

// MemoryRead is `Memory.read`, as Read is for an instance. After the
// import it was handed to returns, it is Err(Expired).
func MemoryRead(h rt.WasmMemory, at, length rt.Int) rt.Result[rt.Bytes, *rt.List[string]] {
	m := memoryOf(h)
	if !m.live {
		return rt.Err[rt.Bytes](failure("expired"))
	}
	return read(m.mem, at, length)
}

// MemoryWrite is `Memory.write`, as Write is for an instance.
func MemoryWrite(h rt.WasmMemory, at rt.Int, data rt.Bytes) rt.Result[rt.Unit, *rt.List[string]] {
	m := memoryOf(h)
	if !m.live {
		return rt.Err[rt.Unit](failure("expired"))
	}
	return write(m.mem, at, data)
}

// MemorySize is `Memory.size`, as Size is for an instance.
func MemorySize(h rt.WasmMemory) rt.Result[int64, *rt.List[string]] {
	m := memoryOf(h)
	if !m.live {
		return rt.Err[int64](failure("expired"))
	}
	return rt.Ok[int64, *rt.List[string]](size(m.mem))
}

// --- run ------------------------------------------------------------------

// DefaultLimit is the memory limit of an instance whose program names
// none: 256 MiB.
const DefaultLimit = 256 << 20

// pagesOf is a memory limit of limit bytes, in whole pages: at most the
// 65536 pages a 32-bit memory addresses, and that for a limit past Int64.
func pagesOf(limit rt.Int) uint64 {
	bytes, ok := limit.Int64()
	if !ok || bytes < 0 {
		bytes = math.MaxInt64
	}
	return uint64(min(bytes/pageSize, 65536)) * pageSize
}

// Run is `wasm.run`: the export name called once, by convention, in a
// fresh instance of m with no imports and its memory limited to limit
// bytes. The instance is dropped after.
func Run(h rt.WasmModule, name string, input rt.Bytes, limit rt.Int) rt.Result[rt.Bytes, *rt.List[string]] {
	m := moduleOf(h)
	if len(m.info.imports) > 0 {
		imp := m.info.imports[0]
		return rt.Err[rt.Bytes](failure("missing_import", imp.module, imp.name))
	}
	inst := &instance{module: m, limit: pagesOf(limit), exports: map[string]*export{}}
	if fail := inst.instantiate(nil); fail != nil {
		return rt.Err[rt.Bytes](fail)
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	defer func() {
		if inst.eng != nil {
			inst.eng.close()
		}
	}()
	return inst.convention(nil, name, input)
}

// RunOn is `Instance.run`: inst's export name called once, by convention,
// on fr.
func RunOn(fr *rt.Frame, h rt.WasmInstance, name string, input rt.Bytes) rt.Result[rt.Bytes, *rt.List[string]] {
	inst := instanceOf(h)
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if fail := inst.usable(); fail != nil {
		return rt.Err[rt.Bytes](fail)
	}
	return inst.convention(fr, name, input)
}

// convention calls inst's export name by the convention std/wasm's `run`
// documents: alloc(len(input)) answers where input is written, name(at,
// length) answers (at << 32 | length) of its output, and the output is
// copied out. inst.mu is held and inst is usable.
func (inst *instance) convention(fr *rt.Frame, name string, input rt.Bytes) rt.Result[rt.Bytes, *rt.List[string]] {
	alloc, fn := inst.exported("alloc"), inst.exported(name)
	switch {
	case fn == nil:
		return rt.Err[rt.Bytes](failure("no_export", name))
	case alloc == nil:
		return rt.Err[rt.Bytes](failure("no_export", "alloc"))
	case !alloc.is([]int64{kindI32}, []int64{kindI32}):
		return rt.Err[rt.Bytes](mismatch("alloc must take an I32 and return an I32"))
	case !fn.is([]int64{kindI32, kindI32}, []int64{kindI64}):
		return rt.Err[rt.Bytes](mismatch("%s must take two I32s and return an I64", name))
	case uint64(len(input)) > math.MaxUint32:
		return rt.Err[rt.Bytes](mismatch("the input is %d bytes, past the 4 GiB a module addresses", len(input)))
	}
	out, fail := inst.run(fr, alloc, []uint64{uint64(len(input))})
	if fail != nil {
		return rt.Err[rt.Bytes](fail)
	}
	ptr := rt.SmallInt(int64(uint32(out[0])))
	if w := write(inst.eng.memory(), ptr, input); w.Tag != rt.TagOk {
		return rt.Err[rt.Bytes](mismatch("alloc returned %d, and %d bytes from there are outside the module's memory", uint32(out[0]), len(input)))
	}
	if out, fail = inst.run(fr, fn, []uint64{out[0] & math.MaxUint32, uint64(len(input))}); fail != nil {
		return rt.Err[rt.Bytes](fail)
	}
	at, n := out[0]>>32, out[0]&math.MaxUint32
	got := read(inst.eng.memory(), rt.SmallInt(int64(at)), rt.SmallInt(int64(n)))
	if got.Tag != rt.TagOk {
		return rt.Err[rt.Bytes](mismatch("%s returned %d bytes at %d, outside the module's memory", name, n, at))
	}
	return got
}

// is reports whether e takes params and returns results, by kind.
func (e *export) is(params, results []int64) bool {
	return e.typed && sameKinds(e.params, params) && sameKinds(e.results, results)
}

// --- helpers --------------------------------------------------------------

func goInts(xs *rt.List[int64]) []int64 {
	var out []int64
	for c := xs; c != nil; c = c.Tail {
		out = append(out, c.Head)
	}
	return out
}

func kindsOf(ts []valType) []int64 {
	out := make([]int64, 0, len(ts))
	for _, t := range ts {
		if k, ok := kindOf(t); ok {
			out = append(out, k)
		}
	}
	return out
}

func sameKinds(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// kindList writes kinds as std/wasm's ValueType writes them, in brackets.
func kindList(kinds []int64) string {
	out := "["
	for i, k := range kinds {
		if i > 0 {
			out += ", "
		}
		out += kindNames[k&3]
	}
	return out + "]"
}

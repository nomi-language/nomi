package vm

// HOST CALLS. A crossing into Go (`ir.Call.Crosses()`) lands on one of two
// things, looked up by the callee's printed name, which is the name the
// producer gave the crossing.
//
//   - A generated ADAPTER (`hostadapt.Func`): a Go function that calls one Go
//     host function with rt values and no reflection. Every stdlib `host fn`
//     has one, generated from internal/stdlibbindings into
//     internal/stdlibadapters, and a machine binds that table by itself. A
//     host adds its own tables with WithHosts: vmhost adds std/compiler's
//     five hosts, whose answers depend on the program being run, and an FFI
//     wrapper adds the adapters it generated for the project's bindings.
//   - An INTRINSIC (`hostFn`, the `hosts` map in vm.go): an operation over
//     this machine's own state rather than a Go function — spawning a task
//     runs a VM closure on a goroutine, `io.print` writes to the machine's
//     serialized writer, `Map.get` hashes with the machine's key kernels,
//     `Result.map_err` calls a VM closure. None of these is a Go function a
//     table could name.
//
// An intrinsic wins a name both answer; none does today.
//
// The adapters are BOUND lazily, once per machine, on the first crossing a
// compile or a call looks up. Binding resolves each descriptor an adapter
// builds with through this machine's descriptor interning (so a `Date` an
// adapter returns carries the descriptor the machine's own `Date`
// constructions do), and gives the adapters an Invoker that calls a VM
// function value, so a Go host that calls back into Nomi runs the callback on
// this machine.

import (
	"errors"
	"fmt"
	"sync"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
	"github.com/nomi-language/nomi/rt"
)

// HostTable binds one table of generated adapters against a machine's Env and
// answers them by crossing name. A generated adapter file's `Bind` is one. An
// alias, so a generated FFI wrapper, a separate module that cannot name this
// package, passes one by its type.
type HostTable = func(env *hostadapt.Env) (map[string]hostadapt.Func, error)

// hostTables is the adapter tables one machine binds, and their binding.
// Every view of the machine shares it.
type hostTables struct {
	// root is the machine the tables were opened on. A callback runs on it,
	// at depth zero: a Go host function's stack is Go's, and the call depth
	// this machine counts is its own activations'.
	root   *Machine
	tables []HostTable

	once  sync.Once
	funcs map[string]hostadapt.Func
	err   error

	// lookup answers a name no bound table answers, at each lookup rather
	// than once at binding. See WithHostLookup.
	lookup func(name string) hostadapt.Func
}

// WithHostLookup answers crossing names no table answers, asked at each
// lookup, so the set it answers may grow after the machine has bound its
// tables. A REPL session is the one user: each input declares new host
// functions (its session values' reads and writes) on one live machine.
// A bound table wins a name both answer.
func (m *Machine) WithHostLookup(lookup func(name string) hostadapt.Func) *Machine {
	m.adapters.lookup = lookup
	return m
}

// Invoke calls the VM function value fn from a Go host function running on
// frame fr, as a generated adapter's callback does (hostadapt.Env.Invoke).
// A host function WithHostLookup answers uses it to call back into Nomi.
func (m *Machine) Invoke(fr *rt.Frame, fn rt.Value, args []rt.Value) (rt.Value, error) {
	return m.adapters.invoke(fr, fn, args)
}

func newHostTables(root *Machine) *hostTables {
	return &hostTables{root: root, tables: []HostTable{stdlibadapters.Bind}}
}

// WithHosts adds tables of generated adapters to the machine's own stdlib
// table. A name two tables answer is an error at binding. Call it before the
// machine runs anything.
func (m *Machine) WithHosts(tables ...HostTable) *Machine {
	m.adapters.tables = append(m.adapters.tables, tables...)
	return m
}

// adapter is the bound adapter for a crossing name, or nil when no table
// answers it.
func (m *Machine) adapter(name string) (hostadapt.Func, error) {
	h := m.adapters
	if h == nil {
		return nil, nil
	}
	h.once.Do(h.bind)
	if h.err != nil {
		return nil, h.err
	}
	if fn := h.funcs[name]; fn != nil || h.lookup == nil {
		return fn, nil
	}
	return h.lookup(name), nil
}

func (h *hostTables) bind() {
	env := &hostadapt.Env{Resolve: resolveSpec, Invoke: h.invoke}
	h.funcs = map[string]hostadapt.Func{}
	for _, table := range h.tables {
		funcs, err := table(env)
		if err != nil {
			h.err = fmt.Errorf("vm: binding host adapters: %w", err)
			return
		}
		for name, fn := range funcs {
			if _, dup := h.funcs[name]; dup {
				h.err = fmt.Errorf("vm: binding host adapters: two tables answer %s", name)
				return
			}
			h.funcs[name] = fn
		}
	}
}

// invoke calls a VM function value for a Go host function, on the frame of
// the host call that is calling back.
func (h *hostTables) invoke(fr *rt.Frame, fn rt.Value, args []rt.Value) (rt.Value, error) {
	f, ok := fn.(*functionValue)
	if !ok {
		return nil, fmt.Errorf("vm: a host function called back %T, which is not a function value", fn)
	}
	if len(args) != f.arity {
		return nil, fmt.Errorf("vm: a host function called a function of %d parameter(s) with %d", f.arity, len(args))
	}
	if fr == nil {
		fr = h.root.hostFrame
	}
	if fr == nil {
		fr = rt.NewFrame(h.root.background())
	}
	return h.root.apply(f, args, fr)
}

// resolveSpec is this machine's descriptor for an adapter's spec: the one its
// own constructions of that name and layout use.
func resolveSpec(spec *hostadapt.DescSpec) (*rt.TypeDesc, error) {
	switch spec.Kind {
	case rt.KindStruct:
		return structDesc(spec.Name, spec.Fields), nil
	case rt.KindEnum:
		return enumDesc(spec.Name, spec.Variants), nil
	case rt.KindDistinct:
		return distinctDesc(spec.Name, spec.Inner), nil
	}
	return nil, errors.New("no descriptor of this kind")
}

// callAdapter calls a bound adapter on runtime. An rt trap the Go function
// raises becomes this machine's fault, which is what an rt trap anywhere else
// in an activation is; any other panic keeps unwinding, which is what a
// cancelled task's unwind must do.
func callAdapter(fn hostadapt.Func, runtime *rt.Frame, args []any) (v any, err error) {
	defer func() {
		if p := recover(); p != nil {
			if fault, ok := p.(*rt.Error); ok {
				v, err = nil, &Fault{err: fault}
				return
			}
			panic(p)
		}
	}()
	return fn(runtime, args)
}

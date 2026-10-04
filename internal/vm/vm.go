// Package vm runs an `ir.Module`: it compiles each `ir.Func` to bytecode and
// executes it.
//
// # WHAT THIS PACKAGE DOES NOT CONTAIN
//
// The linearization is the producer's (`internal/irbuild`), not this package's:
// operand forcing, temporary naming and block construction are read off
// `ir.Func` rather than rebuilt here.
//
// The absences are enforced rather than asserted: `TestVM_ReadsTheIRAndNothingElse` fails if this package's
// non-test sources import `nomi/ast`, `nomi/parser`, `nomi/analysis` or
// `nomi/internal/irbuild`.
//
//   - NO AST. Nothing here sees a `*ast.Binary` or an `*ast.If`.
//   - NO OPERAND FORCING. `gen.operand`/`gen.hold`'s rule — materialize an
//     impure left operand so a later operand's statements cannot be hoisted
//     ahead of it — is an `ir.Copy` in the graph, built by the producer. This
//     package executes the Copy.
//   - NO TEMPORARY NAMING. Every operand is an `ir.Temp` the producer
//     allocated out of `ir.Func`'s namespace; the bytecode compiler gives each
//     one a register in the bank its stored `ir.ValType` names.
//   - NO BLOCK CONSTRUCTION. Control flow is `Block.Term()` plus
//     `Block.Fault()`; the bytecode compiler lays the producer's blocks out in
//     order and turns each edge into a code offset (bytecode.go).
//
// WHAT IT DOES CONTAIN is the consumer's own answers: which rt representation realizes a
// constant, which Go function performs a checked `+`, how a frame is laid
// out, and which Go function a marked crossing lands on.
//
// # THE VALUE REPRESENTATION IS rt's
//
// Scalars and strings live unboxed in typed registers; every other value is
// an rt value (`*rt.Record`, rt's collections, rt's handles), and equality,
// hashing and rendering are rt's kernels. values.go lists the representation
// of each kind. A Go caller hands `Run` rt values and reads one back, and a
// crossing into Go calls a generated adapter over rt values (hostcall.go).
// TestVM_ReadsTheIRAndNothingElse pins which packages this package's
// non-test sources may reach.
//
// # THE FAULT EDGE
//
// The fault edge is part of the representation (`internal/ir/fault.go`):
// when an instruction faults, the VM reads `Block.Fault()`. An edge is a
// transfer to that block; no edge unwinds to the caller, which is the
// default.
package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Machine runs the functions one `ir.Module` declares, and — when it was
// opened over a whole program — resolves a callee those modules' siblings
// declare.
type Machine struct {
	// hostEnv overrides process environment entries in the Startup a boot
	// receives. See WithHostEnv.
	hostEnv map[string]string
	mod     *ir.Module
	onces   map[*ir.Symbol]*onceCell
	// hostFrame belongs to a call-local Machine view passed to a host callback,
	// or to the view Boot answers, whose calls run in the published app.
	// Shared maps and cells stay fixed; concurrent Run calls retain separate lineage.
	hostFrame *rt.Frame
	// depth is the call depth of the activation this view acts for: zero on a
	// machine a caller opened, the calling activation's depth on the view a
	// host call or an iteration driver receives. See depth.go.
	depth int
	// boot is the program boot one of the program's modules records, or nil.
	boot *ir.Symbol
	// testBoots are the `tests` group boots the program's modules record.
	testBoots map[*ir.Symbol]bool
	// dispatch is every module's dispatch entries: by interface method, the
	// implementing body for each declared type name a value carries.
	dispatch map[*ir.Symbol]map[string]*ir.Symbol
	// displays is every module's `impl Display` bodies by the declared type
	// name a value carries, which an erased Display rendering selects by.
	displays map[string]*ir.Symbol
	// rowDebugs is every module's hand-written `impl Debug` bodies by the
	// declared type name, which assertion rows render through (rowText).
	rowDebugs map[string]*ir.Symbol
	// equates and hashes are every module's hand-written `impl Equatable`
	// and `impl Hashable` bodies by declared type name, which the key
	// kernels answer through (keys.go).
	equates, hashes map[string]*ir.Symbol
	// tasks records a machine limit a task body reached. See tasks.go.
	tasks *taskLimit
	// out serializes output independently of the runtime once cells' locks.
	// Declaration maps are fixed after New; each frame belongs to one activation.
	out *syncWriter
	// errOut is where the runtime's own diagnostics go, or nil for os.Stderr.
	// See WithErrorOutput.
	errOut io.Writer
	// input is what `io.read_line` reads, or nil for no input. See WithInput.
	input *rt.Input
	// hosts is the Go function each marked crossing lands on, keyed on the
	// callee's printed name.
	//
	// KEYED ON THE NAME, AND THAT IS THE CONSUMER'S BINDING RATHER THAN THE
	// IR'S IDENTITY. `ir.Call.Crosses()` says THAT control leaves the
	// machine; WHICH Go function it lands on is a fact about this machine.
	hosts map[string]hostFn
	// adapters are the generated host adapters a crossing lands on when no
	// intrinsic answers it, bound once for every view. See hostcall.go.
	adapters *hostTables
	// link is the function each of the OTHER modules of one program declares,
	// by the same identity `ir.Module.FuncFor` uses. Nil for a machine opened
	// over a single module, which is every caller New has.
	//
	// KEYED ON THE `*ir.Symbol` AND NOT ON THE NAME, unlike `hosts` above,
	// and the difference is the point: a crossing's destination is this
	// engine's own binding, while a Nomi callee has a declaration identity
	// the representation already carries. Resolving a cross-module call by
	// printed name would be the defect `ir.Symbol`'s header names — "two
	// same-named declarations are two symbols" — and a program's two files
	// may each declare a private `helper`.
	link map[*ir.Symbol]*ir.Func
	// ambiguous is every symbol more than one linked module declares. A
	// producer cannot make one: a symbol is interned on a declaration node
	// and a node is in one unit. A hand-built set can, and picking one of two
	// would be a silent wrong answer where a report is available.
	ambiguous map[*ir.Symbol]bool
	// modules is every module this machine has opened or linked: the entry,
	// the program's other modules, and each module Extend added. See
	// extend.go.
	modules map[*ir.Module]bool
	// codes is the function table: each function's bytecode, compiled on
	// its first call. See exec.go.
	codes *codeTable
	// fuel is an Evaluate's remaining Limits, or nil. See evaluate.go.
	fuel *fuel
}

// hostFn is one crossing into Go: it reads the operands and answers a value.
//
// `pos` IS THE CALL SITE'S, AND IT IS THERE BECAUSE A CROSSING'S GO SIDE MAY
// PRINT A POSITION. `dbg` is the member: `rt.DbgText`'s line argument is the
// Nomi line, and that line is a VALUE the print emits rather than only a
// diagnostic coordinate. This consumer reads it off the fact the
// representation already carries.
//
// THE POSITION AND NOT THE NODE. A binding that could read `Callee()` could
// re-dispatch, and a binding that could read the operand list's shape could
// second-guess `NumArgs`. `ir.Pos` is exactly the fact needed and nothing
// else, which is `ir.Symbol`'s own "a name plus an identity and NOTHING ELSE"
// applied to a parameter list.
type hostFn func(m *Machine, pos ir.Pos, args []any) (any, error)

// WithHostEnv sets environment entries that override the process
// environment in the `Startup.env` a boot receives. `compiler.run_file` passes `RunFile.env` here.
func (m *Machine) WithHostEnv(env map[string]string) *Machine {
	m.hostEnv = env
	return m
}

// WithErrorOutput sets where the runtime's own diagnostics go — a supervised
// task's failure report — instead of os.Stderr. A host that captures a program's error stream
// passes it here.
func (m *Machine) WithErrorOutput(w io.Writer) *Machine {
	m.errOut = w
	return m
}

// WithInput sets the program's standard input, which `io.read_line` reads a
// line at a time. Without it the program has no input: `io.read_line`
// answers Err("eof"). An *rt.Input holds its reader's buffer, so machines
// that share one input share it.
func (m *Machine) WithInput(in *rt.Input) *Machine {
	m.input = in
	return m
}

// withDiagnostics answers ctx carrying the machine's error output and its
// input, for a frame built over it.
func (m *Machine) withDiagnostics(ctx context.Context) context.Context {
	if m.errOut != nil {
		ctx = rt.WithDiagnosticOutput(ctx, m.errOut)
	}
	if m.input != nil {
		ctx = rt.WithInput(ctx, m.input)
	}
	return ctx
}

// background is the context a frame the machine starts from Go is built
// over.
func (m *Machine) background() context.Context {
	return m.withDiagnostics(context.Background())
}

// New opens a machine over mod, writing `io.print`'s output to out.
//
// THE WRITER IS SERIALIZED HERE, because the machine is what introduces the
// concurrency, so it owns the obligation: a `concurrent` block's tasks run on
// the goroutines rt spawns for them (tasks.go). And the shared thing is
// the MACHINE. `Run` takes no writer and nothing in its signature says one
// activation excludes another, so two activations on two goroutines is the
// documented API used as documented. A caller cannot discharge the
// obligation without knowing which of these fields are shared, and the
// failure mode is silent: unserialized, goroutines printing into one
// `bytes.Buffer` lose whole lines, and `os.Stdout` hides it entirely because
// a small write to a file descriptor is effectively atomic.
//
// THE GUARANTEE IS PER MACHINE, AND THE LIMIT IS STATED RATHER THAN IMPLIED.
// Two Machines handed the SAME raw writer still race with each other: each
// wraps it in its own mutex, and two mutexes over one buffer synchronize
// nothing. Wrapping at construction cannot close that. A caller sharing a sink across machines must therefore hand over
// a writer that is already safe for concurrent use; this wraps it a second
// time, which is correct and costs one uncontended lock per line. There is
// no marker for an already-safe writer: the inner mutex serializes whatever
// the outer ones do, so a marker would only save that one lock.
func New(mod *ir.Module, out io.Writer) *Machine {
	return NewProgram(mod, nil, out)
}

// NewProgram opens a machine over the module set ONE LOWERING produced: entry
// is the module `Run` names its function in, and rest are the program's other
// modules, whose declarations a callee in entry may name.
//
// A Nomi program with sibling files lowers to several `ir.Module`s, and a
// callee may be declared in a module other than the caller's (the tour's
// `modules-and-imports.md` blocks are examples). `internal/irbuild`'s
// `irSiblingCalleeSym` is the producer half that makes the identity agree.
//
// THE LOOKUP IS BUILT ONCE AND KEYED ON THE SYMBOL, so a cross-module call
// costs one map probe rather than a scan of every module. Entry is NOT in the
// map: `callInstr` asks its own module first through `ir.Module.FuncFor`, so
// the single-module path needs no link map.
//
// A SYMBOL TWO MODULES DECLARE IS RECORDED AS AMBIGUOUS RATHER THAN RESOLVED.
// The producer cannot build one — a callee symbol is interned on a declaration
// node and a node belongs to one unit — but a hand-built set can, and a silent
// pick between two declarations is the failure `ir.Table.SelectOverload`
// refuses one layer up.
func NewProgram(entry *ir.Module, rest []*ir.Module, out io.Writer) *Machine {
	m := &Machine{mod: entry, out: &syncWriter{w: out}, boot: entry.Boot(), tasks: &taskLimit{},
		codes: &codeTable{}}
	m.adapters = newHostTables(m)
	m.initOnceCells(entry, rest)
	m.addImpls(entry)
	m.addTestBoots(entry)
	m.modules = map[*ir.Module]bool{entry: true}
	for _, other := range rest {
		if other == nil || other == entry {
			continue
		}
		m.modules[other] = true
		if m.boot == nil {
			m.boot = other.Boot()
		}
		m.addTestBoots(other)
		m.addImpls(other)
		for _, f := range other.Funcs() {
			sym := f.Sym()
			if sym == nil {
				continue
			}
			if m.link == nil {
				m.link = map[*ir.Symbol]*ir.Func{}
			}
			if _, dup := m.link[sym]; dup {
				if m.ambiguous == nil {
					m.ambiguous = map[*ir.Symbol]bool{}
				}
				m.ambiguous[sym] = true
				continue
			}
			m.link[sym] = f
		}
	}
	m.hosts = map[string]hostFn{
		"Result.map_err":       resultMapErrHost,
		"Result.from_maybe":    resultFromMaybeHost,
		"concurrent":           concurrentHost,
		"Task.spawn":           taskSpawnHost,
		"Task.await":           taskHost("Task.await"),
		"Task.cancel":          taskHost("Task.cancel"),
		"Task.outcome":         taskHost("Task.outcome"),
		"Task.spawn_all":       taskSpawnAllHost,
		"Task.await_all":       taskAwaitAllHost,
		"Supervisor.spawn":     supervisorSpawnHost,
		"Supervisor.spawn_all": supervisorSpawnAllHost,
		"Maybe.with_default":   withDefaultHost("Some"),
		"Result.with_default":  withDefaultHost("Ok"),
		"Channel.buffered":     channelHost("Channel.buffered"),
		"Channel.unbuffered":   channelHost("Channel.unbuffered"),
		"Sender.send":          channelHost("Sender.send"),
		"Sender.close":         channelHost("Sender.close"),
		"Receiver.receive":     channelHost("Receiver.receive"),
		"List.head":            listHost("List.head"),
		"List.tail":            listHost("List.tail"),
		"List.concat":          listHost("List.concat"),
		// A call the builder lowered as unreachable: a bound dispatch on a type
		// argument it filled itself (irbuild's holeBoundCall). It faults with
		// the builder's text if control ever arrives.
		"vm.unreachable": unreachableHost,
		"List.compare":   containerCompareHost("List.compare"),
		"Vector.compare": containerCompareHost("Vector.compare"),

		"Range.contains?":      rangeHost(true),
		"Range.step_by":        rangeStepBy,
		"Range.known_count":    rangeKnownCount,
		"Range.bounded?":       rangeHost(false),
		"Range.contains?Float": floatRangeHost,
		"Set.size":             setHost("Set.size"),
		"Set.contains?":        setHost("Set.contains?"),
		"Set.insert":           setHost("Set.insert"),
		"Set.remove":           setHost("Set.remove"),
		"Set.union":            setHost("Set.union"),
		"Set.intersection":     setHost("Set.intersection"),
		"Set.difference":       setHost("Set.difference"),
		"Set.subset?":          setHost("Set.subset?"),
		"Vector.length":        vectorHost("Vector.length"),
		"Vector.at":            vectorHost("Vector.at"),
		"Vector.push":          vectorHost("Vector.push"),
		"Vector.concat":        vectorHost("Vector.concat"),
		"Vector.set":           vectorHost("Vector.set"),
		"Vector.next_item":     vectorHost("Vector.next_item"),
		"Map.get":              mapHost("Map.get"),
		"Map.put":              mapHost("Map.put"),
		"Map.size":             mapHost("Map.size"),
		"Context.with_value":   contextWithValueHost,
		"Context.value":        contextValueHost,
		"Map.remove":           mapHost("Map.remove"),
		"Map.contains_key?":    mapHost("Map.contains_key?"),
		"Map.merge":            mapHost("Map.merge"),
		"Map.keys":             mapHost("Map.keys"),
		"Map.values":           mapHost("Map.values"),
		"Map.map_values":       mapHost("Map.map_values"),
		"Map.map_keys":         mapHost("Map.map_keys"),
		"Map.map_next":         mapHost("Map.map_next"),
		// Each call receives rendered text; the IR chooses Display or Debug.
		"io.print":   outputHost("io.print", "\n"),
		"io.write":   outputHost("io.write", ""),
		"io.inspect": outputHost("io.inspect", "\n"),
		// `dbg` is `rt.DbgText(w, line, expr, rendered)`: rt's layout and
		// colour rule, called rather than transcribed. See dbg.go in this
		// package for the two facts that are this consumer's own: which
		// writer the colour decision is made about, and the Debug rendering.
		dbgKey: dbgHost,
	}
	return m
}

// outputHost writes the rendered operand and then end (a newline for print
// and inspect, nothing for write) to the machine's serialized writer, in one
// Write so concurrent tasks never split a line. Rendering is an explicit
// instruction before the call.
func outputHost(name, end string) hostFn {
	return func(m *Machine, _ ir.Pos, args []any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("vm: %s: expected one rendered operand, got %d", name, len(args))
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("vm: %s: the operand is the RENDERED text and arrived as %T; the producer emits an ir.Render before the call", name, args[0])
		}
		io.WriteString(m.out, s+end)
		return rt.Unit{}, nil
	}
}

// syncWriter serializes writes to the writer New was handed.
//
// Mutex-only, because the lock is uncontended for the single-threaded programs that are the common case.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// target is the writer syncWriter wraps, for a decision that must be made
// about the DESTINATION rather than about the wrapper.
//
// IT EXISTS FOR EXACTLY ONE CALLER AND THE CALLER IS A COLOUR DECISION.
// `rt.ColorEnabledFor` ends in `w.(*os.File)` plus a character-device test, so
// it answers FALSE for every `*syncWriter` — including one wrapping
// `os.Stdout` attached to a terminal. Asking it about the wrapper would make a
// machine over a terminal print `dbg` uncoloured where `nomi run` colours it,
// which is a divergence in the one construct whose every line is wrapped in a
// colour decision. See dbg.go's `dbgHost` and writer_test.go's reading.
//
// NOT EXPORTED AND NOT ON `Machine`: nothing outside this package may reach
// past the serialization, which is the whole point of wrapping at
// construction. `Output()` stays the serialized writer.
func (s *syncWriter) target() io.Writer { return s.w }

// Output is the writer `io.print` reaches, already serialized.
//
// Exported so a test can assert the serialization instead of taking the
// comment above New for it. Two reads answer the same writer, which is what
// makes one machine's mutex one mutex.
func (m *Machine) Output() io.Writer { return m.out }

// Run calls the module's function named entry with args.
//
// BY NAME, because a caller outside the module has nothing else: `ir.Module`
// holds declarations and a program's entry point is not one of them —
// module.go argues that absence at the field it would have been, on the
// ground that `internal/irbuild` decides it from the front end's own rule. So
// the caller names the entry and this
// reports when two functions answer to it.
//
// THE BOUNDARY IS rt's representation, the machine's own: the operands are
// rt values (values.go lists one per kind) and so is the result.
func (m *Machine) Run(entry string, args ...any) (any, error) {
	return m.run(entry, args)
}

// run is Run over an argument slice.
func (m *Machine) run(entry string, args []any) (any, error) {
	var found *ir.Func
	for _, f := range m.mod.Funcs() {
		if f.Name() != entry {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("vm: %s names two of this module's declarations; "+
				"a Func's identity is its Symbol and an entry point is not a declaration "+
				"this container holds", entry)
		}
		found = f
	}
	if found == nil {
		return nil, fmt.Errorf("vm: %s is not a function this module retained", entry)
	}
	return m.call(found, args)
}

// RunSymbol calls a declaration in this machine's entry module by identity.
// Use it when the caller already has an IR declaration: distinct impls can
// have the same display name, while Run intentionally rejects ambiguous names.
func (m *Machine) RunSymbol(entry *ir.Symbol, args ...any) (any, error) {
	return m.runSymbol(entry, args)
}

// runSymbol is RunSymbol over an argument slice.
func (m *Machine) runSymbol(entry *ir.Symbol, args []any) (any, error) {
	if entry == nil {
		return nil, errors.New("vm: entry declaration is nil")
	}
	f := m.mod.FuncFor(entry)
	if f == nil {
		return nil, fmt.Errorf("vm: %s is not a declaration this module retained", entry.Name())
	}
	return m.call(f, args)
}

// paramTemp answers the register of f's parameter declared by sym, so a `Ref`
// naming that declaration resolves by IDENTITY and not by printed name.
// `ir.Symbol`'s header is why that matters: two same-named declarations are
// two symbols.
//
// A SCAN, NOT A MAP. Functions have a handful of parameters, and building a
// map per activation would be the largest single allocation on the call path.
// The scan runs from the last parameter, so a symbol declared twice resolves
// to the later one. TestVMCallAllocations pins the per-call allocation count
// this depends on.
func paramTemp(f *ir.Func, sym *ir.Symbol) (ir.Temp, bool) {
	params := f.Params()
	for i := len(params) - 1; i >= 0; i-- {
		if params[i].Sym == sym {
			return params[i].Temp, true
		}
	}
	return ir.NoTemp, false
}

// irInstr runs one instruction the bytecode compiler left to its handler,
// over operands boxed out of their banks. See bytecode.go for which
// instructions arrive here.
func (m *Machine) irInstr(fr *frame, in ir.Instr) error {
	switch n := in.(type) {
	case *ir.FuncValue:
		captures := make([]any, n.NumCaptures())
		fv := &functionValue{body: n.Body(), slot: m.slotFor(n.Body()), captures: captures, arity: n.Arity()}
		for i := range captures {
			if i == 0 && n.Self() {
				// A recursive nested fn's self capture is this closure.
				captures[i] = fv
				continue
			}
			v, err := fr.read(n.Capture(i))
			if err != nil {
				return err
			}
			captures[i] = v
		}
		fr.write(n.Dst(), fv)
		return nil
	case *ir.Const:
		v, err := constValue(n)
		if err != nil {
			return err
		}
		fr.write(n.Dst(), v)
		return nil

	case *ir.Ref:
		if n.Kind() == ir.RefFunc {
			callee, err := m.resolveFunc(n.Sym(), fr.fn.Name())
			if err != nil {
				return err
			}
			fr.write(n.Dst(), m.slotFor(callee).named())
			return nil
		}
		if n.Kind() == ir.RefOnce {
			return m.forceOnce(fr, n)
		}
		if n.Kind() == ir.RefAppField {
			return appField(fr, n)
		}
		if n.Kind() == ir.RefContext {
			return activeContext(fr, n)
		}
		if n.Kind() == ir.RefTypeWitness {
			fr.write(n.Dst(), typeWitness(n.Sym().Name()))
			return nil
		}
		if n.Kind() == ir.RefScope {
			fr.write(n.Dst(), &scopeValue{runtime: fr.runtime})
			return nil
		}
		if n.Kind() != ir.RefLocal {
			return fmt.Errorf("vm: %s: this machine runs no %s", fr.fn.Name(), n)
		}
		t, isParam := paramTemp(fr.fn, n.Sym())
		if !isParam {
			// A `RefLocal` resolves only to a parameter of this frame.
			//
			// NOTHING THE PRODUCER BUILDS REACHES THIS ARM. The producer
			// declares a DESTRUCTURING parameter and builds its projections,
			// and `ir.Lint`'s RuleLocalDeclared refuses a graph with an
			// undeclared local before this machine reads it. So this is the
			// machine's own fence on a hand-built module.
			return fmt.Errorf("vm: %s: %s names a local this frame does not declare",
				fr.fn.Name(), n)
		}
		v, err := fr.read(t)
		if err != nil {
			return err
		}
		fr.write(n.Dst(), v)
		return nil

	case *ir.Store:
		if n.Kind() == ir.StoreContext {
			return storeContext(fr, n)
		}
		if n.Kind() == ir.StoreScope {
			return storeScope(fr, n)
		}
		return storeAppField(fr, n)

	case *ir.Copy:
		v, err := fr.read(n.Src())
		if err != nil {
			return err
		}
		fr.write(n.Dst(), v)
		return nil

	case *ir.Bind:
		// A declaration whose value is the source's. The VM has no name
		// table: a body's read of a bound name IS the Bind's destination
		// temporary, which is what the producer built (irfuncbody.go's
		// `bound` map) and what makes the def-use chain cross a statement
		// boundary.
		v, err := fr.read(n.Src())
		if err != nil {
			return err
		}
		fr.write(n.Dst(), v)
		return nil

	case *ir.Arith:
		return m.arith(fr, n)

	case *ir.Concat:
		var b []byte
		for i := range n.NumParts() {
			v, err := fr.read(n.Part(i))
			if err != nil {
				return err
			}
			s, isString := v.(string)
			if !isString {
				return fmt.Errorf("vm: %s: concat part %d is %T, not a String",
					fr.fn.Name(), i, v)
			}
			b = append(b, s...)
		}
		fr.write(n.Dst(), string(b))
		return nil

	case *ir.Render:
		v, err := fr.read(n.Src())
		if err != nil {
			return err
		}
		switch n.Kind() {
		case ir.RenderDisplay:
			if n.Erased() {
				s, err := m.displayErased(fr, v)
				if err != nil {
					return err
				}
				fr.write(n.Dst(), s)
				return nil
			}
			s, err := m.displayStructural(fr, v)
			if err != nil {
				return err
			}
			fr.write(n.Dst(), s)
			return nil
		case ir.RenderDebug:
			// Debug composes scalars, scalar-leaf lists, tuples and records.
			// `debugText` is where
			// the refusal by name now lives, and it names the RECEIVER
			// rather than the discipline — which is the more useful
			// message, because the discipline is implemented and a given
			// receiver is what is not.
			s, err := rt.DebugText(v, m.debugImplHook(fr, n))
			if err != nil {
				return fmt.Errorf("vm: %s: %w", fr.fn.Name(), err)
			}
			fr.write(n.Dst(), s)
			return nil
		}
		// The Row discipline is `rt.RowText`, which AGREES with Debug on
		// three of these four kinds and disagrees on a String with a
		// backslash or a quote in it — plus a Decimal, a Dynamic, a struct,
		// a record and a hand-written impl. render.go's six measured
		// disagreements are why a consumer must not substitute one for
		// another, so the one discipline with no assertion surface behind it
		// in this machine is refused by name rather than approximated.
		return fmt.Errorf("vm: %s: this machine does not render the %s discipline",
			fr.fn.Name(), n.Kind())

	case *ir.Match:
		return m.match(fr, n)
	case *ir.Try:
		return tryValue(fr, n)

	case *ir.Proj:
		// A destructuring parameter's names are projections off the one
		// parameter holding the whole value, so a function like
		// `fn sum_point(Point{x, y}): Int { x + y }` reaches here twice.
		//
		// Declared and anonymous fields, tuple components, variant payloads
		// and distinct inners share the projection consumer.
		subj, err := fr.read(n.Subject())
		if err != nil {
			return err
		}
		v, err := projValue(fr, n, subj)
		if err != nil {
			return err
		}
		fr.write(n.Dst(), v)
		return nil

	case *ir.Make:
		// Construction uses boxed values. Distincts and structs carry nominal
		// identity; lists prepend through the shared cons-cell kernel and keep
		// the existing tail. Unsupported construction kinds fail by name.
		return m.makeValue(fr, n)

	case *ir.NoMatch:
		// THE TEXT IS `rt`'s. No arm matched is a runtime fault, not a
		// fallthrough, and its text lives in rt. This calls
		// `rt.NoCaseMatchError(line)` and returns the error rather than
		// trapping, because a REPL line must not kill the process.
		//
		// THE LINE IS THE INSTRUCTION'S: `n.Pos().Line()`, since this
		// consumer has no AST node to read it off.
		if n.Key() != ir.NoTemp {
			// A map destructuring's absent key, in rt's text with the key
			// rendered by Display.
			key, err := fr.read(n.Key())
			if err != nil {
				return err
			}
			return rt.MapKeyMissingError(n.Pos().Line(), rt.DisplayText(key))
		}
		return rt.NoCaseMatchError(n.Pos().Line())

	case *ir.Todo:
		// A `todo` was reached. The text is rt's, and this returns it for
		// NoMatch's reason; the destination is never written.
		return rt.TodoError(n.Pos().File(), n.Pos().Line(), n.Reason())

	case *ir.Assert:
		// See assert.go in this package for which half of an assertion is
		// `rt`'s and which is this consumer's.
		return m.assertInstr(fr, n)

	case *ir.Record:
		return m.recordInstr(fr, n)

	case *ir.Not:
		v, err := fr.read(n.Val())
		if err != nil {
			return err
		}
		b, ok := asBool(v)
		if !ok {
			return fmt.Errorf("vm: Boolean negation operand is %T", v)
		}
		fr.write(n.Dst(), boolValue(!b))
		return nil

	case *ir.Compare:
		// An `assert` subject is usually a comparison; internal/ir/compare.go
		// describes the shape.
		return m.compareInstr(fr, n)

	case *ir.Call:
		return m.callInstr(fr, n)
	case *ir.Defer:
		return deferInstr(fr, n)
	case *ir.RunDefer:
		return m.runDeferInstr(fr, n)
	case *ir.Iter:
		return m.at(fr).iterInstr(fr, n)
	}
	return fmt.Errorf("vm: %s: this machine runs no %s", fr.fn.Name(), in)
}

// callInstr performs one call.
func (m *Machine) callInstr(fr *frame, n *ir.Call) error {
	args, fn, err := callOperands(fr, n)
	if err != nil {
		return err
	}
	if m.transfers(fr, n) {
		next, err := m.tailTarget(fr, n, args, fn)
		if err != nil {
			return err
		}
		if next != nil {
			fr.next = next
			return errTailTransfer
		}
	}
	v, err := m.invoke(fr, n, args, fn)
	if err != nil {
		return err
	}
	fr.write(n.Dst(), v)
	return nil
}

// callOperands reads a call's arguments and, for an indirect call, its callee.
func callOperands(fr *frame, n *ir.Call) ([]any, any, error) {
	args := make([]any, n.NumArgs())
	for i := range n.NumArgs() {
		v, err := fr.read(n.Arg(i))
		if err != nil {
			return nil, nil, err
		}
		args[i] = v
	}
	if n.Form() != ir.CalleeIndirect {
		return args, nil, nil
	}
	fn, err := fr.read(n.Fn())
	if err != nil {
		return nil, nil, err
	}
	return args, fn, nil
}

// invoke calls n's callee over operands already read. A deferred call reads
// them at registration and invokes later.
func (m *Machine) invoke(fr *frame, n *ir.Call, args []any, fn any) (any, error) {
	if n.Form() == ir.CalleeIndirect {
		f, ok := fn.(*functionValue)
		if !ok {
			return nil, fmt.Errorf("vm: indirect callee is not a function")
		}
		if len(args) != f.arity {
			return nil, fmt.Errorf("vm: function takes %d operands, got %d", f.arity, len(args))
		}
		if f.host != nil {
			return f.host(fr.runtime, args)
		}
		return m.activateSlot(f.fnSlot(m), args, f.captures, fr.runtime, false, fr.depth, fr.stk)
	}
	if n.Form() == ir.CalleeDispatched {
		callee, err := m.dispatchTarget(fr, n, args)
		if err != nil {
			return nil, err
		}
		return m.callFrom(fr, callee, args)
	}
	if n.Form() != ir.CalleeDirect {
		return nil, fmt.Errorf("vm: %s: this machine runs no %s call", fr.fn.Name(), n.Form())
	}
	// THE CROSSING, read off the node. `docs/roadmap.md`'s debugger entry
	// asks for the crosses-into-Go boundary to be explicit AT THE CALL so a
	// stepping engine knows, before it steps, whether the next frame is one
	// it owns or one it must hand to `dlv`. This is the branch that fact
	// selects, and it is one field rather than a mechanism.
	if n.Crosses() {
		name := n.Callee().Name()
		if host := m.hosts[name]; host != nil {
			bound := *m
			bound.hostFrame = fr.runtime
			bound.depth = fr.depth
			return host(&bound, n.Pos(), args)
		}
		fn, err := m.adapter(name)
		if err != nil {
			return nil, err
		}
		if fn != nil {
			return callAdapter(fn, fr.runtime, args)
		}
		return nil, fmt.Errorf("vm: %s: %s crosses into Go and this machine binds no "+
			"implementation for it", fr.fn.Name(), name)
	}
	callee, err := m.resolveFunc(n.Callee(), fr.fn.Name())
	if err != nil {
		return nil, err
	}
	return m.callFrom(fr, callee, args)
}

// match answers one refutable question.
func (m *Machine) match(fr *frame, n *ir.Match) error {
	if !n.Answers() {
		// `ir.Match` without an answer destination says WHAT is asked and
		// not where the answer goes, which is right for a consumer that
		// spells the condition inline and unusable for one that branches on
		// a temporary. The answering constructors provide that destination;
		// see internal/ir/match.go.
		return fmt.Errorf("vm: %s: %s has no answer to branch on", fr.fn.Name(), n)
	}
	subj, err := fr.read(n.Subject())
	if err != nil {
		return err
	}
	if n.Kind() == ir.MatchListLen || n.Kind() == ir.MatchListMin {
		xs, ok := subj.(*list)
		if !ok {
			return fmt.Errorf("vm: %s: list test reads %T, not a list", fr.fn.Name(), subj)
		}
		length := 0
		if xs != nil {
			length = xs.Len
		}
		matched := length == n.Arity()
		if n.Kind() == ir.MatchListMin {
			matched = length >= n.Arity()
		}
		fr.write(n.Dst(), boolValue(matched))
		return nil
	}
	if n.Kind() == ir.MatchVariant {
		if b, isBool := subj.(bool); isBool && rt.ShortTypeName(n.Sym().Name()) == "Bool" {
			// A Bool is a Go bool, and its two variants are its two values.
			fr.write(n.Dst(), boolValue((n.Variant() == "True") == b && (n.Variant() == "True" || n.Variant() == "False")))
			return nil
		}
		v, ok := enumRecord(subj)
		if !ok && n.Embeds() != nil {
			// A value widened into an `embeds` variant is the embedded
			// value itself.
			fr.write(n.Dst(), boolValue(runtimeTypeName(subj) == n.Embeds().Name()))
			return nil
		}
		if !ok {
			if widenedEmbed(fr.fn.TempType(n.Subject()), subj) {
				// A struct, distinct or marker widened into one of the enum's
				// `embeds` variants is its own value, which no other variant
				// of the enum can be.
				fr.write(n.Dst(), boolValue(false))
				return nil
			}
			return fmt.Errorf("vm: %s: variant test reads %T, not a variant", fr.fn.Name(), subj)
		}
		fr.write(n.Dst(), boolValue(v.Desc.Name == n.Sym().Name() && variantName(v) == n.Variant()))
		return nil
	}
	lit, err := fr.read(n.Arg())
	if err != nil {
		return err
	}
	// `rt.Equal`, the structural equality `internal/ir/match.go` records as the whole of a literal test: a
	// literal pattern's subject is always a scalar and there is no `impl
	// Equatable` to dispatch to.
	fr.write(n.Dst(), boolValue(rt.Equal(subj, lit)))
	return nil
}

// arith performs one arithmetic operation, boxing a fault as an error.
//
// THE DELIVERY IS THIS CONSUMER'S AND THE FAULT IS THE NODE'S, the
// fault/delivery division of `internal/ir`'s package header:
// `ir.Arith.Faults()` says a fault is possible at this
// position, `rt/arith.go` carries the predicate and the message text, and
// this returns the fault as a boxed error because a REPL line must not kill
// the process.
func (m *Machine) arith(fr *frame, n *ir.Arith) error {
	lhs, err := fr.read(n.Lhs())
	if err != nil {
		return err
	}
	var rhs any
	if n.Rhs() != ir.NoTemp {
		if rhs, err = fr.read(n.Rhs()); err != nil {
			return err
		}
	}
	line := n.Pos().Line()
	switch n.Domain() {
	case ir.DomainDecimal:
		return m.decimalArith(fr, n, lhs, rhs)
	case ir.DomainInt:
		a, ok := lhs.(int64)
		if !ok {
			return fmt.Errorf("vm: %s: an Int operation's left operand is %T",
				fr.fn.Name(), lhs)
		}
		var b int64
		if n.Op() == ir.OpNeg {
			// `rt.NegInt` is `SubInt(0, a, line)`, so negation faults on
			// MinInt64 like the subtraction it performs.
			a, b = 0, a
		} else if b, ok = rhs.(int64); !ok {
			return fmt.Errorf("vm: %s: an Int operation's right operand is %T",
				fr.fn.Name(), rhs)
		}
		v, err := intArith(n, a, b, line)
		if err != nil {
			return err
		}
		fr.write(n.Dst(), v)
		return nil

	case ir.DomainFloat:
		a, ok := lhs.(float64)
		if !ok {
			return fmt.Errorf("vm: %s: a Float operation's left operand is %T",
				fr.fn.Name(), lhs)
		}
		var b float64
		if n.Op() == ir.OpNeg {
			fr.write(n.Dst(), rt.NegFloat(a))
			return nil
		} else if b, ok = rhs.(float64); !ok {
			return fmt.Errorf("vm: %s: a Float operation's right operand is %T",
				fr.fn.Name(), rhs)
		}
		switch n.Op() {
		case ir.OpAdd:
			fr.write(n.Dst(), a+b)
		case ir.OpSub:
			fr.write(n.Dst(), a-b)
		case ir.OpMul:
			fr.write(n.Dst(), a*b)
		case ir.OpDiv:
			// `rt.DivFloat` is Go's own `/`: IEEE has an answer for every
			// input including a zero divisor, which is why Faults reports
			// none here.
			fr.write(n.Dst(), a/b)
		case ir.OpRem:
			// FaultUndefined: `rt.ModFloat` always traps, and the checker
			// rejects the construct, so this is the same backstop
			// `rt.ModFloat` is — kept for the reason rt/arith.go gives for
			// keeping it: a backstop costs one function.
			//
			// THE TEXT IS READ, NOT SPELLED. `rt.FloatModuloText` is the one
			// home; this machine boxes it as an error, which is
			// the ONLY part of the fault this consumer owns. Spelling the
			// string here instead would be a second copy, and
			// `TestTrapText_OneHomePerText` fails if one appears.
			return errors.New(rt.FloatModuloText(line))
		}
		return nil
	}
	return fmt.Errorf("vm: %s: this machine runs no %s arithmetic",
		fr.fn.Name(), n.Domain())
}

// intArith is Nomi's checked Int arithmetic, boxing each fault
// `ir.Arith.Faults()` reports.
func intArith(n *ir.Arith, a, b int64, line int) (int64, error) {
	wraps := n.Overflow() == ir.OverflowWraps
	switch n.Op() {
	case ir.OpAdd:
		if !wraps && rt.AddOverflows(a, b) {
			return 0, rt.OverflowError(line, "+", a, b)
		}
		return a + b, nil
	case ir.OpSub, ir.OpNeg:
		if !wraps && rt.SubOverflows(a, b) {
			return 0, rt.OverflowError(line, "-", a, b)
		}
		return a - b, nil
	case ir.OpMul:
		if !wraps && rt.MulOverflows(a, b) {
			return 0, rt.OverflowError(line, "*", a, b)
		}
		return a * b, nil
	case ir.OpDiv:
		if b == 0 {
			return 0, rt.DivByZeroError(line)
		}
		if rt.QuoOverflows(a, b) {
			// The division case a backend forgets, which is why
			// `rt.QuoOverflows` exists to say so.
			return 0, rt.OverflowError(line, "/", a, b)
		}
		return a / b, nil
	case ir.OpRem:
		if b == 0 {
			return 0, rt.DivByZeroError(line)
		}
		// Go's `%` answers 0 for MinInt64 % -1 without a panic, and that is
		// Nomi's answer, so Faults records no overflow here.
		return a % b, nil
	}
	return 0, fmt.Errorf("vm: no such Int operator %s", n.Op())
}

// constValue is the rt value of one constant.
func constValue(c *ir.Const) (any, error) {
	switch c.Kind() {
	case ir.ConstUnit:
		return rt.Unit{}, nil
	case ir.ConstBool:
		return c.Bool(), nil
	case ir.ConstInt:
		return c.Int(), nil
	case ir.ConstFloat:
		return c.Float(), nil
	case ir.ConstDecimal:
		return rt.ParseDecimalLexeme(c.Text())
	case ir.ConstString:
		return c.Text(), nil
	case ir.ConstMarker:
		return markerValue(c.Type().Name()), nil
	case ir.ConstEmptyList:
		return (*list)(nil), nil
	case ir.ConstEmptyVector:
		return rt.VectorOf[any](nil), nil
	case ir.ConstEmptySet:
		return newSet(nil), nil
	}
	return nil, fmt.Errorf("vm: this machine has no value for a %s constant", c.Kind())
}

// functionValue owns captured values independently of its creator's activation.
type functionValue struct {
	body *ir.Func
	// slot is body's entry in the machine's function table, so a call
	// through the value finds its bytecode without a lookup.
	slot     *fnSlot
	captures []any
	arity    int
	// host is a callable a runtime driver hands the program instead of an IR
	// body: the `yield` rt.UserSeq passes to a user source's `each_while`.
	host func(runtime *rt.Frame, args []any) (any, error)
}

// A function value is an rt.Closure: rt can hold, store and render it, and
// only this machine calls it.
//
// Its Display and `values:` row name the function (rt/funcname.go lists the
// renderings): a declared function
// is `<func: double>` whatever binding it was read through, a lambda is
// `<func: <lambda>>`, and a std host reached as a value is
// `<builtin: strings.String.trim>`, the host key its forwarding body is
// named after. `Debug.inspect` of a function is the name-free `<function>`,
// which the builder emits without reaching this.
func (f *functionValue) OpaqueText() string {
	switch {
	case f.body == nil:
		return "<func: <lambda>>"
	case f.body.Sym() != nil:
		name := f.body.Sym().Name()
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:]
		}
		return "<func: " + name + ">"
	case f.body.Name() == "lambda" || f.body.Name() == "concurrent":
		return "<func: <lambda>>"
	}
	return "<builtin: " + f.body.Name() + ">"
}
func (*functionValue) NomiClosure() {}

// apply calls a function value on a runtime frame, with its captures after
// the operands.
func (m *Machine) apply(f *functionValue, args []any, runtime *rt.Frame) (any, error) {
	if f.host != nil {
		return f.host(runtime, args)
	}
	return m.activateSlot(f.fnSlot(m), args, f.captures, runtime, false, m.depth, nil)
}

// fnSlot is the function table entry of f's body.
func (f *functionValue) fnSlot(m *Machine) *fnSlot {
	if f.slot != nil {
		return f.slot
	}
	return m.slotFor(f.body)
}

// resolveFunc gives direct calls and named function values the same module
// identity and linking rules.
func (m *Machine) resolveFunc(symbol *ir.Symbol, owner string) (*ir.Func, error) {
	// THIS MODULE FIRST, then the program's other modules. A callee is
	// overwhelmingly local, and asking `FuncFor` first means a machine opened
	// by `New` never consults the link map.
	callee := m.mod.FuncFor(symbol)
	if callee == nil {
		if m.ambiguous[symbol] {
			return nil, fmt.Errorf("vm: %s: %s names a declaration more than one of this "+
				"program's modules holds; a Func's identity is its Symbol, so two "+
				"modules answering to one is a producer bug rather than a choice",
				owner, symbol.Name())
		}
		callee = m.link[symbol]
	}
	if callee == nil {
		// "did not retain" IS LOAD-BEARING TEXT: `internal/irbuild`'s
		// `vmClassify` reads that substring into its `LINKING` bucket and two
		// probes pin the bucket's size. Both spellings carry it.
		//
		// TWO SPELLINGS BECAUSE THE TWO FAILURES ARE DIFFERENT. A machine over
		// one module names the module; a machine over a program says the
		// program looked, which is the distinction a reader needs: the first
		// is a retention boundary and the second is a declaration nothing in
		// the program retained.
		if m.link == nil {
			return nil, fmt.Errorf("vm: %s: %s names a declaration this module did not retain",
				owner, symbol.Name())
		}
		return nil, fmt.Errorf("vm: %s: %s names a declaration this program's modules did "+
			"not retain", owner, symbol.Name())
	}
	return callee, nil
}

// widenedEmbed reports whether subj is a value of a type the enum type ty
// embeds, which is how a widened `embeds` value is held.
func widenedEmbed(ty *ir.ValType, subj any) bool {
	if _, isRecord := subj.(*rt.Record); !isRecord || ty == nil || ty.Layout() == nil {
		return false
	}
	name := runtimeTypeName(subj)
	for _, v := range ty.Layout().Variants {
		if v.Form != ir.VariantEmbedded || len(v.Fields) != 1 || v.Fields[0].Type == nil || v.Fields[0].Type.Sym() == nil {
			continue
		}
		if v.Fields[0].Type.Sym().Name() == name {
			return true
		}
	}
	return false
}

package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/cldrdata"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdphraseformat"
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
	// testBoots are the `group` boots the program's modules record.
	testBoots map[*ir.Symbol]bool
	// bootFailures are the `boot failure` renderers the modules record, by
	// the entry boot whose `Err` payload each renders.
	bootFailures map[*ir.Symbol]*ir.Symbol
	// bootExits are the entry boots whose error type is std/process's Exit
	// (ir.BootFailure.Exit).
	bootExits map[*ir.Symbol]bool
	// dispatch is every module's dispatch entries: by interface method, the
	// implementing body for each declared type name a value carries.
	dispatch map[*ir.Symbol]map[string]*ir.Symbol
	// unretainedImpls are the interface methods some module implements
	// without a dispatch entry (ir.Module.ImplementUnretained).
	unretainedImpls map[*ir.Symbol]bool
	// displays is every module's `impl Display` bodies by the declared type
	// name a value carries, which an erased Display rendering selects by.
	displays map[string]*ir.Symbol
	// rowDebugs is every module's hand-written `impl Debug` bodies by the
	// declared type name, which assertion rows render through (rowText),
	// and on an extended machine a Debug rendering too (debugImplHook).
	rowDebugs map[string]*ir.Symbol
	// shapeFuncs holds the machine's own function for each interface
	// function it answers by a value's shape (ByShape), by the interface
	// function, and shapes the same functions as a set.
	shapeFuncs map[string]*ir.Func
	shapes     map[*ir.Func]bool
	// extended says Extend linked a later lowering into this machine, as
	// the REPL does for each input.
	extended bool
	// equates and hashes are every module's hand-written `impl Equatable`
	// and `impl Hashable` bodies by declared type name, which the key
	// kernels answer through (keys.go).
	equates, hashes map[string]*ir.Symbol
	// tasks records a machine limit a task body reached. See tasks.go.
	tasks *taskLimit
	// out serializes output independently of the runtime once cells' locks.
	// Declaration maps are fixed after New; each frame belongs to one activation.
	out *syncWriter
	// fileName names a source file in a run-time failure's text
	// (WithFileNames); nil leaves it to rt.DisplayPath.
	fileName func(string) string
	// errOut is the program's standard error, serialized, where `io.eprint`
	// and the runtime's own diagnostics go, or nil for os.Stderr. See
	// WithErrorOutput.
	errOut *syncWriter
	// liveTests receives each test case's output as it is written. See
	// WithLiveTestOutput.
	liveTests io.Writer
	// input is what `io.read_line` reads, or nil for no input. See WithInput.
	input *rt.Input
	// files is the file system std/fs reaches (WithFiles), or nil.
	files any
	// locales holds the CLDR data the program carries, which loading a
	// locale file replaces (std/locale's Locale.load), or nil for
	// cldrdata.Default. See WithLocaleData.
	locales *cldrdata.Store
	// phrases is what the program knows of its phrases beyond a call's
	// table, or nil. See WithPhrases.
	phrases *stdphraseformat.Runtime
	// embedded is the table of the embedded files the program named, and
	// where it reads their contents. See WithEmbeddedSource and
	// embedded.go.
	embedded *rt.EmbeddedFiles
	// hosts is the intrinsic each marked crossing lands on, keyed on the
	// callee's printed name, and whether it is pure.
	//
	// KEYED ON THE NAME, AND THAT IS THE CONSUMER'S BINDING RATHER THAN THE
	// IR'S IDENTITY. `ir.Call.Crosses()` says THAT control leaves the
	// machine; WHICH Go function it lands on is a fact about this machine.
	hosts map[string]intrinsic
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
	// stopCtx is StopOn's context, which the frames the machine starts
	// from Go are built over, or nil.
	stopCtx context.Context
	// caseLimit is how long each test case RunCases runs may take, or zero
	// for no limit. See caselimit.go.
	caseLimit time.Duration
	// noteGo says each call into Go is noted on the calling task's record
	// (rt.EnterGo): on the view a test case under a virtual clock runs on.
	// See caselimit.go.
	noteGo bool
	// dbg is the debugger this machine reports to, or nil. A machine with
	// one compiles its functions with statement hooks (debug.go).
	dbg *Debugger
	// prof samples the program's logical call stack, on a profiling
	// machine (WithProfiler, profile.go).
	prof *Profiler
	// hooked says dbg or prof is set, so exec tests one field for both.
	hooked bool
	// countEntries says each function this machine compiles counts its
	// entries (entries.go).
	countEntries bool
	// copyUpdates says every struct update copies its base, as on a debug
	// machine (inplace.go).
	copyUpdates bool
	// noInline says every direct call opens an activation (inline.go).
	noInline bool
	// noLoopJump says every self tail call runs as opTail (loop.go).
	noLoopJump bool
	// dvia names, on a debug machine's view, the Go-driven operation whose
	// callbacks the view starts (`Iter.map`, a host call), which the logical
	// call stack shows between the callback and its caller.
	dvia string
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

// intrinsic is one entry of the intrinsic table: the hostFn a crossing
// lands on, and whether it is pure. A pure intrinsic computes its answer
// from its operands alone and acts on nothing outside the machine, so a
// refinement rule, a typed literal's handler, a `check` or a debugger's
// evaluation may call it (Effects). Unset, a call to it is an effect.
//
// held marks one that acts only through a value its operand already holds
// (a task, a channel end, a task scope, a fake run on the frame), whose
// effect an effects list names where the value was made: a list names no
// call to it (effects.go). It is still an effect.
//
// unlisted marks an effect the walk reports and no effects list names:
// `dbg`, which prints, and which no built program holds, since `nomi
// build` refuses one. It is still an effect, shown where a reader looks,
// and it names no std module (effects.go).
//
// world says whether a call to it acts outside the machine, the build fact
// a target is checked by (effects.go): a pure intrinsic is inside and
// leaves it unset, and every other one sets it, a held one inside, which
// TestEveryIntrinsicDecidesItsWorld holds. One a
// list names (neither pure, held nor unlisted) names std, the std module
// that declares it, as an import names it after `std::` (`tasks` for
// `Task.spawn`), since its key does not always start with it.
//
// seeded marks a pure one whose answer is a hash keyed by the run's seed
// (`vm.hash_value`), which a rule, a literal's handler and a `check` refuse
// (Effects.Seeded).
//
// written is how a program writes a call that lands on it, where its key
// is the machine's own name: `Hashable.hash` for `vm.hash_value`. A
// refusal names a crossing by it (crossingName), and every intrinsic a
// refusal can name has a key or a written name a program writes, which
// TestCrossingName_NamesWhatTheProgramWrites holds.
//
// defaults marks one that answers the payload of its first operand, an
// Option's Some or a Result's Ok, and otherwise its second operand: an Option
// or a Result fused in registers (optionfuse.go) takes it as one of the
// uses that take it apart, and the call compiles to opDefaultM.
type intrinsic struct {
	fn       hostFn
	pure     bool
	seeded   bool
	written  string
	held     bool
	unlisted bool
	defaults bool
	world    world
	std      string
}

// WithHostEnv sets environment entries that override the process
// environment in the `Startup.env` a boot receives. `compiler.run_file` passes `RunFile.env` here.
func (m *Machine) WithHostEnv(env map[string]string) *Machine {
	m.hostEnv = env
	return m
}

// WithErrorOutput sets the program's standard error, where `io.eprint`,
// `io.ewrite` and the runtime's own diagnostics (a supervised task's failure
// report) go, instead of os.Stderr. A host that captures a program's error
// stream passes it here. Writes to it are serialized as writes to the output
// are, so two tasks never split a line.
func (m *Machine) WithErrorOutput(w io.Writer) *Machine {
	m.errOut = nil
	if w != nil {
		m.errOut = &syncWriter{w: w}
	}
	return m
}

// WithFileNames makes name how the machine names a source file in the text
// of a run-time failure: the `todo reached at <file>:<line>` of a `todo`,
// and each frame of a timed-out case's stack. Without it a file is named by
// rt.DisplayPath, its path from the working directory when it is under it.
// A host that loaded the program from files it wrote for it (vmhost's
// LoadMarked) names them by their path from the project root, as its
// diagnostics do.
func (m *Machine) WithFileNames(name func(string) string) *Machine {
	m.fileName = name
	return m
}

// fileNameOf is how m names file in a run-time failure's text: as
// WithFileNames names it, else as the front end read it, which rt shows
// through DisplayPath (a name already relative is shown as it is).
func (m *Machine) fileNameOf(file string) string {
	if m.fileName != nil {
		return m.fileName(file)
	}
	return file
}

// stderr serializes a machine's writes to os.Stderr when it was given no
// error output.
var stderr = &syncWriter{w: os.Stderr}

// errorOutput is the machine's standard error, serialized.
func (m *Machine) errorOutput() *syncWriter {
	if m.errOut != nil {
		return m.errOut
	}
	return stderr
}

// WithLiveTestOutput makes RunCases write each case's output to w as the
// case writes it, beside the output it answers for the case: a debugger
// shows what a stopped case has printed so far. Output an `io.capture` or
// `io.replay` inside the case takes stays in that capture.
func (m *Machine) WithLiveTestOutput(w io.Writer) *Machine {
	m.liveTests = w
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

// WithFiles sets the file system the program's std/fs calls reach, an
// internal/stdio Backend, unless a call replaces it (`std::fs::testing.run`).
// Without it they reach the build's default: the operating system's, or in
// a js/wasm build the process's in-memory one.
func (m *Machine) WithFiles(files any) *Machine {
	m.files = files
	return m
}

// WithLocaleData sets the CLDR data the program carries, which std's
// locale, plural and formatting adapters read through the frames the
// machine runs (cldrdata.From): the bundle a built image holds, or the one
// vmhost makes for the locales the program's nomi.toml lists. Without it
// they read cldrdata.Default.
func (m *Machine) WithLocaleData(d *cldrdata.Data) *Machine {
	m.locales = nil
	if d != nil {
		m.locales = cldrdata.NewStore(d)
	}
	return m
}

// WithLocaleLoader is WithLocaleData for the data load answers, which the
// machine asks for when a frame first reads it (cldrdata.NewLazyStore).
func (m *Machine) WithLocaleLoader(load func() *cldrdata.Data) *Machine {
	m.locales = cldrdata.NewLazyStore(load)
	return m
}

// WithPhrases sets the program's phrases: the phrase and noun tables it
// carries, which a catalog loaded at run time is checked against, and the
// translations of the locales it loads (stdphraseformat.Runtime), which
// std/phrase's adapters read through the frames the machine runs.
func (m *Machine) WithPhrases(r *stdphraseformat.Runtime) *Machine {
	m.phrases = r
	return m
}

// withDiagnostics answers ctx carrying the machine's error output, its
// input and its locale data, for a frame built over it.
func (m *Machine) withDiagnostics(ctx context.Context) context.Context {
	if m.errOut != nil {
		ctx = rt.WithDiagnosticOutput(ctx, m.errOut)
	}
	ctx = rt.WithStreams(ctx, m.out, m.errorOutput())
	if m.input != nil {
		ctx = rt.WithInput(ctx, m.input)
	}
	if m.files != nil {
		ctx = rt.WithFiles(ctx, m.files)
	}
	if m.locales != nil {
		ctx = cldrdata.WithStore(ctx, m.locales)
	}
	ctx = rt.WithEmbeddedFiles(ctx, m.embedded)
	if m.phrases != nil {
		ctx = stdphraseformat.WithRuntime(ctx, m.phrases)
	}
	return ctx
}

// background is the context a frame the machine starts from Go is built
// over.
func (m *Machine) background() context.Context {
	if m.stopCtx != nil {
		return m.withDiagnostics(m.stopCtx)
	}
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
// pick between two declarations is a wrong answer nothing would report.
func NewProgram(entry *ir.Module, rest []*ir.Module, out io.Writer) *Machine {
	m := &Machine{mod: entry, out: &syncWriter{w: out}, boot: entry.Boot(), tasks: &taskLimit{},
		codes: &codeTable{}, embedded: rt.NewEmbeddedFiles(DiskEmbedded{})}
	m.adapters = newHostTables(m)
	// The program's phrases are, unless WithPhrases says otherwise, the
	// tables every function of its modules carries, worked out when a
	// catalog is first loaded.
	mods := append([]*ir.Module{entry}, rest...)
	m.phrases = stdphraseformat.NewRuntime(func() ([]string, []string) {
		return PhraseTables(mods, ModuleFuncs(mods), StdPhraseModule)
	}, nil)
	m.initOnceCells(entry, rest)
	m.addImpls(entry)
	m.addBoots(entry)
	m.modules = map[*ir.Module]bool{entry: true}
	for _, other := range rest {
		if other == nil || other == entry {
			continue
		}
		m.modules[other] = true
		if m.boot == nil {
			m.boot = other.Boot()
		}
		m.addBoots(other)
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
	m.hosts = map[string]intrinsic{
		"Result.map_err":        {fn: resultMapErrHost, pure: true},
		"Option.to_result":      {fn: optionToResultHost, pure: true},
		"concurrent":            {fn: concurrentHost, world: inside, held: true},
		"Task.spawn":            {fn: taskSpawnHost, world: outside, std: "tasks"},
		"Task.await":            {fn: taskHost("Task.await"), world: inside, held: true},
		"Task.cancel":           {fn: taskHost("Task.cancel"), world: inside, held: true},
		"Task.outcome":          {fn: taskHost("Task.outcome"), world: inside, held: true},
		"Task.spawn_all":        {fn: taskSpawnAllHost, world: outside, std: "tasks"},
		"Task.await_all":        {fn: taskAwaitAllHost, world: inside, held: true},
		"Supervisor.spawn":      {fn: supervisorSpawnHost, world: outside, std: "supervisors"},
		"Supervisor.spawn_all":  {fn: supervisorSpawnAllHost, world: outside, std: "supervisors"},
		"Option.with_default":   {fn: withDefaultHost("Some"), pure: true, defaults: true},
		"Result.with_default":   {fn: withDefaultHost("Ok"), pure: true, defaults: true},
		"Channel.buffered":      {fn: channelHost("Channel.buffered"), world: inside, std: "channels"},
		"Channel.unbuffered":    {fn: channelHost("Channel.unbuffered"), world: inside, std: "channels"},
		"Sender.send":           {fn: channelHost("Sender.send"), world: inside, held: true},
		"Sender.close":          {fn: channelHost("Sender.close"), world: inside, held: true},
		"Sender.close_on_drain": {fn: channelHost("Sender.close_on_drain"), world: inside, held: true},
		"Receiver.receive":      {fn: channelHost("Receiver.receive"), world: inside, held: true},
		"List.first":            {fn: listHost("List.first"), pure: true},
		"List.tail":             {fn: listHost("List.tail"), pure: true},
		"List.concat":           {fn: listHost("List.concat"), pure: true},
		"List.contains":         {fn: listContainsHost, pure: true},
		"List.remove_at":        {fn: listRemoveAtHost, pure: true},

		// Waiting on several channel operations at once (channelselect).
		"Channel.select":          {fn: channelSelectHost, world: inside, held: true},
		"Arm.receive":             {fn: channelHost("Arm.receive"), world: inside, held: true},
		"Arm.send":                {fn: channelHost("Arm.send"), world: inside, held: true},
		"Arm.after":               {fn: channelHost("Arm.after"), world: outside, std: "channels"},
		"Receiver.receive_within": {fn: channelHost("Receiver.receive_within"), world: outside, std: "channels"},

		// Feeding a channel from the process's signals (signals.go).
		"Signal.subscribe": {fn: signalSubscribeHost, world: outside, std: "signals"},

		// The two traps of a failed postfix `!` (unwrap.go).
		"vm.bang_failed":     {fn: bangFailedHost, pure: true},
		"vm.bang_none":       {fn: bangNoneHost, pure: true},
		"vm.refinement_rule": {fn: refinementRuleHost, pure: true},
		// `String.captures`' groups as the regex's capture type (captureshape.go).
		captureShapeKey: {fn: captureShapeHost, pure: true},
		// `Hashable.hash` of a value hashed by its shape (keys.go).
		hashValueName: {fn: hashValueHost, pure: true, seeded: true, written: "Hashable.hash"},
		// std/phrase/format's `variant_name`: an enum value's variant.
		"phrase/format.variant_name": {fn: variantNameHost, pure: true},
		"List.compare":               {fn: containerCompareHost("List.compare"), pure: true},
		"Vector.compare":             {fn: containerCompareHost("Vector.compare"), pure: true},

		"Range.contains":      {fn: rangeHost(true), pure: true},
		"Range.step_by":       {fn: rangeStepBy, pure: true},
		"Range.known_count":   {fn: rangeKnownCount, pure: true},
		"Range.is_bounded":    {fn: rangeHost(false), pure: true},
		"Range.containsFloat": {fn: floatRangeHost, pure: true},
		"Range.start":         {fn: rangePartHost("Range.start"), pure: true},
		"Range.stop":          {fn: rangePartHost("Range.stop"), pure: true},
		"Range.is_inclusive":  {fn: rangePartHost("Range.is_inclusive"), pure: true},
		"Set.size":            {fn: setHost("Set.size"), pure: true},
		"Set.contains":        {fn: setHost("Set.contains"), pure: true},
		"Set.insert":          {fn: setHost("Set.insert"), pure: true},
		"Set.remove":          {fn: setHost("Set.remove"), pure: true},
		"Set.union":           {fn: setHost("Set.union"), pure: true},
		"Set.intersection":    {fn: setHost("Set.intersection"), pure: true},
		"Set.difference":      {fn: setHost("Set.difference"), pure: true},
		"Set.is_subset_of":    {fn: setHost("Set.is_subset_of"), pure: true},
		"Vector.length":       {fn: vectorHost("Vector.length"), pure: true},
		"Vector.get":          {fn: vectorHost("Vector.get"), pure: true},
		"Vector.push":         {fn: vectorHost("Vector.push"), pure: true},
		"Vector.concat":       {fn: vectorHost("Vector.concat"), pure: true},
		"Vector.set":          {fn: vectorHost("Vector.set"), pure: true},
		"Vector.next_item":    {fn: vectorHost("Vector.next_item"), pure: true},
		"Vector.slice":        {fn: vectorHost("Vector.slice"), pure: true},
		"Vector.contains":     {fn: vectorContainsHost, pure: true},
		"Map.get":             {fn: mapHost("Map.get"), pure: true},
		"Map.put":             {fn: mapHost("Map.put"), pure: true},
		"Map.size":            {fn: mapHost("Map.size"), pure: true},
		"Context.with_value":  {fn: contextWithValueHost, pure: true},
		"Context.value":       {fn: contextValueHost, pure: true},
		"Map.remove":          {fn: mapHost("Map.remove"), pure: true},
		"Map.contains":        {fn: mapHost("Map.contains"), pure: true},
		"Map.merge":           {fn: mapHost("Map.merge"), pure: true},
		"Map.keys":            {fn: mapHost("Map.keys"), pure: true},
		"Map.values":          {fn: mapHost("Map.values"), pure: true},
		"Map.map_values":      {fn: mapHost("Map.map_values"), pure: true},
		"Map.map_keys":        {fn: mapHost("Map.map_keys"), pure: true},
		"Map.map_next":        {fn: mapHost("Map.map_next"), pure: true},
		// Each call receives rendered text; the IR chooses Display or Debug.
		"io.print":   {fn: outputHost("io.print", "\n"), world: outside, std: "io"},
		"io.write":   {fn: outputHost("io.write", ""), world: outside, std: "io"},
		"io.inspect": {fn: outputHost("io.inspect", "\n"), world: outside, std: "io"},
		// The stderr pair, `io.eprint` and `io.ewrite`.
		"io.eprint": {fn: errorOutputHost("io.eprint", "\n"), world: outside, std: "io"},
		"io.ewrite": {fn: errorOutputHost("io.ewrite", ""), world: outside, std: "io"},
		// `io.capture` and `io.replay`: the five above and `io.read_line` go to the
		// capture in force on the calling frame. See capture.go.
		captureKey: {fn: captureHost, world: inside, held: true},
		replayKey:  {fn: replayHost, world: inside, held: true},
		// `Reader.from_chunks`: a reader that pulls its chunks from an
		// Iter. See readers.go.
		readerFromChunksKey:     {fn: readerFromChunksHost(readerFromChunksKey), world: inside, std: "io"},
		readerFromTextChunksKey: {fn: readerFromChunksHost(readerFromTextChunksKey), world: inside, std: "io"},
		// `std::fs::testing.run`: std/fs calls go to the sandbox on the calling
		// frame. See sandbox.go.
		sandboxKey: {fn: sandboxHost, world: inside, held: true},
		// `std::process::testing.run`: std/process calls go to the fake on the
		// calling frame. See sandbox.go.
		processFakeKey:     {fn: processFakeHost, world: inside, held: true},
		networkFakeKey:     {fn: networkFakeHost, world: inside, held: true},
		networkAnswerKey:   {fn: networkAnswerHost, world: inside, held: true},
		networkRecordedKey: {fn: networkRecordedHost, world: inside, held: true},
		networkRequestKey:  {fn: networkRequestHost, world: inside, held: true},
		// `dbg` is `rt.DbgText(w, line, expr, rendered)`: rt's layout and
		// colour rule, called rather than transcribed. See dbg.go in this
		// package for the two facts that are this consumer's own: which
		// writer the colour decision is made about, and the Debug rendering.
		dbgKey: {fn: dbgHost, world: outside, unlisted: true},
	}
	if debugEveryMachine.Load() {
		m.WithDebugger(NewDebugger())
	}
	return m
}

// outputHost writes the rendered operand and then end (a newline for print
// and inspect, nothing for write) to the machine's serialized writer, in one
// Write so concurrent tasks never split a line, or to the `io.capture` in
// force on the calling frame. Rendering is an explicit instruction before the
// call.
func outputHost(name, end string) hostFn {
	return func(m *Machine, _ ir.Pos, args []any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("vm: %s: expected one rendered operand, got %d", name, len(args))
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("vm: %s: the operand is the RENDERED text and arrived as %T; the producer emits an ir.Render before the call", name, args[0])
		}
		line := s + end
		if !rt.CaptureWrite(m.hostFrame, line) {
			io.WriteString(m.out, line)
		}
		return rt.Unit{}, nil
	}
}

// errorOutputHost is outputHost for stderr: the rendered operand and end go
// to the `io.capture` in force on the calling frame, whose `errors` they
// join, or to the machine's error output (rt.WriteError).
func errorOutputHost(name, end string) hostFn {
	return func(m *Machine, _ ir.Pos, args []any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("vm: %s: expected one rendered operand, got %d", name, len(args))
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("vm: %s: the operand is the RENDERED text and arrived as %T; the producer emits an ir.Render before the call", name, args[0])
		}
		if m.hostFrame != nil {
			rt.WriteError(m.hostFrame, s+end)
		} else {
			io.WriteString(m.errorOutput(), s+end)
		}
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

// Destination is the writer syncWriter wraps (rt.Destination), for a
// decision rt makes about where the bytes land rather than about the
// wrapper: whether colour suits them (rt.ColorEnabledFor, which `dbg` asks)
// and whether standard output is a terminal (rt.StandardFile). Writes still
// go through the wrapper: nothing writes to what Destination answers.
func (s *syncWriter) Destination() io.Writer { return s.w }

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
		var kept *rt.AppFields
		if n.KeepsFields() {
			kept = rt.KeptFields(fr.runtime)
		}
		if n.NumCaptures() == 0 {
			// A function value that captures nothing is the same value
			// wherever the same app fields are in force: the slot's own.
			fr.write(n.Dst(), m.slotFor(n.Body()).keeping(kept))
			return nil
		}
		captures := make([]any, n.NumCaptures())
		fv := &functionValue{body: n.Body(), slot: m.slotFor(n.Body()), captures: captures, arity: n.Arity(), kept: kept}
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
		if n.Kind() == ir.ConstEmbedded {
			ref := n.Embedded()
			fr.write(n.Dst(), m.embedded.Handle(rt.EmbeddedFile{Source: ref.Source, Size: ref.Size, Sum: ref.Sum}))
			return nil
		}
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
			// A named function the program makes a value keeps the
			// fields in force here, as a lambda does (ir.Ref.KeepsFields).
			var kept *rt.AppFields
			if n.KeepsFields() {
				kept = rt.KeptFields(fr.runtime)
			}
			fr.write(n.Dst(), m.slotFor(callee).keeping(kept))
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
			s, err := m.displayShape(fr, v, n.PartImpls(), fr.fn.TempType(n.Src()))
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
				if _, fault := asFault(err); fault {
					// A fault inside a hand-written Debug impl, or a safe
					// point in it that stopped the run, is the program's
					// own failure at this rendering, not a machine error.
					return err
				}
				return fmt.Errorf("vm: %s: %w", fr.fn.Name(), err)
			}
			fr.write(n.Dst(), s)
			return nil
		}
		// The Row discipline is `rt.RowText`, which shares Debug's leaves
		// and disagrees with it on a struct's field order, a host value with
		// no written impl and a hand-written impl. render.go's recorded
		// differences are why a consumer must not substitute one for
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
		// `fn sum_point(Point{x:, y:}) -> Int { x + y }` reaches here twice.
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
		return rt.NoCaseMatchError(n.Pos().Line())

	case *ir.Todo:
		// A `todo` was reached. The text is rt's, and this returns it for
		// NoMatch's reason; the destination is never written.
		return rt.TodoError(m.fileNameOf(n.Pos().File()), n.Pos().Line(), n.Reason())

	case *ir.Unreachable:
		// An `unreachable` was reached: the program's own claim failed.
		// The text is rt's, positioned at its line as other traps are, so
		// one in std is reported at the program's call (stdlibfault.go).
		return rt.UnreachableError(n.Pos().Line(), n.Reason())

	case *ir.Assert:
		// See assert.go in this package for which half of an assertion is
		// `rt`'s and which is this consumer's.
		return m.assertInstr(fr, n)

	case *ir.Record:
		return m.recordInstr(fr, n)

	case *ir.Subtests:
		// See subtests.go in this package.
		return m.at(fr).subtestsInstr(fr, n)

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
	case *ir.CtlRead:
		return ctlReadInstr(fr, n)
	case *ir.Iter:
		if m.dbg != nil {
			view := *m.at(fr)
			view.dvia = "Iter." + n.Op().String()
			return view.iterInstr(fr, n)
		}
		// iterInstr counts its callbacks from fr's depth itself
		// (iterCaller), so it needs no view of m.
		return m.iterInstr(fr, n)
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

// errBoundCall is the failure a call through a type parameter's bound
// compiles to: only an abstract body holds one (ir.NewBoundCall), which the
// effect walk reads and which reaches a machine only by a producer's bug.
func errBoundCall(f *ir.Func, n *ir.Call) error {
	return fmt.Errorf("vm: %s: a call through a bound, %s, is in an abstract body, which no machine runs", f.Name(), n.Callee().Name())
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
			if m.dbg != nil {
				// A host-backed function value is the machine's own (an
				// each_while's yield), so a replay runs it again.
				th, prev, prevName := m.dbg.crossIn(fr, "<host function>")
				defer m.dbg.crossOut(th, prev, prevName)
			}
			return f.host(fr.runtime, args)
		}
		return m.callValue(f, args, fr.runtime, false, fr.depth, fr.stk, 0)
	}
	if n.Form() == ir.CalleeDispatched {
		callee, err := m.dispatchTarget(fr, n, args)
		if err != nil {
			return nil, err
		}
		return m.callFrom(fr, callee, args)
	}
	if n.Form() == ir.CalleeBound {
		return nil, errBoundCall(fr.fn, n)
	}
	if n.Form() != ir.CalleeDirect {
		return nil, fmt.Errorf("vm: %s: this machine runs no %s call", fr.fn.Name(), n.Form())
	}
	// THE CROSSING, read off the node. A VM debugger (`docs/roadmap.md`)
	// needs the crosses-into-Go boundary to be explicit AT THE CALL so a
	// stepping engine knows, before it steps, whether the next frame is one
	// it owns or one it must hand to `dlv`. This is the branch that fact
	// selects, and it is one field rather than a mechanism.
	if n.Crosses() {
		name := n.Callee().Name()
		if m.dbg != nil {
			// Recorded, or answered from the recording in a replay
			// (debugtravel.go).
			return m.dbg.cross(fr, name, m.pureCrossing(name), args, func() (any, error) { return m.crossInto(fr, n, name, args) })
		}
		return m.crossInto(fr, n, name, args)
	}
	callee, err := m.resolveFunc(n.Callee(), fr.fn.Name())
	if err != nil {
		return nil, err
	}
	return m.callFrom(fr, callee, args)
}

// crossInto performs a crossing into Go: an intrinsic, or a bound adapter.
func (m *Machine) crossInto(fr *frame, n *ir.Call, name string, args []any) (any, error) {
	if m.prof != nil {
		return m.profCross(fr, n, name, func() (any, error) { return m.crossOver(fr, n, name, args) })
	}
	return m.crossOver(fr, n, name, args)
}

// crossOver is crossInto's crossing itself.
func (m *Machine) crossOver(fr *frame, n *ir.Call, name string, args []any) (any, error) {
	if host := m.hosts[name].fn; host != nil {
		return host(m.boundAt(fr, name), n.Pos(), args)
	}
	fn, err := m.adapter(name)
	if err != nil {
		return nil, err
	}
	if fn != nil {
		return callAdapter(m.noteGo, fr, n.Pos(), fn, args)
	}
	return nil, fmt.Errorf("vm: %s: %s is a host function, and this machine binds no "+
		"implementation for it", fr.fn.Name(), hostadapt.DisplayName(name))
}

// boundAt is m as an intrinsic called from fr sees it: on fr's dynamic
// context and at fr's depth. A host may keep the machine it is handed (a lazy
// Iter stage calls back through it later), so the view is never changed
// once made. fr's stack keeps the view it made last at each depth and
// hands it to the next crossing at that depth whose view would be the
// same, since one activation after another runs at a depth, and a body
// compiled into its caller (inline.go) runs at the depth its call would:
// without that, every `Vector.get` or `Map.get` copied the whole machine
// onto the heap. A debug machine names the crossing in its view, so it
// makes one per crossing, and a frame of no stack (a boot's deferred call
// run at cleanup) keeps none.
func (m *Machine) boundAt(fr *frame, name string) *Machine {
	var kept *hostView
	if s := fr.stk; s != nil && m.dbg == nil {
		for len(s.hostViews) <= fr.depth {
			s.hostViews = append(s.hostViews, hostView{})
		}
		kept = &s.hostViews[fr.depth]
		if v := kept.view; v != nil && kept.of == m && v.hostFrame == fr.runtime {
			return v
		}
	}
	bound := *m
	bound.hostFrame = fr.runtime
	bound.depth = fr.depth
	if m.dbg != nil {
		bound.dvia = name
	}
	if kept != nil {
		kept.view, kept.of = &bound, m
	}
	return &bound
}

// hostView is a machine boundAt made from of.
type hostView struct {
	view, of *Machine
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
	if n.Kind() == ir.MatchVectorLen || n.Kind() == ir.MatchVectorMin {
		vec, ok := subj.(rt.Vector[any])
		if !ok {
			return fmt.Errorf("vm: %s: vector test reads %T, not a vector", fr.fn.Name(), subj)
		}
		matched := vec.Len() == n.Arity()
		if n.Kind() == ir.MatchVectorMin {
			matched = vec.Len() >= n.Arity()
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
	if n.Kind() == ir.MatchStrPrefix {
		s, sok := subj.(string)
		prefix, pok := lit.(string)
		if !sok || !pok {
			return fmt.Errorf("vm: %s: prefix test reads %T and %T, not two strings", fr.fn.Name(), subj, lit)
		}
		fr.write(n.Dst(), boolValue(rt.StringPrefixOf(prefix, s)))
		return nil
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
		if !rt.IsInt(lhs) {
			return fmt.Errorf("vm: %s: an Int operation's left operand is %T",
				fr.fn.Name(), lhs)
		}
		if !n.Op().Unary() && !rt.IsInt(rhs) {
			return fmt.Errorf("vm: %s: an Int operation's right operand is %T",
				fr.fn.Name(), rhs)
		}
		v, err := intArith(n, lhs, rhs, line)
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
			fr.write(n.Dst(), rt.ModFloat(a, b))
		}
		return nil
	}
	return fmt.Errorf("vm: %s: this machine runs no %s arithmetic",
		fr.fn.Name(), n.Domain())
}

// intArith is Nomi's checked Int arithmetic, boxing each fault
// `ir.Arith.Faults()` reports.
// intArith is one Int operation over Int values (an int64 or a
// *rt.BigInt), exact, or modulo 2^64 under OverflowWraps. Only a zero divisor
// faults.
func intArith(n *ir.Arith, a, b any, line int) (any, error) {
	if n.Overflow() == ir.OverflowWraps {
		x := rt.Wrap64(a)
		switch n.Op() {
		case ir.OpAdd:
			return x + rt.Wrap64(b), nil
		case ir.OpSub:
			return x - rt.Wrap64(b), nil
		case ir.OpMul:
			return x * rt.Wrap64(b), nil
		case ir.OpNeg:
			return -x, nil
		}
	}
	switch n.Op() {
	case ir.OpAdd:
		return rt.IntAdd(a, b), nil
	case ir.OpSub:
		return rt.IntSub(a, b), nil
	case ir.OpNeg:
		return rt.IntNeg(a), nil
	case ir.OpMul:
		return rt.IntMul(a, b), nil
	case ir.OpDiv:
		if q, ok := rt.IntQuo(a, b); ok {
			return q, nil
		}
		return nil, rt.DivByZeroError(line)
	case ir.OpRem:
		if r, ok := rt.IntRem(a, b); ok {
			return r, nil
		}
		return nil, rt.DivByZeroError(line)
	case ir.OpAnd:
		return rt.IntAnd(a, b), nil
	case ir.OpOr:
		return rt.IntOr(a, b), nil
	case ir.OpXor:
		return rt.IntXor(a, b), nil
	case ir.OpNot:
		return rt.IntNot(a), nil
	case ir.OpShl:
		if v, ok := rt.IntShl(a, b); ok {
			return v, nil
		}
		return nil, rt.ShiftCountError(line, b, true)
	case ir.OpShr:
		if v, ok := rt.IntShr(a, b); ok {
			return v, nil
		}
		return nil, rt.ShiftCountError(line, b, false)
	}
	return nil, fmt.Errorf("vm: no such Int operator %s", n.Op())
}

// constValue is the rt value of one constant.
func constValue(c *ir.Const) (any, error) {
	switch c.Kind() {
	case ir.ConstUnit:
		return rt.Unit{}, nil
	case ir.ConstBool:
		return c.Bool(), nil
	case ir.ConstInt:
		if big := c.Big(); big != nil {
			return rt.IntFromBig(big), nil
		}
		return c.Int(), nil
	case ir.ConstByte:
		return rt.Byte(c.Int()), nil
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
	// kept is the app fields in force where the value was made, which its
	// body runs under wherever it is called (callValue), or nil where none
	// were in force (rt.KeptFields).
	kept *rt.AppFields
	// host is a callable a runtime driver hands the program instead of an IR
	// body: the `yield` rt.UserSeq passes to a user source's `each_while`.
	host func(runtime *rt.Frame, args []any) (any, error)
}

// A function value is an rt.Closure: rt can hold, store and render it, and
// only this machine calls it.
//
// Its Display and `values:` row name the function (rt.FunctionRowText for a
// declared one): a declared function
// is `<func: double>` whatever binding it was read through (an impl block's
// function by its own name, ir.ImplFunc.Func), a lambda is
// `<func: <lambda>>`, and a std host reached as a value is
// `<builtin: strings.String.trim>`, the host key its forwarding body is
// named after. `Debug.inspect` of a function is the name-free `<function>`,
// which the builder emits without reaching this.
func (f *functionValue) OpaqueText() string {
	switch {
	case f.body == nil:
		return "<func: <lambda>>"
	case f.body.Sym() != nil:
		if impl, ok := f.body.Sym().Impl(); ok {
			return rt.FunctionRowText(impl.Func)
		}
		name := f.body.Sym().Name()
		if i := strings.LastIndexByte(name, '.'); i >= 0 {
			name = name[i+1:]
		}
		return rt.FunctionRowText(name)
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
	return m.callValue(f, args, runtime, false, m.depth, nil, 0)
}

// callValue is every call of a function value with an IR body: one
// activation of its body over args and its captures, on runtime with the
// app fields the value kept in force (rt.EnterKeptFields). The caller's
// frame is untouched, so its fields are back in force however the body
// leaves: a return, a `?`, a failure or a cancellation's unwind. owned is
// activateOwned's.
func (m *Machine) callValue(f *functionValue, args []any, runtime *rt.Frame, testBody bool, caller int, stk *regStack, owned uint64) (any, error) {
	runtime, release := rt.EnterKeptFields(runtime, f.kept)
	if release != nil {
		return m.callReleasing(release, f, args, runtime, testBody, caller, stk, owned)
	}
	return m.activateOwned(f.fnSlot(m), args, f.captures, runtime, testBody, caller, stk, owned)
}

// callReleasing is callValue's activation when entering the kept fields
// started a deadline, which ends however the body leaves.
func (m *Machine) callReleasing(release func(), f *functionValue, args []any, runtime *rt.Frame, testBody bool, caller int, stk *regStack, owned uint64) (any, error) {
	defer release()
	return m.activateOwned(f.fnSlot(m), args, f.captures, runtime, testBody, caller, stk, owned)
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

// variantNameHost answers the name of the variant its one operand, an enum
// value, is.
func variantNameHost(_ *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("vm: phrase/format.variant_name: expected one operand, got %d", len(args))
	}
	r, ok := enumRecord(args[0])
	if !ok {
		return nil, fmt.Errorf("vm: phrase/format.variant_name: the operand is %T, not an enum value", args[0])
	}
	return variantName(r), nil
}

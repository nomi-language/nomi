package irbuild

import (
	"fmt"
	"reflect"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/std"
)

// Lowering the standard library — the two-sided answer, implemented.
//
// A stdlib module is ordinary Nomi source with `host fn` externs in it, so it
// has two halves and they need two mechanisms:
//
//   - the Nomi-bodied half lowers like any other module, into its own
//     ir.Module;
//   - the `host fn` half has no body to lower, so each one needs a Go function
//     the VM can cross into: an rt function or an adapter package's, answered
//     through internal/stdlibbindings (see the host-fn registry below).
//
// Hand-writing Go equivalents of whole stdlib modules in rt would be a second
// implementation of the Nomi-bodied half; for the extern half it is the only
// implementation there is.
//
// # The scalar boundary, and why it is where it is
//
// A stdlib function is lowered only when every parameter type AND the result
// type is a scalar (Int/Float/String/Bool/Unit). Not a phasing decision — a
// consequence of two facts:
//
//  1. `kind` identity for a named type IS the *typeDef pointer of its
//     declaration, never its name, so two generated
//     packages cannot exchange a struct, enum or distinct type without a shared
//     type table that does not exist. `Int` is `int64` in every package and needs
//     no table.
//  2. It makes every generated stdlib package a LEAF. std/strings and std/iter
//     import each other; Go forbids import cycles. With the scalar boundary a
//     lowered stdlib function references only its own module and rt, so the
//     generated import graph is a star centred on the entry — acyclic by
//     construction rather than by a sort that would have to fail somewhere.
//
// # Refusals from inside the stdlib are SILENT, and that is deliberate
//
// std/strings contains `String.split` (returns List<String>), and std/int
// contains `Int.modulo` (takes a NonZeroInt). Reporting those as
// blockers would blame every user program in the corpus for constructs in a file
// the user did not write. So a stdlib function is lowered SPECULATIVELY: one
// that refuses is
// discarded along with its refusals and marked unlowerable, and a CALL SITE to it
// refuses at its own position, which is a position somebody wrote. Same mechanism
// impl.go uses for front-end-synthesized impl blocks, applied to a whole module.

// --- the host-fn registry --------------------------------------------------

// A std `host fn` is retained only when internal/stdlibbindings, the one
// hand-maintained host table, answers its key, in one of three ways:
//
//   - an RtFuncs row: an rt function the VM calls through a generated adapter.
//     Its Go signature is projected onto kinds here (hostFnFor) and must equal
//     the declaration's (stdSignatureMatches), and the call is a crossing by
//     that key (irScalarHost, irFrameHost).
//   - a Funcs row: a first-party adapter package's FFI-shaped function, which
//     the VM reaches through a generated adapter. The crossing is named by the binding (irExternHost).
//     Its signature is not projected: internal/hostgen checked it against the
//     declaration when it generated the adapter, and refuses to generate one
//     that disagrees.
//   - a std/compiler key internal/compilerhosts answers (irCompilerHost).
//
// A key none of these answers declines as "stdlib host function". The VM
// holds a Dynamic as the rt.Dynamic the Dynamic rows read and answer.

// hostFn is one stdlib `host fn` with a Go implementation, fully derived from
// the Go function value: nothing here is typed by hand.
type hostFn struct {
	// call is the function as it is emitted, read off the symbol.
	call string
	// pkg is the Go IMPORT PATH the symbol lives in, so a call site can import
	// exactly what it names. See hostPackages.
	pkg string
	// params and result are the Go signature projected onto kinds. A leading
	// *rt.Frame is NOT among them — see takesFrame.
	params []kind
	result kind
	// takesFrame is set when the rt function's first Go parameter is the frame.
	// A host fn needs one exactly when its behaviour depends on the activation
	// rather than only on its arguments — `timer.sleep` waits on the frame's
	// cancellation context — and it is read off the SIGNATURE rather than
	// declared, so the table cannot claim a frame the implementation does not
	// take, or omit one it does.
	takesFrame bool
}

var (
	stdlibHostFuncs = map[string]hostFn{}
	// stdlibBindingErrors names every binding whose Go signature does not
	// project onto scalar kinds. Collected rather than panicked: a compiler that
	// refuses to start over a registry defect is worse than one that refuses the
	// affected call, and TestStdlibBindingsProject fails on any entry — so the
	// failure is loud where loudness helps and degrades to a REFUSAL, never to a
	// wrong answer, everywhere else.
	stdlibBindingErrors []string
)

func init() {
	for _, b := range stdlibbindings.RtFuncs() {
		h, err := hostFnFor(b.Fn)
		if err != "" {
			stdlibBindingErrors = append(stdlibBindingErrors, b.Name+": "+err)
			continue
		}
		if prev, dup := stdlibHostFuncs[b.Name]; dup {
			stdlibBindingErrors = append(stdlibBindingErrors,
				b.Name+": bound twice, to "+prev.call+" and "+h.call)
			continue
		}
		stdlibHostFuncs[b.Name] = h
	}
}

// hostFnFor projects an rt function value onto the builder's view of it.
func hostFnFor(fn any) (hostFn, string) {
	rv := reflect.ValueOf(fn)
	if !rv.IsValid() || rv.Kind() != reflect.Func {
		return hostFn{}, "not a function value"
	}
	ft := rv.Type()
	if ft.IsVariadic() {
		return hostFn{}, "variadic"
	}
	if ft.NumOut() != 1 {
		return hostFn{}, fmt.Sprintf("returns %d values, want 1", ft.NumOut())
	}
	h := hostFn{}
	h.call, h.pkg = hostCallName(rv)
	if h.call == "" {
		return hostFn{}, "symbol name is not resolvable"
	}
	first := 0
	if ft.NumIn() > 0 && ft.In(0) == reflect.TypeFor[*rt.Frame]() {
		// The frame is threading, not an argument: it occupies no Nomi
		// parameter slot and the call site supplies it. Recognized only in
		// position 0, because that is where every generated call passes it and
		// a frame anywhere else is a signature nobody writes.
		h.takesFrame = true
		first = 1
	}
	for i := first; i < ft.NumIn(); i++ {
		k := kindOfGoType(ft.In(i))
		// kindInvalid: no-position — reads a Go reflect type for the extern registry; no Nomi position.
		if k == kindInvalid {
			return hostFn{}, fmt.Sprintf("parameter %d is %s, which is outside the representable set", i+1, ft.In(i))
		}
		h.params = append(h.params, k)
	}
	h.result = kindOfGoType(ft.Out(0))
	// kindInvalid: no-position — reads a Go reflect type for the extern registry; no Nomi position.
	if h.result == kindInvalid {
		return hostFn{}, fmt.Sprintf("result is %s, which is outside the representable set", ft.Out(0))
	}
	return h, ""
}

// hostPackages are the Go packages a stdlib `host fn` implementation may live
// in, and the local name a host call is spelled under.
//
// # Why most of them are not in rt
//
// std/compiler's functions cannot go in rt. Their implementations ARE the
// compiler, and rt must not import the compiler's packages at all —
// TestRuntimeImportsOnlyItsAllowlist (rt/imports_test.go). So they live in
// packages outside rt.
//
// TWO of them, split on WHICH PART of the compiler each needs:
//
//   - nomi/stdcompiler is the ANALYZER half — `check` and `hover`.
//   - nomi/stdcompilerrun is the RUN half — `run` and `run_file`. Its engine
//     is the VM, which nomi/vmhost installs; it cannot import vmhost, because
//     vmhost imports this package and this package imports it.
//
// None of this weakens TestRuntimeArtifactLinksNoFrontEnd (internal/vm), which
// asserts that rt and the VM reach none of nomi/ast, nomi/parser and
// nomi/analysis. A compiler host package is not one of the packages that guard
// walks, so rt still imports no analyzer.
//
// The local name is the import path's last element.
var hostPackages = [...]struct{ path, local string }{
	{rtModulePath, "rt"},
	{compilerHostPath, "stdcompiler"},
	{compilerRunHostPath, "stdcompilerrun"},
	{ioHostPath, "stdio"},
	{stringsHostPath, "stdstrings"},
	{regexHostPath, "stdregex"},
	{randomHostPath, "stdrandom"},
	{calendarHostPath, "stdcalendar"},
}

// calendarHostPath is std/calendar's implementation, and it is the FIRST row
// that is the co-located adapter ITSELF rather than a shim beside one.
//
// std/regex is written in the FFI's shapes, so `nomi/stdregex` exists to
// project it onto rt's. Calendar's boundary is
// spelled in `Int`, `String` and the structs rt declares — the same Go types
// under both projections — so there is nothing to project and the rows in
// stdlibBindings name `nomi/std/calendar` directly.
//
// The tzdb embed (`time/tzdata`) is this adapter's import rather than rt's, so
// rt does not carry its binary size.
const calendarHostPath = "github.com/nomi-language/nomi/internal/stdcalendar"

// compilerHostPath is std/compiler's ANALYZER-backed host implementation, in the
// compiler's own module.
const compilerHostPath = "github.com/nomi-language/nomi/internal/stdcompiler"

// compilerRunHostPath is std/compiler's run host: `run` and `run_file`, over
// the engine nomi/vmhost installs. See hostPackages.
const compilerRunHostPath = "github.com/nomi-language/nomi/internal/stdcompilerrun"

// compilerHostsPath is the package whose table answers std/compiler's host
// functions on the VM. See irCompilerHost.
const compilerHostsPath = "github.com/nomi-language/nomi/internal/compilerhosts"

// ioHostPath is std/io's FILESYSTEM host implementation.
//
// Outside rt because rt is linked by every runner, and real filesystem effect
// must not be entrenched in it. See nomi/stdio's package comment.
const ioHostPath = "github.com/nomi-language/nomi/internal/stdio"

// stringsHostPath is std/strings' host implementation for the operations that
// need a dependency rt does not carry.
//
// The FIFTH host package, and its reason is a THIRD one — neither "the
// implementation IS the compiler" nor "the effect must not be entrenched", but
// DEPENDENCY SCOPE. `String.normalize` needs
// golang.org/x/text/unicode/norm. In rt that would be a new entry in rt's
// import allowlist, a decision about everything that links rt. Here it is an
// import outside rt, of a module the compiler already carries, so the repo
// gains no module at all.
//
// See nomi/stdstrings' package comment for why this is the opposite call from
// `github.com/rivo/uniseg`, which WAS allowed in rt for `String.length`.
const stringsHostPath = "github.com/nomi-language/nomi/internal/stdstrings"

// regexHostPath is std/regex's host implementation: the marshalling surface
// between a CO-LOCATED ADAPTER and rt's shapes.
//
// The SIXTH host package, and its reason is a FOURTH one. The first five are
// each about where an implementation the TOOLCHAIN owns may live — it is the
// compiler, it is filesystem effect, it is a dependency. std/regex's
// implementation is an adapter with FFI-shaped signatures — `(*T, error)`,
// `*string`, `[]string` — which have to be projected onto rt's somewhere, and
// this package is that somewhere.
//
// The adapter itself does not import rt: registering it directly would put the
// runtime in every co-located adapter's import list and mix two API shapes in
// one file. A REIMPLEMENTATION in rt is also declined, although `regexp` is the
// Go standard library and would cost rt no require: it would be a second regex
// implementation to keep in agreement with the first. One implementation, two
// marshalling surfaces, which is nomi/stdstrings' arrangement.
//
// The handle TYPE nevertheless lives in rt (`rt.Regex`), and rt/regexhandle.go
// says why.
const regexHostPath = "github.com/nomi-language/nomi/internal/stdregex"

// randomHostPath is std/random's host implementation, a co-located adapter.
//
// The reasoning is regexHostPath's verbatim and the SHAPE is smaller: std/random
// needs no handle type, because its three externs are scalars and tuples. Only
// the trailing-`error` one names an rt type, which is exactly what the adapter
// must not do. See nomi/stdrandom's header for why re-implementing splitmix64 in
// rt would be worse than a shim rather than merely different — two PRNG
// implementations that agree now and diverge on one rounding detail produce
// different sequences from the same seed, against a doc that promises "same `n`
// -> same sequence, forever".
const randomHostPath = "github.com/nomi-language/nomi/internal/stdrandom"

// hostCallName is the emitted spelling of a host implementation, read off the
// symbol itself so a rename cannot leave a stale string behind, plus the import
// path it needs.
//
// runtime.FuncForPC gives the fully-qualified Go name
// ("github.com/nomi-language/nomi/rt.StringContains"); the emitted form is the
// package's local name plus the function's, because generated code imports a
// host package under its package name. Guarded rather than trusted: anything
// that is not a plain top-level func in exactly one of the registered packages
// is rejected, which keeps a method value or a closure from producing a
// plausible-looking wrong name.
func hostCallName(rv reflect.Value) (call, pkg string) {
	full := goruntime.FuncForPC(rv.Pointer())
	if full == nil {
		return "", ""
	}
	name := full.Name()
	for _, hp := range hostPackages {
		local, inPkg := strings.CutPrefix(name, hp.path+".")
		if !inPkg {
			continue
		}
		if local == "" || strings.ContainsAny(local, ".[(") {
			return "", ""
		}
		return hp.local + "." + local, hp.path
	}
	return "", ""
}

// kindOfGoType projects a Go type in an rt signature onto the kind it means.
//
// The five scalar rows are the representation table's mechanical ones, read in
// the direction the registry needs. Beyond them three families are admitted, and
// all three qualify for the SAME reason a scalar does — their Go spelling is the
// same in every generated package, so they carry no package-relative rendering:
//
//   - a stdlib OPAQUE newtype whose Go type rt declares (`rt.Duration`).
//     Admitting it BY THE GO TYPE rather than by width is what keeps
//     `rt.DurationToString(rt.Duration) string` from projecting onto `(Int) ->
//     String`. See opaque.go.
//   - a PRELUDE instance over those (`rt.Maybe[int64]`), whose type arguments
//     are read out of the struct's own payload fields. See stdprelude.go.
//   - a STRUCTURAL `*rt.List[T]` over those. A structural kind's identity is
//     its interned *compKind, and sharedcomp.go makes that identity
//     process-wide for neutral components, so a `List<String>` projected here
//     matches the same type at a call site in another gen.
//
// Anything else answers kindInvalid.
func kindOfGoType(t reflect.Type) kind {
	switch t {
	case reflect.TypeFor[int64]():
		return kindInt
	case reflect.TypeFor[float64]():
		return kindFloat
	case reflect.TypeFor[string]():
		return kindString
	case reflect.TypeFor[bool]():
		return kindBool
	case reflect.TypeFor[rt.Unit]():
		return kindUnit
	}
	// kindInvalid: lookup — "not an anchored opaque newtype", so fall through.
	if k := opaqueKindOfGoType(t); k != kindInvalid {
		return k
	}
	// `rt.DateTime` reaches here as a parameter and as a result, and as the `Ok`
	// payload of an `rt.Result[rt.DateTime, rt.CalendarError]`. See stdstruct.go.
	//
	// kindInvalid: lookup — "not an anchored stdlib opaque struct", so fall through.
	if k := stdStructKindOfGoType(t); k != kindInvalid {
		return k
	}
	// `rt.Bytes` reaches here as a parameter and as a result. Asked BEFORE the
	// scalar switch would have been wrong and after it is required: rt.Bytes is
	// a Go `string` underneath, and reflect's identity comparison at the top of
	// this function already answered kindString for `string` itself, so a
	// defined type over it falls through to here and gets its own answer. See
	// stdhost.go.
	//
	// kindInvalid: lookup — "not an anchored stdlib host type", so fall through.
	if k := stdHostKindOfGoType(t); k != kindInvalid {
		return k
	}
	// Without this arm every `Result<_, calendar.Error>` extern would fail
	// stdSignatureMatches with the declaration and the registry both correct.
	// See stdenum.go.
	//
	// kindInvalid: lookup — "not an anchored monomorphic stdlib enum", so fall through.
	if k := stdEnumKindOfGoType(t); k != kindInvalid {
		return k
	}
	// A NOMI TUPLE, whose Go representation is a flat anonymous struct over
	// `F0`, `F1`, …: `(Int, Int)` is `struct { F0 int64; F1 int64 }`.
	//
	// THIS IS READING THE BUILDER'S OWN CONVENTION BACK, not inventing one: tuples
	// are an interned tagTuple over flat F0/F1 fields (sharedcomp.go's
	// `tupleSigKind`).
	//
	// WHY A GO FUNCTION RETURNS ONE STRUCT RATHER THAN TWO VALUES. The FFI
	// projects a Go multiple return element-by-element onto a tuple, and
	// `hostFnFor` cannot: it requires exactly one result because the builder
	// treats the call as an EXPRESSION and a multiple return is not one. Go's
	// structural typing closes that with no new machinery — an anonymous struct
	// a host package writes IS the type the builder interns — so a shim converts
	// two results into one value and every call site is unchanged. See
	// nomi/stdrandom.
	//
	// kindInvalid: lookup — "not a tuple-shaped anonymous struct", so fall through.
	if k := tupleKindOfGoType(t); k != kindInvalid {
		return k
	}
	// kindInvalid: lookup — "not a *rt.List", so fall through.
	if k := listKindOfGoType(t); k != kindInvalid {
		return k
	}
	// See stdmapfield.go for why that projection is a shape-checked WALK where
	// the list one above is a field read: a Map exposes neither type argument,
	// on a field or on a method.
	// kindInvalid: lookup — "not an rt.Map", so fall through.
	if k := mapKindOfGoType(t); k != kindInvalid {
		return k
	}
	return preludeKindOfGoType(t)
}

// listKindOfGoType is the kind an rt signature's `*rt.List[T]` names, or
// kindInvalid.
//
// Identified STRUCTURALLY, for the reason preludeKindOfGoType is: a hand-written
// `reflect.Type` per instantiation would be an unbounded table, and a name-only
// match would accept an unrelated rt type spelled `List`. So: a pointer, to a
// struct rt declares, whose name is `List[…]`, with EXACTLY the three fields
// list.go gives it and the two that constrain the shape checked — `Tail` is the
// same pointer type and `Len` is an int.
//
// The element type comes out of `Head`, which is where reflect exposes it, and
// that makes the projection recursive for free: `*rt.List[*rt.List[string]]`
// answers `List<List<String>>`. `Tail` is deliberately NOT recursed into — it is
// the same type, so it would not terminate — and checking it for IDENTITY with t
// is what proves this really is a cons cell of T and not a struct that happens
// to carry a T.
func listKindOfGoType(t reflect.Type) kind {
	if t == nil || t.Kind() != reflect.Pointer {
		return kindInvalid
	}
	cell := t.Elem()
	if cell.Kind() != reflect.Struct || cell.PkgPath() != rtModulePath {
		return kindInvalid
	}
	if base, _, generic := strings.Cut(cell.Name(), "["); !generic || base != "List" {
		return kindInvalid
	}
	if cell.NumField() != 3 {
		// An unaccounted field is storage this builder never writes, and reading
		// an element type off a struct whose shape has drifted would project a
		// signature nobody declared. Same check, same reason, as preludeGoArgs.
		return kindInvalid
	}
	head, hasHead := cell.FieldByName("Head")
	tail, hasTail := cell.FieldByName("Tail")
	length, hasLen := cell.FieldByName("Len")
	if !hasHead || !hasTail || !hasLen || tail.Type != t || length.Type.Kind() != reflect.Int {
		return kindInvalid
	}
	elem := kindOfGoType(head.Type)
	// kindInvalid: propagates — an rt type outside the representable set.
	if elem == kindInvalid {
		return kindInvalid
	}
	// A non-neutral element cannot occur here (every kind kindOfGoType answers
	// is neutral), and the decline is honoured rather than assumed away: a
	// *compKind interned in no gen would be a kind no call site could match.
	k, shared := sharedListKind(elem)
	if !shared {
		return kindInvalid
	}
	return k
}

// --- the index -------------------------------------------------------------

// stdFunc is one stdlib function as a call site sees it.
type stdFunc struct {
	// tailRec marks a body that makes a tail call into a recursion cycle it
	// belongs to. Its graph is retained for the VM and its Go is a stub. See
	// irStdTailCycles.
	tailRec bool
	// key is the host key for a `host fn` (the key internal/stdlibbindings
	// answers), and the same shape for a Nomi body. It is what a refusal names.
	key    string
	module string
	// recv is the impl block's receiver type name ("String", "Int"), empty for a
	// top-level declaration.
	recv string
	// iface is the impl block's interface base NAME ("Debug", "Add"), empty for
	// an inherent block or a top-level declaration. It is what byIface is keyed
	// on, because that is the qualifier a call site writes.
	iface string
	// ifaceKey is the impl block's full interface INSTANTIATION
	// ("Add<Duration, NaiveDateTime>"), empty when the interface takes no type
	// arguments. It is what separates several impls of one generic interface
	// for one receiver, and it is `n.Interface.TypeString()` — the same string,
	// not a parallel spelling. See stdImplKey.
	ifaceKey string
	name     string
	// pub is the declaration's `pub` bit, for a TOP-LEVEL function only. It is
	// what makes a file-qualified call (`timer.sleep(d)`) resolvable: `pub`
	// controls cross-FILE visibility, and a std module's private helper —
	// `strings.string_compare` — is reachable from inside that module and
	// nowhere else. Belt and braces against the analyzer, which rejects the
	// program first; a backend that resolved a private name would be lowering
	// something Nomi does not permit.
	pub bool
	// selfIdx is the parameter position holding the receiver, or -1 when the
	// function has none. Only 0 is served: the design doc's dispatch table says
	// the receiver is the Self-TYPED parameter at ANY position, and a stdlib impl
	// whose Self parameter is not first is refused rather than guessed at.
	selfIdx int

	params []kind
	result kind
	// paramNames is the declared parameter names, positionally and the same
	// length as params, so a call site can place a NAMED argument.
	//
	// Held HERE rather than read off `decl`, and that is the whole point of the
	// field: `decl` is a `*ast.FuncDef` and is NIL for a `host fn`, which is an
	// `*ast.ExternFunc`. `supervisors.Supervisor.new`, the usual target of a
	// named argument, is an extern, so a names-from-decl route would answer for
	// exactly the population that does not need it.
	//
	// A DESTRUCTURING parameter contributes "" — it has no name a call site
	// could write, and no NamedArg's name can be empty, so the slot is
	// unclaimable by name rather than mis-claimable.
	paramNames []string

	// arityMin is the lowest POSITIONAL arity a call site may use. It is
	// len(params) for everything except a Nomi body whose TRAILING parameters
	// carry defaults and for which arity-reduced bodies were built — see
	// emitStdDefaultWrappers, which is also where the value is lowered.
	//
	// It starts at len(params) and drops one step per trailing defaulted
	// parameter as emitStdDefaultWrappers builds that arity's body
	// (irStdArityRetain).
	//
	// READ THROUGH callArityMin AND NEVER DIRECTLY, for the snapshot reason
	// below.
	arityMin int

	// defaultFill is, per parameter slot, whether the declaring module built
	// an accessor that evaluates that parameter's DEFAULT. It is what lets a NAMED argument skip a
	// defaulted parameter: the arity wrappers above are indexed by a length and
	// so can only fill a trailing run, while a skipped parameter is a HOLE.
	//
	// Same pessimism and same timing as arityMin, and READ THROUGH
	// callDefaultFill AND NEVER DIRECTLY for the same snapshot reason. See
	// stdslotfill.go.
	defaultFill []bool

	// canon is the CANONICAL *stdFunc this one is a snapshot of, and nil when
	// this IS the canonical one.
	//
	// bindStdSiblings copies each candidate as `view := *f` so it can override
	// `body` and `pkg`, and that copy is taken BEFORE the lowering pass runs.
	// arityMin is lowered DURING that pass, on the canonical pointer, so reading
	// a copy would freeze the pessimistic value and a stdlib body calling a
	// defaulted SIBLING in its own module would refuse `call arity` however many
	// wrappers had since been written. std's `//!` prompt cases make exactly that
	// call, for instance `Response.new(Status.ok())` against three parameters and
	// `Time.new(h, m)` against four.
	//
	// Reading through the pointer is not a relaxation of the pessimism, it is
	// the pessimism applied at the right TIME. A body lowered before the arity
	// body exists still sees the full arity and still refuses;
	// lowerStdlibModule's fixed point re-lowers it afterwards.
	canon *stdFunc

	// rtCall is set for a `host fn` with a registry row.
	rtCall string
	// rtHostPkg is the Go import path rtCall lives in, so a call site imports
	// exactly what it names. Empty until rtCall is set. See hostPackages.
	rtHostPkg string
	// rtFrame is set when that rt function's Go signature takes the frame, so
	// the call site passes one. Copied off the registry row rather than
	// re-derived, and false for everything else. See hostFn.takesFrame.
	rtFrame bool
	// body and pkg are set for a Nomi body that lowered.
	body bool
	pkg  string
	// why names the refusal when this function is not lowerable, and detail
	// qualifies it. Exactly one of rtCall, body and why is set.
	why string

	decl *ast.FuncDef
	// irBody is the final retained declaration, owned by its cached module.
	irBody *ir.Func
	// irArity is the VM's body of each arity-reduced wrapper, by arity, set
	// on the canonical function. See irsupervisor.go's irStdArityRetain.
	irArity map[int]*ir.Func
	// irSlot is the VM's body of each `_DEFAULT<slot>` accessor, by slot, set
	// on the canonical function. See irsupervisor.go's irStdSlotRetain.
	irSlot map[int]*ir.Func
}

func (f *stdFunc) lowerable() bool { return f.rtCall != "" || f.body }

// stdlibIndex is every stdlib function a call site can resolve, plus the IR
// graphs built for the ones with Nomi bodies.
type stdlibIndex struct {
	// byType keys "String.contains?" — the type-qualified spelling a call site
	// writes.
	//
	// The value is an OVERLOAD SET rather than one function, because the
	// spelling is what a CALL SITE writes and std legitimately declares it
	// several times: `NaiveDateTime.add` is eleven `impl Add<X, NaiveDateTime>`
	// blocks. A single slot would make that a map overwrite, so the surviving
	// entry and every sibling would have to be refused outright. Selection is by
	// the full
	// parameter-kind signature, which is exactly the information a call site
	// supplies. See addOverload and stdPick.
	byType map[string][]*stdFunc
	// byFile keys "timer.sleep" — a `pub` TOP-LEVEL function under
	// "<module>.<name>", which is how a file-qualified call through a stdlib
	// file's API object spells it. Free functions only: that is all a file API
	// object reaches in Nomi.
	byFile map[string]*stdFunc
	// byIface keys "Debug.inspect" and then the RECEIVER kind. An interface
	// qualifier with a statically-known scalar receiver is the concrete-qualifier
	// row of the design's dispatch table: it names exactly one function, so it
	// lowers to a direct call and needs no table at run time.
	//
	// An overload set for the same reason byType is one, and the receiver kind
	// is NOT enough on its own: all eleven `impl Add<X, NaiveDateTime>` blocks
	// have `NaiveDateTime` in the receiver position and differ only in the
	// operand.
	byIface map[string]map[kind][]*stdFunc
	// unlowered names, per generated package, each Go function whose body the IR
	// did not lower and the reason. A program that reaches one is refused;
	// see irStdReach.
	unlowered map[string]map[string]string
	// irModules holds the graphs built while emitting each cached package.
	// Callers must link these declaring modules, preserving symbol identity.
	irModules map[string]*ir.Module
	// modulePkg maps a stdlib module name to its generated package name.
	modulePkg map[string]string
	// byKey is every declaration the walk saw, under its host key (stdKey),
	// whether it lowered or not. Nothing in the builder reads it; it is
	// what lets TestStdlibRegistryMatchesStdSource check the registry against
	// the declarations std actually carries, so a row for a function std does
	// not declare — or one whose signature drifted when std was edited — fails a
	// test instead of lowering calls against a signature nobody wrote.
	byKey map[string]*stdFunc
	// byOnce keys "Int.max_value" — a `once` declared in a stdlib impl block,
	// under the type-qualified spelling a reference site writes. One slot per
	// spelling rather than byType's overload set: a `once` takes no arguments,
	// so there is nothing for a reference to select on. See stdonce.go.
	byOnce map[string]*stdOnce
	// views is the lowering context each module was lowered THROUGH, keyed by
	// module name, retained so a per-program instance of a generic stdlib
	// declaration can be emitted against exactly the candidate set, sibling
	// scope and AST this module's `mod.go` was built from.
	//
	// RETAINED RATHER THAN RE-DERIVED, and that is a correctness requirement
	// rather than an optimisation. Calling stdModuleContext again would call
	// std.Load() again and resolve an instance against a second analysis, whose
	// candidate set must then match the one behind the cached module in the
	// SAME program or an instance body references a function the program does
	// not contain. analysis.installCanonicalStdTypes keeps two loads' `Bool`
	// the same type; see stdinstance.go.
	views map[string]*stdModuleView
	// earlierMods[module] is every stdlib module lowered STRICTLY BEFORE module,
	// which is the resolution scope that module's own lowering had.
	//
	// Recorded as a set of names rather than as a snapshot of the index, because
	// `earlier` is the accumulating index itself — buildStdlibIndex passes `idx`
	// and it keeps growing — so a retained pointer would be the COMPLETE index
	// by the time anything read it. A name set plus `stdFunc.module` is enough
	// to rebuild the restricted view on demand, and it keeps the DAG argument
	// exactly as buildStdlibIndex states it: every generated edge points
	// backwards. See restrictedTo.
	earlierMods map[string]map[string]bool
}

// stdlibOnce caches the index for the process.
//
// The cache owns its source contexts, signatures, generated text and IR graphs.
// Cached declarations and their IR symbols belong to that same lowering; they
// must not be reconstructed from another std.Load()'s fresh AST pointers. The
// lowering reads std.Shared, the process's one stdlib analysis.
// Consumers treat the cached modules as immutable.
var (
	stdlibOnce  sync.Once
	stdlibCache *stdlibIndex
)

// stdAnchorLib is the ONE std analysis every spec-table validation in this
// package asks: "does std declare this name, in this shape". Those questions
// are fixed per compiler binary, so one analysis answers them for the process.
//
// A std.Load per question would be reached from stdBaseAnchors for every
// module and every interface-anchor pass, which would dominate the cost of
// lowering hello world.
//
// It is std.Shared, the same analysis the front end resolved the program
// against and buildStdlibIndex lowers from, so a `nomi run` analyzes the
// stdlib once (about 52 ms). Sharing it does not change what preludeAt
// answers inside std.
//
// Consumers only read it (Scope.Lookup, node walks), and it is built under
// sync.OnceValue, so concurrent gens may read it freely.
var stdAnchorLib = std.Shared

func stdlibLowering() *stdlibIndex {
	// The cache holds no test body: a module's `//!` prompt cases are built
	// per call, in their own module, by GenerateStdlibTestIR (stdtests.go).
	stdlibOnce.Do(func() { stdlibCache = buildStdlibIndex() })
	return stdlibCache
}

// stdPackage is the package name the builder's Go spellings use for the i'th
// stdlib module. A
// separate namespace from unitPackage's so a stdlib package and a user module
// package can never collide.
func stdPackage(i int) string { return fmt.Sprintf("nomistd%d", i) }

// buildStdlibIndex lowers every stdlib module, in dependency order.
func buildStdlibIndex() *stdlibIndex {
	lib := std.Shared()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	// Sorted so a program's generated package numbering — and therefore its
	// content-addressed output directory — never depends on map iteration order.
	sort.Strings(names)

	idx := &stdlibIndex{
		byType:      map[string][]*stdFunc{},
		byFile:      map[string]*stdFunc{},
		byIface:     map[string]map[kind][]*stdFunc{},
		irModules:   map[string]*ir.Module{},
		unlowered:   map[string]map[string]string{},
		modulePkg:   map[string]string{},
		byKey:       map[string]*stdFunc{},
		byOnce:      map[string]*stdOnce{},
		views:       map[string]*stdModuleView{},
		earlierMods: map[string]map[string]bool{},
	}
	// A stdlib module's Nomi body may call another module's lowered function —
	// `std/instant`'s `impl Add<Duration, Instant>` is
	// `Instant(nanos + Duration.as_nanos(rhs))`, and there is no way to write
	// that without reaching std/duration. The generated packages then import
	// each other, and Go forbids an import CYCLE. Nomi's own import graph may be
	// cyclic (std/strings and std/iter import each other), so acyclicity cannot
	// be inherited from it.
	//
	// The rule is therefore: a module resolves cross-module references only
	// against modules ALREADY lowered, i.e. earlier in the loop below. Every
	// generated edge then points from a later module to an earlier one, so the
	// package graph is a DAG by construction and needs no cycle detection —
	// FOR ANY total order, which is the property that makes the next paragraph
	// safe. TestStdlibGeneratedPackageGraphIsAcyclic checks it rather than
	// trusting the argument.
	//
	// # The order is the DEPENDENCY order
	//
	// The order decides which DIRECTION of a mutually-importing Nomi pair may
	// lower. The package NAME is `stdPackage(i)` over the SORTED list and the
	// LOWERING ORDER is the loop; nothing requires them to be the same sequence,
	// so names are stable and only the visiting order follows dependencies.
	//
	// A name order would refuse bodies on ORDER alone: `std/calendar` sorts near
	// the front and imports `comparable`, `duration`, `int` and `strings`, all of
	// which sort later, so `Years.compare`, `Years.hash`, `Years.inspect` and the
	// Nomi-bodied `Add<Unit, NaiveDateTime>` rungs would all refuse.
	//
	// A cycle needs no special case. Ignoring back edges leaves the members in
	// their alphabetical relative order, which is exactly what the sorted order
	// gave them, so `std/strings` may still call `std/iter` and not the
	// reverse. Nothing is lost and nothing new is representable inside a cycle.
	for _, name := range names {
		idx.modulePkg[name] = stdPackage(sort.SearchStrings(names, name))
	}
	// done is the modules lowered so far, which is exactly the resolution scope
	// each next module gets. Recorded per module BEFORE that module is lowered,
	// so `earlierMods[name]` is "strictly before" and never "including itself".
	done := map[string]bool{}
	for _, name := range stdLoweringOrder(names, lib) {
		nodes := lib.Nodes[name]
		if len(nodes) == 0 {
			continue
		}
		pkg := idx.modulePkg[name]
		// SourcePath, not FileURI: a position names the file, and nothing
		// here opens it, so no per-module stat or materialization runs.
		path := lib.SourcePath(name)
		earlier := make(map[string]bool, len(done))
		for m := range done {
			earlier[m] = true
		}
		idx.earlierMods[name] = earlier
		// The FileAnalysis goes with the nodes: the tree carries the annotations
		// the stdlib author WROTE and the analysis carries the ones the checker
		// solved, and a backend handed only nodes has to re-infer or refuse.
		//
		// The view is built HERE and retained on the index rather than built
		// inside lowerStdlibModule, so that a per-program instance is emitted
		// against this exact context. See stdlibIndex.views for why re-deriving
		// it is unsound rather than merely wasteful.
		v := stdModuleContext(name, path, pkg, nodes, lib.Files[name], idx)
		idx.views[name] = v
		funcs, onces, irModule := lowerStdlibModule(v, idx)
		// A module none of whose bodies lowered holds nothing to link.
		if irModule != nil && (len(irModule.Funcs()) > 0 || len(irModule.Cells()) > 0) {
			irLintFinished(irModule)
			idx.irModules[pkg] = irModule
		}
		if len(v.unlowered) > 0 {
			idx.unlowered[pkg] = v.unlowered
		}
		for _, f := range funcs {
			idx.add(f)
		}
		for _, o := range onces {
			idx.addOnce(o)
		}
		done[name] = true
	}
	return idx
}

// stdLoweringOrder lowers imports before their users, starting with Context's
// dependency graph because every execution installs a Context. Remaining roots
// and each module's dependencies are visited in sorted name order.
//
// Back edges are skipped. A module resolves only against modules already
// lowered, so generated packages form a DAG; an unavailable backward reference
// inside a Nomi import cycle is refused. The Context seed fixes the initial
// cycle orientation independently of optional surface modules' names.
// TestStdlibModuleOrderIsStable guards the deterministic traversal.
func stdLoweringOrder(names []string, lib *std.StdLib) []string {
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	state := make(map[string]int, len(names))
	order := make([]string, 0, len(names))
	var visit func(string)
	visit = func(name string) {
		switch state[name] {
		case active, done:
			// active: a back edge, i.e. this module is one of our own
			// ancestors. Skipping it is what confines a Nomi import cycle to a
			// refusal rather than an infinite descent.
			return
		}
		state[name] = active
		for _, dep := range stdModuleImports(lib.Nodes[name]) {
			if _, known := lib.Nodes[dep]; known {
				visit(dep)
			}
		}
		state[name] = done
		order = append(order, name)
	}
	// Context is an implicit dependency of every execution. Seed its dependency
	// graph before optional library surfaces so their names cannot choose the
	// direction of cycles among the foundational scalar and collection files.
	if _, known := lib.Nodes["context"]; known {
		visit("context")
	}
	for _, name := range names {
		visit(name)
	}
	return order
}

// stdModuleImports is the sorted set of sibling std modules a module imports.
//
// Read off the analyzed tree rather than off a manifest, so it cannot go stale
// when a std file gains an import. A `go` extern entry names a Go package and
// is not a Nomi module, so it is skipped.
//
// The stdlib is ONE Nomi module, so a sibling is named BARE — `import iter` and
// `import maybe.Maybe.{self, None}` — and only the FIRST segment is the module.
// A second segment is a drill-through owner (`Maybe`), never a path step, so
// this takes the head and ignores the rest. Reading a two-segment `std/<name>`
// would match nothing, so stdLoweringOrder would see zero edges and a module
// could be lowered before the module it imports.
func stdModuleImports(nodes []ast.Node) []string {
	seen := map[string]bool{}
	add := func(imp *ast.ImportStmt) {
		if imp.Extern || len(imp.ModulePath) == 0 {
			return
		}
		head, isHead := imp.ModulePath[0].(*ast.Ident)
		if !isHead {
			return
		}
		seen[head.Name] = true
	}
	for _, n := range nodes {
		switch t := n.(type) {
		case *ast.ImportStmt:
			add(t)
		case *ast.ImportBlock:
			for _, e := range t.Entries {
				add(e)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// add files one stdlib function under every spelling a call site may use.
//
// A duplicate under one spelling is marked ambiguous on BOTH entries rather than
// resolved by arrival order, because picking one by map order is silently
// nondeterministic. What counts as a duplicate is decided by addOverload.
func (x *stdlibIndex) add(f *stdFunc) {
	if x.byKey != nil {
		// A Nomi BODY may not displace a `host fn` under one key, and this is
		// an ordering rule rather than a preference.
		//
		// stdKey is documented as "the host key". A Nomi body has no host
		// implementation — only a `host fn` is answered under a key — so when
		// two declarations claim one key, exactly one of them is what the key
		// NAMES and it is the extern.
		//
		// The collision stdKey's own comment does not cover: it argues that a
		// non-generic interface needs no instantiation in the key because it
		// "can be implemented for a receiver at most once". True, and it is
		// about two impls of one interface. `bytes.Bytes.to_string` is an
		// INHERENT `host fn` colliding with `impl Display for Bytes`, which
		// that argument does not reach. Re-keying the interface half would
		// change the key of every non-generic impl host fn in std, and
		// internal/stdlibbindings' rows with it, so the rule is here instead: the
		// key keeps its meaning and the entry keeps the declaration that has one.
		//
		// TestStdlibRegistryMatchesStdSource (a registry row whose key resolves
		// to a Nomi body) and TestCollapsedStdlibKeysCostOnlyCoverage (an absorbed
		// declaration that needs a binding decision) both depend on the entry
		// being deterministic in the direction the key's own definition implies.
		//
		// This changes WHICH entry survives, never how many: byType and
		// byIface still hold both, addOverload still marks both ambiguous, and
		// a call site is still refused rather than routed.
		if prev, held := x.byKey[f.key]; !held || prev.decl != nil || f.decl == nil {
			x.byKey[f.key] = f
		}
	}
	if f.recv == "" && x.byFile != nil {
		// A top-level function, `pub` or not. No ambiguity is possible: two
		// declarations of one name in one file is a front-end error, and the
		// key carries the module.
		//
		// There is no `pub` gate here, because it would be a visibility check
		// in the wrong layer. `byFile` is the IMPLICIT FILE ALIAS — how a stdlib module
		// names its OWN free functions — and a module names its private ones
		// that way most of all. Whether a caller outside the module may spell
		// `calendar.date_parse_raw` is the front end's decision and it has
		// already been made before any of this runs; the key carries the
		// module, so a private entry is unreachable from anywhere the checker
		// would not have rejected first.
		//
		// A gate would make every module-private extern unreachable however
		// carefully it was bound: std/random's `below_state`, `unit_float_state`
		// and `os_state`, and std/calendar's boundary, where the public API is
		// Nomi bodies over private externs because the FFI cannot cross a
		// `Result<_, Error>` or a `Duration`.
		x.byFile[f.key] = f
	}
	if f.recv != "" {
		key := f.recv + "." + f.name
		x.byType[key] = addOverload(x.byType[key], f, "ambiguous stdlib method")
	}
	// A non-scalar receiver is NOT indexed, and leaving it out matters twice.
	// It could never be selected — a call site's receiver kind is a real kind or
	// the call already failed — and indexing it would key every non-scalar impl
	// of one interface method under kindInvalid, colliding them all and
	// overwriting each one's real refusal reason with "ambiguous stdlib impl".
	// That is a report about this table rather than about the program, and
	// TestEveryScalarHostFnIsBoundOrDeclared would read calendar and decimal
	// externs as scalar-and-unbound when they are non-scalar.
	// kindInvalid: no-position — indexes the registry by receiver kind; no Nomi position.
	if f.iface != "" && f.selfIdx == 0 && len(f.params) > 0 && f.params[0] != kindInvalid {
		key := f.iface + "." + f.name
		byRecv := x.byIface[key]
		if byRecv == nil {
			byRecv = map[kind][]*stdFunc{}
			x.byIface[key] = byRecv
		}
		recv := f.params[0]
		byRecv[recv] = addOverload(byRecv[recv], f, "ambiguous stdlib impl")
	}
}

// addOverload appends f to a spelling's overload set, refusing every member of
// a pair a call site could not tell apart.
//
// The discriminator is the full PARAMETER-KIND signature and nothing else,
// because that is precisely what a call site supplies. Two declarations with
// different parameter kinds are selectable and neither is at risk; two with the
// same ones are not, and both stay refused.
//
// Return types are deliberately EXCLUDED from the discriminator. Nomi has no
// return-type overloading, so two declarations differing only in result are
// indistinguishable at the call site. Adding the result would silently admit
// such a pair and route to whichever arrived first.
//
// # INHERENT BEATS INTERFACE-IMPL, and this is the FRONT END'S rule rather than
// # a preference of this table
//
// `analysis/type_method_identity.go`'s preferTypeMethodSymbol says it in these
// words: "Inherent (`impl Type { pub fn f }`) beats interface-impl
// (`impl Iface for Type { fn f }`), the same way the per-file table resolves
// the clash. Not a duplicate: the source said which one wins." So it is stated
// TWICE in the analyzer, and the contest is not an ambiguity at all — one side
// wins on merit.
//
// std/bytes declares `Bytes.to_string` twice for one receiver: inherent
// `pub host fn to_string(data: Bytes): Result<String, String>`, and
// `impl Display for Bytes` returning `String`. Both take exactly `(Bytes)`.
// Marking both ambiguous would clear `rtCall` (markAmbiguous does) and refuse
// a call the analyzer resolves without hesitating; the tour's `Bytes` chapter
// (`scalars-and-strings.md`) destructures the `Result` only the inherent
// declaration returns.
//
// THE LOSER IS STILL REFUSED, and that is the precedence rule and not a
// shortfall. "Inherent wins" says which declaration `Type.method` selects; it
// says nothing about making the interface half lowerable, and the two share one
// `key` (stdKey has no interface component for a non-generic impl), so letting
// both settle would put two functions under one name. The loser's reason is its
// own rather than the ambiguity key, because a declaration that lost a
// precedence contest is not one nobody can choose between.
//
// Exactly ONE spelling in the whole stdlib has an inherent/interface-impl
// collision, and it is `Bytes.to_string`. The other two
// same-signature collisions are same-class (`Error.inspect` and
// `Error.to_string`, each two modules' `Error`) and are untouched by this rule.
func addOverload(fs []*stdFunc, f *stdFunc, reason string) []*stdFunc {
	out := append(fs, f)
	// The whole GROUP f joins, not the pairs it forms. A pairwise walk gets the
	// two-member case right and would decide a three-member one arrival by
	// arrival, which is how a rule and its guard drift into agreeing about
	// nothing — stdPrecedenceWinner is the single predicate both use.
	var group []*stdFunc
	for _, g := range out {
		if sameParamKinds(g.params, f.params) {
			group = append(group, g)
		}
	}
	if len(group) < 2 {
		return out
	}
	if winner := stdPrecedenceWinner(group); winner != nil {
		for _, g := range group {
			if g != winner {
				markAmbiguous(g, shadowedByInherent)
			}
		}
		return out
	}
	for _, g := range group {
		markAmbiguous(g, reason)
	}
	return out
}

// shadowedByInherent names the interface-impl half of a contest the inherent
// declaration won. A refusal rather than silence because both halves stay in
// the overload set and stdPick must be able to answer for either.
const shadowedByInherent = "stdlib interface impl shadowed by an inherent method"

// stdPrecedenceWinner is the member of a same-signature group that the SOURCE
// decided, or nil when the group is a genuine ambiguity.
//
// Exactly one shape is decided: one INHERENT declaration against one or more
// interface-impl siblings. Anything else — two inherents, two impls, or a group
// with no inherent at all — is undecidable at the call site and every member
// stays refused.
//
// One implementation shared by addOverload's arm and by
// TestCollapsedStdlibKeysCostOnlyCoverage's assertion, so the guard cannot
// certify a rule different from the one the builder applies. Writing the
// predicate twice is how the two halves of a rule drift into agreeing about
// nothing.
func stdPrecedenceWinner(group []*stdFunc) *stdFunc {
	var inherent *stdFunc
	for _, f := range group {
		if f.iface != "" {
			continue
		}
		if inherent != nil {
			return nil
		}
		inherent = f
	}
	if inherent == nil || len(group) < 2 {
		return nil
	}
	return inherent
}

func sameParamKinds(a, b []kind) bool {
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

// stdPick selects the member of an overload set whose parameter kinds are
// exactly `args`.
//
// A set of one answers unconditionally, so for a spelling std declares once
// stdlibInvoke's own `call arity` and
// `argument type mismatch` refusals stay the report for a bad call, rather than
// being replaced by a silent miss here.
//
// A set of several with no exact match answers nil, and the caller reports.
//
// SEVERAL EXACT MATCHES ARE NOT ALL EQUAL, and this loop is not
// order-dependent. addOverload implements the front end's rule that an INHERENT
// declaration beats an interface-impl one for the type-qualified spelling, so
// this has to select the winner rather than the first arrival — the losing half
// stays in the set and answering with it would refuse a call the analyzer
// resolves. Relying on declaration order would happen to work (std/bytes
// declares `impl Bytes` above `impl Display for Bytes`) and is exactly the
// "picking one by map order" add()'s own header rules out. Two matches of the
// SAME class are still refused by addOverload, so
// returning either is safe: stdlibInvoke rejects it by its mark.
func stdPick(fs []*stdFunc, args []kind) *stdFunc {
	if len(fs) == 1 {
		return fs[0]
	}
	var match *stdFunc
	for _, f := range fs {
		if !sameParamKinds(f.params, args) {
			continue
		}
		if match == nil || (match.iface != "" && f.iface == "") {
			match = f
		}
	}
	return match
}

// stdSole is the declaration a spelling names when it names exactly one, for the
// two callers that have no argument kinds to select with.
func stdSole(fs []*stdFunc) *stdFunc {
	if len(fs) == 1 {
		return fs[0]
	}
	return nil
}

// underlay adds every spelling `earlier` resolves that this index does not,
// leaving this index's own entries untouched.
//
// It is deliberately NOT `add`. add marks a duplicate key AMBIGUOUS on both
// entries, which is right for two declarations inside one module and wrong here:
// a module's own `Instant.inspect` is not in competition with another module's
// `Duration.inspect`, and a cross-module name that collided with a local one
// must lose silently rather than poison the local entry's refusal reason.
// Locals win, which is also the resolution rule Nomi itself applies.
//
// byFile is deliberately not merged. A std gen's local index has a byFile map,
// so `bindStdSiblings` files this module's own `pub` free functions and
// `compiler.check(...)` inside std/compiler resolves. What is declined is the
// CROSS-MODULE spelling — `timer.sleep(...)` written inside some other stdlib
// module — which would admit a reference under a route nothing exercises.
func (x *stdlibIndex) underlay(earlier *stdlibIndex) {
	if earlier == nil {
		return
	}
	// The earlier slice is ALIASED rather than copied, and that is sound
	// because underlay runs after every local add and nothing appends to a
	// local set afterwards. An append on the earlier index for a later module
	// writes past this alias's length, so it cannot be seen here.
	for key, fs := range earlier.byType {
		if len(x.byType[key]) == 0 {
			x.byType[key] = fs
		}
	}
	for key, byRecv := range earlier.byIface {
		mine := x.byIface[key]
		if mine == nil {
			mine = map[kind][]*stdFunc{}
			x.byIface[key] = mine
		}
		for recv, fs := range byRecv {
			if len(mine[recv]) == 0 {
				mine[recv] = fs
			}
		}
	}
	for module, pkg := range earlier.modulePkg {
		if _, mine := x.modulePkg[module]; !mine {
			x.modulePkg[module] = pkg
		}
	}
	// An earlier module's `once` bindings, so a later module's body may read
	// `Int.max_value`. The local index has none of its own: this module's are on
	// the gen (bindStdOnces) rather than here, because they take no part in the
	// phase split bindStdSiblings rebuilds `local` for. See stdonce.go.
	if x.byOnce == nil && len(earlier.byOnce) > 0 {
		x.byOnce = map[string]*stdOnce{}
	}
	for key, o := range earlier.byOnce {
		if _, mine := x.byOnce[key]; !mine {
			x.byOnce[key] = o
		}
	}
}

// markAmbiguous refuses a function that two declarations answer to, WITHOUT
// overwriting a refusal it already had.
//
// The precedence matters: `stdcalendar.Date.add` is declared twice
// (`impl Add<Duration, Date>` and `impl Add<Period, Date>`) and it is also
// non-scalar, so clobbering its reason would make it report as
// ambiguous-but-otherwise fine, which the coverage guard would read as a
// scalar extern nobody had bound.
// The specific reason is the useful one; ambiguity is only news for a function
// that would otherwise have lowered.
func markAmbiguous(f *stdFunc, why string) {
	f.rtCall, f.rtHostPkg, f.body = "", "", false
	if f.why == "" {
		f.why = why
	}
}

// --- lowering one stdlib module -------------------------------------------

// stdCandidate is one declaration collected from a stdlib module before its
// lowerability is known.
//
// The CANDIDATE POINTER is the identity, and the fixed points are keyed on it
// rather than on any string. `sib` is a SPELLING and spellings collapse: all
// eleven `impl Add<X, NaiveDateTime>` blocks spell `NaiveDateTime.add`, so a
// `map[string]bool` of settled functions answers for the GROUP when it is asked
// about a MEMBER. stdKey carries the type arguments for the same reason. A
// spelling-keyed map is unsound in the direction that matters: one bound rung
// (`Add<Duration, NaiveDateTime>`, whose rtCall seeds the least fixed point)
// would mark the other ten settled, so bindStdSiblings would offer a name for
// a body that then fails to emit while a SIBLING already references it. See
// lowerStdlibModule.
type stdCandidate struct {
	f  *stdFunc
	fd *ast.FuncDef
	// sib is the unqualified spelling a sibling in this module calls it by —
	// `<recv>.<name>`, or `.<name>` for a top-level function. Used ONLY for
	// gen.stdSiblings, which is the unqualified-name path and is genuinely
	// keyed on what a caller writes. NEVER an identity: see above.
	sib string
}

// lowerStdlibModule emits one stdlib module's lowerable scalar functions as one
// Go package, and returns every function it saw — lowered or refused — so call
// sites can resolve or refuse by name.
//
// # Two fixed points, because one of them cannot express recursion
//
// The lowering order is settled by a MONOTONE fixed point rather than one pass.
// A Nomi body may call a sibling (`impl Debug for Float`'s `inspect` calls
// `to_string`; `Float.finite?` calls `Float.infinite?`), so round 1 offers only
// the host siblings, round 2 adds the bodies that lowered in round 1, and so on.
// Growing the sibling set can only turn a refusal into a success, never the
// reverse — which is what makes the loop terminate without having to invalidate
// anything, and what makes the final lowering pass safe: every function it
// lowers has already lowered against exactly the sibling set it will see.
//
// A pass that lowered optimistically instead would call a sibling that a later
// refusal removed.
//
// The property this buys is an INVARIANT with a guard, not a comment:
// TestStdlibEmittedCallsAreDefined asserts over the emitted TEXT that every
// NomiStd_ name a generated package references is defined — in that package, or
// in the package its reference qualifies.
//
// That fixed point is a LEAST one, and it is STRATIFIED. The sibling table a
// round offers is
// snapshotted by newStdGen at the top of the round, so a body can only reach
// functions that settled in a strictly EARLIER round. The dependency relation
// among its members is therefore acyclic by construction, so recursion never
// arises inside it.
//
// The same stratification is why it can never offer a function to its own body:
// `settled[c]` is false for the whole of the round in which c is tried. So a
// SELF-RECURSIVE std function cannot settle in phase 1; `bytes.join_debug_parts`,
// whose body is `head + ", " + join_debug_parts(tail)`, is one. A
// name-matching call graph would over-report recursion: `Display.to_string(inner)`
// inside `derive Display for Bool` resolves by the ARGUMENT's kind to a
// different receiver's method, not to itself.
//
// # Phase 2: the recursive closure, and why it terminates
//
// stdRecursiveClosure runs a GREATEST fixed point over what phase 1 left. It
// offers every remaining candidate at once and then REMOVES the ones that do not
// lower, repeating until a round removes nothing. Written as F(S) = { c in S : c
// lowers when offered S }:
//
//   - F(S) is a subset of S by construction, so the sequence S, F(S), F(F(S)),
//     … is decreasing over a FINITE candidate set: it terminates in at most
//     |S|+1 rounds. Termination does not rest on monotonicity at all, which is
//     the difference between this and "assume it settles".
//   - The loop stops only at a genuine fixed point F(S*) = S*: every surviving
//     member has lowered against EXACTLY the sibling set the lowering pass will
//     offer it. So a lowered body cannot call a sibling that did not lower.
//   - A member whose body genuinely depends on an UNSETTLEABLE sibling is
//     removed, not masked. The unsettleable one fails in round 1 (it fails even
//     with everything offered), so it is gone from S1; anything that needed it
//     fails against S1 and is gone from S2; and so on. Removal propagates
//     transitively, which is the property that separates this from relaxing the
//     criterion.
//   - Because F is monotone — a wider sibling set can only turn a refusal into a
//     success — S* is the GREATEST fixed point below the starting set, so the
//     answer does not depend on the order candidates are visited in. Within one
//     round the decisions are all taken against the round's snapshot, so it does
//     not depend on order either.
//
// Phase 1's members are emitted against phase 1's sibling set and phase 2's
// against the enlarged one, rather than both against the enlarged one. Two
// reasons, and neither is caution for its own sake. Phase 2 cannot change
// phase 1's output, so no function phase 1 lowers can stop lowering; and it
// confines every cycle to phase 2's members,
// because a phase-1 body's emitted text cannot name a phase-2 function. Nothing
// is lost by it: a phase-1 member already lowered, so there is no refusal inside
// it for a wider sibling set to fix.
//
// `earlier` is the index of every module lowered BEFORE this one, and it is what
// admits a cross-module reference. See buildStdlibIndex for why "earlier" rather
// than "any" is the whole of the acyclicity argument. The third return value is
// the generated stdlib packages this module's emitted text imports.
// hosts is the same for the HOST packages outside the generated module.
// Reachable in principle — a stdlib
// module's Nomi body may call a `host fn` in another module — and empty in
// practice, which is why it is returned rather than assumed empty: the alternative is a
// generated import with no require behind it, and Generate cannot see it.
// The `once` bindings this module declares come back beside the functions, for
// the reason they go INTO the index at all: a later module's body may read
// `Int.max_value`. See stdonce.go.
//
// `v` is the module's lowering context, built by the caller through
// stdModuleContext. It is built by the caller because buildStdlibIndex retains
// it on the index, so a per-program instance of a generic declaration can be
// emitted against the SAME context this function emitted `mod.go` from.
// Re-deriving it there would call std.Load() again; see stdlibIndex.views.
//
// MarkTailCalls is stdModuleContext's first line, and its own header carries why:
// std.Load() runs the sweeps and CheckTypes but never MarkTailCalls, so
// `Call.IsTailCall` is FALSE on every stdlib node this builder reads and any
// tail-position reasoning about a stdlib body is VACUOUS without it.
// TestStdSettling_TailMarksAreNotAssumed
// hands the pipeline an unmarked tree on purpose.
func lowerStdlibModule(v *stdModuleView, earlier *stdlibIndex) (funcs []*stdFunc, onces []*stdOnce, irModule *ir.Module) {
	module, pkg := v.module, v.pkg
	cands, onces, settled, withCycles := v.cands, v.onces, v.settled, v.withCycles

	g := v.gen(earlier)
	// The bindings before the bodies, because a body may read one and the text
	// then reads in the order the declarations are written. An emitted binding
	// counts as output on its own: a module with a lowerable `once` and no
	// lowerable function still has a package to write, and returning an empty
	// Source for it would leave every reference naming a symbol nobody emitted.
	emitted := false
	for _, o := range onces {
		if !o.settled {
			continue
		}
		g.emitStdOnce(o)
		emitted = true
	}
	emittable := make(map[*stdCandidate]bool, len(settled)+len(withCycles))
	for c := range settled {
		emittable[c] = true
	}
	for c := range withCycles {
		emittable[c] = true
	}
	g.irPrebuildStdBodies(module, pkg, cands, emittable, earlier)
	g.irStdTailCycles(cands, emittable)
	// Every body sees every emitted sibling: the graphs irPrebuildStdBodies
	// left in the module may link to a member of a recursive cycle from any
	// body. The arity-reduced bodies are built inside the same window,
	// because a default expression is Nomi written in this module and may
	// reach a sibling exactly as the body may.
	//
	// Bound ONCE for the loop. Lowering a body changes its candidate's
	// body (to the value the view already holds) and, through the canonical
	// pointer every reader goes through, arityMin, defaultFill, irArity and
	// irSlot; the refusal arm below changes body, pkg and why, and a fresh
	// binding would differ from this one only in the view's why, which the
	// arm writes to the view as well.
	restoreSiblings, views := g.bindStdSiblingViews(module, pkg, cands, emittable, earlier)
	for _, c := range cands {
		if c.fd == nil || c.f.why != "" {
			continue
		}
		if !emittable[c] {
			c.f.why = "unlowered stdlib function"
			continue
		}
		if !g.emitStdBody(c.f, c.fd, pkg) {
			c.f.body, c.f.pkg = false, ""
			// The fixed points are sound, so this is unreachable — and it is
			// treated as a refusal rather than trusted because the alternative
			// is emitting a call to a function that is not there. Note what it
			// does NOT do: a sibling emitted EARLIER in this loop against the
			// same settled set may already have written a reference to c, and
			// setting `why` here cannot retract that text. So this arm is a
			// last resort and not a safety net; the invariant has to hold
			// upstream, which is why `settled` is keyed on the candidate.
			c.f.why = "unlowered stdlib function"
			if v := views[c]; v != nil {
				v.why = c.f.why
			}
			continue
		}
		emitted = true
	}
	restoreSiblings()

	g.stdUnloweredWrappers(cands)
	v.unlowered = g.stdUnlowered

	funcs = make([]*stdFunc, 0, len(cands))
	for _, c := range cands {
		if c.fd != nil && g.irMod != nil {
			c.f.irBody = g.irMod.FuncFor(g.irCalleeSym(c.fd, c.f.key))
			g.irRecordStdDisplay(c.f)
			if !isSynthesized(c.fd) {
				g.irRecordStdKey(c.f)
			}
		}
		funcs = append(funcs, c.f)
	}
	if !emitted {
		// `onces` is returned here too, and not nilled out: a SETTLED binding
		// implies emitted, so every member of this slice is refused — and its
		// refusal has to reach the index or a reference site reports the generic
		// `type-qualified reference` instead of the binding's own reason.
		return funcs, onces, nil
	}
	return funcs, onces, g.irMod
}

// collectStdCandidates walks a stdlib module's declarations and records every
// function it declares, with the signature check applied but no body inspected.
//
// The receiver and the interface are read with analysis.TypeExprBaseName, the
// analyzer's own reading of an impl header. A reading of *ast.SimpleType alone
// would answer "" for a GENERIC receiver (`impl Equatable for List<T>`, and
// every impl block on `Map<K, V>`, `Set<T>`, `Maybe<T>`, `Result<T, E>`,
// `Vector<T>`, `Range<T>`) and file its methods under the shape of a FREE
// function, reachable under no spelling a call site can write. One
// implementation also answers a QUALIFIED receiver
// (`impl Debug for Probe.Reading`).
//
// The resulting key is `lists.List.head`, the documented host-key convention
// (`int.Int.to_string`, `iter.MapIter.next`) and the key
// `lookupImplBlockExtern` builds. A generic receiver's methods are generic, so
// they refuse under their own name at their own call site.
func collectStdCandidates(module string, nodes []ast.Node, anchors stdAnchors) []*stdCandidate {
	var out []*stdCandidate
	for _, n := range nodes {
		switch t := n.(type) {
		case *ast.FuncDef:
			out = append(out, stdCandidateFor(module, "", "", "", t.Name, t.Params, t.ReturnTypeExpr, t.Public,
				len(t.TypeParams) > 0 || len(t.WhereClauses) > 0, len(t.Decorators) > 0, t, anchors))
		case *ast.ExternFunc:
			out = append(out, stdCandidateFor(module, "", "", "", t.Name, t.Params, t.ReturnTypeExpr, t.Public,
				len(t.TypeParams) > 0 || len(t.WhereClauses) > 0, false, nil, anchors))
		case *ast.ImplBlock:
			recv := analysis.TypeExprBaseName(t.Receiver)
			iface := analysis.TypeExprBaseName(t.Interface)
			ifaceKey := stdImplKey(t.Interface)
			implGeneric := len(t.Generics) > 0 || len(t.WhereClauses) > 0
			// The type parameters the GENERIC RECEIVER contributes, which the
			// line above cannot see and the front end always could.
			//
			// `*ast.ImplBlock.Generics` is POPULATED for a derive-synthesized
			// block (`derive Debug for Channel<T>`) and EMPTY for a hand-written
			// one (`impl Hashable for Map<K, V>`), so `implGeneric` alone
			// answers correctly for exactly the half that does not matter.
			// `analysis.ReceiverTypeParamNames` is the front end's own rule,
			// shared rather than reimplemented; see its header.
			recvTP := map[string]bool{}
			if anchors.declared != nil {
				for _, n := range analysis.ReceiverTypeParamNames(t.Receiver, anchors.declared) {
					if n != "" {
						recvTP[n] = true
					}
				}
			}
			blocked := recv == ""
			// A DERIVE-SYNTHESIZED block's signatures resolve their type names
			// through the COMPILER-KNOWN route rather than through this file's
			// imports, so they see a wider anchor set. `SynthOriginLine` is the
			// AST's own marker — "Zero on every hand-written block" — rather
			// than an inference from the shape. See stdEnumSynthAnchors.
			at := anchors
			if t.SynthOriginLine != 0 {
				at = anchors.forSynthesized()
			}
			for _, item := range t.Items {
				switch it := item.(type) {
				case *ast.FuncDef:
					out = append(out, stdCandidateFor(module, recv, iface, ifaceKey, it.Name, it.Params, it.ReturnTypeExpr, it.Public,
						implGeneric || len(it.TypeParams) > 0 || len(it.WhereClauses) > 0 ||
							sigNamesTypeParam(it.Params, it.ReturnTypeExpr, recvTP),
						blocked || len(it.Decorators) > 0, it, at))
				case *ast.ExternFunc:
					out = append(out, stdCandidateFor(module, recv, iface, ifaceKey, it.Name, it.Params, it.ReturnTypeExpr, it.Public,
						implGeneric || len(it.TypeParams) > 0 || len(it.WhereClauses) > 0 ||
							sigNamesTypeParam(it.Params, it.ReturnTypeExpr, recvTP), blocked, nil, at))
				}
			}
		case *ast.InterfaceDef:
			// A `host fn` DEFAULT declared inside an interface. Without this
			// arm it would be absent from the index outright and every call to
			// it would fall into the undifferentiated `qualified call` bucket.
			// `Struct.update` — `interface Struct { host fn
			// update(original: self, updates: Partial<self>): self }` in
			// std/structs.nomi — is the one instance std carries.
			//
			// The interface NAME goes in the receiver slot, which is what makes
			// the key `structs.Struct.update`, the host key of exactly this
			// declaration; the iface slot stays empty on purpose,
			// because `add` reads it as "this is one receiver's impl of an
			// interface method" and a host default is the implementation for
			// EVERY receiver, not one.
			//
			// Externs only, and the exclusion is load-bearing rather than
			// scoping. A Nomi-bodied default is a FALLBACK an `impl` may
			// override, so filing it under `byType["Debug.inspect"]` would
			// claim it is the answer — and stdlibCall consults byType before
			// byIface, so `Debug.inspect(42)` would stop resolving to
			// `NomiStd_Int_Debug_inspect` and start refusing because `self` is
			// not a scalar.
			for i := range t.Methods {
				m := &t.Methods[i]
				if !m.Extern {
					continue
				}
				out = append(out, stdCandidateFor(module, t.Name, "", "", m.Name, m.Params, m.ReturnTypeExpr,
					t.Public, len(t.TypeParams) > 0 || len(t.WhereClauses) > 0 ||
						len(m.TypeParams) > 0 || len(m.WhereClauses) > 0, false, nil, anchors))
			}
		}
	}
	return out
}

func stdCandidateFor(module, recv, iface, ifaceKey, name string, params []ast.Param, ret ast.TypeExpr, pub, generic, blocked bool, fd *ast.FuncDef, anchors stdAnchors) *stdCandidate {
	f := &stdFunc{
		key:      stdKey(module, recv, ifaceKey, name),
		ifaceKey: ifaceKey,
		module:   module,
		recv:     recv,
		iface:    iface,
		name:     name,
		pub:      pub,
		selfIdx:  -1,
		result:   kindUnit,
		decl:     fd,
	}
	defaulted := 0
	for i, p := range params {
		k, headName := stdParamKind(p, anchors)
		f.params = append(f.params, k)
		// Same loop as params, so the two vectors cannot get out of step: a
		// call site indexes one by the slot it read from the other.
		if p.Destructure != nil {
			f.paramNames = append(f.paramNames, "")
		} else {
			f.paramNames = append(f.paramNames, p.Name)
		}
		if recv != "" && f.selfIdx < 0 && headName == recv {
			f.selfIdx = i
		}
		// kindInvalid: no-position — classifies a stdlib declaration into f.why; no Nomi position.
		if k == kindInvalid {
			blocked = true
		}
		// A DEFAULT is counted, not folded into `blocked`: a default says
		// nothing about the SIGNATURE. `stdcalendar.DateTime.in_zone` projects
		// the IDENTICAL signature to `in_zone_resolve` beside it —
		// [NaiveDateTime, String, Disambiguation] -> Result<DateTime, Error> —
		// which LOWERS through rt.ResolveInZone.
		//
		// Only a TRAILING run counts, because that is the only run a
		// POSITIONAL call can leave empty: with `f(a, b = 1, c)` there is no
		// arity that omits `b`, and a named argument — the other way to omit
		// one — is refused outright (`named argument`). So a default in the
		// middle contributes nothing and must not be counted, or a wrapper
		// would be emitted for an arity no call site can spell.
		if p.Default != nil {
			defaulted++
		} else {
			defaulted = 0
		}
	}
	f.arityMin = len(params)
	if ret != nil {
		f.result = stdTypeKind(ret, anchors)
		// kindInvalid: no-position — classifies a stdlib declaration into f.why; no Nomi position.
		if f.result == kindInvalid {
			blocked = true
		}
	}
	c := &stdCandidate{f: f, fd: fd, sib: recv + "." + name}
	switch {
	case generic:
		// Kept apart from the subset key, and the split is not cosmetic: a
		// generic std function is not waiting on a representation, it is
		// waiting on the type-parameter DISPATCH dictionary — calling an
		// interface method on a type parameter. `io.inspect<T>` and
		// `testing.check<T>(subject: T): Result<T, AssertionFailure>` are the
		// reachable instances, and reporting them as "outside the scalar
		// subset" would say a representation is missing when the representation
		// is not the problem.
		f.why = "stdlib generic function"
	case blocked:
		f.why = "stdlib function outside the scalar subset"
	case fd == nil && defaulted > 0:
		// A `host fn` whose declared default THIS BUILDER MAY NOT READ, and the
		// reason is which side owns the value.
		//
		// For a Nomi body the declaration IS the authority: the default is
		// evaluated from that same declaration wherever the body is called. For
		// a `host fn` there is no Nomi body to carry it: the call crosses into a
		// Go function, which has no defaults. So the declared default is
		// documentation of whatever the Go implementation does with a short
		// argument list, and a builder filling from the declaration would agree
		// with a second encoding by luck.
		//
		// Refused rather than bound, and refused at the DECLARATION rather than
		// at a short call, which is deliberate: it also forecloses a registry
		// row for one of these, because an rt function has no defaults either
		// and a bound row would make a legal short call report the false
		// `call arity`.
		//
		// Letting a differential test watch the two encodings agree would measure
		// nothing. A declared default whose value is the ZERO VALUE makes "read
		// the declaration", "pad with Go zeros" and "drop the trailing
		// arguments" produce byte-identical output, and std carries no `host
		// fn` whose default is not the zero value. So the answer is one owner
		// for the value: a Nomi body carrying the defaults over a
		// module-private `host fn` with none, as `NaiveDateTime.new` over
		// `new_exact` and `DateTime.in_zone` over `in_zone_resolve` are.
		//
		// THE ARM HAS NO STD MEMBER, and the guard is
		// TestDefaultedHostFnArmStillEvaluates rather than a corpus file.
		// `case generic:` and `case blocked:` PRECEDE this arm, so an arm's
		// real population is what reaches it, not what fits it.
		//
		// Walled by ARCHITECTURE rather than by unwritten work, which is why
		// the check stays: an extern is a crossing into a Go function, and that
		// carries no defaults by construction. Any future `pub host fn` in std
		// that grows a trailing default lands here, and the answer will be the
		// same restructure rather than a fill.
		f.why = "stdlib host function with a defaulted parameter"
	case fd == nil:
		// A `host fn`: retained when internal/stdlibbindings answers it. See
		// the note above hostFn.
		if h, mapped := stdlibHostFuncs[f.key]; mapped {
			if !stdSignatureMatches(h, f) {
				f.why = "stdlib host function"
				break
			}
			f.rtCall, f.rtHostPkg, f.rtFrame = h.call, h.pkg, h.takesFrame
			break
		}
		if b, ok := externBindingFor(f.key); ok {
			call, pkg := hostCallName(reflect.ValueOf(b.Fn))
			if call == "" {
				f.why = "stdlib host function"
				break
			}
			f.rtCall, f.rtHostPkg = call, pkg
			break
		}
		if compilerHostNames()[f.key] {
			f.rtCall, f.rtHostPkg = "compilerhosts."+strings.TrimPrefix(f.key, "compiler."), compilerHostsPath
			break
		}
		f.why = "stdlib host function"
	case fd.Body == nil:
		f.why = "stdlib function without a body"
	}
	// std/compiler gets no key of its own here. TestRuntimeArtifactLinksNoFrontEnd
	// (internal/vm) walks rt and the VM and asserts THEY reach no front end;
	// a std/compiler call crosses into internal/compilerhosts, which that guard
	// does not walk. So each std/compiler declaration is retained or declined
	// under the same rules as any other `host fn`.
	return c
}

// stdKey is the host key for a stdlib declaration.
//
// `<module>.<recv>.<name>` for an inherent method or a non-generic interface
// impl, and `<module>.<recv>.<Iface<Args>>.<name>` when the interface is
// INSTANTIATED, e.g. `stdcalendar.NaiveDateTime.Add<Duration, NaiveDateTime>.add`.
//
// # Why only an instantiated interface
//
// A non-generic interface can be implemented for a receiver at most once, so
// the instantiation would add no information and every registry row keeps the
// documented `file.Type.function` spelling. A GENERIC one can be implemented
// once per instantiation: `stdcalendar.NaiveDateTime.add` is ELEVEN
// declarations, one `impl Add<X, NaiveDateTime>` per unit, which a key without
// the instantiation would collapse into one. Generic interface instantiations
// use the full impl identity.
func stdKey(module, recv, ifaceKey, name string) string {
	if recv == "" {
		return module + "." + name
	}
	if ifaceKey == "" {
		return module + "." + recv + "." + name
	}
	return module + "." + recv + "." + ifaceKey + "." + name
}

// stdImplKey is the interface instantiation an impl block names, or "" when the
// interface takes no type arguments.
//
// `n.Interface.TypeString()`, taken only for the instantiated case, so the
// string comes from one implementation in ast.GenericType and cannot drift.
func stdImplKey(te ast.TypeExpr) string {
	switch t := te.(type) {
	case *ast.GenericType:
		if len(t.Params) == 0 {
			return ""
		}
		return t.TypeString()
	case *ast.QualifiedType:
		// `impl render.Add<Int, Point> for Point`. The qualifier belongs in the
		// key, which takes TypeString() of the whole expression; the member decides whether there is anything to
		// qualify at all.
		if stdImplKey(t.Member) == "" {
			return ""
		}
		return t.TypeString()
	}
	return ""
}

// stdSignatureMatches reports whether a registry row agrees with the declaration
// the stdlib actually carries. A mismatch refuses rather than lowering: the row
// is the builder's belief about a signature and the declaration is the fact.
func stdSignatureMatches(h hostFn, f *stdFunc) bool {
	if len(h.params) != len(f.params) || h.result != f.result {
		return false
	}
	for i := range h.params {
		if h.params[i] != f.params[i] {
			return false
		}
	}
	return true
}

// stdParamKind is a stdlib parameter's kind and the type name its pattern head
// names, for the receiver test.
//
// A parameter is written one of two ways in std, and both have to answer here.
// An ANNOTATED one (`rhs: Duration`) reads its annotation. A DESTRUCTURING one
// (`Duration.as_nanos(Duration(ns))`, and every operator impl in std/duration
// and std/instant) carries no annotation at all: the pattern's head IS the
// annotation, which is sugar.go's self-typing rule read without a gen.
//
// Only a head that names a type directly is served, because that is the only
// shape std uses for a receiver — a tuple or anonymous-struct pattern has no
// head to read and answers kindInvalid, so the function is refused rather than
// guessed at.
func stdParamKind(p ast.Param, anchors stdAnchors) (kind, string) {
	if p.TypeAnnotation != nil {
		return stdTypeKind(p.TypeAnnotation, anchors), stdHeadTypeName(p.TypeAnnotation)
	}
	if p.Destructure == nil {
		return kindInvalid, ""
	}
	var head ast.TypeExpr
	switch pat := p.Destructure.(type) {
	case *ast.StructPattern:
		head = pat.TypeName
	case *ast.EnumPattern:
		head = pat.Variant
	}
	name := simpleTypeName(head)
	if name == "" {
		return kindInvalid, ""
	}
	// A destructuring parameter's kind is the head's type, and the head is
	// resolved through the same anchored table an annotation is: nothing else
	// is admitted, so `Point{x, y}` in a hypothetical std module refuses rather
	// than resolving against a declaration this pass cannot see.
	//
	// Opaques only, and deliberately not every stdAnchors family: a MONOMORPHIC
	// stdlib enum has none but bare variants, so `Ordering(x)` is not a pattern
	// the front end accepts and admitting one here would be a phantom.
	if i, anchored := anchors.opaques[name]; anchored {
		return named(opaqueDefs()[i]), name
	}
	return kindInvalid, name
}

// stdTypeKind is the kind a stdlib type annotation names: a scalar, one of the
// non-scalar types this module's analysis anchored (stdAnchors), or a STRUCTURAL
// type over those.
//
// Deliberately NOT gen.typeOf. typeOf resolves a module's declared types too,
// and admitting one here would produce a kind whose identity is a *typeDef
// belonging to a different generated package — which is the boundary the whole
// scalar rule exists to hold. The exceptions are not a relaxation of that rule,
// they are cases where the boundary is not crossed at all: an anchored type's
// *typeDef is process-wide and its Go type is declared in rt, and a structural
// type over package-neutral components has a process-wide *compKind and renders
// to the same Go text everywhere. See opaque.go, stdenum.go and sharedcomp.go.
func stdTypeKind(te ast.TypeExpr, anchors stdAnchors) kind {
	// kindInvalid: lookup — scalarKind's "not one of the five", so fall through.
	if k := scalarKind(te); k != kindInvalid {
		return k
	}
	// A NESTED std type name — `Json.DecodeError`, `Json.ShapeError`, `Json.Case`
	// — which the parser gives as an *ast.QualifiedType and not as a SimpleType,
	// so `simpleTypeName` below answers "" for it and every lookup would miss.
	// Without this arm `Json.decode`'s `Result<Json, Json.DecodeError>` would
	// refuse with both types anchored, because nothing would ask for the anchor
	// under the spelling std wrote.
	//
	// A NAME lookup, exactly as the bare arms below are, and it inherits their
	// identity argument rather than weakening it: each anchor was established by
	// the (origin, name) rule against a validated declaration, so the map holds
	// only names std really declares. A MODULE-qualified spelling (`io.Reader`)
	// has the same syntax and simply misses, which is the right answer.
	if nested := stdNestedTypeName(te); nested != "" {
		if i, anchored := anchors.structs[nested]; anchored {
			return named(stdStructDefs()[i])
		}
		if i, anchored := anchors.enums[nested]; anchored {
			return named(stdEnumDefs()[i])
		}
		// Nothing else, deliberately. An opaque newtype, a `host type` and a
		// generic std struct are all declared with UNDOTTED names in std,
		// so an arm for one would be a path with no member — and the two above
		// are the two families that really do carry a dotted row.
		return kindInvalid
	}
	name := simpleTypeName(te)
	if i, anchored := anchors.opaques[name]; anchored {
		return named(opaqueDefs()[i])
	}
	if i, anchored := anchors.enums[name]; anchored {
		// A monomorphic stdlib enum: `Int.compare(a: Int, b: Int): Ordering`.
		// Admitted on the same terms as an opaque newtype and for the same
		// reason — its *typeDef is process-wide and its Go type is declared in
		// rt, so it belongs to no generated package and crossing the boundary
		// costs nothing. See stdenum.go.
		return named(stdEnumDefs()[i])
	}
	if i, anchored := anchors.structs[name]; anchored {
		// A stdlib opaque STRUCT: `DateTime.with_zone(d: DateTime, zone:
		// String): Result<DateTime, Error>`. The same terms once more — a
		// process-wide *typeDef over a Go struct rt declares, every field a
		// scalar so nothing about it is package-relative. See stdstruct.go.
		return named(stdStructDefs()[i])
	}
	if i, anchored := anchors.hosts[name]; anchored {
		// A stdlib `pub host type`: `Bytes.length(data: Bytes): Int`. The same
		// terms a fourth time — a process-wide *typeDef over a Go type rt
		// declares, and a LEAF, so it has no components to be
		// package-relative. See stdhost.go.
		return stdHostKind(i)
	}
	if i, anchored := anchors.ifaces[name]; anchored {
		// A stdlib INTERFACE in a type position: `Toml.from_fragments(
		// fragments: List<Fragment<Display>>): Toml`. Admitted on the same
		// terms as the three families above and for the same reason — the
		// *ifaceDef is process-wide, the value renders `rt.Dyn` in every
		// generated package, and the dispatch tables are variables in rt. See
		// stdiface.go.
		//
		// It is `rt.Dyn` for EVERY interface, so the intern tables must not key
		// on that spelling, or two anchored interfaces reaching one structural
		// constructor would collapse. The identity travels in the components.
		return stdIfaceKind(i)
	}
	// A TUPLE — `random.below_state(...): (Int, Int)`. Placed before the
	// GenericType block because a tuple is spelled neither nominally nor
	// generically: the parser gives it as an *ast.FuncType with a nil Return.
	// Admitted on the List and Map arms' terms; see tupleSigKind.
	if k, isTuple := tupleSigKind(te, anchors); isTuple {
		return k
	}
	if ft, isFunc := te.(*ast.FuncType); isFunc && ft.Return != nil {
		// A FUNCTION type — `Server.serve`'s `handler: (Request) -> Response`.
		// Its parts resolve through this same function, and the kind interns
		// process-wide when every part is package-neutral (funcKindIn with no
		// gen); a part that is not declines rather than interning nowhere.
		parts := make([]kind, 0, len(ft.Params)+1)
		for _, p := range ft.Params {
			parts = append(parts, stdTypeKind(p, anchors))
		}
		result := stdTypeKind(ft.Return, anchors)
		for _, p := range append(parts, result) {
			// kindInvalid: propagates — the part's own refusal is the answer.
			if p == kindInvalid || !p.packageNeutral() {
				return kindInvalid
			}
		}
		return funcKindIn(nil, parts, result)
	}
	if gt, generic := te.(*ast.GenericType); generic {
		// A PRELUDE instance over the types above: `Float.to_int(x: Float):
		// Maybe<Int>`. The same terms again, one level up — `rt.Maybe[int64]`
		// is declared in rt too — and admitted only when every type ARGUMENT is
		// itself package-neutral, which is what stdprelude.go interns on.
		//
		// Reported as handled even when it refuses, so an unrepresentable type
		// argument is the answer rather than falling through to a lookup that
		// would answer the same kindInvalid for a different reason.
		if k, isPrelude := preludeSigKind(gt, anchors); isPrelude {
			return k
		}
		if k, isList := listSigKind(gt, anchors); isList {
			return k
		}
		if k, isMap := mapSigKind(gt, anchors); isMap {
			return k
		}
		if k, isGenStruct := genStructSigKind(gt, anchors); isGenStruct {
			return k
		}
	}
	return kindInvalid
}

// stdAnchors is every NON-SCALAR type a stdlib signature may name and this
// builder has a representation for, resolved against one module's analysis.
//
// A struct rather than a parameter per family, because there are several
// families and the reason each is admissible is identical — a process-wide
// *typeDef whose Go type rt
// declares, so it is not any generated package's type. A threaded map per
// family would make each addition a five-signature edit.
type stdAnchors struct {
	// opaques indexes opaqueSpecs by Nomi name.
	opaques map[string]int
	// enums indexes stdEnumSpecs by Nomi name.
	enums map[string]int
	// structs indexes stdStructSpecs by Nomi name.
	structs map[string]int
	// hosts indexes stdHostSpecs by Nomi name. A base family rather than a
	// late one: a `host type` declares no contents, so resolving it needs
	// nothing from any other family. See stdhost.go.
	hosts map[string]int
	// preludes maps `Maybe`/`Result` to THIS module's anchor. An anchor rather
	// than an index because the prelude family is parameterized: the spec alone
	// does not name a type, so the type ARGUMENTS have to be resolved at the
	// annotation and the instance interned per instantiation. See stdprelude.go.
	preludes map[string]*preludeAnchor
	// ifaces indexes stdIfaceSpecs by Nomi name. Filled LAST by stdAnchorsOf,
	// because stdIfaceAnchorsWith resolves its own method signatures through
	// the other families; see stdiface.go.
	ifaces map[string]int
	// synthEnums indexes stdEnumSpecs by Nomi name for the names a
	// DERIVE-SYNTHESIZED signature in this module resolves through the
	// COMPILER-KNOWN route rather than through the module's imports. Disjoint
	// from `enums` by construction — see stdEnumSynthAnchors — and consulted
	// only through forSynthesized.
	synthEnums map[string]int
	// genStructs indexes stdGenStructSpecs by Nomi name, through the same
	// (origin, name) + validated-shape anchor a gen uses.
	//
	// It lets std's own signatures name a generic std struct as a user's
	// annotation can: `ranges.Range.from(n: Int): Range<Int>` and
	// `Range.naturals(): Range<Int>` resolve here. See stdgensig.go.
	genStructs map[string]*stdGenStructAnchor
	// declared answers "is this name a TYPE in the declaring module" — the
	// resolver half of analysis.ReceiverTypeParamNames, which needs it to tell a
	// receiver's type PARAMETER (`Map<K, V>`) from a concrete type argument
	// (`Map<String, V>`). nil when the module was analyzed without a library,
	// in which case the receiver-derived rule is skipped entirely rather than
	// answering "everything is a type parameter".
	declared func(string) bool
}

// forSynthesized is these anchors as a DERIVE-SYNTHESIZED declaration in the
// same module sees them: the ordinary enum anchors plus the compiler-known ones.
//
// A union rather than a fallback chain because the two maps cannot collide:
// stdEnumSynthAnchors admits a name only when the module scope binds it to
// nothing, and `enums` holds only names the module scope binds to std's own
// declaration. So there is no precedence to get wrong, and a local `Ordering`
// is absent from BOTH and still refuses.
//
// Returns the receiver unchanged when there is nothing to add, which is the case
// for every std module that imports what it names — three of the four that
// derive Comparable are the exception, not the rule.
func (a stdAnchors) forSynthesized() stdAnchors {
	if len(a.synthEnums) == 0 {
		return a
	}
	merged := make(map[string]int, len(a.enums)+len(a.synthEnums))
	for n, i := range a.enums {
		merged[n] = i
	}
	for n, i := range a.synthEnums {
		merged[n] = i
	}
	a.enums = merged
	return a
}

// stdAnchorsOf resolves every family against one module's analysis. A module
// analyzed without an analysis library gets empty maps, and every non-scalar
// signature position then refuses.
//
// stdIfaceAnchorsWith is filled LAST because a stdlib interface's METHOD
// signatures name every other family. The struct family resolves its own FIELD
// annotations too, but against the DECLARING module rather than this one — see
// stdStructValidated — so it needs nothing from here.
func stdAnchorsOf(fa *analysis.FileAnalysis) stdAnchors {
	a := stdBaseAnchors(fa)
	_, a.structs = stdStructAnchors(fa)
	_, a.ifaces = stdIfaceAnchorsWith(fa, a)
	return a
}

// stdBaseAnchors is the families that resolve without reference to any other:
// opaque newtypes, monomorphic enums, `host type`s and the prelude anchors.
//
// synthEnums is resolved here beside `enums` because it is the same question
// asked of a different scope, and asking it once keeps the two maps' disjointness
// a property of one function rather than of two call sites.
//
// `declared` closes over fa rather than being derived later, so every consumer
// asks the SAME module's scope the receiver question. It is nil exactly when the
// module has no scope, which is the case the receiver-derived generic rule must
// decline rather than guess at.
func stdBaseAnchors(fa *analysis.FileAnalysis) stdAnchors {
	_, opaques := opaqueAnchors(fa)
	_, enums := stdEnumAnchors(fa)
	hosts := stdHostAnchors(fa)
	_, preludes := preludeAnchorsOf(fa)
	var declared func(string) bool
	if fa != nil && fa.ModuleScope != nil {
		scope := fa.ModuleScope
		declared = func(n string) bool { return scope.Lookup(n) != nil }
	}
	return stdAnchors{
		opaques:    opaques,
		enums:      enums,
		hosts:      hosts,
		preludes:   preludes,
		synthEnums: stdEnumSynthAnchors(fa),
		genStructs: stdGenStructAnchorsOf(fa),
		declared:   declared,
	}
}

// scalarKind is the kind a type annotation names, restricted to the scalars.
func scalarKind(te ast.TypeExpr) kind {
	st, ok := te.(*ast.SimpleType)
	if !ok {
		return kindInvalid
	}
	switch st.Name {
	case "Int":
		return kindInt
	case "Float":
		return kindFloat
	case "String":
		return kindString
	case "Bool":
		return kindBool
	case "Unit":
		return kindUnit
	}
	return kindInvalid
}

// simpleTypeName is a type expression's name when it is written UNDECORATED,
// and "" for every other spelling.
//
// Deliberately narrow, and the narrowness is the contract. Its callers ask
// "is this annotation literally `Bool`/`Int`/`Duration`/the type parameter
// `S`?" — shape probes over a `host fn`'s declared signature (iter.go's
// each_while and known_count checks, ctrlflow.go's Iter.loop check) and the
// anchored-opaque lookup below. For those a GENERIC spelling must NOT match:
// `S<X>` is not the type parameter `S`, and no anchored opaque newtype is
// generic.
//
// It is NOT the reading for the impl-block RECEIVER, which is the other
// question entirely; see the receiver read in collectStdCandidates.
func simpleTypeName(te ast.TypeExpr) string {
	st, ok := te.(*ast.SimpleType)
	if !ok {
		return ""
	}
	return st.Name
}

// stdNestedTypeName is a NESTED type name's whole dotted spelling —
// `Json.DecodeError` — and "" for every other shape.
//
// simpleTypeName's sibling one level up, and deliberately just as narrow. The
// MEMBER must be undecorated: `Json.Wrapper<Int>` is a generic nested type, a
// family with no rows and its own identity question, and admitting it here would
// project it onto whatever `Json.Wrapper` happened to be keyed under.
//
// The Module segment is NOT checked for being a type rather than a module alias,
// because the lookup that follows is what decides: an anchor map holds only names
// std declares, established through the (origin, name) rule, so `io.Reader`
// simply misses. Checking here as well would be a second, weaker copy of the
// identity rule.
func stdNestedTypeName(te ast.TypeExpr) string {
	qt, qualified := te.(*ast.QualifiedType)
	if !qualified || qt.Module == "" {
		return ""
	}
	member, undecorated := qt.Member.(*ast.SimpleType)
	if !undecorated {
		return ""
	}
	return qt.Module + "." + member.Name
}

// stdHeadTypeName is the type name a stdlib parameter's annotation names, for
// the RECEIVER TEST — the undotted spelling, or a nested type's whole dotted
// one.
//
// The receiver test compares this against `analysis.TypeExprBaseName(impl
// receiver)`, which answers `Json.DecodeError` for `impl Display for
// Json.DecodeError` (an uppercase qualifier is part of a nominal name, per that
// function's own doc). `simpleTypeName` answers "" for the same annotation, so
// with it the two sides could never agree and `selfIdx` would stay -1, which
// keeps the declaration out of `byIface` entirely because addOverload requires
// `selfIdx == 0`: `Display.to_string(e)` over a `Json.DecodeError` would find
// no impl.
//
// Deliberately NOT `analysis.TypeExprBaseName` itself, even though that is the
// question's own function. That one also STRIPS generic arguments, so `xs:
// List<T>` would start answering `List` where this answers "", and `selfIdx` is
// read by more than the index. The dotted spelling is the only shape added
// beyond simpleTypeName's.
func stdHeadTypeName(te ast.TypeExpr) string {
	if n := simpleTypeName(te); n != "" {
		return n
	}
	return stdNestedTypeName(te)
}

// tupleKindOfGoType answers a tuple kind for the flat anonymous struct a Nomi
// tuple lowers to, or kindInvalid.
//
// THREE CONDITIONS, and each excludes a real Go type that must not be read as a
// tuple:
//
//  1. ANONYMOUS. `t.Name()` empty and `t.PkgPath()` empty — a DEFINED struct
//     type is a nominal type with its own identity, and `rt.Regex` or a user's
//     `struct { F0 int64 }` must not become a tuple because its fields happen to
//     be named that way.
//  2. FIELDS EXACTLY `F0`..`Fn-1`, IN ORDER. Any other name, any gap, or any
//     reordering is some other struct. Checked positionally rather than as a
//     set, because the tuple's component ORDER is its meaning.
//  3. EVERY COMPONENT REPRESENTABLE. Recursive, so `(Int, (Float, Int))` works
//     and a component this builder cannot represent refuses the whole tuple
//     rather than producing a kind with a hole in it.
//
// A ZERO-FIELD anonymous struct is NOT a tuple: Nomi has no 0-tuple, `Unit` is
// its own kind with its own rt type, and admitting one here would give two
// answers for one Go type.
func tupleKindOfGoType(t reflect.Type) kind {
	if t.Kind() != reflect.Struct || t.Name() != "" || t.PkgPath() != "" {
		return kindInvalid
	}
	n := t.NumField()
	if n == 0 {
		return kindInvalid
	}
	parts := make([]kind, 0, n)
	for i := 0; i < n; i++ {
		f := t.Field(i)
		if f.Name != fmt.Sprintf("F%d", i) {
			return kindInvalid
		}
		k := kindOfGoType(f.Type)
		// kindInvalid: cascade — a component with no representation refuses the tuple.
		if k == kindInvalid {
			return kindInvalid
		}
		parts = append(parts, k)
	}
	k, ok := sharedTupleKind(parts)
	if !ok {
		return kindInvalid
	}
	return k
}

// stdArityName is the symbol name of one std function's arity-reduced wrapper:
// the function's key, which carries its receiver and instantiation, plus the
// arity.
func stdArityName(f *stdFunc, arity int) string {
	return f.key + " arity " + strconv.Itoa(arity)
}

// stdCallArity is the arity a call of n POSITIONAL arguments uses, and whether
// that is a call this function accepts at all.
//
// Shared by both call sites (stdlibInvoke and stdlibSiblingCall) so a short call
// gets one answer. It is the ONLY place that reads callArityMin, which is what
// keeps "how short may a call be" from being asked twice.
func stdCallArity(f *stdFunc, n int) (int, bool) {
	if n == len(f.params) {
		return n, true
	}
	if n < len(f.params) && n >= callArityMin(f) {
		return n, true
	}
	return 0, false
}

// callArityMin is f.arityMin read on the CANONICAL *stdFunc, which is where the
// lowering pass lowers it. See stdFunc.canon: a sibling snapshot froze the
// pessimistic value and refused eleven short intra-module calls whose arity
// body was already built.
func callArityMin(f *stdFunc) int {
	if f.canon != nil {
		return f.canon.arityMin
	}
	return f.arityMin
}

// --- the stdlib module's gen ----------------------------------------------

// newStdGen builds a gen for one stdlib module: the same builder user code goes
// through, with a sibling table instead of a module function table and with the
// stdlib index restricted to this module's own functions plus every module
// lowered BEFORE it.
//
// "This module only" would make the generated import graph a trivially acyclic
// star, and would be too strong: `std/instant`'s `impl Add<Duration, Instant>`
// is `Instant(nanos + Duration.as_nanos(rhs))` and cannot be written without
// reaching std/duration.
//
// "Earlier" is a DAG by construction: buildStdlibIndex
// lowers modules in one fixed order and hands each the index of the ones already
// done, so every generated edge points backwards and no cycle is representable.
// Nomi's own import graph cannot supply that guarantee — std/strings and std/iter
// import each other.
func newStdGen(module, path, pkg string, nodes []ast.Node, fa *analysis.FileAnalysis, cands []*stdCandidate, settled map[*stdCandidate]bool, onces []*stdOnce, earlier *stdlibIndex) *gen {
	g := &gen{nomiPath: path, pkg: pkg, // stdModule, so this gen can answer "which stdlib module am I". The
		// module name is the caller's — buildStdlibIndex takes it from
		// std.Load()'s keys — and a user gen never reaches this constructor, so
		// a non-empty stdModule IS "this is the embedded stdlib module of that
		// name". loadIter's declaresStdIter is the one reader. See native.go.
		stdModule: module, fa: fa, // nodes, which every `g.nodes` reader in this package needs for a
		// stdlib module too: declaresIterImpl (iter.go), appImplNames and
		// declaresBoot (appfield.go), templateWall (generictype.go), the
		// generic-impl template collection (genericimpl.go), collectHostPkgs
		// (hostpkg.go), declareOnces (once.go), typeParamNames and
		// nestedTypeKeys (sigreason.go), and tail.go's graph. Without it
		// declaresIterImpl would answer "this module declares no `impl Iter`
		// block" about std/iter.nomi, whose whole API is one.
		// TestStdModuleView_PopulatesNodes and
		// TestIterAnchor_DeclaringModuleFailsProvenanceAndNotShape hold it.
		nodes: nodes, funcs: map[string]*fnSig{}, types: map[string]*typeDef{}, ifaces: map[string]*ifaceDef{}, implsByIface: map[string]map[kind]*implDef{}, tids: map[kind]bool{}, anonTids: map[kind]bool{}}
	g.pushScope()
	// Interface shells and type declarations so typeOf can answer inside a body.
	// Nothing is EMITTED from either: a stdlib package exposes functions only,
	// because a type it declared would be a type in the wrong package.
	g.declareIfaces(nodes)
	g.buildTypes(nodes)
	g.resolveIfaces()

	g.bindStdSiblings(module, pkg, cands, settled, earlier)
	// After bindStdSiblings, and on a field of its own, so the rebind for phase 2
	// cannot drop it. See stdonce.go's bindStdOnces.
	g.bindStdOnces(onces)
	return g
}

// bindStdSiblings installs the sibling table and the stdlib index one stdlib
// body is lowered against, and returns the undo.
//
// Separate from newStdGen because the lowering pass binds TWO scopes over one
// gen: phase 1's members are lowered against phase 1's sibling set and phase 2's
// against the enlarged one. See lowerStdlibModule for why that split is what
// confines a cycle to phase 2 and leaves phase 1's text byte-identical.
//
// `settled` is asked about the CANDIDATE, not about `c.sib`, which is a
// spelling several declarations share. Asked about the spelling, the answer
// would be the group's: `Add<Duration, NaiveDateTime>` is bound to an rt
// symbol and seeds the least fixed point, so `settled["NaiveDateTime.add"]`
// would be true and this loop would hand out a name for all ten OTHER rungs,
// none of which lowers. A sibling resolving `lhs + Hours(-n)` to one of them
// would then reference a function the pass goes on to refuse.
// TestStdlibEmittedCallsAreDefined is the guard.
func (g *gen) bindStdSiblings(module, pkg string, cands []*stdCandidate, settled map[*stdCandidate]bool, earlier *stdlibIndex) func() {
	restore, _ := g.bindStdSiblingViews(module, pkg, cands, settled, earlier)
	return restore
}

// stdSiblingBinds counts bindStdSiblingViews calls, process-wide, so a test can
// hold the number of sibling bindings per stdlib lowering to a bound. Each one
// rebuilds the module's sibling table, its local index and the underlay of
// every earlier module. See TestStdSiblingBinds_* and irPrebuildStdBodies.
var stdSiblingBinds atomic.Int64

// bindStdSiblingViews is bindStdSiblings plus the view it installed for each
// candidate, so a caller that keeps one binding across several bodies can
// update a view's irBody in place instead of rebinding. A view is a COPY of
// the candidate's stdFunc taken now, and irBody is the one field the prebuild
// changes between its attempts.
func (g *gen) bindStdSiblingViews(module, pkg string, cands []*stdCandidate, settled map[*stdCandidate]bool, earlier *stdlibIndex) (func(), map[*stdCandidate]*stdFunc) {
	stdSiblingBinds.Add(1)
	prevSiblings, prevStd := g.stdSiblings, g.std
	views := make(map[*stdCandidate]*stdFunc, len(cands))
	local := &stdlibIndex{
		byType: map[string][]*stdFunc{},
		// THE FILE-QUALIFIED ROUTE. `stdlibIndex.add` files a `pub` free
		// function under "<module>.<name>" only when `byFile != nil`; without
		// the map a module's own `pub host fn`s would not be filed,
		// `stdFileCall` could only decline, and `stdFreeCall` would report the
		// QUALIFIER's key. `compiler.check(...)` inside std/compiler and
		// `timer.sleep(...)` inside std/timer need it: the implicit file alias
		// is how a stdlib module names its own free functions.
		byFile:    map[string]*stdFunc{},
		byIface:   map[string]map[kind][]*stdFunc{},
		modulePkg: map[string]string{module: pkg},
	}
	g.stdSiblings = map[string]*stdFunc{}
	// The unqualified-name table is keyed on the spelling, so two lowerable
	// declarations sharing one spelling would resolve by map arrival order.
	// Neither is installed instead, and the
	// unqualified call then refuses through the ordinary path. `local` is NOT
	// affected: its overload sets select by the full parameter-kind signature,
	// which is what an operator or a qualified call supplies.
	ambiguous := map[string]bool{}
	for _, c := range cands {
		f := c.f
		if !settled[c] && f.rtCall == "" {
			continue
		}
		// A settled Nomi body is reachable under the Go-spelled name it will be
		// lowered with; the sibling table and the lowering use one function to
		// build it.
		//
		// `canon` keeps the copy tied to the original for the ONE field the
		// lowering pass mutates after this point — arityMin. See stdFunc.canon:
		// without it a short intra-module call to a defaulted sibling reads a
		// frozen arity and refuses `call arity` for an arity body that exists.
		view := *f
		view.canon = f
		if view.rtCall == "" {
			view.body = true
			view.pkg = pkg
		}
		if _, taken := g.stdSiblings[c.sib]; taken {
			ambiguous[c.sib] = true
		}
		g.stdSiblings[c.sib] = &view
		local.add(&view)
		views[c] = &view
	}
	for sib := range ambiguous {
		delete(g.stdSiblings, sib)
	}
	local.underlay(earlier)
	g.std = local
	return func() { g.stdSiblings, g.std = prevSiblings, prevStd }, views
}

// emitStdFunc emits one stdlib function as a top-level Go func.
//
// Not gen.funcDecl: funcDecl reports a refusal for every parameter and return
// shape it cannot take, and inside the stdlib those reports are exactly what must
// not reach the tally. Here a shape outside the subset was already settled by
// collectStdCandidates, so the body is the only thing left to try.
func (g *gen) emitStdFunc(f *stdFunc, fd *ast.FuncDef) {
	g.at(fd.Line)
	g.pushScope()
	// A destructuring parameter's names come out of the parameter's value in
	// the function's prologue; std relies on it heavily — every operator impl
	// in std/duration and std/instant is `fn add(Duration(a), Duration(b))`.
	type destructuring struct {
		pat ast.Node
		at  int
	}
	var patterns []destructuring
	for i, p := range fd.Params {
		if p.Destructure != nil {
			// The synthesized `__destr_<line>_<col>` name the parser gave this
			// parameter is not spellable in Nomi, so only the pattern's own
			// names reach the body.
			patterns = append(patterns, destructuring{p.Destructure, i})
			continue
		}
		if !ast.IsDiscardName(p.Name) {
			g.bind(p.Name, local{k: f.params[i]})
		}
	}

	prevResult, prevSelf := g.result, g.implSelf
	g.result, g.implSelf = f.result, f.recv
	// `g.stdInstName` when a per-program INSTANCE of a generic declaration is
	// being emitted, `f.key` otherwise. An instance is the same body at a
	// concrete instantiation and differs from the template only in its name, so
	// this is the one line that has to know. See stdinstance.go, and `monoSig`
	// on the gen for the same arrangement one construct over.
	name := f.key
	if g.stdInstName != "" {
		name = g.stdInstName
	}
	// The same seam `funcDecl` opens, in the same place relative to
	// the `var rN R` line, because the shell describes THAT declaration. See
	// irstdbody.go for why this caller exists and what it deliberately does
	// not widen.
	stdSig := irStdFuncSig(f, fd)
	shell := g.irFuncShellFor(fd, stdSig, f.params)
	destructured := true
	if len(patterns) > 0 {
		closePrologue := g.irPrologueOpen(shell)
		for _, d := range patterns {
			if !g.destructure(d.pat, shell.paramValue(d.at, f.params[d.at]), fd) {
				destructured = false
				g.bindPatternNamesInvalid(d.pat)
			}
		}
		closePrologue(destructured)
	}
	g.irBodyObserve(irBodyStdFn, true)
	produced, retained := g.irScalarLower(fd, stdSig, nil, shell, f.result)
	if !retained {
		g.stdUnloweredFunc(name, f.key, irTakeDeclineWhy())
		produced = f.result
	}
	if !destructured {
		// A pattern that did not lower leaves its names bound at an invalid
		// kind, so the body reports nothing further and this function is simply
		// not lowered. Same rule funcDecl applies.
		produced = kindInvalid
	}
	switch {
	case produced == kindInvalid:
		// Declines `stdlib return type mismatch`.
		g.suppress(fd)
	case produced != f.result:
		g.reject("stdlib return type mismatch", f.key, fd)
	}
	g.result, g.implSelf = prevResult, prevSelf
	g.popScope()
}

// emitStdBody lowers one stdlib function plus every arity-reduced body its
// trailing defaults earn, against whatever sibling scope is bound right now.
//
// One function so the two arms of lowerStdlibModule's lowering switch cannot
// disagree about the order: body has to be set before the wrappers, because a
// wrapper calls it, and the wrappers have to be inside phase 2's
// sibling window, because a default expression is ordinary Nomi from this
// module.
func (g *gen) emitStdBody(f *stdFunc, fd *ast.FuncDef, pkg string) bool {
	if !g.speculate(func() { g.emitStdFunc(f, fd) }) {
		return false
	}
	f.body, f.pkg = true, pkg
	g.emitStdDefaultWrappers(f, fd)
	// Beside the arity wrappers and for the same two reasons: body is set, and
	// this is the declaring module's own sibling window, which is where a
	// default expression's names resolve. See stdslotfill.go.
	g.emitStdSlotDefaults(f, fd)
	return true
}

// emitStdDefaultWrappers builds one arity body (irStdArityRetain) per
// POSITIONAL arity a call site may spell short, lowering f.arityMin as it goes.
//
// # Why a wrapper in the STDLIB's package rather than a fill at the call site
//
// A parameter default is Nomi source belonging to the module that DECLARED it,
// and `mode: Disambiguation = Disambiguation.Compatible` is the proof: filling
// it at the call site means lowering `Disambiguation.Compatible` in the CALLER's
// gen, where the name resolves through the caller's own type registry. A caller
// that omits the argument has no reason to have imported `Disambiguation` at
// all — 05-calendar-and-time/datetime_test.nomi is exactly that file — so the
// fill would refuse for files that are perfectly legal Nomi. Worse in the other
// direction: a default naming a module-level binding would resolve against
// whatever the CALLER happens to have under that spelling, which is a silent
// wrong answer, which is why a user field default reached from another file is
// lowered in its declaring file's gen (foreignfielddefault.go).
//
// Emitting the fill HERE removes the question rather than answering it. This gen
// IS the declaring module's, so g.scopes[0] is the scope the programmer wrote
// the default in and g.types resolves the names they wrote; the caller sees only
// a function of the shorter arity. Nothing about the caller can change what
// the default means, which is the property `portableDefault` has to CHECK for a
// sibling file's interface default and this gets for free.
//
// The fill itself is fillParamDefaults — the one implementation module `fn`
// calls and impl-method calls already share — so "what does an omitted argument
// mean" still has exactly one encoding in this package. It is reached with the
// std module's own scope stack, so its documented rules (evaluated at each call
// that omits the argument, in the declaring module's scope) hold here by
// construction rather than by analogy.
//
// Top down and stopping at the first refusal, so the arities that exist are
// contiguous: a wrapper for arity n-2 calls the full function with TWO defaults
// filled, and if the last one cannot be lowered neither can any shorter arity.
func (g *gen) emitStdDefaultWrappers(f *stdFunc, fd *ast.FuncDef) {
	if len(fd.Params) != len(f.params) {
		return
	}
	for arity := len(f.params) - 1; arity >= 0; arity-- {
		if fd.Params[arity].Default == nil {
			return
		}
		f.arityMin = arity
		g.irStdArityRetain(f, fd, arity)
	}
}

// --- call sites -----------------------------------------------------------

// stdOperatorIfaces is the operator-to-interface mapping std declares. `%` is
// absent because std declares no Modulo interface: `%` on a named type has
// nothing to dispatch to and keeps refusing.
var stdOperatorIfaces = map[string]string{
	"+": "Add.add",
	"-": "Subtract.subtract",
	"*": "Multiply.multiply",
	"/": "Divide.divide",
}

// stdlibImplOf resolves one stdlib interface METHOD at a statically-known
// receiver kind, checking the PARAMETER shape the call site assumes.
//
// The shared half of the three routes below, all of which are the same row of
// the dispatch table stdlibCall's second bullet describes: an interface method
// whose receiver kind is known statically names exactly one function, so it
// lowers to a direct call and needs no table. Reached through an operator or a
// renderer instead of through a qualifier.
//
// Selection is on the receiver KIND and never on a name, which is what keeps a
// user's own `type Duration Int` out: its kind is a different *typeDef, so the
// index misses and the existing refusal reports. A declaration whose parameters
// disagree answers nil rather than being called against a signature nobody
// wrote, which is stdSignatureMatches' rule one level up.
func (g *gen) stdlibImplOf(key string, recv kind, params ...kind) *stdFunc {
	if g.std == nil || recv.def == nil {
		return nil
	}
	// stdPick answers a single-member set unconditionally, so the shape checks
	// below are the ones that decide.
	f := stdPick(g.std.byIface[key][recv], params)
	if f == nil || f.why != "" || len(f.params) != len(params) {
		return nil
	}
	for i := range params {
		if f.params[i] != params[i] {
			return nil
		}
	}
	return f
}

// stdlibOperatorFor is the shared binary-operator selection. Decimal arithmetic
// belongs to its primitive implementation, ahead of stdlib dispatch.
func (g *gen) stdlibOperatorFor(op string, left, right kind) *stdFunc {
	key, known := stdOperatorIfaces[op]
	if !known || left.def == nil || (isDecimalKind(left) && isDecimalKind(right)) {
		return nil
	}
	return g.stdlibImplOf(key, left, left, right)
}

// stdlibSibling resolves an unqualified call inside a stdlib body.
//
// `impl Debug for Float`'s `inspect` calls `to_string(x)`, and `impl Hashable for
// Float`'s `hash` calls the module-private `float_bits(x)`. The first resolves
// through the receiver type's methods and the second through module scope, so
// both are looked up here: receiver-qualified first,
// then module-level. Nothing else — a name that resolves to neither is refused by
// the ordinary path, and the function is then simply not lowered.
func (g *gen) stdlibSibling(name string) *stdFunc {
	if g.stdSiblings == nil {
		return nil
	}
	if g.implSelf != "" {
		if f, ok := g.stdSiblings[g.implSelf+"."+name]; ok {
			return f
		}
	}
	if f, ok := g.stdSiblings["."+name]; ok {
		return f
	}
	return nil
}

// irPrebuildStdBodies builds every emittable body's graph before any body is
// emitted and leaves the graphs in the module.
//
// A body's graph may call a sibling only once the sibling's graph is in the
// module, so building in declaration order left a forward call — `Date.parse`
// calling the later `Error.of` — declining for want of its callee, and a
// recursive one could never build. So the prebuild takes the greatest fixed
// point: every body is assumed to build (irStdPending), and a round builds
// them until no more build; a body still unbuilt is withdrawn and the round
// starts over without it, until a round withdraws nothing. The lowering pass
// that follows finds every callee already there; each body then rebuilds its
// own graph (irScalarLower replaces the prebuilt graph). The prebuild's text
// is discarded.
func (g *gen) irPrebuildStdBodies(module, pkg string, cands []*stdCandidate, emitted map[*stdCandidate]bool, earlier *stdlibIndex) {
	var todo []*stdCandidate
	for _, c := range cands {
		if c.fd != nil && c.f.why == "" && emitted[c] {
			// The callee spelling a sibling call names, set ahead of lowering
			// so a graph built now can link to a body lowered later.
			c.f.body, c.f.pkg = true, pkg
			todo = append(todo, c)
		}
	}
	if len(todo) == 0 {
		return
	}
	defer irMuteObservers()()
	g.irPrebuilding = true
	defer func() { g.irPrebuilding = false }()
	mod := g.irModule()
	g.irStdPending = make(map[string]bool, len(todo))
	defer func() { g.irStdPending = nil }()
	for _, c := range todo {
		g.irStdPending[c.f.key] = true
	}
	// ONE sibling binding for the whole prebuild, not one per body attempt.
	// Every input to the binding — cands, emitted, earlier and each candidate's
	// stdFunc — is fixed for the prebuild's duration except irBody, which this
	// loop alone writes; setIRBody writes it to the candidate and to the view
	// the binding installed for it, so every attempt sees exactly the views a
	// fresh binding would have built. A binding rebuilds the local index, the
	// underlay of every earlier module and every sibling's Go name, so one per
	// attempt is most of the prebuild's cost; TestStdSiblingBinds_* holds the
	// count.
	restoreSiblings, views := g.bindStdSiblingViews(module, pkg, cands, emitted, earlier)
	defer restoreSiblings()
	setIRBody := func(c *stdCandidate, fn *ir.Func) {
		c.f.irBody = fn
		if v := views[c]; v != nil {
			v.irBody = fn
		}
	}
	for declined := true; declined; {
		declined = false
		// Every graph from the last round goes, because it may link to a body
		// that round withdrew.
		for _, c := range todo {
			if fn := mod.FuncFor(g.irCalleeSym(c.fd, c.fd.Name)); fn != nil {
				mod.RemoveFunc(fn)
			}
			setIRBody(c, nil)
		}
		// A qualified or operator sibling call links through the callee's
		// irBody, which exists only once that body has built in this round,
		// so the round repeats until nothing more builds.
		for progress := true; progress; {
			progress = false
			for _, c := range todo {
				if !g.irStdPending[c.f.key] || c.f.irBody != nil {
					continue
				}
				sym := g.irCalleeSym(c.fd, c.fd.Name)
				restore := g.snapshot()
				tmp := g.tmp
				g.emitStdFunc(c.f, c.fd)
				g.tmp = tmp
				restore()
				if fn := mod.FuncFor(sym); fn != nil {
					setIRBody(c, fn)
					progress = true
				}
			}
		}
		for _, c := range todo {
			if g.irStdPending[c.f.key] && c.f.irBody == nil {
				delete(g.irStdPending, c.f.key)
				declined = true
			}
		}
	}
}

// irStdTailCycles marks every prebuilt body that makes a tail call into a
// recursion cycle it belongs to.
//
// Nomi guarantees constant-stack tail calls with no annotation (spec §12.7).
// The VM keeps that guarantee by running a tail call in the caller's
// activation, so such a body keeps its graph. Go eliminates no tail calls, so
// its Go is a stub named `stdlib tail-recursive function` and a program that
// reaches it does not build. The cycles are read off the
// prebuilt graphs, whose callees are the ones the builder resolved by argument
// kind, so an interface-qualified call names the one impl it reaches rather
// than every impl of that method.
func (g *gen) irStdTailCycles(cands []*stdCandidate, emittable map[*stdCandidate]bool) {
	mod := g.irMod
	if mod == nil {
		return
	}
	built := map[*stdCandidate]*ir.Func{}
	for _, c := range cands {
		if c.fd != nil && emittable[c] {
			if fn := mod.FuncFor(g.irCalleeSym(c.fd, c.fd.Name)); fn != nil {
				built[c] = fn
			}
		}
	}
	calleeOf := func(call *ir.Call) *stdCandidate {
		for c, fn := range built {
			if fn.Sym() == call.Callee() {
				return c
			}
		}
		return nil
	}
	type edge struct {
		to   *stdCandidate
		tail bool
	}
	edges := map[*stdCandidate][]edge{}
	for c, fn := range built {
		for _, b := range fn.Blocks() {
			for _, in := range b.Instrs() {
				if call, isCall := in.(*ir.Call); isCall {
					if to := calleeOf(call); to != nil {
						edges[c] = append(edges[c], edge{to, call.Tail()})
					}
				}
			}
		}
	}
	reaches := func(from, to *stdCandidate) bool {
		seen := map[*stdCandidate]bool{}
		stack := []*stdCandidate{from}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, e := range edges[n] {
				if e.to == to {
					return true
				}
				if !seen[e.to] {
					seen[e.to] = true
					stack = append(stack, e.to)
				}
			}
		}
		return false
	}
	for _, c := range cands {
		for _, e := range edges[c] {
			if e.tail && reaches(e.to, c) {
				c.f.tailRec = true
				break
			}
		}
	}
}

// irMuteObservers silences every test hook and answers the restore.
func irMuteObservers() func() {
	decline, fn, declined, body := IRDeclineObserved, irFuncObserved, irRetentionDeclined, irBodyObserved
	effect, iter, proj, tail := irEffectObserved, irIterObserved, irProjKindObserved, irTailObserved
	testBody, at := irTestBodyObserved, IRDeclineAt
	IRDeclineAt = nil
	IRDeclineObserved, irFuncObserved, irRetentionDeclined, irBodyObserved = nil, nil, nil, nil
	irEffectObserved, irIterObserved, irProjKindObserved, irTailObserved = nil, nil, nil, nil
	irTestBodyObserved = nil
	return func() {
		IRDeclineObserved, irFuncObserved, irRetentionDeclined, irBodyObserved = decline, fn, declined, body
		irEffectObserved, irIterObserved, irProjKindObserved, irTailObserved = effect, iter, proj, tail
		irTestBodyObserved, IRDeclineAt = testBody, at
	}
}

// irRecordStdKey records a std hand-written `impl Equatable` or `impl
// Hashable` body of a declared record type (OffsetDateTime's instant-only
// equality), as irRecordKeyImpls does for a program's own.
func (g *gen) irRecordStdKey(f *stdFunc) {
	if f.irBody == nil || len(f.params) == 0 || !irKeyImplRecv(f.params[0]) {
		return
	}
	k := f.params[0]
	switch {
	case f.iface == "Equatable" && f.name == "equal?" && len(f.params) == 2:
		g.irMod.ImplementEquatable(g.irTypeSym(k.def), f.irBody.Sym())
	case f.iface == "Hashable" && f.name == "hash" && len(f.params) == 1:
		g.irMod.ImplementHashable(g.irTypeSym(k.def), f.irBody.Sym())
	}
}

// irRecordStdDisplay records a std `impl Display` body of a declared,
// non-generic type for erased Display renderings, as irRecordDisplayImpls
// does for a program's own.
func (g *gen) irRecordStdDisplay(f *stdFunc) {
	if f.iface != "Display" || f.name != "to_string" || f.irBody == nil || len(f.params) != 1 {
		return
	}
	k := f.params[0]
	if k.tag != tagNamed || k.def == nil || k.def.genericOf != nil || k.def.preludeOf != nil {
		return
	}
	g.irMod.ImplementDisplay(g.irTypeSym(k.def), f.irBody.Sym())
}

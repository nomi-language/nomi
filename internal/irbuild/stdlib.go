package irbuild

import (
	"fmt"
	"maps"
	"reflect"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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
// # The signature boundary
//
// The shared lowering lowers a stdlib function only when every parameter
// type and the result type, as the checker recorded it, has a kind with no
// gen (projectShared): a scalar (Int/Float/String/Bool/Unit), a std type
// with a process-wide def, or a structural type over those. `kind` identity for a named type is the
// *typeDef pointer of its declaration, never its name, and a stdFunc's kinds
// are built once and compared by pointer against a call site's argument
// kinds in another gen. A kind with no process-wide identity would equal no
// call site's kind. Any other signature is refused as `stdlib function
// outside the scalar subset`.
//
// # Refusals from inside the stdlib are SILENT, and that is deliberate
//
// Reporting a std function the builder declines as a blocker would blame every
// user program in the corpus for constructs in a file the user did not write.
// So a stdlib function is lowered SPECULATIVELY: one that refuses is discarded
// along with its refusals and marked unlowerable, and a CALL SITE to it
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
//   - a Funcs row: a first-party adapter package's function in Go's own
//     types, which the VM reaches through a generated adapter. The crossing is named by the binding (irExternHost).
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
	// call is the host package's local name and the function's
	// (`rt.StringContains`), read off the symbol. A stdFunc keeps it as
	// rtCall, and stdlibHostRegistry prints it for a key bound twice.
	call string
	// pkg is the Go import path the symbol lives in. A stdFunc keeps it as
	// rtHostPkg, which chooses the crossing a call makes. See hostPackages.
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

// The registry is built on first use, never at init: projecting every row
// reads each rt function's Go signature and symbol (about 0.4 ms), and a
// lowering asks for a few rows. stdlibHostFunc projects one row the first
// time a lowering asks for it; stdlibHostRegistry projects them all, for the
// tests.

// hostRow is one RtFuncs name and the function bound to it, projected the
// first time stdlibHostFunc asks for it. A name bound twice keeps its first
// function, as stdlibHostRegistry does.
type hostRow struct {
	fn   any
	once sync.Once
	h    hostFn
	ok   bool
}

// stdlibHostRows indexes RtFuncs by name.
var stdlibHostRows = sync.OnceValue(func() map[string]*hostRow {
	rows := stdlibbindings.RtFuncs()
	byName := make(map[string]*hostRow, len(rows))
	for _, b := range rows {
		if _, dup := byName[b.Name]; !dup {
			byName[b.Name] = &hostRow{fn: b.Fn}
		}
	}
	return byName
})

// stdlibHostFunc is the registry's row for a std `host fn` key: the rt
// function's projection, or false for a key no RtFuncs row binds and for
// one whose Go signature does not project.
func stdlibHostFunc(key string) (hostFn, bool) {
	r := stdlibHostRows()[key]
	if r == nil {
		return hostFn{}, false
	}
	r.once.Do(func() {
		h, err := hostFnFor(r.fn)
		r.h, r.ok = h, err == ""
	})
	return r.h, r.ok
}

// hostRegistry is every RtFuncs row projected, and the bindings whose Go
// signature does not project onto scalar kinds. The errors are collected
// rather than panicked: a compiler that refuses to start over a registry
// defect is worse than one that refuses the affected call, and
// TestStdlibBindingsProject fails on any entry, so the failure is loud where
// loudness helps and degrades to a refusal, never to a wrong answer,
// everywhere else.
type hostRegistry struct {
	funcs  map[string]hostFn
	errors []string
}

// stdlibHostRegistry projects every RtFuncs row.
var stdlibHostRegistry = sync.OnceValue(func() hostRegistry {
	reg := hostRegistry{funcs: map[string]hostFn{}}
	for _, b := range stdlibbindings.RtFuncs() {
		h, err := hostFnFor(b.Fn)
		if err != "" {
			reg.errors = append(reg.errors, b.Name+": "+err)
			continue
		}
		if prev, dup := reg.funcs[b.Name]; dup {
			reg.errors = append(reg.errors,
				b.Name+": bound twice, to "+prev.call+" and "+h.call)
			continue
		}
		reg.funcs[b.Name] = h
	}
	return reg
})

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
	if ft.NumOut() == 0 {
		return hostFn{}, "returns no value"
	}
	h := hostFn{}
	h.call, h.pkg = hostCallName(rv)
	if h.call == "" {
		return hostFn{}, "symbol name is not resolvable"
	}
	first := 0
	if ft.NumIn() > 0 && ft.In(0) == reflect.TypeFor[*rt.Frame]() {
		// The frame is threading, not an argument: it occupies no Nomi
		// parameter slot. Recognized only in position 0; a frame anywhere
		// else is a signature nobody writes.
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
	// Several Go results are one Nomi tuple, in order, as the generated
	// adapter builds them (internal/hostgen's results).
	outs := make([]kind, ft.NumOut())
	for i := range outs {
		outs[i] = kindOfGoType(ft.Out(i))
		// kindInvalid: no-position — reads a Go reflect type for the extern registry; no Nomi position.
		if outs[i] == kindInvalid {
			return hostFn{}, fmt.Sprintf("result is %s, which is outside the representable set", ft.Out(i))
		}
	}
	h.result = outs[0]
	if len(outs) > 1 {
		k, ok := sharedTupleKind(outs)
		if !ok {
			return hostFn{}, "its results are not a tuple every module can share"
		}
		h.result = k
	}
	return h, ""
}

// hostPackages are the Go packages a stdlib `host fn` implementation may live
// in, and the local name hostCallName puts before a function's name.
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
	{processHostPath, "stdprocess"},
	{httpHostPath, "stdhttp"},
	{stringsHostPath, "stdstrings"},
	{regexHostPath, "stdregex"},
	{randomHostPath, "stdrandom"},
	{calendarHostPath, "stdcalendar"},
	{localeHostPath, "stdlocale"},
	{pluralHostPath, "stdplural"},
	{formatHostPath, "stdformat"},
	{phraseHostPath, "stdphraseformat"},
	{tomlHostPath, "stdtoml"},
	{csvHostPath, "stdcsv"},
	{xmlHostPath, "stdxml"},
	{passwordsHostPath, "stdpasswords"},
	{wasmHostPath, "stdwasm"},
}

// wasmHostPath is std/wasm/host's implementation: WebAssembly modules
// compiled and run by wazero. Its boundary is the Compiled, Live and Memory
// handles, Bytes, String, Int, List<Int64>, List<String> and the import
// functions over them.
const wasmHostPath = "github.com/nomi-language/nomi/internal/stdwasm"

// passwordsHostPath is std/passwords/host's implementation: the Argon2id and
// scrypt key derivations and bcrypt, over golang.org/x/crypto. Its boundary
// is Bytes, String, Int and Bool.
const passwordsHostPath = "github.com/nomi-language/nomi/internal/stdpasswords"

// csvHostPath is std/csv/host's implementation: a CSV reader over a
// Reader, over Go's encoding/csv, and one record written as CSV. Its
// boundary is the Records handle, Reader, String, Int, Bool, List<String>
// and io.Error.
const csvHostPath = "github.com/nomi-language/nomi/internal/stdcsv"

// xmlHostPath is std/xml/host's implementation: an XML token reader over
// a Reader, over Go's encoding/xml, and the writer's escaping. Its
// boundary is the Tokens handle, Reader, String, Int, Bool, List<String>
// and io.Error.
const xmlHostPath = "github.com/nomi-language/nomi/internal/stdxml"

// tomlHostPath is std/toml's host implementation: a `Toml"..."` literal's
// check and build, and a document's decode, over github.com/BurntSushi/toml.
// Its boundary is String, Float, Int and List<String>.
const tomlHostPath = "github.com/nomi-language/nomi/internal/stdtoml"

// phraseHostPath is std/phrase/format's host implementation, the
// MessageFormat 2 formatter. Its boundary is String, List<Int> and
// List<String>; the `Locale` crosses as its tag.
const phraseHostPath = "github.com/nomi-language/nomi/internal/stdphraseformat"

// formatHostPath is the host implementation of locale-aware number and list
// formatting and number parsing (`Int.format`, `Decimal.parse_in`,
// `String.join_list`, ...), declared in std/int, std/float, std/decimal and
// std/strings. Its boundary is String, Int, Float, Bool, List<String> and
// `Result<_, NumberParseError>`; the `Locale` crosses as its tag.
const formatHostPath = "github.com/nomi-language/nomi/internal/stdformat"

// pluralHostPath is std/plural's host implementation, CLDR plural rule
// selection. Its boundary is String, Int and List<Int>; std/plural.nomi
// turns a category's position back into the `PluralCategory` variant.
const pluralHostPath = "github.com/nomi-language/nomi/internal/stdplural"

// localeHostPath is std/locale's host implementation, a co-located adapter
// over golang.org/x/text/language and Nomi's CLDR data. Its boundary is
// String, Int and List<String>; the `Locale` type itself never crosses.
const localeHostPath = "github.com/nomi-language/nomi/internal/stdlocale"

// calendarHostPath is std/calendar's implementation, and it is the FIRST row
// that is the co-located adapter ITSELF rather than a shim beside one.
//
// std/regex's adapter is written in Go's own types, so `nomi/stdregex`
// exists to project it onto rt's. Calendar's boundary is
// spelled in `Int`, `String` and the structs rt declares — the same Go types
// under both projections — so there is nothing to project and the rows in
// internal/stdlibbindings name `nomi/std/calendar` directly.
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

// processHostPath is std/process/host's implementation: starting programs,
// for the reason ioHostPath is outside rt.
const processHostPath = "github.com/nomi-language/nomi/internal/stdprocess"

// httpHostPath is std/http/host's implementation: Go's HTTP client, for the
// reason ioHostPath is outside rt.
const httpHostPath = "github.com/nomi-language/nomi/internal/stdhttp"

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
// implementation is an adapter with Go-typed signatures — `(*T, error)`,
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

// hostCallName is a host implementation's name, the host package's local name
// plus the function's (`rt.StringContains`), and the import path of the
// package it lives in. Both are read off the symbol itself, so a rename
// cannot leave a stale string behind.
//
// runtime.FuncForPC gives the fully-qualified Go name
// ("github.com/nomi-language/nomi/rt.StringContains"). Anything that is not a
// plain top-level func in exactly one of the registered packages is rejected,
// which keeps a method value or a closure from producing a plausible-looking
// wrong name. A stdFunc keeps the name as rtCall, which marks it bound, and
// the path as rtHostPkg, which chooses the crossing (irExternHost,
// irCompilerHost).
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
// the direction the registry needs. Beyond them three families are admitted,
// and all three qualify for the reason a scalar does: each has one
// process-wide kind, so a kind projected here equals the same type's kind at
// a call site in any gen:
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
	case reflect.TypeFor[int64](), reflect.TypeFor[rt.Int]():
		// An int64 takes an Int that fits it, and rt.Int any Int.
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
	// A Nomi tuple, which a host function's signature spells as a flat
	// anonymous struct over `F0`, `F1`, …: `(Int, Int)` is
	// `struct { F0 int64; F1 int64 }`. The kind is the interned tagTuple a
	// std signature's tuple projects to (projectShared).
	//
	// A Go multiple return is a tuple too: the generated adapter projects it
	// element by element, and so does hostFnFor.
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
	// kindInvalid: lookup — "not a frame-taking Go callback", so fall through.
	if k := funcKindOfGoType(t); k != kindInvalid {
		return k
	}
	return preludeKindOfGoType(t)
}

// funcKindOfGoType is the function kind of a frame-taking Go callback, `func(fr
// *rt.Frame, A...) R`, or kindInvalid. The frame is the callback's own
// threading, as it is a host function's (hostFnFor), so it takes no Nomi
// parameter: `func(*rt.Frame) rt.Result[rt.Maybe[rt.Bytes], rt.IOError]` is
// `() -> Result<Maybe<Bytes>, Error>`. A callback without the frame is not
// admitted: the generated adapter would call it back on the frame of the
// call that made it, which a value kept past that call (a Reader.from_fn
// reader) has left.
func funcKindOfGoType(t reflect.Type) kind {
	if t == nil || t.Kind() != reflect.Func || t.IsVariadic() || t.NumOut() != 1 ||
		t.NumIn() == 0 || t.In(0) != reflect.TypeFor[*rt.Frame]() {
		return kindInvalid
	}
	params := make([]kind, 0, t.NumIn()-1)
	for i := 1; i < t.NumIn(); i++ {
		k := kindOfGoType(t.In(i))
		// kindInvalid: propagates — a parameter outside the representable set.
		if k == kindInvalid {
			return kindInvalid
		}
		params = append(params, k)
	}
	return funcKindIn(nil, params, kindOfGoType(t.Out(0)))
}

// listKindOfGoType is the kind an rt signature's `*rt.List[T]` names, or
// kindInvalid.
//
// Identified STRUCTURALLY, for the reason preludeKindOfGoType is: a hand-written
// `reflect.Type` per instantiation would be an unbounded table, and a name-only
// match would accept an unrelated rt type spelled `List`. So: a pointer, to a
// struct rt declares, whose name is `List[…]`, with EXACTLY the three fields
// rt.ListCell gives it and the two that constrain the shape checked — `Tail` is the
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
	// ifaceDefault is the interface function a std interface default's
	// template presents as decl, nil for any other function
	// (stdifacedefault.go).
	ifaceDefault *ast.InterfaceMethod
	// key is the host key for a `host fn` (the key internal/stdlibbindings
	// answers), and the same shape for a Nomi body. It is what a refusal names.
	key    string
	module string
	// id is the function's identity as the checker names it, the
	// analysis.StdFunc a call's record carries (Callee.Std): what byID
	// files it under. Zero for a declaration of a file that is not std's
	// (a test's fixture module).
	id analysis.StdFunc
	// recv is the impl block's receiver type name ("String", "Int"), empty for a
	// top-level declaration.
	recv string
	// iface is the interface the impl block implements, by the checker's
	// record of the block (analysis.ImplBlockTypeArgs), zero for an inherent
	// block, a top-level declaration and an impl of an interface no std
	// module declares. byIface files the function under it.
	iface analysis.StdType
	name  string
	// selfIdx is the parameter position holding the receiver, or -1 when the
	// function has none. Only 0 is served: the design doc's dispatch table says
	// the receiver is the Self-TYPED parameter at ANY position, and a stdlib impl
	// whose Self parameter is not first is refused rather than guessed at.
	selfIdx int

	params []kind
	result kind
	// defaultFill is, per parameter slot, whether the declaring module built
	// an accessor that evaluates that parameter's DEFAULT, which is how a
	// call that leaves the parameter out fills it.
	//
	// It starts nil and is set as emitStdSlotDefaults builds each accessor.
	// READ THROUGH callDefaultFill AND NEVER DIRECTLY, for the snapshot
	// reason below. See stdslotfill.go.
	defaultFill []bool

	// canon is the CANONICAL *stdFunc this one is a snapshot of, and nil when
	// this IS the canonical one.
	//
	// bindStdSiblings copies each candidate as `view := *f` so it can override
	// `body` and `pkg`, and that copy is taken BEFORE the lowering pass runs.
	// defaultFill and irSlot are set DURING that pass, on the canonical
	// pointer, so reading a copy would freeze them empty and a stdlib body
	// calling a defaulted SIBLING short would refuse however many accessors
	// had since been built. std's `//!` prompt cases make exactly that call,
	// for instance `Response.new(Status.ok())` against three parameters and
	// `Time.new(h, m)` against four.
	//
	// Reading through the pointer is not a relaxation of the pessimism, it is
	// the pessimism applied at the right TIME. A body lowered before the
	// accessor exists still refuses; lowerStdlibModule's fixed point re-lowers
	// it afterwards.
	canon *stdFunc

	// rtCall is set for a `host fn` the host tables bind: the name
	// hostCallName read, or `compilerhosts.<name>` for a std/compiler key.
	// The builder tests only whether it is set.
	rtCall string
	// rtHostPkg is the Go import path that function lives in, which chooses
	// the crossing a call makes (irExternHost, irCompilerHost). Empty until
	// rtCall is set. See hostPackages.
	rtHostPkg string
	// rtFrame is set when that rt function's Go signature takes the frame, so
	// the call site passes one. Copied off the registry row rather than
	// re-derived, and false for everything else. See hostFn.takesFrame.
	rtFrame bool
	// body and pkg are set for a Nomi body that lowered. pkg is the declaring
	// module's namespace key (stdPackage).
	body bool
	pkg  string
	// why names the refusal when this function is not lowerable, and detail
	// qualifies it. Exactly one of rtCall, body and why is set.
	why string

	decl *ast.FuncDef
	// node is the function's declaration as the std module's parse holds
	// it: its *ast.FuncDef, *ast.ExternFunc or interface's
	// *ast.InterfaceMethod (stdCandidateFor), what a call's record names
	// (analysis.Callee.Decl, verifyCallee).
	node ast.Node
	// irBody is the final retained declaration, owned by its cached module.
	irBody *ir.Func
	// irSlot is the VM's body of each default accessor (stdSlotName), by slot, set
	// on the canonical function. See irsupervisor.go's irStdSlotRetain.
	irSlot map[int]*ir.Func
}

func (f *stdFunc) lowerable() bool { return f.rtCall != "" || f.body }

// stdlibIndex is every stdlib function a call site can resolve, plus the IR
// graphs built for the ones with Nomi bodies.
type stdlibIndex struct {
	// byID keys each function by its identity (stdFunc.id): a module-level
	// function and an impl's alike, of every module the index holds. It is
	// the one lookup a call's record reaches std's function through
	// (stdFuncByID). An identity names its module, so no module's
	// declaration can answer for another's, as one spelling in byType
	// does for two modules' `Error.inspect`.
	byID map[analysis.StdFunc]*stdFunc
	// byType keys "String.contains" — the type-qualified spelling a call site
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
	// byIface keys an interface's function by its identity (the interface
	// as Owner, analysis.StdType.Func: `std/debug`'s `Debug.inspect`) and
	// then the RECEIVER kind, from the checker's record of the interface
	// each impl block implements; read through stdIfaceImpls. An interface
	// qualifier with a statically-known scalar receiver is the concrete-qualifier
	// row of the design's dispatch table: it names exactly one function, so it
	// lowers to a direct call and needs no table at run time.
	//
	// An overload set for the same reason byType is one, and the receiver kind
	// is NOT enough on its own: all eleven `impl Add<X, NaiveDateTime>` blocks
	// have `NaiveDateTime` in the receiver position and differ only in the
	// operand.
	byIface map[analysis.StdFunc]map[kind][]*stdFunc
	// unlowered names, per module namespace key (modulePkg), each function,
	// wrapper, accessor and `once` whose body the builder did not retain, and
	// the reason. A program's call to one declines, and noteStdBodyDeclined
	// reports the reason.
	unlowered map[string]map[string]string
	// irModules holds each lowered module's IR, by namespace key.
	// Callers must link these declaring modules, preserving symbol identity.
	irModules map[string]*ir.Module
	// modulePkg maps a stdlib module name to its namespace key (stdPackage).
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
	// declaration can be lowered against exactly the candidate set, sibling
	// scope and AST this module's own lowering used.
	//
	// RETAINED RATHER THAN RE-DERIVED, and that is a correctness requirement
	// rather than an optimisation. Calling stdModuleContext again would call
	// std.Load() again and resolve an instance against a second analysis, whose
	// candidate set must then match the one behind the cached module in the
	// SAME program or an instance body references a function the program does
	// not contain. analysis.installCanonicalStdTypes keeps two loads' `Bool`
	// the same type.
	views map[string]*stdModuleView
	// earlierMods[module] is the resolution scope module's own lowering had:
	// every stdlib module of an earlier lowering unit, and the other members
	// of its own unit, an import cycle (buildStdlibIndexBy). Never the module
	// itself.
	//
	// Recorded as a set of names rather than as a snapshot of the index,
	// because the index keeps growing as later units are lowered, so a
	// retained pointer would be the COMPLETE index by the time anything read
	// it. A name set plus `stdFunc.module` is enough to rebuild the
	// restricted view on demand (filtered).
	earlierMods map[string]map[string]bool
	// order is every module the index lowered or restores, in lowering
	// order, and modFuncs and modOnces what each one's lowering filed, by
	// module: what the index files again, in this order, when it restores
	// more modules (withModules).
	order    []string
	modFuncs map[string][]*stdFunc
	modOnces map[string][]*stdOnce
	// pending holds the std bundle's record of each module the index has not
	// restored yet, which the stdlib has not built: a process restores a
	// module once a program reaches it (stdlibIndex.withModules).
	pending map[string]*stdModuleRecord
	// objects is the std bundle's shared modules the records decode from
	// (nil for an index lowered without one), and objectsGen the decodes
	// irModules holds (stdObjects.gen): irModules holds every module
	// decoded when the index was made, restored or not, since a decoded
	// module's IR names what its dependencies define.
	objects    *stdObjects
	objectsGen int64
	// lib is the stdlib whose modules the index holds.
	lib *std.StdLib
	// unitSyms is set on the scope lowerStdUnit lowers a member of an
	// import cycle against: the unit's symbols by linkage name, which the
	// member's gen keeps (gen.stdUnitSyms).
	unitSyms map[string]*ir.Symbol
}

// stdlibOnce caches the index for the process.
//
// The cache owns its source contexts, signatures and IR graphs.
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
// A std.Load per question would be reached from every gen's anchor loaders
// and every interface-anchor pass, which would dominate the cost of lowering
// hello world.
//
// It is std.Shared, the same analysis the front end resolved the program
// against and buildStdlibIndex lowers from, so a `nomi run` analyzes the
// stdlib once (about 52 ms). Sharing it does not change what preludeAt
// answers inside std.
//
// Consumers only read it (Scope.Lookup, node walks), and it is built under
// sync.OnceValue, so concurrent gens may read it freely.
var stdAnchorLib = std.Shared

// stdBodies makes sure the shared stdlib has checked module's bodies before
// anything here reads them: std.Shared builds the stdlib from its modules'
// interfaces, whose bodies are stubs, and checks a module's bodies when
// asked (std.StdLib.Bodies). Every path that lowers a std body asks first:
// the module's own lowering, an instance of one of its generic functions,
// a listed module a program lowers, and a module's cases.
func stdBodies(module string) {
	if err := std.Shared().Bodies(module); err != nil {
		panic("irbuild: " + err.Error())
	}
}

// ensureStdModule has the shared stdlib build module, and what it imports,
// when it has not yet: a module's cases and its reference editors lower it
// from the stdlib's analysis and lowering, which hold only the modules
// built (std.Shared).
func ensureStdModule(module string) {
	std.Shared().Ensure(module)
}

// sharedStdTestDecls is the shared stdlib's std.StdLib.TestDecls.
func sharedStdTestDecls(module string) ([]ast.Node, error) {
	return std.Shared().TestDecls(module)
}

// stdlibCurrent is the index for the largest stdlib the process has built
// (std.Shared): stdlibCache with every module restored since.
var (
	stdlibCurrent   atomic.Pointer[stdlibIndex]
	stdlibCurrentMu sync.Mutex
)

// stdlibLowering is the process's stdlib lowering, holding every module the
// shared stdlib has built and every module whose object the process has
// decoded: decoded from the std bundle a module at a time as programs reach
// modules (withModules), or lowered whole.
func stdlibLowering() *stdlibIndex {
	first := stdlibFirstLowering()
	lib := std.Shared()
	if cur := stdlibCurrent.Load(); cur != nil && cur.lib == lib && !cur.staleObjects() {
		return cur
	}
	stdlibCurrentMu.Lock()
	defer stdlibCurrentMu.Unlock()
	cur := stdlibCurrent.Load()
	if cur == nil {
		cur = first
	}
	lib = std.Shared()
	if cur.lib != lib || cur.staleObjects() {
		cur = cur.withModules(lib)
	}
	stdlibCurrent.Store(cur)
	return cur
}

// stdlibFirstLowering is the index the process first made.
func stdlibFirstLowering() *stdlibIndex {
	// The cache holds no test body: a module's `//!` prompt cases are built
	// per call, in their own module, by GenerateStdlibTestIR (stdtests.go).
	stdlibOnce.Do(func() {
		// The cache is built inside whichever load comes first, in gens
		// of its own whose census has no hooks: its declines reaching that
		// one program's hooks and no later one's would make a program's
		// diagnostics depend on what the process loaded before it
		// (TestLoadOrderDoesNotChangeAProgram).
		// Decoded from the std bundle when this binary has one, else lowered
		// and kept as one for the next process (stdbundle.go).
		stdlibCache = loadStdlibIndex()
	})
	return stdlibCache
}

// stdPackage is the namespace key of the i'th stdlib module: its gen's pkg,
// and the key of its IR module and its unlowered table in the index. It
// names no compiled artifact. The keys are apart from unitPackage's, so a
// stdlib module's key and a user unit's never collide.
func stdPackage(i int) string { return fmt.Sprintf("nomistd%d", i) }

// buildStdlibIndex lowers every stdlib module, in dependency order.
func buildStdlibIndex() *stdlibIndex {
	return buildStdlibIndexBy(lowerSharedStdModule, nil)
}

// stdModuleLowering lowers one stdlib module through its view: what
// lowerStdlibModule does, or what restoreStdModule does from a std bundle.
type stdModuleLowering func(v *stdModuleView, earlier *stdlibIndex) (funcs []*stdFunc, onces []*stdOnce, irModule *ir.Module)

// newStdlibIndex is an index holding no module, whose namespace keys are
// numbered over names, every module of the stdlib, sorted.
func newStdlibIndex(names []string) *stdlibIndex {
	idx := &stdlibIndex{
		byID:        map[analysis.StdFunc]*stdFunc{},
		byType:      map[string][]*stdFunc{},
		byIface:     map[analysis.StdFunc]map[kind][]*stdFunc{},
		irModules:   map[string]*ir.Module{},
		unlowered:   map[string]map[string]string{},
		modulePkg:   map[string]string{},
		byKey:       map[string]*stdFunc{},
		byOnce:      map[string]*stdOnce{},
		views:       map[string]*stdModuleView{},
		earlierMods: map[string]map[string]bool{},
		modFuncs:    map[string][]*stdFunc{},
		modOnces:    map[string][]*stdOnce{},
	}
	for _, name := range names {
		idx.modulePkg[name] = stdPackage(sort.SearchStrings(names, name))
	}
	return idx
}

// buildStdlibIndexBy is buildStdlibIndex with each module lowered by lower.
// lowered, when not nil, is told of each module as soon as lower returns it,
// before the index files its functions. It lowers every module of the
// stdlib, which it builds whole first (std.All).
func buildStdlibIndexBy(lower stdModuleLowering, lowered func(name string, v *stdModuleView, irModule *ir.Module)) *stdlibIndex {
	lib := std.All()
	names := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		names = append(names, name)
	}
	// Sorted so a module's namespace key (stdPackage) never depends on map
	// iteration order.
	sort.Strings(names)

	idx := newStdlibIndex(names)
	idx.lib = lib
	// A stdlib module's Nomi body may call another module's lowered function:
	// `std/instant`'s `impl Add<Duration, Instant>` is
	// `Instant(nanos + Duration.as_nanos(rhs))`. A module resolves such a call
	// against the modules lowered before it, so modules are lowered in
	// dependency order, imports first (stdLoweringOrder). A module's namespace
	// key is `stdPackage(i)` over the sorted list; only the visiting order follows
	// dependencies. A name order would refuse bodies on order alone:
	// `std/calendar` sorts near the front and imports `comparable`,
	// `duration`, `int` and `strings`, all of which sort later.
	//
	// # An import cycle is one unit
	//
	// Nomi's import graph may be cyclic (std/strings and std/iter import each
	// other, and std/int, std/bytes and std/code_points). No order puts every
	// member of a cycle after every other, so the members of one are lowered
	// as one unit (stdLoweringUnits, lowerStdUnit): each against the modules
	// before the unit and every other member, until no member lowers more.
	// What lowers then does not depend on the members' order, which an edit
	// to one member's imports may change
	// (TestStdLoweringIsTheSameInEveryCycleOrder). earlierMods[name] is that
	// scope: the modules of earlier units and the unit's other members.
	done := map[string]bool{}
	for _, unit := range stdLoweringUnits(stdLoweringOrder(names, lib), lib) {
		var members []string
		for _, name := range unit {
			if len(lib.Nodes[name]) > 0 && !isProgramStd(name) {
				members = append(members, name)
			}
		}
		if stdUnitOrderForTest != nil && len(members) > 1 {
			members = stdUnitOrderForTest(slices.Clone(members))
		}
		for _, name := range members {
			scope := make(map[string]bool, len(done)+len(members))
			for m := range done {
				scope[m] = true
			}
			for _, m := range members {
				if m != name {
					scope[m] = true
				}
			}
			idx.earlierMods[name] = scope
		}
		for _, r := range lowerStdUnit(members, idx, lower) {
			name, v := r.v.module, r.v
			pkg := idx.modulePkg[name]
			// The FileAnalysis goes with the nodes: the tree carries the
			// annotations the stdlib author WROTE and the analysis carries the
			// ones the checker solved, and a backend handed only nodes has to
			// re-infer or refuse.
			//
			// The view is retained on the index rather than built inside
			// lowerStdlibModule, so that a per-program instance is lowered
			// against this exact context. See stdlibIndex.views for why
			// re-deriving it is unsound rather than merely wasteful.
			idx.views[name] = v
			if lowered != nil {
				lowered(name, v, r.irModule)
			}
			if r.irModule != nil {
				if lintEmbeddedStdlib {
					irLintFinished(r.irModule)
				}
				idx.irModules[pkg] = r.irModule
			}
			if len(v.unlowered) > 0 {
				idx.unlowered[pkg] = v.unlowered
			}
			for _, f := range r.funcs {
				idx.add(f)
			}
			for _, o := range r.onces {
				idx.addOnce(o)
			}
			idx.order = append(idx.order, name)
			idx.modFuncs[name], idx.modOnces[name] = r.funcs, r.onces
		}
		for _, name := range members {
			done[name] = true
		}
	}
	return idx
}

// stdUnitOrderForTest, when set, reorders the members of each import
// cycle buildStdlibIndexBy lowers, so a test can hold what lowers to be
// the same in every order (TestStdLoweringIsTheSameInEveryCycleOrder).
var stdUnitOrderForTest func(members []string) []string

// stdModuleLowerings counts, process-wide, the std modules lowerStdUnit has
// lowered, a member of an import cycle once per pass, so a test can hold the
// sibling bindings to a bound per lowering (TestStdSiblingBinds_*).
var stdModuleLowerings atomic.Int64

// stdUnitModule is one module of a lowering unit as lowered: its view and
// what lower returned for it.
type stdUnitModule struct {
	v        *stdModuleView
	funcs    []*stdFunc
	onces    []*stdOnce
	irModule *ir.Module
}

// stdUnitMaxPasses bounds lowerStdUnit's passes over one import cycle.
// Each pass but the last lowers more bodies than the one before, so the
// bound is a guard against a lowering that is not monotone, not a limit
// the stdlib comes near: its largest cycle settles in two or three.
const stdUnitMaxPasses = 16

// lowerStdUnit lowers the members of one lowering unit (stdLoweringUnits),
// in order, against idx, which holds every module of the earlier units.
//
// A unit of one module is lowered once, against idx. The members of an
// import cycle are lowered in passes. In each pass a member is lowered
// against idx, the members before it as this pass lowered them, and the
// members after it as the pass before lowered them, so a body may call a
// function of any member; the passes stop when a pass lowers each member
// after the first exactly as the pass before did, which is when no member
// can lower more. Lowering is monotone: offered more callees, a module
// lowers a superset of its bodies. So what lowers is the least fixed point
// over the unit, whichever member comes first.
//
// A member lowered again keeps the symbols its earlier pass gave its
// declarations (scope.unitSyms, gen.stdUnitSyms), since the other
// members' bodies call them by symbol. In the last pass each member called
// only functions the members after it lowered the pass before, which that
// pass lowers too, under the same symbols, so every call names a defined
// function.
func lowerStdUnit(members []string, idx *stdlibIndex, lower stdModuleLowering) []*stdUnitModule {
	lib := idx.lib
	lowerOne := func(name string, scope *stdlibIndex) *stdUnitModule {
		stdModuleLowerings.Add(1)
		// SourcePath, not FileURI: a position names the file, and nothing
		// here opens it, so no per-module stat or materialization runs.
		v := stdModuleContext(name, lib.SourcePath(name), idx.modulePkg[name], lib.Nodes[name], lib.Files[name], scope)
		v.embedded = true
		scope.views[name] = v
		funcs, onces, m := lower(v, scope)
		// A module none of whose bodies lowered holds nothing to link.
		if m != nil && len(m.Funcs()) == 0 && len(m.Cells()) == 0 {
			m = nil
		}
		return &stdUnitModule{v: v, funcs: funcs, onces: onces, irModule: m}
	}
	switch len(members) {
	case 0:
		return nil
	case 1:
		return []*stdUnitModule{lowerOne(members[0], idx)}
	}
	syms := map[string]*ir.Symbol{}
	var prev []*stdUnitModule
	for pass := 1; ; pass++ {
		cur := make([]*stdUnitModule, len(members))
		for i, name := range members {
			scope := idx.filtered(func(string) bool { return true })
			scope.lib, scope.unitSyms = lib, syms
			for j := range members {
				var r *stdUnitModule
				switch {
				case j < i:
					r = cur[j]
				case j > i && prev != nil:
					r = prev[j]
				}
				if r == nil {
					continue
				}
				for _, f := range r.funcs {
					scope.add(f)
				}
				for _, o := range r.onces {
					scope.addOnce(o)
				}
			}
			cur[i] = lowerOne(name, scope)
			cur[i].keepSymbols(syms)
		}
		if prev != nil {
			settled := true
			for j := 1; j < len(members); j++ {
				was, now := prev[j].loweredKeys(), cur[j].loweredKeys()
				for k := range was {
					if !now[k] {
						panic(fmt.Sprintf("irbuild: std/%s lowered %s in pass %d of its import cycle and not in pass %d; "+
							"a member's lowering must be monotone in what the others lower", members[j], k, pass-1, pass))
					}
				}
				if len(now) != len(was) {
					settled = false
				}
			}
			if settled {
				return cur
			}
		}
		if pass == stdUnitMaxPasses {
			panic(fmt.Sprintf("irbuild: the import cycle %v still lowers more after %d passes", members, pass))
		}
		prev = cur
	}
}

// keepSymbols files under its linkage name the symbol of each declaration
// m lowered, so the next pass over its unit gives the declaration the same
// symbol.
func (m *stdUnitModule) keepSymbols(syms map[string]*ir.Symbol) {
	keep := func(s *ir.Symbol) {
		if s != nil && s.Link() != "" && syms[s.Link()] == nil {
			syms[s.Link()] = s
		}
	}
	for _, f := range m.funcs {
		if f.canon != nil {
			f = f.canon
		}
		if f.irBody != nil {
			keep(f.irBody.Sym())
		}
		for _, s := range f.irSlot {
			keep(s.Sym())
		}
	}
	for _, o := range m.onces {
		keep(o.irCell)
	}
}

// loweredKeys is what m lowered that another module can call or read: each
// function body and default accessor, and each
// `once` cell.
func (m *stdUnitModule) loweredKeys() map[string]bool {
	out := map[string]bool{}
	for _, f := range m.funcs {
		c := f
		if f.canon != nil {
			c = f.canon
		}
		id := fmt.Sprintf("%s@%p", f.key, f.decl)
		if c.irBody != nil {
			out[id] = true
		}
		for s := range c.irSlot {
			out[fmt.Sprintf("%s/slot%d", id, s)] = true
		}
	}
	for _, o := range m.onces {
		if o.irCell != nil {
			out["once "+o.key] = true
		}
	}
	return out
}

// stdLoweringUnits groups order, a lowering order (stdLoweringOrder), into
// the units buildStdlibIndexBy lowers: each import cycle of the lowering
// graph (stdModuleImports) is one unit, its members in their relative
// order, at the place of its last member; every other module is a unit of
// its own. A module's imports are then in its own unit or an earlier one:
// a module the order puts between two members of a cycle and that imports
// one of them would reach the other, and be a member itself.
func stdLoweringUnits(order []string, lib *std.StdLib) [][]string {
	pos := make(map[string]int, len(order))
	for i, name := range order {
		pos[name] = i
	}
	// Tarjan's strongly connected components over the modules of order.
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	comp := map[string]int{}
	var comps [][]string
	var connect func(string)
	connect = func(name string) {
		index[name] = len(index)
		low[name] = index[name]
		stack = append(stack, name)
		onStack[name] = true
		for _, dep := range stdModuleImports(lib.Nodes[name]) {
			if _, known := pos[dep]; !known {
				continue
			}
			if _, seen := index[dep]; !seen {
				connect(dep)
				low[name] = min(low[name], low[dep])
			} else if onStack[dep] {
				low[name] = min(low[name], index[dep])
			}
		}
		if low[name] != index[name] {
			return
		}
		var members []string
		for {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[top] = false
			comp[top] = len(comps)
			members = append(members, top)
			if top == name {
				break
			}
		}
		sort.Slice(members, func(a, b int) bool { return pos[members[a]] < pos[members[b]] })
		comps = append(comps, members)
	}
	for _, name := range order {
		if _, seen := index[name]; !seen {
			connect(name)
		}
	}
	units := make([][]string, 0, len(comps))
	for _, name := range order {
		members := comps[comp[name]]
		if members[len(members)-1] == name {
			units = append(units, members)
		}
	}
	return units
}

// withModules answers x with every module lib has built and x has not
// restored restored from the std bundle's records (pending), in a new
// index, so a gen holding x keeps reading it unchanged. The new index files
// every restored module's functions again in lowering order, so it is the
// index a lowering of those modules would file: a bundle records each
// declaration's outcome over the whole stdlib (lowerStdBundle), and filing
// a part of it marks no declaration the whole would not.
func (x *stdlibIndex) withModules(lib *std.StdLib) *stdlibIndex {
	y, err := x.restoring(lib, true)
	if err != nil {
		panic("irbuild: " + err.Error())
	}
	return y
}

// staleObjects reports that a module's object was decoded after x was
// made, so x's irModules lacks it.
func (x *stdlibIndex) staleObjects() bool {
	return x.objects.generation() != x.objectsGen
}

// restoring is withModules. It decodes each module it restores from the
// bundle's objects first, with the modules whose objects define what its
// own names (stdObjects.decode). A module the bundle does not restore is an
// error when lower is false; otherwise it is lowered, as a process without
// the bundle lowers it, and in a test it panics: the bundle was made from
// this stdlib (its digest), so a module it does not match is a defect.
func (x *stdlibIndex) restoring(lib *std.StdLib, lower bool) (*stdlibIndex, error) {
	var restore []string
	for _, name := range x.order {
		if x.pending[name] != nil && lib.Files[name] != nil && len(lib.Nodes[name]) > 0 {
			restore = append(restore, name)
		}
	}
	if len(restore) > 0 && x.objects != nil {
		if err := x.objects.decode(restore...); err != nil {
			if !lower {
				return nil, err
			}
			if testing.Testing() {
				panic("irbuild: " + err.Error())
			}
			stdBundleDebug("%v", err)
		}
	}
	irModules, objectsGen := x.objects.decodedModules(x.irModules, x.modulePkg)
	if len(restore) == 0 {
		y := *x
		y.lib = lib
		y.irModules, y.objectsGen = irModules, objectsGen
		return &y, nil
	}
	y := newStdlibIndex(nil)
	y.modulePkg, y.earlierMods, y.order = x.modulePkg, x.earlierMods, x.order
	y.lib = lib
	y.objects, y.irModules, y.objectsGen = x.objects, irModules, objectsGen
	for pkg, u := range x.unlowered {
		y.unlowered[pkg] = u
	}
	for name, v := range x.views {
		y.views[name] = v
	}
	for name, fs := range x.modFuncs {
		y.modFuncs[name] = fs
	}
	for name, os := range x.modOnces {
		y.modOnces[name] = os
	}
	y.pending = make(map[string]*stdModuleRecord, len(x.pending))
	for name, rec := range x.pending {
		y.pending[name] = rec
	}
	for _, name := range restore {
		rec := y.pending[name]
		delete(y.pending, name)
		v := stdModuleContext(name, lib.SourcePath(name), y.modulePkg[name], lib.Nodes[name], lib.Files[name], y)
		v.embedded = true
		funcs, onces, irModule, err := restoreStdModule(v, rec)
		if err != nil {
			if !lower {
				return nil, err
			}
			if testing.Testing() {
				panic("irbuild: the std bundle does not restore " + name + ": " + err.Error())
			}
			stdBundleDebug("restoring %s: %v", name, err)
			v = stdModuleContext(name, lib.SourcePath(name), y.modulePkg[name], lib.Nodes[name], lib.Files[name], y)
			v.embedded = true
			funcs, onces, irModule = lowerSharedStdModule(v, y)
			if irModule != nil && len(irModule.Funcs()) == 0 && len(irModule.Cells()) == 0 {
				irModule = nil
			}
		}
		y.views[name] = v
		pkg := y.modulePkg[name]
		if irModule != nil {
			y.irModules[pkg] = irModule
		}
		if len(v.unlowered) > 0 {
			y.unlowered[pkg] = v.unlowered
		}
		y.modFuncs[name], y.modOnces[name] = funcs, onces
	}
	if len(y.pending) == 0 {
		y.pending = nil
	}
	for _, name := range y.order {
		for _, f := range y.modFuncs[name] {
			y.add(f)
		}
		for _, o := range y.modOnces[name] {
			y.addOnce(o)
		}
	}
	return y, nil
}

// stdLoweringOrder lowers imports before their users, starting with Context's
// dependency graph because every execution installs a Context. Remaining roots
// and each module's dependencies are visited in sorted name order.
//
// Back edges are skipped, so the members of an import cycle come in the
// order the walk reached them; stdLoweringUnits groups each cycle into one
// unit, whose members' order does not change what lowers (lowerStdUnit).
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
			// ancestors. Skipping it is what confines a Nomi import cycle to
			// one unit rather than an infinite descent.
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
//
// Every import is an edge, an import that closes a cycle included: the
// members of a cycle are lowered as one unit (lowerStdUnit), so no import
// is left out to decide which member comes first.
func stdModuleImports(nodes []ast.Node) []string {
	seen := map[string]bool{}
	add := func(imp *ast.ImportStmt) {
		if len(imp.ModulePath) == 0 {
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
	if x.byID != nil && !f.id.IsZero() {
		// No two declarations share an identity (TestStdFuncsAreDistinct),
		// and an index files each declaration once.
		x.byID[f.id] = f
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
	if !f.iface.IsZero() && f.selfIdx == 0 && len(f.params) > 0 && f.params[0] != kindInvalid {
		key := f.iface.Func(f.name)
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
// `pub host fn to_string(data: Bytes) -> Result<String, String>`, and
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
		if !f.iface.IsZero() {
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
// the call site's own checks stay the report for a bad call, rather than
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
// returning either is safe: the caller declines it by its mark.
func stdPick(fs []*stdFunc, args []kind) *stdFunc {
	if len(fs) == 1 {
		return fs[0]
	}
	var match *stdFunc
	for _, f := range fs {
		if !sameParamKinds(f.params, args) {
			continue
		}
		if match == nil || (!match.iface.IsZero() && f.iface.IsZero()) {
			match = f
		}
	}
	return match
}

// underlay adds every entry `earlier` holds that this index, a std
// module's own (bindStdSiblings), does not, leaving this index's own
// entries untouched.
//
// It is deliberately NOT `add`. add marks a duplicate key AMBIGUOUS on both
// entries, which is right for two declarations inside one module and wrong
// here. Under one spelling in byType or byIface a module's own entries hide
// an earlier module's: in std/calendar's gen `Error.inspect` is calendar's
// `Error`'s, never std/locale's, and a cross-module spelling that collided
// with a local one loses silently rather than poison the local entry's
// refusal reason. A call whose record names the other module's function
// finds it in byID, whose keys carry the module, so nothing hides there.
func (x *stdlibIndex) underlay(earlier *stdlibIndex) {
	if earlier == nil {
		return
	}
	byID := maps.Clone(earlier.byID)
	if byID == nil {
		byID = map[analysis.StdFunc]*stdFunc{}
	}
	for id, f := range x.byID {
		byID[id] = f
	}
	x.byID = byID
	// The earlier slice is ALIASED rather than copied, and that is sound
	// because underlay runs after every local add and nothing appends to a
	// local set afterwards. An append on the earlier index for a later module
	// writes past this alias's length, so it cannot be seen here.
	// The earlier map cloned whole and this module's own sets laid over it,
	// which is the map the per-key fill would build: a key keeps its own set
	// unless the set is empty and an earlier module has one. A clone copies
	// the table in one pass; a generic stdlib instance binds its module this
	// way each time it is built (stdInstances.queue).
	byType := maps.Clone(earlier.byType)
	if byType == nil {
		byType = map[string][]*stdFunc{}
	}
	for key, fs := range x.byType {
		if len(fs) > 0 || len(byType[key]) == 0 {
			byType[key] = fs
		}
	}
	x.byType = byType
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
// (`Add<Duration, NaiveDateTime>`, which is bound to an rt function) would
// mark the other ten settled, so bindStdSiblings would offer siblings a body
// that does not lower. See lowerStdlibModule.
type stdCandidate struct {
	f  *stdFunc
	fd *ast.FuncDef
	// decl is the declaration, of a `host fn` too: what a call the checker
	// resolved to it finds it by (gen.stdSiblingDecls).
	decl ast.Node
}

// lowerSharedStdModule lowers one of the shared stdlib's modules, asking std
// to check its bodies first (stdBodies). lowerStdlibModule itself does not
// ask: a test's fixture module and an edited module bring a view checked
// from their own source, which std does not carry.
func lowerSharedStdModule(v *stdModuleView, earlier *stdlibIndex) ([]*stdFunc, []*stdOnce, *ir.Module) {
	stdBodies(v.module)
	return lowerStdlibModule(v, earlier)
}

// lowerStdlibModule lowers one stdlib module's bodies into one ir.Module, and
// returns every function it saw, lowered or refused, so call sites can
// resolve or refuse by name.
//
// # Which bodies lower
//
// stdSettle offers every host binding and every Nomi body whose signature is
// representable (v.settled). A Nomi body may call a sibling declared before
// it, one declared after it, or itself (`impl Debug for Float`'s `inspect`
// calls `to_string`; `bytes.join_debug_parts` calls itself), so which bodies
// build is a greatest fixed point, which irPrebuildStdBodies takes. Every
// offered body is assumed to build, a round builds them until no more build,
// a body still unbuilt is withdrawn, and the rounds repeat until one
// withdraws nothing. A body that needs a withdrawn sibling is withdrawn in
// the next round. The offered set is finite and only shrinks, so the rounds
// end.
//
// The lowering pass that follows lowers each survivor once, under one sibling
// binding, and finds every callee already in the module.
//
// `earlier` is the index of the modules this one may reference: those of
// earlier lowering units and the other members of its own import cycle
// (buildStdlibIndexBy). The third result is the module's IR, nil when
// nothing lowered.
// The `once` bindings this module declares come back beside the functions, for
// the reason they go into the index at all: a later module's body may read
// `Int.max_value`. See stdonce.go.
//
// `v` is the module's lowering context, built by the caller through
// stdModuleContext. It is built by the caller because buildStdlibIndex retains
// it on the index, so a per-program instance of a generic declaration can be
// lowered against the same context this function used. Re-deriving it there
// would resolve the instance against a second analysis; see
// stdlibIndex.views.
//
// MarkTailCalls is stdModuleContext's first line, and its own header carries why:
// std.Load() runs the sweeps and CheckTypes but never MarkTailCalls, so
// `Call.IsTailCall` is FALSE on every stdlib node this builder reads and any
// tail-position reasoning about a stdlib body is VACUOUS without it.
func lowerStdlibModule(v *stdModuleView, earlier *stdlibIndex) (funcs []*stdFunc, onces []*stdOnce, irModule *ir.Module) {
	return lowerStdlibModuleIn(v, earlier, nil)
}

// lowerStdlibModuleIn is lowerStdlibModule noting its declines in dc, a
// lowering's census, or in the module gen's own when dc is nil.
func lowerStdlibModuleIn(v *stdModuleView, earlier *stdlibIndex, dc *declineCensus) (funcs []*stdFunc, onces []*stdOnce, irModule *ir.Module) {
	module, pkg := v.module, v.pkg
	cands, onces, emittable := v.cands, v.onces, v.settled

	g := v.gen(earlier)
	if dc != nil {
		g.dc = dc
	}
	g.skipLint = v.embedded && !lintEmbeddedStdlib
	emitted := false
	// The prebuild settles the `once` bindings with the bodies, since an
	// initializer may call a body and a body may read a binding.
	g.irPrebuildStdBodies(module, pkg, cands, emittable, onces, earlier)
	// Every body sees every offered sibling: the graphs irPrebuildStdBodies
	// left in the module may link to a member of a recursive cycle from any
	// body. The default accessors are built inside the same window,
	// because a default expression is Nomi written in this module and may
	// reach a sibling exactly as the body may.
	//
	// Bound ONCE for the loop. Lowering a body changes its candidate's
	// body (to the value the view already holds) and, through the canonical
	// pointer every reader goes through, defaultFill and irSlot; the refusal arm below changes body, pkg and why, and a fresh
	// binding would differ from this one only in the view's why, which the
	// arm writes to the view as well.
	restoreSiblings, views := g.bindStdSiblingViews(module, pkg, cands, emittable, earlier)
	// The bindings before the bodies, because a body reads one through its
	// cell, and inside the sibling window, because an initializer calls a
	// body as a body does. A lowered binding counts as output on its own: a
	// module with a lowerable `once` and no lowerable function still returns
	// its IR module, which holds the cell a reference names.
	for _, o := range onces {
		if !o.settled {
			continue
		}
		g.emitStdOnce(o)
		emitted = true
	}
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
			// The prebuild built this body against the same siblings, so the
			// arm is not expected to run. It refuses all the same, so a call
			// site reads `why` and declines. A sibling lowered earlier in
			// this loop may already hold a call to c's symbol, which setting
			// `why` here does not remove, so the prebuild has to withdraw
			// such a body first.
			c.f.why = "unlowered stdlib function"
			if v := views[c]; v != nil {
				v.why = c.f.why
			}
			continue
		}
		emitted = true
	}
	restoreSiblings()

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
// The resulting key is `lists.List.first`, the documented host-key convention
// (`int.Int.to_string`, `iter.MapIter.next`). A generic receiver's methods are
// generic, so they refuse under their own name at their own call site.
func collectStdCandidates(module string, nodes []ast.Node, fa *analysis.FileAnalysis) []*stdCandidate {
	var out []*stdCandidate
	declared := stdDeclared(fa)
	for _, n := range nodes {
		switch t := n.(type) {
		case *ast.FuncDef:
			out = append(out, stdCandidateFor(module, "", analysis.StdType{}, "", t.Name, t.Params, t.ReturnTypeExpr,
				len(t.TypeParams) > 0 || len(t.WhereClauses) > 0, len(t.Decorators) > 0, t, fa))
		case *ast.ExternFunc:
			generic := len(t.TypeParams) > 0 || len(t.WhereClauses) > 0
			if generic && phantomOnly(typeParamNames(t.TypeParams), fa, nil, stdSig{t.Params, t.ReturnTypeExpr, t.WhereClauses}) {
				generic = false
			}
			out = append(out, stdCandidateFor(module, "", analysis.StdType{}, "", t.Name, t.Params, t.ReturnTypeExpr,
				generic, false, t, fa))
		case *ast.ImplBlock:
			recv := analysis.TypeExprBaseName(t.Receiver)
			iface := analysis.ImplBlockTypeArgs(fa, t).Std()
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
			if declared != nil {
				for _, n := range analysis.ReceiverTypeParamNames(t.Receiver, declared) {
					if n != "" {
						recvTP[n] = true
					}
				}
			}
			// A block whose every type parameter is a phantom host type's
			// argument (`impl Matcher for Regex<C>`, `impl<C> Contains<Regex<C>>
			// for String`) is not generic to this builder: see phantomOnly.
			if blockTP := typeParamNames(t.Generics); len(t.WhereClauses) == 0 && len(blockTP)+len(recvTP) > 0 {
				if blockTP == nil {
					blockTP = map[string]bool{}
				}
				for n := range recvTP {
					blockTP[n] = true
				}
				sigs := make([]stdSig, 0, len(t.Items))
				for _, item := range t.Items {
					switch it := item.(type) {
					case *ast.FuncDef:
						sigs = append(sigs, stdSig{it.Params, it.ReturnTypeExpr, it.WhereClauses})
					case *ast.ExternFunc:
						sigs = append(sigs, stdSig{it.Params, it.ReturnTypeExpr, it.WhereClauses})
					}
				}
				if phantomOnly(blockTP, fa, []ast.TypeExpr{t.Receiver, t.Interface}, sigs...) {
					implGeneric = false
					recvTP = map[string]bool{}
				}
			}
			blocked := recv == ""
			for _, item := range t.Items {
				switch it := item.(type) {
				case *ast.FuncDef:
					// namesBlockParam is the owner-function rule a user
					// gen applies too (ownerimpl.go): `Iter.from(start:
					// Int)` names no block parameter and is monomorphic.
					own := (len(it.TypeParams) > 0 || len(it.WhereClauses) > 0) &&
						!phantomOnly(typeParamNames(it.TypeParams), fa, nil, stdSig{it.Params, it.ReturnTypeExpr, it.WhereClauses})
					out = append(out, stdCandidateFor(module, recv, iface, ifaceKey, it.Name, it.Params, it.ReturnTypeExpr,
						implGeneric || own || namesBlockParam(it, recvTP),
						blocked || len(it.Decorators) > 0, it, fa))
				case *ast.ExternFunc:
					own := (len(it.TypeParams) > 0 || len(it.WhereClauses) > 0) &&
						!phantomOnly(typeParamNames(it.TypeParams), fa, nil, stdSig{it.Params, it.ReturnTypeExpr, it.WhereClauses})
					out = append(out, stdCandidateFor(module, recv, iface, ifaceKey, it.Name, it.Params, it.ReturnTypeExpr,
						implGeneric || own ||
							sigNamesTypeParam(it.Params, it.ReturnTypeExpr, recvTP), blocked, it, fa))
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
			// claim it is the answer — and a qualified call consults byType before
			// byIface, so `Debug.inspect(42)` would stop resolving to Int's
			// `impl Debug` body and start refusing because `self` is not a
			// scalar.
			for i := range t.Methods {
				m := &t.Methods[i]
				if !m.Extern {
					continue
				}
				out = append(out, stdCandidateFor(module, t.Name, analysis.StdType{}, "", m.Name, m.Params, m.ReturnTypeExpr,
					len(t.TypeParams) > 0 || len(t.WhereClauses) > 0 ||
						len(m.TypeParams) > 0 || len(m.WhereClauses) > 0, false, m, fa))
			}
		}
	}
	return out
}

// decl is the function's declaration: an *ast.FuncDef for a Nomi body, an
// *ast.ExternFunc or an interface's *ast.InterfaceMethod for a `host fn`.
func stdCandidateFor(module, recv string, iface analysis.StdType, ifaceKey, name string, params []ast.Param, ret ast.TypeExpr, generic, blocked bool, decl ast.Node, fa *analysis.FileAnalysis) *stdCandidate {
	fd, _ := decl.(*ast.FuncDef)
	progSig := false
	f := &stdFunc{
		key:     stdKey(module, recv, ifaceKey, name),
		module:  module,
		recv:    recv,
		iface:   iface,
		name:    name,
		selfIdx: -1,
		result:  kindUnit,
		decl:    fd,
		node:    decl,
	}
	defaulted := false
	for i, p := range params {
		k := projectShared(stdParamType(fa, p))
		f.params = append(f.params, k)
		if recv != "" && f.selfIdx < 0 && stdParamHeadName(p) == recv {
			f.selfIdx = i
		}
		// kindInvalid: no-position — classifies a stdlib declaration into f.why; no Nomi position.
		if k == kindInvalid {
			if fd != nil && fd.Body != nil && stdProgSigType(fa, p.TypeAnnotation) {
				progSig = true
			} else {
				blocked = true
			}
		}
		// A DEFAULT is noted, not folded into `blocked`: a default says
		// nothing about the SIGNATURE. `stdcalendar.DateTime.in_zone` projects
		// the IDENTICAL signature to `in_zone_resolve` beside it —
		// [NaiveDateTime, String, Disambiguation] -> Result<DateTime, Error> —
		// which LOWERS through rt.ResolveInZone.
		if p.Default != nil {
			defaulted = true
		}
	}
	if ret != nil {
		f.result = projectShared(stdTypeExprType(fa, ret))
		// kindInvalid: no-position — classifies a stdlib declaration into f.why; no Nomi position.
		if f.result == kindInvalid {
			if fd != nil && fd.Body != nil && stdProgSigType(fa, ret) {
				progSig = true
			} else {
				blocked = true
			}
		}
	}
	c := &stdCandidate{f: f, fd: fd, decl: decl}
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
		f.why = stdOutsideSubsetWhy
	case progSig:
		f.why = stdProgSigWhy
	case fd == nil && defaulted:
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
		if h, mapped := stdlibHostFunc(f.key); mapped {
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

// stdParamType is the type the checker gave a stdlib parameter: its
// annotation's (`rhs: Duration`), its pattern's for a destructuring one
// (`Duration.as_nanos(Duration(ns))`, whose head is its type), or, for one
// written with neither (`parents = False`), the type it takes from its
// default. A signature's kinds are this type projected with no gen
// (projectShared), as every other std position's are.
func stdParamType(fa *analysis.FileAnalysis, p ast.Param) analysis.Type {
	if fa == nil {
		return nil
	}
	switch {
	case p.TypeAnnotation != nil:
		return fa.TypeExprTypes[p.TypeAnnotation]
	case p.Destructure != nil:
		return fa.PatternTypes[p.Destructure]
	}
	return paramSymbolType(fa, p)
}

// stdTypeExprType is the type the checker recorded for a type std writes in a
// signature or on a `once`, or nil.
func stdTypeExprType(fa *analysis.FileAnalysis, te ast.TypeExpr) analysis.Type {
	if fa == nil {
		return nil
	}
	return fa.TypeExprTypes[te]
}

// stdDeclared answers whether a name is a type in a std module's scope, the
// resolver half of analysis.ReceiverTypeParamNames, which tells a receiver's
// type parameter (`Map<K, V>`) from a concrete type argument (`Map<String,
// V>`). It is nil when the module has no scope, and the receiver-derived
// rule is then skipped rather than answering "everything is a type
// parameter".
func stdDeclared(fa *analysis.FileAnalysis) func(string) bool {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	scope := fa.ModuleScope
	return func(n string) bool { return scope.Lookup(n) != nil }
}

// stdParamHeadName is the type name a stdlib parameter is written with, for
// the receiver test: its annotation's (stdHeadTypeName), or the head of its
// pattern when it destructures (`Duration(ns)`, `Instant{nanos:}`).
func stdParamHeadName(p ast.Param) string {
	if p.TypeAnnotation != nil {
		return stdHeadTypeName(p.TypeAnnotation)
	}
	switch pat := p.Destructure.(type) {
	case *ast.StructPattern:
		return simpleTypeName(pat.TypeName)
	case *ast.EnumPattern:
		return simpleTypeName(pat.Variant)
	}
	return ""
}

// simpleTypeName is a type expression's name when it is written UNDECORATED,
// and "" for every other spelling.
//
// Deliberately narrow, and the narrowness is the contract. Its callers ask
// "is this annotation literally `Bool`/`Int`/`Duration`/the type parameter
// `S`?" — shape probes over a `host fn`'s declared signature (iter.go's
// each_while and known_count checks, ctrlflow.go's Iter.loop check) and the
// receiver test's head names (stdParamHeadName). For those a GENERIC
// spelling must NOT match: `S<X>` is not the type parameter `S`.
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

// stdHeadTypeName is the type name a stdlib parameter's annotation names, for
// the RECEIVER TEST — the undotted spelling, or a nested type's whole dotted
// one (`Json.DecodeError`, a member written undecorated).
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
	// `Regex<C>`: a phantom-parameter host type is its one leaf (stdhost.go).
	if gt, isGeneric := te.(*ast.GenericType); isGeneric && phantomHostSpec("", gt.Name) >= 0 {
		return gt.Name
	}
	if qt, qualified := te.(*ast.QualifiedType); qualified && qt.Module != "" {
		if member, undecorated := qt.Member.(*ast.SimpleType); undecorated {
			return qt.Module + "." + member.Name
		}
	}
	return ""
}

// tupleKindOfGoType answers a tuple kind for the flat anonymous struct a host
// function's signature spells a Nomi tuple as, or kindInvalid.
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

// --- the stdlib module's gen ----------------------------------------------

// newStdGen builds a gen for one stdlib module: the same builder user code goes
// through, with a sibling table instead of a module function table and with the
// stdlib index restricted to this module's own functions plus the modules it
// may reference: those of earlier lowering units and the other members of its
// import cycle (buildStdlibIndexBy).
//
// "This module only" would be too strong: `std/instant`'s `impl Add<Duration,
// Instant>` is `Instant(nanos + Duration.as_nanos(rhs))` and cannot be written
// without reaching std/duration.
func newStdGen(module, path, pkg string, nodes []ast.Node, fa *analysis.FileAnalysis, cands []*stdCandidate, settled map[*stdCandidate]bool, onces []*stdOnce, earlier *stdlibIndex) *gen {
	return newStdGenIn(module, path, pkg, nodes, fa, cands, settled, onces, earlier, nil)
}

// newStdGenIn is newStdGen for a gen inside a program's lowering (in, nil
// for none): a shared module's cases that import listed std modules
// (stdtests.go). It joins the program before its types are built, since a
// case's block import of a listed module's type is bound then.
func newStdGenIn(module, path, pkg string, nodes []ast.Node, fa *analysis.FileAnalysis, cands []*stdCandidate, settled map[*stdCandidate]bool, onces []*stdOnce, earlier *stdlibIndex, in *programBuild) *gen {
	g := newStdGenShell(module, path, pkg, nodes, fa)
	if earlier != nil {
		g.stdUnitSyms = earlier.unitSyms
	}
	if in != nil {
		g.reg, g.files, g.impls, g.stdInsts = in.reg, in.files, in.impls, in.insts
		g.dc = in.insts.dc
		// Past the program's units: every file of the program is another
		// file to this gen, and none is its own.
		g.fileUnit = in.units
		g.foreignDefs = map[ast.Node]*typeDef{}
		g.foreignIfaces = map[ast.Node]*ifaceDef{}
	}
	// Eager for newGen's reason.
	g.loadPreludes()
	// Interface shells and type declarations so typeOf can answer inside a body.
	g.declareIfaces(nodes)
	g.buildTypes(nodes)
	g.resolveIfaces()

	g.bindStdSiblings(module, pkg, cands, settled, earlier)
	// After bindStdSiblings, and on a field of its own, so a later rebind of
	// the siblings cannot drop it. See stdonce.go's bindStdOnces.
	g.bindStdOnces(onces)
	return g
}

// newStdGenShell is a gen for one stdlib module with nothing declared: the
// one construction newStdGenIn and newStdTypesGen share.
func newStdGenShell(module, path, pkg string, nodes []ast.Node, fa *analysis.FileAnalysis) *gen {
	return &gen{dc: newDeclineCensus(DeclineHooks{}), nomiPath: path, pkg: pkg, // stdModule, so this gen can answer "which stdlib module am I". The
		// module name is the caller's — buildStdlibIndex takes it from
		// std.Load()'s keys — and a user gen never reaches this constructor, so
		// a non-empty stdModule IS "this is the embedded stdlib module of that
		// name". loadIter's declaresStdIter is the one reader. See native.go.
		stdModule: module, fa: fa, // nodes, which every `g.nodes` reader in this package needs for a
		// stdlib module too: declaresIterImpl (iter.go), declaresBoot
		// (appfield.go), templateWall (generictype.go), the
		// generic-impl template collection (genericimpl.go), collectHostTypes
		// (hostpkg.go), declareOnces (once.go), typeParamNames and
		// nestedTypeKeys (sigreason.go). Without it
		// declaresIterImpl would answer "this module declares no `impl Iter`
		// block" about std/iter.nomi, whose whole API is one.
		// TestStdModuleView_PopulatesNodes and
		// TestIterAnchor_DeclaringModuleIsItsOwnProtocol hold it.
		nodes: nodes, funcs: map[string]*fnSig{}, types: map[string]*typeDef{}, ifaces: map[string]*ifaceDef{}, implsByIface: map[string]map[kind]*implDef{}, tids: map[kind]bool{}}
}

// newStdTypesGen is the gen that builds a std module's types for the process
// (stdtypes.go): declared, and lowering nothing.
func newStdTypesGen(module string, nodes []ast.Node, fa *analysis.FileAnalysis) *gen {
	g := newStdGenShell(module, module+".nomi", "nomistdtypes/"+module, nodes, fa)
	g.stdTypesBuild = true
	g.loadPreludes()
	return g
}

// bindStdSiblings installs the sibling table and the stdlib index one stdlib
// body is lowered against, and returns the undo.
//
// Separate from newStdGen because more than one scope is bound over one gen:
// newStdGen binds the gen against v.settled, and the prebuild and the
// lowering pass each bind it against the bodies they offer.
//
// `settled` is asked about the CANDIDATE, not about `c.sib`, which is a
// spelling several declarations share. Asked about the spelling, the answer
// would be the group's: `Add<Duration, NaiveDateTime>` is bound to an rt
// function, so `settled["NaiveDateTime.add"]` would be true and this loop
// would offer all ten other rungs whether or not they lower. A sibling
// resolving `lhs + Hours(-n)` to one of them would then call a function the
// pass goes on to refuse.
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
	prevDecls, prevStd := g.stdSiblingDecls, g.std
	views := make(map[*stdCandidate]*stdFunc, len(cands))
	local := &stdlibIndex{
		byID:      map[analysis.StdFunc]*stdFunc{},
		byType:    map[string][]*stdFunc{},
		byIface:   map[analysis.StdFunc]map[kind][]*stdFunc{},
		modulePkg: map[string]string{module: pkg},
	}
	// A bare call finds its sibling by the declaration the checker resolved
	// it to, so two declarations of one spelling are two entries.
	g.stdSiblingDecls = map[ast.Node]*stdFunc{}
	for _, c := range cands {
		f := c.f
		if !settled[c] && f.rtCall == "" {
			continue
		}
		// A settled Nomi body is offered as a body of this module: its view
		// carries the module's namespace key, which irStdSiblingSym compares
		// with the calling gen's.
		//
		// `canon` keeps the copy tied to the original for the fields the
		// lowering pass mutates after this point, defaultFill and irSlot. See
		// stdFunc.canon: without it a short intra-module call to a defaulted
		// sibling reads no accessor and refuses for one that exists.
		view := *f
		view.canon = f
		if view.rtCall == "" {
			view.body = true
			view.pkg = pkg
		}
		if c.decl != nil {
			g.stdSiblingDecls[c.decl] = &view
		}
		local.add(&view)
		views[c] = &view
	}
	local.underlay(earlier)
	g.std = local
	return func() { g.stdSiblingDecls, g.std = prevDecls, prevStd }, views
}

// emitStdFunc lowers one stdlib function's body into the module's IR.
//
// Not gen.funcDecl: funcDecl reports a refusal for every parameter and return
// shape it cannot take, and inside the stdlib those reports are exactly what must
// not reach the tally. Here a shape outside the subset was already settled by
// collectStdCandidates, so the body is the only thing left to try.
func (g *gen) emitStdFunc(f *stdFunc, fd *ast.FuncDef) {
	g.at(fd.Line)
	prevResult, prevSelf := g.result, g.implSelf
	g.result, g.implSelf = f.result, f.recv
	// `g.stdInstName` when a per-program INSTANCE of a generic declaration is
	// being lowered, `f.key` otherwise. An instance is the same body at a
	// concrete instantiation and differs from the template only in its name, so
	// this is the one line that has to know. See stdinst.go, and `monoSig`
	// on the gen for the same arrangement one construct over.
	name := f.key
	if g.stdInstName != "" {
		name = g.stdInstName
	}
	// The same seam `funcDecl` opens. See irstdbody.go for why this caller
	// exists and what it deliberately does not widen.
	stdSig := irStdFuncSig(f, fd)
	shell := g.irFuncShellFor(fd, stdSig, f.params)
	g.irBodyObserve(irBodyStdFn, true)
	produced, retained := g.irScalarLower(fd, stdSig, shell, f.result)
	if !retained {
		g.stdUnloweredFunc(name, f.key, g.dc.take().why)
		produced = f.result
	}
	switch {
	case produced == kindInvalid:
		// Declines `stdlib return type mismatch`.
		g.suppress(fd)
	case produced != f.result:
		g.reject("stdlib return type mismatch", f.key, fd)
	}
	g.result, g.implSelf = prevResult, prevSelf
}

// emitStdBody lowers one stdlib function plus an accessor per defaulted
// parameter, against whatever sibling scope is bound right now.
//
// The accessors have to be inside the sibling window, because a default
// expression is ordinary Nomi from this module, and after the body, which
// emitStdSlotDefaults requires retained.
func (g *gen) emitStdBody(f *stdFunc, fd *ast.FuncDef, pkg string) bool {
	if !g.speculate(func() { g.emitStdFunc(f, fd) }) {
		return false
	}
	f.body, f.pkg = true, pkg
	// This is the declaring module's own sibling window, which is where a
	// default expression's names resolve. See stdslotfill.go.
	g.emitStdSlotDefaults(f, fd)
	return true
}

// --- call sites -----------------------------------------------------------

// stdlibImplOf resolves one stdlib interface METHOD at a statically-known
// receiver kind, checking the PARAMETER shape the call site assumes.
//
// The shared half of the three routes below, all of which are the same
// rule: an interface method
// whose receiver kind is known statically names exactly one function, so it
// lowers to a direct call and needs no table. Reached through an operator or a
// renderer instead of through a qualifier.
//
// Selection is on the receiver KIND and never on a name, which is what keeps a
// user's own `type Duration Int` out: its kind is a different *typeDef, so the
// index misses and the existing refusal reports. A declaration whose parameters
// disagree answers nil rather than being called against a signature nobody
// wrote, which is stdSignatureMatches' rule one level up.
func (g *gen) stdlibImplOf(fn analysis.StdFunc, recv kind, params ...kind) *stdFunc {
	if g.std == nil || recv.def == nil {
		return nil
	}
	// stdPick answers a single-member set unconditionally, so the shape checks
	// below are the ones that decide.
	f := stdPick(g.std.stdIfaceImpls(fn, recv), params)
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

// stdIfaceImpls is std's functions of the interface function fn (named
// as analysis.StdType.Func names one: the interface's module, the
// interface as Owner) for a receiver of kind recv: those of the impl
// blocks the checker recorded as implementing that interface
// (analysis.ImplBlockTypeArgs), an overload set (byIface). The one
// lookup of a std impl by its interface.
func (x *stdlibIndex) stdIfaceImpls(fn analysis.StdFunc, recv kind) []*stdFunc {
	if x == nil {
		return nil
	}
	return x.byIface[fn][recv]
}

// stdOperatorFits reports whether the std function f, the impl an
// operator's record names, is called for `left op right`: it lowered,
// and it takes the two operands' kinds exactly.
func stdOperatorFits(f *stdFunc, left, right kind) bool {
	return f != nil && f.why == "" && sameParamKinds(f.params, []kind{left, right})
}

// irPrebuildStdBodies builds every offered body's graph before the lowering
// pass lowers any body, and leaves the graphs in the module.
//
// A body's graph may call a sibling only once the sibling's graph is in the
// module, so building in declaration order left a forward call — `Date.parse`
// calling the later `Error.of` — declining for want of its callee, and a
// recursive one could never build. So the prebuild takes the greatest fixed
// point: every body is assumed to build (irStdPending), and a round builds
// them until no more build; a body still unbuilt is withdrawn and the round
// starts over without it, until a round withdraws nothing. The lowering pass
// that follows finds every callee already there; each body then rebuilds its
// own graph (irScalarLower replaces the prebuilt graph).
//
// The module's settled `once` bindings take part in the same fixed point,
// since an initializer may call a body and a body may read a binding: each
// is assumed to build, its cell named ahead (stdOnce.irCell) so a body
// reading it builds, and one whose initializer does not build is withdrawn
// (settled cleared) with the bodies. The cells the prebuild built are
// removed, and lowerStdlibModuleIn lowers each survivor once.
func (g *gen) irPrebuildStdBodies(module, pkg string, cands []*stdCandidate, emitted map[*stdCandidate]bool, onces []*stdOnce, earlier *stdlibIndex) {
	var todo []*stdCandidate
	for _, c := range cands {
		if c.fd != nil && c.f.why == "" && emitted[c] {
			// body and pkg are set ahead of lowering, so a graph built now
			// can link to a body lowered later.
			c.f.body, c.f.pkg = true, pkg
			todo = append(todo, c)
		}
	}
	if len(todo) == 0 {
		return
	}
	defer irMuteObservers()()
	defer g.dc.mute()()
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
	// underlay of every earlier module and every sibling's view, so one per
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
	var pendingOnces []*stdOnce
	for _, o := range onces {
		if o.settled {
			pendingOnces = append(pendingOnces, o)
		}
	}
	onceSym := func(o *stdOnce) *ir.Symbol { return g.irTypes().Symbol(o, o.nomiName()) }
	dropCell := func(o *stdOnce) {
		if cell := mod.Cell(onceSym(o)); cell != nil {
			mod.RemoveCell(cell)
		}
	}
	defer func() {
		for _, o := range pendingOnces {
			if o.settled {
				dropCell(o)
			}
		}
	}()
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
		builtOnce := map[*stdOnce]bool{}
		for _, o := range pendingOnces {
			if o.settled {
				dropCell(o)
				o.irCell = onceSym(o)
			}
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
			for _, o := range pendingOnces {
				if !o.settled || builtOnce[o] {
					continue
				}
				restore := g.snapshot()
				tmp := g.tmp
				g.emitStdOnce(o)
				g.tmp = tmp
				restore()
				// Named ahead again: a body later in the round reads it.
				o.irCell = onceSym(o)
				if mod.Cell(onceSym(o)) != nil {
					builtOnce[o] = true
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
		for _, o := range pendingOnces {
			if o.settled && !builtOnce[o] {
				// closeStdOnces' refusal, which a reference names.
				o.settled, o.irCell, o.why = false, nil, "unlowered stdlib once binding"
				declined = true
			}
		}
	}
}

// irMuteObservers silences every test observer and answers the restore. The
// observers are package state that only tests set, and a test that sets one
// runs one lowering at a time; with none set, as in production, it writes
// nothing, so lowerings on two goroutines never race on them. A lowering's
// decline hooks are its census's (declineCensus.suspend), not these.
func irMuteObservers() func() {
	fn, declined, body := irFuncObserved, irRetentionDeclined, irBodyObserved
	iter, proj := irIterObserved, irProjKindObserved
	testBody := irTestBodyObserved
	if fn == nil && declined == nil && body == nil && iter == nil && proj == nil && testBody == nil {
		return func() {}
	}
	irFuncObserved, irRetentionDeclined, irBodyObserved = nil, nil, nil
	irIterObserved, irProjKindObserved = nil, nil
	irTestBodyObserved = nil
	return func() {
		irFuncObserved, irRetentionDeclined, irBodyObserved = fn, declined, body
		irIterObserved, irProjKindObserved = iter, proj
		irTestBodyObserved = testBody
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
	case f.iface == stdEquatable && f.name == "equal" && len(f.params) == 2:
		g.irMod.ImplementEquatable(g.irTypeSym(k.def), f.irBody.Sym())
	case f.iface == stdHashable && f.name == "hash" && len(f.params) == 1:
		g.irMod.ImplementHashable(g.irTypeSym(k.def), f.irBody.Sym())
	}
}

// irRecordStdDisplay records a std `impl Display` body of a declared,
// non-generic type for erased Display renderings, as irRecordDisplayImpls
// does for a program's own.
func (g *gen) irRecordStdDisplay(f *stdFunc) {
	if f.iface != stdDisplay || f.name != "to_string" || f.irBody == nil || len(f.params) != 1 {
		return
	}
	k := f.params[0]
	if k.tag != tagNamed || k.def == nil || k.def.genericOf != nil || k.def.preludeOf != nil {
		return
	}
	g.irMod.ImplementDisplay(g.irTypeSym(k.def), f.irBody.Sym())
}

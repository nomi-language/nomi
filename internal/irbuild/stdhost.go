package irbuild

import (
	"reflect"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/nomi-language/nomi/rt"
)

// The stdlib's blessed primitive types, `Byte`, `Bytes` and the rows beside
// them, each paired with the Go type rt declares for it.
//
// # These are the analyzer's own singletons
//
// std/bytes declares `pub host type Byte` and `pub host type Bytes`, but these
// are not handles and need no (Origin, Name) anchor family. `analysis/types.go`
// declares
//
//	TypeByte  = &PrimitiveType{"Byte"}
//	TypeBytes = &PrimitiveType{"Bytes"}
//
// and registers both in `primitiveTypes`, the map the type registry resolves a
// bare type NAME through. So the front end never builds a `DistinctType` for
// either: `pub host type Byte` in std/bytes.nomi is where the Nomi-visible
// SURFACE is declared, and the TYPE is one of the analyzer's own singletons, in
// the same class as `Int` and `String`. `buildExternType` says so by omission:
// it asks for a `*DistinctType` and returns early when the symbol is not one.
//
// The analyzer blesses EIGHT primitives and `scalarKind` handles FIVE; the
// other three (`Byte`, `Bytes`, `Decimal`) have an rt type and a row here. A
// primitive is identified by the analyzer's own singleton POINTER, so no other
// family's (Origin, Name) reconstruction is needed for them.
//
// # Identity, and why this is the strongest of the four
//
// A spec is keyed on the SINGLETON POINTER — `analysis.TypeBytes`, not the
// string "Bytes". Every other family in this package has to reconstruct
// identity from (Origin, Name) because two files may declare one name; here the
// analyzer has already collapsed the question, and a pointer comparison against
// a package-level singleton cannot be satisfied by anything else in the process.
//
// An annotation reaches the def through the type the checker recorded for it,
// which is that singleton. stdHostAnchors confirms each row's name against a
// module's scope, the check this family's tests hold each row to, and it fails
// toward no anchor, which is the direction every identity check in this
// package fails in.
//
// # The *typeDef is a LEAF, and process-wide
//
// `isDistinct` with `inner: kindInvalid` — the zero-sized-marker shape types.go's
// own field comment names ("kindInvalid for a zero-sized marker (`type Expired`),
// which has no inner at all"). That is what a primitive with no Nomi-visible
// contents is from here: no inner to convert through, and no Nomi syntax that
// constructs or destructures one, because there is nothing to build it out of.
// What differs from a marker is that the value has contents rt owns, which
// `rtOpaque` says. `rtDeclared` marks the def as process-wide.
//
// Shared process-wide for opaque.go's reason, restated because it is a
// precondition and not a preference: a stdFunc's parameter and result kinds are
// built once in stdCandidateFor and compared BY POINTER against a call site's
// kinds in a different gen, so a per-gen def would make `Bytes.length(data)`
// mismatch against itself. A leaf def has no components, so nothing about it is
// package-relative — TestStdHostDefsAreDistinctAndPackageNeutral asserts that
// directly.

// stdHostSpec is one blessed stdlib `host type`, and there are TWO identity
// channels because the analyzer gives the family two shapes.
//
// `buildExternTypeShell` (analysis/type_builder.go) answers a non-generic
// `host type` with `primitiveTypes[name]` WHEN THE NAME IS ONE THE ANALYZER
// BLESSES, and otherwise with a FRESH `PrimitiveType` carrying its declaring
// file's Origin. So `Byte`, `Bytes` and `Decimal` are package-level
// singletons a pointer comparison settles, and `Context` is a per-load value
// that no pointer comparison can identify — `std.Load()` is not memoized, so
// its pointers differ between loads and a spec holding one would be stale the
// moment a second program was analyzed in the same process (stdIfaceDefs
// records the same hazard for a declaration node). Its (Origin, Name)
// identifies it instead.
//
// A row therefore sets EITHER `prim` OR `origin`, and `matches` requires
// exactly one. A row setting neither identifies nothing and anchors nowhere,
// which is the direction every identity check in this package fails in.
type stdHostSpec struct {
	// prim is the analyzer's singleton, for a name in `primitiveTypes`. The
	// identity, by pointer. Nil for a row identified by origin instead.
	prim *analysis.PrimitiveType
	// origin is the declaring module's build key (`std/context`), for a
	// `host type` the analyzer does NOT bless as a singleton. It is the
	// `(Origin, Name)` rule opaque.go and stdenum.go anchor on. A solved type
	// carries it (projectOriginHost); a spelling is confirmed through scope,
	// `fa.Origin` for the declaring file itself and `stdImportModuleOf` for a
	// module that imported the name (stdHostOriginAnchored). Empty for a row
	// identified by prim instead.
	origin string
	// nomi is the type's Nomi spelling, used for the def's name and for the
	// scope confirmation on the annotation channel. Derived from prim would
	// be circular for the scope check, so it is stated and
	// TestStdHostSpecNamesMatchTheirSingleton pins the pair.
	nomi string
	// goType is rt's Go type, held as the type rather than as its spelling for
	// opaqueSpec.goType's reason: a rename or deletion in rt is then a Go
	// compile error in this file. stdHostKindOfGoType reads it to give a host
	// function's Go signature its kinds.
	goType reflect.Type
	// marker says the type is ZERO-WIDTH — one inhabitant, nothing inside —
	// rather than a leaf whose payload rt owns.
	//
	// It flips exactly one bit of the def below, `rtOpaque`, and that bit
	// decides whether `zeroSized()` answers true (types.go asks `rtOpaque`
	// FIRST, before the marker arm). The two answers are not
	// interchangeable in either direction: a marker read as a leaf cannot be
	// MATERIALIZED, so `case b { Bool.False(x) -> … }` has nowhere to get `x`
	// from; a leaf read as a marker is equal to every other and takes the unit
	// hash over a value with contents, which is a wrong answer and the reason
	// `rtOpaque` exists.
	//
	// Stated per row rather than derived from `goType.Size() == 0`, because a
	// Go type's width is not the question. `rt.Unit` is zero-width and `Unit` is
	// not this family; a future rt type could be zero-width by accident of its
	// fields. What licenses materialization is the NOMI declaration having one
	// inhabitant, which only the row's author can know.
	marker bool
	// handle says the type is a GO-BACKED OPAQUE HANDLE — its Go type is
	// registered through internal/stdlibbindings' Types() and the VM holds a
	// value of it as an rt.HostHandle — rather than a type rt implements.
	//
	// IT IS NOT READABLE OFF THE DECLARATION. Every std declaration is a bare
	// `host type`, and `Regex`, `Client` and `Server` are indistinguishable in
	// SOURCE from `Context` and `Dynamic`.
	//
	// So the authority is internal/stdlibbindings' Types(), and
	// TestStdHostHandleRowsMatchTheBindingsTypeTable asserts both
	// directions against it.
	//
	// What the bit decides here is the `values:` RENDERING: a handle renders
	// as `"<" + short type name + ">"`, so a row's operand renders
	// `<Regex>`. Every other row in this table is a type std
	// writes its own `impl Debug` for.
	handle bool
	// phantom names the declaration's type parameters when every one is
	// PHANTOM: no value of the type stores anything of it, so the
	// representation is one leaf whatever the arguments are. std/regex's
	// `Regex<C>` is the row: C is the shape the checker gives a match's
	// capture groups (`String.captures`), and the compiled pattern the handle
	// holds is the same for every C. Each channel reads `Regex<X>` as the
	// row's one kind and drops X (typeOf, projectPhantomHost), a std function whose type parameters stand only
	// there is not generic to the builder (stdrecvgeneric.go's phantomOnly),
	// and a generic function of a program's is instantiated at X from the
	// checker's record of the call (calleeTypeArgs).
	phantom []string
}

// declaredHostType reports whether decl is the `host type` declaration this
// origin-identified spec describes.
//
// Everything checked here decides REPRESENTATION, and each check fails toward NO
// anchor rather than a wrong layout — opaqueSpec.matches' rule, applied to the
// other declaration node. `Public`, because a file-private declaration cannot be
// the type another module names. Exactly the row's PHANTOM type parameters,
// unbounded, and none for any other row, because a generic `host type` is a
// `*DistinctType` and a different mechanism entirely (see
// buildExternTypeShell's other branch): a row here must not redirect one whose
// parameters a value stores. An attached `//!` test is not checked: it cannot
// affect the representation and it refuses at its own position — see
// stdStructSpec.mismatch.
func (s *stdHostSpec) declaredHostType(decl *ast.ExternType) bool {
	if decl.Name != s.nomi || !decl.Public || len(decl.TypeParams) != len(s.phantom) || len(decl.WhereClauses) > 0 {
		return false
	}
	for i, tp := range decl.TypeParams {
		if tp.Name != s.phantom[i] || len(tp.Bounds) > 0 {
			return false
		}
	}
	for _, dec := range decl.Decorators {
		// `derive` is the one decorator that is not a gap, for the reason
		// opaqueSpec.matches gives: it is also synthesized into an ordinary impl
		// block that lowers through the ordinary path, so it is metadata about
		// work already done.
		if dec.Name != "derive" {
			return false
		}
	}
	return true
}

// stdHostSpecs is the whole set, and every row is a decision.
//
// The analyzer blesses eight primitives a program can name. Five are the
// scalars `scalarKind` already answers, and this table has the other three.
//
// `Decimal`'s row is backed by `rt.Decimal`, 545 lines of exact base-10
// arithmetic with one implementation; decimal_test.go and the two fixtures
// beside it pin its answers absolutely.
//
// `Any` and `Infallible` are also `*PrimitiveType` and have no rt type, so
// neither belongs here: `Any` is compiler-internal, and `Infallible` has a
// kind of its own with no values (irnever.go).
var stdHostSpecs = []stdHostSpec{
	// Two rows for two types: `Byte` is one octet and `Bytes` a buffer of
	// them, and they are separate Nomi types, so a single row would collapse
	// `Bytes.get(data, i) -> Option<Byte>`'s two positions onto one.
	{prim: analysis.TypeByte, nomi: "Byte", goType: reflect.TypeFor[rt.Byte]()},
	{prim: analysis.TypeBytes, nomi: "Bytes", goType: reflect.TypeFor[rt.Bytes]()},
	// `Decimal` differs from its two neighbours in one way that matters to the
	// def built below: rt.Decimal has CONTENTS a Nomi program can observe --
	// a mantissa and a scale -- where rt.Byte and rt.Bytes are leaves whose
	// payload only rt reads. It is still `rtOpaque` here, because "contents rt
	// owns" and "contents this builder may look inside" are different claims
	// and only the second would license a field access. Nomi has no syntax
	// that reaches inside a Decimal; `Decimal.scale(d)` is a host function,
	// not a projection.
	{prim: analysis.TypeDecimal, nomi: "Decimal", goType: reflect.TypeFor[rt.Decimal]()},
	// AFTER the singleton rows: nothing here is keyed by index, but a row
	// inserted mid-table is the hazard opaque.go's header names for its own.
	//
	// `Context` is identified by ORIGIN rather than by a singleton pointer.
	// `pub host type Context` is this family's shape, and `buildExternTypeShell`
	// gives it a `*PrimitiveType` exactly as it does `Byte`.
	//
	// `with_value<T>` and `value<T>` key a store by a `Type<T>` witness.
	// `Type<T>` is a `stdGenHostSpecs` row over `rt.Type[T]`, `rt.Context` has
	// value storage keyed on a `*rt.TypeID` ADDRESS, and both calls lower
	// through `contextValueCall` ahead of the stdlib index, so the
	// INDEX stamps both `stdlib generic function` while the LANGUAGE lowers
	// them, as it does for `Sender.send` and `Task.await`. See
	// ircontextvalue.go, rt/context.go and rt/typewitness.go.
	{origin: "std/context", nomi: "Context", goType: reflect.TypeFor[rt.Context]()},
	// `Dynamic` is the SECOND origin-identified row, and it is here for exactly
	// the reason `Context` is: `pub host type Dynamic` in std/dynamic.nomi is
	// literally this family's shape, and `buildExternTypeShell` gives it a
	// per-load `*PrimitiveType` that no pointer comparison can identify, so the
	// origin channel is the only one that can anchor it.
	//
	// A LEAF whose payload is a Go `any`, and the wrapper around that `any` is
	// load-bearing: see rt.Dynamic. A binding typed over a bare `any` would
	// project onto every declaration whose parameter has no representation, which
	// is the same wrong-anchor `rt.Bytes` avoids by not being a plain `string`.
	//
	// `as_option`, `at_field` and `at_index` are generic Nomi bodies taking a
	// callback; they are the stdlib index's generic question, not this
	// family's.
	{origin: "std/dynamic", nomi: "Dynamic", goType: reflect.TypeFor[rt.Dynamic]()},
	// `True` and `False`, the two SINGLETONS `pub enum Bool { embeds False;
	// embeds True }` is built out of: `marker: true` rows, whose type has no
	// contents at all.
	//
	// Both resolve to a `*analysis.PrimitiveType` over an `*ast.ExternType`
	// that is public, has no type parameters and only `derive` decorators,
	// which is `declaredHostType`'s test. Neither
	// is a blessed singleton, so the ORIGIN channel is the only one that can
	// anchor them, as for `Context` and `Dynamic`.
	//
	// A `Bool` value is the scalar kindBool, which names neither embedded
	// type, so these two types need rows of their own for `bool.Bool.inspect`, `bool.Bool.to_string`, `bool.Bool.equal`
	// and `bool.Bool.hash` to lower. Each is a DERIVE-SYNTHESIZED body over
	// `case value { Bool.False(v0) -> … }` (analysis/derive_synthesis.go's
	// `stringifyEnumExpr` plus `stringifyVariantBody`'s `"embedded"` arm), so
	// the payload binding is what needs the representation. See
	// rt/boolmarker.go.
	{origin: "std/bool", nomi: "True", goType: reflect.TypeFor[rt.BoolTrue](), marker: true},
	{origin: "std/bool", nomi: "False", goType: reflect.TypeFor[rt.BoolFalse](), marker: true},
	// `Supervisor`, std/supervisors' owner for work that outlives the call that
	// started it, origin-identified for the reason `Context` and `Dynamic` are.
	// An app-struct field such as `Config.audit: Supervisor` needs it for its
	// layout.
	//
	// A LEAF whose payload rt owns, which is what `rtOpaque` says: `rt.Supervisor`
	// is a one-field struct over an unexported `*supervisor`, so the VALUE is
	// copyable — it sits in a `Config{}` literal and in a struct field with no
	// position spelling a pointer — while nothing a program lowers can
	// reach inside it. `Task[T]`'s and `Sender[T]`'s arrangement.
	//
	// A representation is not a runtime: `Supervisor.new` also needs the POLICY
	// enums' kinds (stdenum.go), a Nomi-bodied `new` over an undefaulted
	// `new_exact`, an rt symbol for each function, and a drain wired into both
	// the program and the test harness.
	{origin: "std/supervisors", nomi: "Supervisor", goType: reflect.TypeFor[rt.Supervisor]()},
	// `Regex`, std/regex's compiled pattern, origin-identified, and a type that
	// belongs to a CO-LOCATED ADAPTER rather than to the toolchain.
	//
	// It is this family's shape rather than opaque.go's: the declaration is an
	// `*ast.ExternType`, and the analyzer's buildExternTypeShell gives it a
	// per-load `*PrimitiveType`, which is `declaredHostType`'s test verbatim
	// and `Context`/`Dynamic`/`Supervisor`'s channel exactly.
	//
	// THE GO TYPE IS IN rt WHILE THE IMPLEMENTATION IS NOT. `rt.Regex` is a
	// handle whose payload rt cannot name (`*nomi/stdregex.Regex` is in the
	// compiler's module, which requires rt), and the functions over it live in
	// `nomi/stdregex`. See rt/regexhandle.go for why the value belongs in rt
	// anyway.
	//
	// A LEAF whose payload rt owns, which is what `rtOpaque` says, and
	// rt.Dynamic's arrangement precisely: a one-field struct over `any`, so the
	// value is copyable and nothing a program lowers can reach inside it.
	//
	// `handle: true`, which is not readable from the declaration. It is
	// registered as an extern type (`regex.Regex` in internal/stdlibbindings'
	// Types()), which is what makes its value a host handle and its `values:`
	// operand `<Regex>`. TestStdHostHandleRowsMatchTheBindingsTypeTable asserts
	// that correspondence in both directions.
	{origin: "std/regex", nomi: "Regex", goType: reflect.TypeFor[rt.Regex](), handle: true, phantom: []string{"C"}},
	// `Reader` and `Writer`, std/io's streams, origin-identified for the
	// reason `Supervisor` is: a `pub host type` with rt's representation, a
	// one-field struct over an unexported stream, so the value is copyable
	// and nothing a program lowers reaches inside it. Their functions are
	// RtFuncs rows (rt/streams.go).
	{origin: "std/io", nomi: "Reader", goType: reflect.TypeFor[rt.Reader]()},
	{origin: "std/io", nomi: "Writer", goType: reflect.TypeFor[rt.Writer]()},
	// `File`, std/io's handle for reads and writes at a position, for the
	// same reason (rt/filehandle.go).
	{origin: "std/io", nomi: "File", goType: reflect.TypeFor[rt.File]()},
	// `FileSystem`, std/fs/host's file system a call runs against
	// (`std::fs::testing.run`'s sandbox, `fs.within`'s directory), for the reason
	// `Reader` is: rt's struct over the backend internal/stdio builds, which
	// rt carries and does not name.
	{origin: "std/fs/host", nomi: "FileSystem", goType: reflect.TypeFor[rt.FileSystem]()},
	// `Child` and `Fake`, std/process/host's started program and the fake
	// `std::process::testing.run` installs, for the reason `FileSystem` is: rt's
	// structs over what internal/stdprocess builds (rt/processes.go).
	{origin: "std/process/host", nomi: "Child", goType: reflect.TypeFor[rt.ChildProcess]()},
	// `Client` and `Exchange`, std/http/host's client and a response it
	// answered: rt's structs over what internal/stdhttp builds (rt/network.go).
	{origin: "std/http/host", nomi: "Client", goType: reflect.TypeFor[rt.HTTPClient]()},
	{origin: "std/http/host", nomi: "Exchange", goType: reflect.TypeFor[rt.HTTPExchange]()},
	{origin: "std/http/host", nomi: "Listener", goType: reflect.TypeFor[rt.HTTPListener]()},
	{origin: "std/http/host", nomi: "Incoming", goType: reflect.TypeFor[rt.HTTPIncoming]()},
	{origin: "std/http/host", nomi: "Certificate", goType: reflect.TypeFor[rt.TLSCertificate]()},
	{origin: "std/http/host", nomi: "TlsConfig", goType: reflect.TypeFor[rt.TLSConfig]()},
	{origin: "std/http/host", nomi: "Conn", goType: reflect.TypeFor[rt.NetConn]()},
	{origin: "std/http/host", nomi: "TcpListener", goType: reflect.TypeFor[rt.NetListener]()},
	{origin: "std/http/host", nomi: "Mux", goType: reflect.TypeFor[rt.HTTPMux]()},
	{origin: "std/http/host", nomi: "Jar", goType: reflect.TypeFor[rt.HTTPJar]()},
	{origin: "std/http/host", nomi: "Handle", goType: reflect.TypeFor[rt.HTTPHandle]()},
	{origin: "std/process/host", nomi: "Fake", goType: reflect.TypeFor[rt.ProcessFake]()},
	// `Records` and `Tokens`, std/csv/host's and std/xml/host's readers
	// over a Reader, for the reason `Child` is: rt's structs over what
	// internal/stdcsv and internal/stdxml build (rt/formats.go).
	{origin: "std/csv/host", nomi: "Records", goType: reflect.TypeFor[rt.CsvRecords]()},
	{origin: "std/xml/host", nomi: "Tokens", goType: reflect.TypeFor[rt.XmlTokens]()},
	// `Compiled`, `Live` and `Memory`, std/wasm/host's module, instance and
	// an instance's memory during an import, for the reason `Child` is:
	// rt's structs over what internal/stdwasm builds (rt/wasm.go).
	{origin: "std/wasm/host", nomi: "Compiled", goType: reflect.TypeFor[rt.WasmModule]()},
	{origin: "std/wasm/host", nomi: "Live", goType: reflect.TypeFor[rt.WasmInstance]()},
	{origin: "std/wasm/host", nomi: "Memory", goType: reflect.TypeFor[rt.WasmMemory]()},
	// `Builder`, std/bytes's byte buffer built by appending, for the reason
	// `Reader` is: rt's struct, a window over an append-only buffer that
	// only its host calls look inside (rt/bytesbuilder.go). It is a value
	// with no equality, not a handle to running state.
	{origin: "std/bytes", nomi: "Builder", goType: reflect.TypeFor[rt.BytesBuilder]()},
}

// stdHostDefs is the process-wide *typeDef per spec, keyed by spec index.
var stdHostDefs = sync.OnceValue(func() []*typeDef {
	defs := make([]*typeDef, len(stdHostSpecs))
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		defs[i] = &typeDef{nomi: s.nomi, // A LEAF by default: nominally its own type, structurally nothing
			// this builder may look inside. `isDistinct` puts it on the
			// defined-type paths and `rtOpaque` keeps it OFF the zero-sized
			// marker's, which `isDistinct && inner == kindInvalid` otherwise
			// means — see types.go's rtOpaque for the wrong answers that would
			// produce.
			//
			// A `marker` row is the other answer, and it is the one case where
			// `isDistinct && inner == kindInvalid` means what it looks like:
			// there is genuinely nothing inside, so `zeroSized()` must answer
			// true and the value must be materializable from its type alone.
			isDistinct: true, inner: kindInvalid, rtOpaque: !s.marker, lowerable: true, // rtDeclared: rt declares the type, so the def is process-wide and
			// package-neutral.
			rtDeclared: true}
	}
	return defs
})

// stdHostKind is the kind of the i'th spec's values.
func stdHostKind(i int) kind { return named(stdHostDefs()[i]) }

// stdHostKindOfPrimitive is the kind an analyzer singleton names, or
// kindInvalid. The INFERRED channel — a type the checker solved for a position
// the program never spells, which is where a lambda parameter arrives.
//
// Origin-identified rows are answered by projectOriginHost, on the solved
// type's (Origin, Name). Matching on the name alone here would let a
// program's own type named `Context` adopt rt's representation at an
// inferred position: a wrong ANSWER, not a coverage gap.
func stdHostKindOfPrimitive(t analysis.Type) kind {
	p, isPrim := t.(*analysis.PrimitiveType)
	if !isPrim {
		return kindInvalid
	}
	for i := range stdHostSpecs {
		if stdHostSpecs[i].prim == nil {
			continue
		}
		if stdHostSpecs[i].prim == p {
			return named(stdHostDefs()[i])
		}
	}
	return kindInvalid
}

// stdHostKindOfGoType is the kind an rt signature's Go type names, or
// kindInvalid.
//
// The direction kindOfGoType needs, and it is what keeps
// `rt.BytesLength(rt.Bytes) int64` from projecting onto `(String) -> Int` and
// silently binding to an unrelated declaration — which is exactly what typing
// the extern over a plain `string` would have done, since rt.Bytes IS a string
// underneath. Identity on the reflect.Type, never on the underlying kind.
func stdHostKindOfGoType(t reflect.Type) kind {
	for i := range stdHostSpecs {
		if stdHostSpecs[i].goType == t {
			return named(stdHostDefs()[i])
		}
	}
	return kindInvalid
}

// --- anchoring -------------------------------------------------------------

// stdHostAnchors resolves the specs against one module's analysis: the spec
// index under the Nomi name, for the ANNOTATION channel.
//
// Only a name index, where the other three families also return a declaration
// index. For a SINGLETON row there is no declaration node worth keying on: the
// type is an analyzer singleton and `pub host type Bytes` is a SURFACE
// declaration the builder has no path for at all (nothing in this package
// handles `*ast.ExternType` as a lowering). So std/bytes reaches `Bytes` through
// exactly the same lookup a user module does, and there is no local shell for an
// anchor to displace. An ORIGIN row reads the node anyway — not to key on, but
// to CHECK the shape, because the singleton pointer that made that unnecessary
// for the first three rows does not exist for it.
func stdHostAnchors(fa *analysis.FileAnalysis) map[string]int {
	byName := map[string]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byName
	}
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		sym := fa.ModuleScope.Lookup(s.nomi)
		if sym == nil {
			continue
		}
		resolved := sym
		if resolved.Resolved != nil {
			resolved = resolved.Resolved
		}
		switch {
		case s.prim != nil:
			if resolved.Type != analysis.Type(s.prim) {
				// The spelling resolves to something else here. No anchor, and
				// the ordinary paths report — the direction this family fails in.
				continue
			}
		case s.origin != "":
			if !stdHostOriginAnchored(fa, sym, resolved, s) {
				continue
			}
		default:
			// A row identifying nothing. Unreachable while
			// TestStdHostSpecsIdentifyExactlyOneWay holds, and stated rather
			// than assumed because the failure it prevents is a row that anchors
			// on the NAME alone.
			continue
		}
		byName[s.nomi] = i
	}
	return byName
}

// stdHostOriginAnchored is the ORIGIN channel's whole identity test, for a
// `host type` the analyzer did not bless with a singleton.
//
// Three conjuncts, and each rules out a different wrong anchor:
//
//   - the resolved type is a `*PrimitiveType` spelling this name, which is what
//     `buildExternTypeShell` produces for a non-generic `host type`. A generic
//     one is a `*DistinctType` and fails here.
//   - the resolved DECLARATION is an `*ast.ExternType` of the shape the row
//     describes. This is the check the singleton rows get for free.
//   - the declaration belongs to the row's ORIGIN MODULE, either because this IS
//     that module (`fa.Origin`) or because the name was reached through an
//     import of it (stdiface.go's stdImportModuleOf, which reports the ORIGIN
//     module even for a prelude re-export). A user file declaring its own
//     `pub host type Context` satisfies neither, so it never anchors — and
//     `std` is unforgeable as a module name, because analysis/manifest.go's
//     LoadManifest rejects `[package].name = "std"`.
//
// `sym` is the UNRESOLVED symbol and `resolved` the followed one: the module
// path lives on the import symbol and the declaration on its target, so both
// halves are needed and neither substitutes for the other.
func stdHostOriginAnchored(fa *analysis.FileAnalysis, sym, resolved *analysis.Symbol, s *stdHostSpec) bool {
	if len(s.phantom) > 0 {
		// A generic `host type` is a `*DistinctType`: the declaration's own
		// shell, with no arguments (stdGenHostDeclIn's rule).
		dt, isDistinct := resolved.Type.(*analysis.DistinctType)
		if !isDistinct || dt.Name != s.nomi || len(dt.TypeArgs) > 0 {
			return false
		}
	} else if p, isPrim := resolved.Type.(*analysis.PrimitiveType); !isPrim || p.String() != s.nomi {
		return false
	}
	decl, isExtern := resolved.Node.(*ast.ExternType)
	if !isExtern || !s.declaredHostType(decl) {
		return false
	}
	if fa.Origin == s.origin {
		return true
	}
	module, imported := stdImportModuleOf(sym, stdImporterOf(fa, sym))
	return imported && module == s.origin
}

// decimalKind is the kind a `1.50d` literal has, WITHOUT going through module
// scope: `1.50d` names no type and requires no import, and the checker types
// it as `analysis.TypeDecimal` unconditionally. Resolved against the spec
// table by singleton pointer, which is the same identity the anchors use.
func decimalKind() kind {
	for i := range stdHostSpecs {
		if stdHostSpecs[i].prim == analysis.TypeDecimal {
			return stdHostKind(i)
		}
	}
	return kindInvalid
}

// projectOriginHost is the kind of a non-generic `host type` at a position
// the checker solved, by the type's own (Origin, Name). A std row matches on
// its origin module, which a user file cannot spell (analysis/manifest.go's
// LoadManifest rejects `[package].name = "std"`), so a user's own
// `pub host type Context` never adopts rt's representation. A user `host
// type` (an embedder's plain handle) matches the declaration in
// the module that declares it, so an inferred position agrees with an
// annotated one, including when the two came from different analyses of
// that module.
func (g *gen) projectOriginHost(t *analysis.PrimitiveType) kind {
	if t.Origin == "" {
		return kindInvalid
	}
	for i := range stdHostSpecs {
		if s := &stdHostSpecs[i]; s.origin == t.Origin && s.nomi == t.Name_ {
			return stdHostKind(i)
		}
	}
	// With no gen (projectShared) only the specs above answer.
	if g == nil {
		return kindInvalid
	}
	owners := []*gen{g}
	if g.reg != nil {
		owners = g.reg.gens
	}
	for _, owner := range owners {
		if owner == nil || owner.fa == nil || owner.fa.ModuleScope == nil {
			continue
		}
		d, ok := owner.hostTypes[t.Name_]
		if !ok {
			continue
		}
		sym := owner.fa.ModuleScope.Lookup(t.Name_)
		for sym != nil && sym.Resolved != nil {
			sym = sym.Resolved
		}
		if sym != nil && analysis.SameScopedType(sym.Type, t) {
			return named(d)
		}
	}
	return kindInvalid
}

// phantomHostSpec is the stdHostSpecs row for a phantom-parameter std host
// type (std/regex's `Regex<C>`) named name, with its index, or -1.
func phantomHostSpec(origin, name string) int {
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		if len(s.phantom) > 0 && s.nomi == name && (origin == "" || s.origin == origin) {
			return i
		}
	}
	return -1
}

// projectPhantomHost is the kind of a solved `Regex<X>`: the row's one leaf,
// whatever X is (stdHostSpec.phantom).
func projectPhantomHost(ty *analysis.DistinctType) (kind, bool) {
	if ty.Origin == "" {
		return kindInvalid, false
	}
	i := phantomHostSpec(ty.Origin, ty.Name)
	// No arguments is the declaration's own shell, which a witness
	// (`Context.value(c, Regex)`) names.
	if i < 0 || (len(ty.TypeArgs) != 0 && len(ty.TypeArgs) != len(stdHostSpecs[i].phantom)) {
		return kindInvalid, false
	}
	return stdHostKind(i), true
}

// irStdHandleKind is a std `host type` carried as an opaque Go handle
// (Regex): a value, whose equality and hash rt.Equal and rt.Hash answer by
// the handle's key (rt.HostKeyed), at the top or nested. A project's
// host handle is not one: what its equality means is its own.
func irStdHandleKind(k kind) bool {
	if k.tag != tagNamed || k.def == nil || !k.def.rtOpaque {
		return false
	}
	for i, d := range stdHostDefs() {
		if d == k.def {
			return stdHostSpecs[i].handle
		}
	}
	return false
}

// irHostHandleKind is a std `host type` carried as an opaque Go handle, such
// as Regex. Retained code only passes it between host calls: the VM holds it
// as the rt.HostHandle a generated adapter answers.
func irHostHandleKind(k kind) bool {
	if k.tag != tagNamed || k.def == nil || !k.def.rtOpaque {
		return false
	}
	if hostHandleDef(k.def) {
		return true
	}
	for i, d := range stdHostDefs() {
		if d == k.def {
			return stdHostSpecs[i].handle
		}
	}
	return false
}

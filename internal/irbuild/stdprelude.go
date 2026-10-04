package irbuild

import (
	"reflect"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ast"
)

// A prelude enum instantiated at PACKAGE-NEUTRAL type arguments — `Maybe<Int>`,
// `Result<String, String>`, `Maybe<Duration>` — as a type a stdlib signature may
// name.
//
// # Identity across gens
//
// `Maybe<Int>` has a representation (prelude.go). What a stdlib signature also
// needs is IDENTITY ACROSS GENS, for the reason opaque.go gives for sharing an
// opaque def: "a stdFunc's parameter and result kinds are built once in
// stdCandidateFor and compared by POINTER against a call site's argument kinds
// in a different gen, so a per-gen def would make `Duration.as_nanos(d)`
// mismatch against itself." `Int.to_non_zero` and `Int.to_positive` return
// `Maybe<NonZeroInt>` and `Maybe<PositiveInt>`, which is how those types are
// reached.
//
// This is that argument applied to a PARAMETERIZED type, and the applicability
// is conditional rather than automatic, which is the whole content of this
// file.
//
// # What makes an instance shareable, and why it is exactly this predicate
//
// foreign.go rejects sharing a *typeDef across gens, and the reason it gives is
// specific: "a shared def's field kinds are interned in the OWNER's g.comps, so
// a field of type `List<Point>` renders as `*rt.List[NomiT_Point]`: correct in
// the owner, undefined in every other package."
//
// That objection is about RENDERING, so the exemption is too: a def whose Go
// spelling contains nothing package-relative is correct in every package by
// construction. `rt.Maybe[int64]` names rt and a Go builtin; `rt.Maybe[rt.Duration]`
// names rt twice. `rt.Maybe[NomiT_Point]` names a type declared in one gen, so
// it is not shareable and keeps the per-gen path.
//
// kind.packageNeutral is that predicate, and it is deliberately CLOSED rather
// than a default-allow with exceptions: a kind whose tag this builder grows
// later is not neutral until somebody says so.
//
// # `List`/`Map`/tuple/func are neutral when their components are
//
// `*rt.List[int64]` renders identically in every package, and a structural
// kind's IDENTITY is its interned `*compKind` (native.go's `kind.comp`).
// sharedcomp.go interns a structural kind over neutral components
// process-wide, so two gens' `List<Int>` are one `kind`. The rule is this
// predicate: neutral means "renders to the same Go type text in every gen",
// and a structural kind satisfies it exactly when every COMPONENT does, the
// same recursive shape the prelude arm below has. So `List<Int>` is neutral and
// `List<Point>` is not, and the second half is the whole safety argument
// (sharedcomp.go's header).
//
// So `String.split`'s `List<String>` SIGNATURE is inside the subset, and
// whatever refuses it refuses in the BODY.
//
// # What makes the shared def safe to share
//
// opaque.go's shared defs are leaves; these carry payloads, so the immutability
// claim needs its own argument. There is exactly one place a *typeDef's
// `lowerable` bit is written after construction — types.go's settleLowerable,
// over `g.typeOrder` plus `g.preludeOrder` — and a shared instance is never
// appended to `g.preludeOrder` (see preludeInstanceOf). That is not merely an
// omission that happens to work: settleLowerable only ever RETRACTS, through a
// mention that is itself unlowerable, and a neutral instance's payload kinds are
// scalars and rt-declared defs, which are unconditionally lowerable. So the bit
// could not change even if it were in the list. Nothing else mutates a typeDef:
// `assignSlots` is not called on a prelude instance (preludeInstance assigns
// slots directly), and every per-gen table that a def participates in — `g.tids`,
// `g.comps`, `g.implsByIface` — is keyed BY the def and never writes to it.
//
// TestSharedPreludeDefsAreImmutableAndNeutral asserts the reachable half of that
// rather than restating it.

// preludeNames renders one instantiation's Go type and its Nomi spelling.
//
// Both are derived from the same walk so they cannot disagree about which
// arguments an instance has — which matters because the Go name is the INTERN
// KEY and the Nomi name is what a collision is detected against.
func preludeNomi(spec *preludeSpec, args []kind) string {
	var nomi strings.Builder
	nomi.WriteString(spec.nomi)
	nomi.WriteByte('<')
	for i, arg := range args {
		if i > 0 {
			nomi.WriteString(", ")
		}
		nomi.WriteString(arg.nomi())
	}
	nomi.WriteByte('>')
	return nomi.String()
}

// buildPreludeDef constructs one instantiation's typeDef.
//
// Lifted out of preludeInstance unchanged so the shared and per-gen paths build
// the SAME def rather than two that have to be kept in step — the layout is a
// property of the rt type, not of where the pointer is cached.
func buildPreludeDef(spec *preludeSpec, args []kind) *typeDef {
	nomi := preludeNomi(spec, args)
	d := &typeDef{
		nomi:    nomi,
		tagName: spec.tagField,
		isEnum:  true,
		// The spec's anchor-independent identity and the arguments, so another
		// gen can REBUILD this instance at its own spelling of
		// them: `rt.Maybe` is package-neutral and the `T` inside it need not
		// be. See foreign.go's importNamed, which is the only reader.
		preludeOf:   sharedAnchorFor(spec),
		preludeArgs: args,
		lowerable:   true,
	}
	for i, vs := range spec.variants {
		vd := variantDef{nomi: vs.nomi, tag: i + 1, kind: "bare"}
		if pk, carries := vs.payload(args); carries {
			vd.kind = "positional"
			vd.payloads = []payload{{k: pk, slot: len(d.slots)}}
			d.slots = append(d.slots, slotDef{k: pk, users: []string{vs.nomi}})
			// The payload graph, so a struct whose field is `Maybe<Bad>`
			// refuses with it and a `Maybe<Node>` inside a Node is boxed
			// rather than laid out at infinite size.
			d.note(pk)
		}
		d.variants = append(d.variants, vd)
	}
	return d
}

// sharedAnchorFor is the spec-only anchor a def carries.
//
// A `*typeDef` is reached from every gen, and a module's anchor holds that
// MODULE's `*ast.EnumDef` — a pointer `std.Load()` hands out fresh per call. So
// a def that outlives one module must not carry one, and none of the three
// readers wants it: foreign.go reads `.spec.nomi`, try.go compares `.spec`, and
// impl.go only tests the field for nil. Interned per spec so `preludeOf` stays
// comparable by pointer as well as by spec.
var sharedAnchorFor = func() func(*preludeSpec) *preludeAnchor {
	anchors := make(map[*preludeSpec]*preludeAnchor, len(preludeSpecs))
	for i := range preludeSpecs {
		spec := &preludeSpecs[i]
		anchors[spec] = &preludeAnchor{spec: spec}
	}
	return func(spec *preludeSpec) *preludeAnchor { return anchors[spec] }
}()

// packageNeutral reports whether this kind holds no pointer into one program's
// gen, so a *typeDef or a *compKind built over it may be shared process-wide.
//
// The cached stdlib lowering outlives every program, and the stdlib boundary
// compares kinds by pointer across gens (a stdFunc's parameter kinds against a
// call site's argument kinds). A process-wide entry that reached a def owned by
// one program's gen would tie the cache to that program and would never equal
// the same type as another gen interns it. A scalar, an rt-declared std type
// and a prelude or std generic instance over neutral arguments are neutral; a
// user-declared type is not.
//
// Closed by construction: a tag not listed answers false, so a kind whose tag
// this builder grows later is not neutral until somebody says so.
func (k kind) packageNeutral() bool {
	switch k.tag {
	case tagUnit, tagInt, tagFloat, tagString, tagBool:
		return true
	case tagNamed:
		if k.def == nil {
			return false
		}
		if k.def.genStructOf != nil {
			// A GENERIC std struct instance. `rtDeclared` is set on one, so this
			// clause has to come FIRST or the arm below would answer true for
			// `Set<Point>` on the strength of the rt type's name alone. The
			// answer is the prelude arm's answer for the prelude arm's reason:
			// neutral exactly when every type argument is.
			//
			// Each argument is checked here rather than trusting where the
			// instance was interned: genStructInstance also builds per-gen
			// instances over non-neutral arguments (`rt.Channel[NomiT_Request]`),
			// and this answers false for those. See stdgenstruct.go.
			for _, arg := range k.def.genStructArgs {
				if !arg.packageNeutral() {
					return false
				}
			}
			return true
		}
		if k.def.genHostOf != nil {
			// A GENERIC std HOST type instance — `Sender<Int>`. Beside the
			// clause above and BEFORE the rtDeclared arm for its exact reason:
			// `rtDeclared` is set on one of these too, so without this clause
			// `Sender<Point>` would answer true on the strength of the rt type's
			// name alone. See stdgenhost.go.
			for _, arg := range k.def.genHostArgs {
				if !arg.packageNeutral() {
					return false
				}
			}
			return true
		}
		if k.def.rtDeclared {
			// An anchored stdlib opaque newtype: its Go type is hand-written in
			// rt and its def is already process-wide. See opaque.go.
			return true
		}
		if k.def.preludeOf == nil {
			return false
		}
		// A prelude instance is neutral exactly when its arguments are, and by
		// sharedPreludeInstance's rule such an instance IS the shared one — so
		// this arm never reports a per-gen def as neutral.
		for _, arg := range k.def.preludeArgs {
			if !arg.packageNeutral() {
				return false
			}
		}
		return true
	case tagIface:
		// An ANCHORED stdlib interface. Neutral for the same three reasons the
		// rtDeclared arm above is: the *ifaceDef is process-wide
		// (stdIfaceDefs), the value renders `rt.Dyn` in every generated
		// package, and the dispatch tables it names are variables in rt rather
		// than in anybody's output. Pointer identity against the shared set,
		// never a name — a USER's `pub interface Display` has its own def and
		// answers false here, which is the half a name check gets wrong.
		//
		// `rt.Dyn` is the spelling of EVERY existential, so this arm makes a
		// kind neutral whose Go text is not an identity. That is safe only
		// because the intern tables key on the components as well as the text;
		// otherwise `List<Display>` and `List<Comparable>` would collapse into
		// one entry.
		// Read as a FIELD rather than through stdIfaceOf's scan, and the reason
		// is layering rather than speed: this predicate is asked by
		// shareableParts, which the stdlib TABLES call while building
		// themselves, so a dependency on stdIfaceDefs here is an
		// INITIALIZATION CYCLE — `stdEnumDefs -> mapKindIn -> internComp ->
		// shareableParts -> packageNeutral -> stdIfaceOf -> stdIfaceDefs ->
		// stdIfaceSpecs -> stdEnumKind -> stdEnumDefs`, which the compiler
		// rejects by name the moment an enum spec grows a container payload.
		// The flag is set in exactly one place, that table's own builder, so
		// this is the same pointer-identity answer with the dependency removed.
		// See ifaceDef.stdShared.
		return k.iface != nil && k.iface.stdShared
	case tagList, tagMap, tagTuple, tagSeq, tagFunc, tagAnonStruct:
		// A STRUCTURAL kind. Neutral exactly when every component is, which is
		// the same recursive shape as the prelude arm and holds for the same
		// reason: the Go spelling is built out of the components' spellings and
		// a fixed frame of `rt.` names, `struct`, `func` and `*rt.Frame`. And as
		// with the prelude arm, by internComp's rule such a kind IS the shared
		// one, so this arm never reports a per-gen *compKind as neutral.
		//
		// The negative half is load-bearing, not conservatism: `List<Point>`
		// renders `*rt.List[NomiT_Point]`, correct in the package that declares
		// Point and undefined in every other, and two files may each declare a
		// Point. See sharedcomp.go.
		if k.comp == nil {
			return false
		}
		for _, p := range k.comp.parts {
			if !p.packageNeutral() {
				return false
			}
		}
		return true
	}
	// tagEmptyList, tagBareNone and tagEmptyMap answer FALSE here even though
	// each renders to a fixed rt type. They are UNTYPED literals whose type the
	// checker solves from context and coerce discharges; a signature position is
	// exactly where a type is declared rather than solved, so admitting one
	// would let a stdlib signature name `List<Unit>` where the source said
	// nothing of the kind.
	return false
}

// --- the process-wide intern table -----------------------------------------

var (
	sharedPreludeMu sync.Mutex
	// sharedPreludeDefs is keyed on the FULL instantiation's spelling —
	// `Maybe<Int>`, not `Maybe`.
	//
	// Keying on the base name would make a Go map assignment a silent
	// overwrite: `Maybe<String>` would take `Maybe<Int>`'s slot and every
	// later mention would resolve to the wrong payload type, a wrong ANSWER
	// rather than a refusal.
	sharedPreludeDefs = map[string][]*typeDef{}
)

// sharedPreludeInstance answers the process-wide def for an instantiation whose
// arguments are all package-neutral, and reports whether it is one.
func sharedPreludeInstance(spec *preludeSpec, args []kind) (kind, bool) {
	for _, arg := range args {
		if !arg.packageNeutral() {
			return kindInvalid, false
		}
	}
	nomi := preludeNomi(spec, args)
	sharedPreludeMu.Lock()
	defer sharedPreludeMu.Unlock()
	return named(internSharedPrelude(nomi, args, func() *typeDef {
		return buildPreludeDef(spec, args)
	})), true
}

// internSharedPrelude is the table's one insertion point, and the collision
// check lives here rather than at the call so a second caller cannot bypass
// it.
//
// Grouped by Nomi spelling and separated by the type ARGUMENTS, which is
// composite.go's rule, applied here for the same reason. Nothing admitted
// through packageNeutral breaks that; the grouping is here so all four
// intern tables answer the identity question one way rather than three.
//
// Caller holds sharedPreludeMu.
func internSharedPrelude(nomi string, args []kind, build func() *typeDef) *typeDef {
	for _, d := range sharedPreludeDefs[nomi] {
		if !samePartsSlice(d.preludeArgs, args) {
			continue
		}
		return d
	}
	d := build()
	sharedPreludeDefs[nomi] = append(sharedPreludeDefs[nomi], d)
	return d
}

// --- the stdlib signature side ---------------------------------------------

// preludeSigKind is the kind a stdlib signature's `Maybe<…>` / `Result<…>`
// annotation names, and reports whether the annotation named a prelude enum at
// all.
//
// The identity check is the ANCHOR, not the name: `anchors.preludes` was built from
// this module's own scope by preludeAnchorsOf, which requires the analyzer's
// (Origin, Name) rule AND the declaration's shape. A stdlib module declaring its
// own `Maybe` therefore gets no anchor and refuses, exactly as a user module
// does.
//
// A type ARGUMENT is resolved by the same restricted stdTypeKind the outer
// position uses, so `Maybe<List<String>>` refuses on the `List` rather than
// admitting a def this boundary cannot share. Recursion is what makes
// `Result<Maybe<Int>, String>` work without a second rule.
func preludeSigKind(gt *ast.GenericType, anchors stdAnchors) (kind, bool) {
	a := anchors.preludes[gt.Name]
	if a == nil {
		return kindInvalid, false
	}
	if len(gt.Params) != len(a.spec.params) {
		// The checker rejects the program first; refusing rather than
		// instantiating at the wrong arity keeps this from emitting Go against
		// a type nobody wrote.
		return kindInvalid, true
	}
	args := make([]kind, len(gt.Params))
	for i, p := range gt.Params {
		args[i] = stdTypeKind(p, anchors)
		// kindInvalid: propagates — the argument's own refusal is the answer.
		if args[i] == kindInvalid {
			return kindInvalid, true
		}
	}
	k, shared := sharedPreludeInstance(a.spec, args)
	if !shared {
		// Unreachable while stdTypeKind admits only neutral kinds, and treated
		// as a refusal rather than trusted: a def interned in no gen at all
		// would be a kind no call site could ever match.
		return kindInvalid, true
	}
	return k, true
}

// --- the extern registry side ----------------------------------------------

// preludeKindOfGoType is the kind an rt signature's `rt.Maybe[T]` /
// `rt.Result[T, E]` names, or kindInvalid.
//
// kindOfGoType's direction, read off the Go type rather than declared, so a
// registry row cannot claim a shape its implementation does not have. Identified
// STRUCTURALLY — the rt package path, the base name before the type-argument
// bracket, and the tag field plus one field per payload variant — because a
// hand-written `reflect.Type` per instantiation would be an unbounded table and
// a name-only match would accept an unrelated rt type that happened to be
// spelled `Maybe`.
//
// The type ARGUMENTS come out of the payload FIELDS, which is the only place
// reflect exposes them: `rt.Maybe[int64]`'s `Some` field is `int64`. That also
// makes the projection recursive for free, so `rt.Result[rt.Maybe[int64], string]`
// answers `Result<Maybe<Int>, String>`.
func preludeKindOfGoType(t reflect.Type) kind {
	if t == nil || t.Kind() != reflect.Struct || t.PkgPath() != rtModulePath {
		return kindInvalid
	}
	base, _, generic := strings.Cut(t.Name(), "[")
	if !generic {
		return kindInvalid
	}
	for i := range preludeSpecs {
		spec := &preludeSpecs[i]
		if base != strings.TrimPrefix(spec.rtType, "rt.") {
			continue
		}
		args, ok := preludeGoArgs(spec, t)
		if !ok {
			return kindInvalid
		}
		k, shared := sharedPreludeInstance(spec, args)
		if !shared {
			return kindInvalid
		}
		return k
	}
	return kindInvalid
}

// preludeGoArgs reads one instantiation's type arguments out of its Go fields.
//
// The field COUNT is checked as well as the names, for the reason
// TestPreludeLayoutMatchesRT checks it: an unaccounted field is storage this
// builder never writes, and reading arguments off a struct whose shape has
// drifted would project a signature nobody declared.
//
// A CONCRETE payload field is checked rather than read. `rt.Fragment[T]`'s
// `Static` is `string` for every T, so it answers no type argument — but it is
// storage, so it has to be counted, and its Go type has to be the one the spec
// fixed it to. Skipping it entirely would let rt declare `Static int64` while
// the builder kept giving it a `string` kind.
func preludeGoArgs(spec *preludeSpec, t reflect.Type) ([]kind, bool) {
	if _, ok := t.FieldByName(spec.tagField); !ok {
		return nil, false
	}
	args := make([]kind, len(spec.params))
	fields := 1
	for _, v := range spec.variants {
		if !v.carries() {
			continue
		}
		fields++
		f, ok := t.FieldByName(v.field)
		if !ok {
			return nil, false
		}
		k := kindOfGoType(f.Type)
		// kindInvalid: propagates — an rt type outside the representable set.
		if k == kindInvalid {
			return nil, false
		}
		if v.param < 0 {
			if k != v.fixed {
				return nil, false
			}
			continue
		}
		args[v.param] = k
	}
	if t.NumField() != fields {
		return nil, false
	}
	for _, a := range args {
		// A type parameter no variant carries: the specs have none, so this is a
		// backstop against a spec whose params outrun its payload fields.
		// kindInvalid: lookup — an argument no payload field resolved.
		if a == kindInvalid {
			return nil, false
		}
	}
	return args, true
}

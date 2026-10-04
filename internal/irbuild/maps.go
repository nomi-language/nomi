package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// `Map<K, V>`: its kind, and whether a key type has a structural hash and
// equality, which is the part that carries the design.
//
// # The representation lives in rt, and why
//
// rt/map.go argues it in full: a Nomi Map key is any keyable VALUE, compared by
// structural hash and equality (rt.Hash/rt.Equal) with no user hook and no
// `K: Hashable` bound, so a Go `map` is unavailable — for a
// `*rt.List[T]` key it is not even a compile error, it is an address-keyed map
// that silently loses every key. And a Nomi Map is IMMUTABLE, so the compact
// answer (an ordered slice plus a hash index) makes `put` O(n) and turns the
// accumulate-into-a-map fold quadratic, which is the mistake rt/list.go
// declined for `List`. It is a persistent hash trie.
//
// # The key hash and the key equality are NOT the user-facing interfaces
//
// This is the constraint the whole file is arranged around, and it is a
// silent-divergence risk rather than a preference.
//
// `Hashable` and `Equatable` are Nomi INTERFACES, so a program may implement
// them, and may implement them inconsistently with each other. A Map key
// buckets on the structural hash and matches on structural equality, neither
// of which consults an impl. A Map that dispatched to `Hashable.hash` would
// behave on every well-behaved key and fail on exactly the programs that got
// the law wrong: the key would be unretrievable, with no error anywhere. So:
//
//	The bucketing hash and the key equality are STRUCTURAL and un-overridable.
//	They are rt.Hash and rt.Equal for a kind, assembled at the call site.
//
// That is why this file has `valueHashes`/`valueEquates`, and they are the
// builder.s ONE statement of which kinds have a structural equality.
//
// # The distinction is POSITION, not KIND
//
// Nomi's `==` dispatches to `impl Equatable` at the TOP of a `==`, for a
// struct, an enum variant or a distinct, and nowhere else. So:
//
//   - `p == q` on a STRUCT with an impl dispatches. The operator's own
//     lowering does that.
//   - `p == q` on an ENUM or a DISTINCT dispatches when the impl is
//     HAND-WRITTEN and not when it is derived. See equatableDispatches for
//     the struct rule and handWrittenNominalEquatable for the nominal one.
//   - `[p] == [q]`, `(p, 1) == (q, 1)`, a `p` key inside a Map: never, at any
//     depth, because each is structural equality from the outermost step down
//     and structural equality does not dispatch. A `derive Equatable` on a
//     struct FIELD is likewise not consulted when the containing struct falls
//     back structurally: for an `Inner` whose impl returns True
//     unconditionally, `a == b` is True and `Outer{i: a} == Outer{i: b}` is
//     False.
//
// So every caller here wants structural equality and there is nothing left for a second
// rule to be. The operator's dispatch lives at exactly one site, the
// operator's lowering (ircompare.go), and everything underneath it is this
// file.
//
// # Base-name laxness cannot be observed through a map key, or through the operator
//
// A named type compares structurally. Comparing a struct's type name by base
// name would let `a.Point` and `b.Point` compare equal. That is unreachable
// here: a `Map<K, V>` has ONE K, so two keys in one map always have the same
// static Nomi type. The laxness needs two types in one
// comparison and a map key never gets them; the operator never gets them
// either, because two same-named types from two files are two kinds and a
// mixed-kind comparison declines first.

// mapKind is the kind of `Map<key, val>`.
func (g *gen) mapKind(key, val kind) kind {
	return mapKindIn(g, key, val)
}

// mapKindIn is `mapKind` with the gen passed explicitly, so the STDLIB side can
// build a Map kind where it has no gen to build it in.
//
// `g` may be nil, which internComp documents as the stdlib signature boundary:
// with both components package-neutral it routes to the PROCESS-WIDE table, so
// the kind a `stdMapField` produces is the SAME entry a call site's `g.mapKind`
// produces and the two compare equal. A per-gen kind would be one no other gen
// could ever match — the failure `stdMaybeField`'s `!shared` guard exists to
// catch. A nil gen with a non-neutral component panics there rather than
// yielding a kind interned nowhere.
//
// One function rather than two because the Go type NAME is written once. Two
// places rendering `rt.Map[...]` is a divergence generator of exactly the kind
// this package keeps finding, and the rendering is what interning keys on.
func mapKindIn(g *gen, key, val kind) kind {
	// kindInvalid: propagates — a kind CONSTRUCTOR; the key/value decline is counted in elementKind.
	if key == kindInvalid || val == kindInvalid {
		return kindInvalid
	}
	return kind{tag: tagMap, comp: internComp(g, "Map<"+key.nomi()+", "+val.nomi()+">", key, val)}
}

// mapTypeOf reads the annotation `Map<K, V>`, reporting whether the generic
// type named a Map at all so an unrepresentable `Foo<Bar>` keeps its own
// refusal.
func (g *gen) mapTypeOf(t *ast.GenericType) (kind, bool) {
	if t.Name != "Map" || len(t.Params) != 2 {
		return kindInvalid, false
	}
	return g.mapKind(g.typeOf(t.Params[0]), g.typeOf(t.Params[1])), true
}

// --- literals ---------------------------------------------------------------

// mapKeyOK reports whether key has a structural hash and equality, refusing
// by name when it has not.
//
// One function so the refusal is stated once: a key that cannot be hashed
// cannot be compared either, and reporting two refusals for one gap would let
// it outvote two real ones.
func (g *gen) mapKeyOK(key kind, at ast.Node) bool {
	if !g.valueHashes(key) || !g.valueEquates(key) {
		g.reject("map key type", key.nomi(), at)
		return false
	}
	return true
}

// --- the recursion guard both walks need ------------------------------------

// expanding is keyed on the `*typeDef` POINTER, which IS the type's identity
// (types.go) — never a rendered name and never a base name, so two
// same-named declarations cannot stand in for each other here and a collapse
// is unrepresentable rather than detected.
//
// beginExpand reports whether d may be expanded, and records it as in progress
// when it may. A PATH stack rather than a visited set: endExpand pops, so a
// type mentioned twice side by side (`struct Pair { a: Point, b: Point }`)
// expands twice and only a type that can reach ITSELF declines. Mutating the
// pop away refuses `Map<Pair, Int>`, which is what pins the distinction.
func (g *gen) beginExpand(d *typeDef) bool {
	if g.expanding[d] {
		return false
	}
	if g.expanding == nil {
		g.expanding = map[*typeDef]bool{}
	}
	g.expanding[d] = true
	return true
}

func (g *gen) endExpand(d *typeDef) { delete(g.expanding, d) }

// --- structural hash and equality, for a kind -------------------------------

// valueHashes reports whether kind k has a structural hash (rt.Hash), the hash
// that buckets a map key.
//
// Defined over exactly the kinds valueEquates is defined over, which is a
// requirement and not a coincidence: a hash without a matching equality is a
// bucket nobody can search. TestMapKeyOpsAgreeOnTheirDomain holds the two in
// step over the shapes its table lists.
func (g *gen) valueHashes(k kind) bool {
	if isDecimalKind(k) || isStdHostPrim(k, analysis.TypeByte) || isStdHostPrim(k, analysis.TypeBytes) {
		// A Decimal's, a Byte's and a Bytes' kinds are `tagNamed` over an
		// rt-opaque def, which namedHashes declines; rt.Hash has a case for
		// each (rt.HashDecimal, rt.HashByte, rt.HashBytes).
		return true
	}
	switch k.tag {
	case tagInt, tagFloat, tagString, tagBool, tagUnit, tagEmptyList, tagEmptyVector, tagEmptySet:
		return true
	case tagList:
		return g.valueHashes(k.comp.parts[0])
	case tagTuple, tagAnonStruct:
		return g.allHash(k.comp.parts)
	case tagMap:
		return g.valueHashes(k.comp.parts[0]) && g.valueHashes(k.comp.parts[1])
	case tagNamed:
		// A Vector's def is rt-opaque and a Set's is a struct over its backing
		// map; both hash by their elements (rt.HashVector, rt.SetHash).
		if elem, isVec := vectorElem(k); isVec {
			return g.valueHashes(elem)
		}
		if elem, isSet := setElem(k); isSet {
			return g.valueHashes(elem)
		}
		return g.namedHashes(k)
	}
	return false
}

// allHash reports whether every one of parts hashes.
func (g *gen) allHash(parts []kind) bool {
	for _, p := range parts {
		if !g.valueHashes(p) {
			return false
		}
	}
	return true
}

// namedHashes is valueHashes for a declared struct, enum or distinct type: a
// distinct hashes its inner (a marker has one inhabitant), a struct its
// fields, an enum its tag and slots.
//
// RECURSION DECLINES rather than crashing: `enum Chain { End; Link Chain }`
// used as `Map<Chain, Int>` would otherwise not terminate.
func (g *gen) namedHashes(k kind) bool {
	d := k.def
	if !g.beginExpand(d) {
		return false
	}
	defer g.endExpand(d)
	switch {
	// An rt-opaque leaf. DECLINED: the hash has to agree with rt.Hash's
	// structural scheme, which is a claim about the whole scheme rather than
	// about this type. Asked before the marker arm, which would otherwise
	// hash a value with contents as Unit.
	case d.rtOpaque:
		return false
	// kindInvalid: marker — a marker hashes as Unit; this SUCCEEDS rather than declining.
	case d.isDistinct && d.inner == kindInvalid:
		return true
	case d.isDistinct:
		return g.valueHashes(d.inner)
	case d.isEnum:
		for _, s := range d.slots {
			if !g.valueHashes(s.k) {
				return false
			}
		}
		return true
	default:
		for _, f := range d.fields {
			if !g.valueHashes(f.k) {
				return false
			}
		}
		return true
	}
}

// valueEquates reports whether kind k has a structural equality (rt.Equal),
// recursive and never dispatching to an impl. See the file comment for why it
// never dispatches.
func (g *gen) valueEquates(k kind) bool {
	if isDecimalKind(k) || isStdHostPrim(k, analysis.TypeByte) || isStdHostPrim(k, analysis.TypeBytes) {
		// rt.EqDecimal compares `1.50d == 1.5d` as Nomi does; Byte and Bytes
		// compare their octets. The pair moves with valueHashes.
		return true
	}
	switch k.tag {
	case tagInt, tagFloat, tagString, tagBool, tagUnit, tagEmptyList, tagEmptyVector, tagEmptySet:
		return true
	case tagList:
		return g.valueEquates(k.comp.parts[0])
	case tagTuple, tagAnonStruct:
		return g.allEquate(k.comp.parts)
	case tagMap:
		// Order-insensitive, so it needs the key's hash as well.
		return g.valueHashes(k.comp.parts[0]) && g.valueEquates(k.comp.parts[0]) && g.valueEquates(k.comp.parts[1])
	case tagNamed:
		if elem, isVec := vectorElem(k); isVec {
			return g.valueEquates(elem)
		}
		// A Set compares as its backing map does, so it needs its element's
		// hash as well.
		if elem, isSet := setElem(k); isSet {
			return g.valueEquates(elem) && g.valueHashes(elem)
		}
		return g.namedEquates(k)
	}
	return false
}

// allEquate reports whether every one of parts has a structural equality.
func (g *gen) allEquate(parts []kind) bool {
	for _, p := range parts {
		if !g.valueEquates(p) {
			return false
		}
	}
	return true
}

// namedEquates is valueEquates for a declared struct, enum or distinct type.
// The shapes and the recursion guard mirror namedHashes'.
func (g *gen) namedEquates(k kind) bool {
	d := k.def
	if !g.beginExpand(d) {
		return false
	}
	defer g.endExpand(d)
	switch {
	// An rt-opaque leaf. DECLINED because namedHashes declines: equality
	// without a matching hash is a wrong BUCKET. The pair moves together or
	// not at all.
	case d.rtOpaque:
		return false
	// kindInvalid: marker — two markers are always equal; this SUCCEEDS rather than declining.
	case d.isDistinct && d.inner == kindInvalid:
		return true
	case d.isDistinct:
		return g.valueEquates(d.inner)
	case d.isEnum:
		for _, s := range d.slots {
			if !g.valueEquates(s.k) {
				return false
			}
		}
		return true
	default:
		for _, f := range d.fields {
			if !g.valueEquates(f.k) {
				return false
			}
		}
		return true
	}
}

// --- rendering and equality hooks ------------------------------------------

// --- call sites -------------------------------------------------------------

// mapFn is one `Map.` function this builder lowers: the rt symbol, the arity,
// and where the key operations go in the argument list.
type mapFn struct {
	rtCall string
	// args is how many Nomi arguments the call takes.
	args int
	// opsAfter is the number of leading Nomi arguments that precede the
	// `hash, eq` pair in the Go-spelled call. It is 1 for every entry except
	// merge, which takes both maps first.
	opsAfter int
	// result names how the result kind is derived from the map's key and value
	// kinds. See mapCall.
	result string
}

// mapFuncs is the RT-CALL surface of `impl Map<K, V>`: the five host fns plus
// `contains_key?`, each a direct rt symbol with a uniform "coerce every
// argument, then join" shape.
//
// Use across the corpus: get 20, size 14, empty 9, put 8, remove 1.
//
// # The projections are NOT here
//
// `keys`, `values`, `map_values` and `map_keys` have Nomi bodies written over
// `Iter`, but the ordinary stdlib path cannot lower them: stdCandidateFor
// (stdlib.go) puts `case generic:` FIRST in its switch, and `generic` is a
// purely syntactic `implGeneric || len(TypeParams) > 0 || len(WhereClauses) > 0`,
// so every function of `impl Map<K, V>` is filed under `stdlib generic
// function` before anything about its BODY is consulted. As in preludefn.go,
// the index can NAME these and cannot SUPPLY the type argument, while the CALL
// SITE can.
//
// So they lower in mapProjection below, and they do not become a second
// implementation of std's rule: rt.MapKeys/MapValues/MapMapValues are all
// built on rt.MapEntries, the single ordered accessor that FormatMap and
// MapEqual already go through, so there is ONE order in rt and the equivalence
// with the Nomi body is a shared bottom rather than an argument.
//
// `map_keys` lowers in mapMapValuesPlan. `map_next` is a row above: it
// answers `Maybe<((K, V), Map<K, V>)>`, the first entry in insertion order and
// the map without it.
var mapFuncs = map[string]mapFn{
	"get":           {rtCall: "rt.MapGet", args: 2, opsAfter: 1, result: "maybe"},
	"put":           {rtCall: "rt.MapPut", args: 3, opsAfter: 1, result: "map"},
	"size":          {rtCall: "rt.MapSize", args: 1, opsAfter: 0, result: "int"},
	"remove":        {rtCall: "rt.MapRemove", args: 2, opsAfter: 1, result: "map"},
	"merge":         {rtCall: "rt.MapMerge", args: 2, opsAfter: 2, result: "map"},
	"contains_key?": {rtCall: "rt.MapContains", args: 2, opsAfter: 1, result: "bool"},
	"map_next":      {rtCall: "rt.MapNext", args: 1, opsAfter: 0, result: "next"},
}

// mapResultKind turns a mapFn's result tag into a kind.
func (g *gen) mapResultKind(tag string, key, val kind, at ast.Node) (kind, bool) {
	switch tag {
	case "int":
		return kindInt, true
	case "bool":
		return kindBool, true
	case "map":
		return g.mapKind(key, val), true
	case "next":
		// `Map.map_next` is `Maybe<((K, V), Map<K, V>)>`.
		entry := g.tupleKind([]kind{key, val})
		// kindInvalid: reports — an unrepresentable tuple part, named by tupleKind.
		if entry == kindInvalid {
			return kindInvalid, false
		}
		pair := g.tupleKind([]kind{entry, g.mapKind(key, val)})
		// kindInvalid: reports — an unrepresentable tuple part, named by tupleKind.
		if pair == kindInvalid {
			return kindInvalid, false
		}
		return g.maybeOf(pair, at)
	case "maybe":
		// `Map.get` returns `Maybe<V>`, whose representation prelude.go owns.
		// Asked for rather than assembled here, so there is one shape of Maybe
		// in the builder and not two. The anchors are loaded on first use.
		g.loadPreludes()
		a, anchored := g.preludeByName["Maybe"]
		if !anchored {
			g.reject("Map.get without the prelude Maybe", val.nomi(), at)
			return kindInvalid, false
		}
		k := g.preludeInstance(a, []kind{val})
		// kindInvalid: reports — rejects `Map.get over an unrepresentable value type`.
		if k == kindInvalid {
			g.reject("Map.get over an unrepresentable value type", val.nomi(), at)
			return kindInvalid, false
		}
		return k, true
	}
	return kindInvalid, false
}

// --- patterns ---------------------------------------------------------------

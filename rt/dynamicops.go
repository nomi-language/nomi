package rt

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// std/dynamic's thirteen host operations, as the Go the VM calls.
//
// # One implementation of each rule
//
// Every rule below — the type vocabulary in a `DecodeError`'s `expected`/`got`
// fields, the eleven fault messages, the Int-vs-Float acceptance rule, the
// unsigned range guards, the sorted dict-key order, and the JSON-shaped
// rendering — lives here once, as `rt/json.go` holds the JSON scanner and
// `rt.FormatAssertionFailure` the assertion renderer. rt/trap.go states the
// reason for `NoCaseMatchError`: an observable string has one copy.
//
// # The guard is ABSOLUTE
//
// A comparison between two callers cannot see a bug in code both reach, so the
// assertion for these strings is ABSOLUTE: rt/dynamicops_test.go pins the
// renderings and the type vocabulary as literal tables.

// Dynamic is Nomi's `std/dynamic.Dynamic`: a value of unknown shape that crossed
// an FFI boundary.
//
// A one-field struct over `any` rather than a bare `any`, and the wrapper is
// load-bearing rather than stylistic. `stdHostKindOfGoType` identifies this
// family by `reflect.Type`, so a binding whose parameter were `any` would
// project onto EVERY declaration whose parameter has no representation — the
// same failure `rt.Bytes` avoids by not being a plain `string`
// (internal/irbuild/stdhost.go says so in those terms). Identity on the named
// type, never on the underlying shape.
//
// `Inner` holds the Go shapes `encoding/json.Unmarshal` produces — string,
// int64, float64, bool, []any, map[string]any, nil — plus the native integer and
// float widths a non-JSON host may hand over.
type Dynamic struct {
	Inner any
}

// --- the type vocabulary ---------------------------------------------------

// dynamicTypeName names the wrapped value's runtime kind in the vocabulary the
// Nomi-side `expected`/`got` fields use.
//
// Unknown Go types fall back to `<%T>` so an embedder sees the real type rather
// than a generic label: `*foo.Bar` in a decode error is worth more than
// "Unknown". Pinned by TestDynamicTypeNameVocabulary.
func dynamicTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "Null"
	case bool:
		return "Bool"
	case float64, float32:
		return "Float"
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return "Int"
	case string:
		return "String"
	case []any:
		return "List"
	case map[string]any:
		return "Dict"
	default:
		return fmt.Sprintf("<%T>", v)
	}
}

// --- DecodeError construction ----------------------------------------------

// dynamicErr builds the `Err(DecodeError{…})` these operations return.
//
// `path` is nil for a terminal extractor and one segment for a navigator, which
// is std/dynamic.nomi's documented single-segment policy: each navigator's error
// carries only the segment it generated, and `at_field`/`at_index` are the
// combinators that accumulate. A `nil *List` IS the empty list, so the two cases
// need no separate constructor.
func dynamicErr(path *List[DynamicPathSegment], expected, got string) Result[Dynamic, DynamicDecodeError] {
	return Err[Dynamic](DynamicDecodeError{Path: path, Expected: expected, Got: got})
}

// dynamicErrOf is dynamicErr for an extractor whose Ok type is not Dynamic.
//
// Two functions rather than one generic over both parameters because Go cannot
// infer `T` from a call that mentions only the error side; the alternative is
// every call site spelling its own `Err[string]`, which is the annotation this
// hides.
func dynamicErrOf[T any](path *List[DynamicPathSegment], expected, got string) Result[T, DynamicDecodeError] {
	return Err[T](DynamicDecodeError{Path: path, Expected: expected, Got: got})
}

// dynamicPathField is a one-segment path naming a field navigation.
func dynamicPathField(name string) *List[DynamicPathSegment] {
	return Cons(DynamicPathSegment{Tag: TagPathSegmentField, Field: name}, nil)
}

// dynamicPathIndex is a one-segment path naming an index navigation.
func dynamicPathIndex(i int64) *List[DynamicPathSegment] {
	return Cons(DynamicPathSegment{Tag: TagPathSegmentIndex, Index: i}, nil)
}

// --- navigators ------------------------------------------------------------

// DynamicField is `std/dynamic.Dynamic.field`: drill into a dict-shaped Dynamic
// by field name.
//
// Two gates with two different messages — a shape gate ("object") and a presence
// gate ("object with field …") — and both attach the segment they were
// navigating, so a caller sees both what went wrong and where.
func DynamicField(d Dynamic, name string) Result[Dynamic, DynamicDecodeError] {
	dict, ok := d.Inner.(map[string]any)
	if !ok {
		return dynamicErr(dynamicPathField(name), "object", dynamicTypeName(d.Inner))
	}
	child, exists := dict[name]
	if !exists {
		return dynamicErr(dynamicPathField(name),
			"object with field "+strconv.Quote(name),
			"object without field "+strconv.Quote(name))
	}
	return Ok[Dynamic, DynamicDecodeError](Dynamic{Inner: child})
}

// DynamicIndex is `std/dynamic.Dynamic.index`: drill into a list-shaped Dynamic
// by zero-based index.
//
// A negative index takes the out-of-bounds arm rather than a third one, because
// "list with at least 0 elements" is the honest expectation for `index(d, -1)`
// and inventing a fourth message for it would be a vocabulary nobody reads.
func DynamicIndex(d Dynamic, i int64) Result[Dynamic, DynamicDecodeError] {
	list, ok := d.Inner.([]any)
	if !ok {
		return dynamicErr(dynamicPathIndex(i), "list", dynamicTypeName(d.Inner))
	}
	if i < 0 || i >= int64(len(list)) {
		return dynamicErr(dynamicPathIndex(i),
			fmt.Sprintf("list with at least %d elements", i+1),
			fmt.Sprintf("list with %d elements", len(list)))
	}
	return Ok[Dynamic, DynamicDecodeError](Dynamic{Inner: list[i]})
}

// DynamicPath is `std/dynamic.Dynamic.path`: the dotted-path navigator.
//
// Every segment is a FIELD navigation; index notation is deliberately absent
// (std/dynamic.nomi says so) because `a.0.b` would be ambiguous against a dict
// whose key is "0", and `index` is available for a list.
//
// An empty string and an empty segment are separate messages with NO path,
// because neither names a navigation that was attempted — the input was
// malformed before any drilling started.
func DynamicPath(d Dynamic, dotted string) Result[Dynamic, DynamicDecodeError] {
	if dotted == "" {
		return dynamicErr(nil, "non-empty dotted path", "empty string")
	}
	cursor := d.Inner
	for _, seg := range strings.Split(dotted, ".") {
		if seg == "" {
			return dynamicErr(nil, "non-empty path segments",
				"empty segment in "+strconv.Quote(dotted))
		}
		dict, ok := cursor.(map[string]any)
		if !ok {
			return dynamicErr(dynamicPathField(seg), "object", dynamicTypeName(cursor))
		}
		next, exists := dict[seg]
		if !exists {
			return dynamicErr(dynamicPathField(seg),
				"object with field "+strconv.Quote(seg),
				"object without field "+strconv.Quote(seg))
		}
		cursor = next
	}
	return Ok[Dynamic, DynamicDecodeError](Dynamic{Inner: cursor})
}

// --- extractors ------------------------------------------------------------

// DynamicAsString is `std/dynamic.Dynamic.as_string`. Strict: only a Go string.
func DynamicAsString(d Dynamic) Result[string, DynamicDecodeError] {
	s, ok := d.Inner.(string)
	if !ok {
		return dynamicErrOf[string](nil, "String", dynamicTypeName(d.Inner))
	}
	return Ok[string, DynamicDecodeError](s)
}

// dynamicIntFromFloat is the shared float-to-Int path, for both float64 and
// (widened) float32.
//
// THE RANGE GUARD'S FORM IS LOAD-BEARING and the naive spelling is wrong.
// math.MaxInt64 (2^63 - 1) has no exact float64, and converting it rounds UP to
// 2^63 — so `n > math.MaxInt64` is FALSE for n == 2^63 even though the int64
// cast overflows. Testing `>= float64(1<<63)` compares against the rounded-up
// boundary directly, which is what rejects 2^63. Moved with the comment because
// the comment is why the code is not simpler.
func dynamicIntFromFloat(n float64) Result[int64, DynamicDecodeError] {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return dynamicErrOf[int64](nil, "Int", "Float (NaN/Inf)")
	}
	if n != math.Trunc(n) {
		return dynamicErrOf[int64](nil, "Int", "Float with fractional part")
	}
	if n < -float64(1<<63) || n >= float64(1<<63) {
		return dynamicErrOf[int64](nil, "Int", "Float (out of int64 range)")
	}
	return Ok[int64, DynamicDecodeError](int64(n))
}

// DynamicAsInt is `std/dynamic.Dynamic.as_int`.
//
// Accepts every native integer width plus an integral float, because JSON's `36`
// arrives as float64(36) and rejecting it would make the common case fail. A
// float with a fractional part is refused rather than truncated: losing data
// silently is the one thing an extractor must not do.
//
// The three unsigned guards are not decoration. uint64 — and uint/uintptr on a
// 64-bit platform — can hold values above MaxInt64 whose top bit becomes the
// SIGN bit under an int64 cast, so an unguarded conversion answers a negative
// number for a positive input. uint8/uint16/uint32 fit losslessly and need no
// guard, which is why they have none.
func DynamicAsInt(d Dynamic) Result[int64, DynamicDecodeError] {
	switch n := d.Inner.(type) {
	case int:
		return Ok[int64, DynamicDecodeError](int64(n))
	case int8:
		return Ok[int64, DynamicDecodeError](int64(n))
	case int16:
		return Ok[int64, DynamicDecodeError](int64(n))
	case int32:
		return Ok[int64, DynamicDecodeError](int64(n))
	case int64:
		return Ok[int64, DynamicDecodeError](n)
	case uint:
		if uint64(n) > math.MaxInt64 {
			return dynamicErrOf[int64](nil, "Int", "Uint (out of int64 range)")
		}
		return Ok[int64, DynamicDecodeError](int64(n))
	case uintptr:
		if uint64(n) > math.MaxInt64 {
			return dynamicErrOf[int64](nil, "Int", "Uintptr (out of int64 range)")
		}
		return Ok[int64, DynamicDecodeError](int64(n))
	case uint8:
		return Ok[int64, DynamicDecodeError](int64(n))
	case uint16:
		return Ok[int64, DynamicDecodeError](int64(n))
	case uint32:
		return Ok[int64, DynamicDecodeError](int64(n))
	case uint64:
		if n > math.MaxInt64 {
			return dynamicErrOf[int64](nil, "Int", "Uint64 (out of int64 range)")
		}
		return Ok[int64, DynamicDecodeError](int64(n))
	case float64:
		return dynamicIntFromFloat(n)
	case float32:
		return dynamicIntFromFloat(float64(n))
	default:
		return dynamicErrOf[int64](nil, "Int", dynamicTypeName(d.Inner))
	}
}

// DynamicAsFloat is `std/dynamic.Dynamic.as_float`. Integers widen the way a
// Nomi-side `Float(n)` would; native floats pass through.
//
// No range guard on the unsigned widths here, and the asymmetry with DynamicAsInt
// is deliberate rather than an omission: every uint64 has a float64 that
// represents it approximately, so the conversion is lossy in PRECISION but never
// wrong in SIGN, and refusing it would refuse a value the caller asked to see as
// a Float.
func DynamicAsFloat(d Dynamic) Result[float64, DynamicDecodeError] {
	switch n := d.Inner.(type) {
	case float64:
		return Ok[float64, DynamicDecodeError](n)
	case float32:
		return Ok[float64, DynamicDecodeError](float64(n))
	case int:
		return Ok[float64, DynamicDecodeError](float64(n))
	case int8:
		return Ok[float64, DynamicDecodeError](float64(n))
	case int16:
		return Ok[float64, DynamicDecodeError](float64(n))
	case int32:
		return Ok[float64, DynamicDecodeError](float64(n))
	case int64:
		return Ok[float64, DynamicDecodeError](float64(n))
	case uint:
		return Ok[float64, DynamicDecodeError](float64(n))
	case uint8:
		return Ok[float64, DynamicDecodeError](float64(n))
	case uint16:
		return Ok[float64, DynamicDecodeError](float64(n))
	case uint32:
		return Ok[float64, DynamicDecodeError](float64(n))
	case uint64:
		return Ok[float64, DynamicDecodeError](float64(n))
	default:
		return dynamicErrOf[float64](nil, "Float", dynamicTypeName(d.Inner))
	}
}

// DynamicAsBool is `std/dynamic.Dynamic.as_bool`. Strict: only a Go bool.
//
// No truthiness. A numeric or string coercion here would be a wrong ANSWER on
// the first `0`-means-false source, and std/dynamic.nomi calls the strictness
// deliberate.
func DynamicAsBool(d Dynamic) Result[bool, DynamicDecodeError] {
	b, ok := d.Inner.(bool)
	if !ok {
		return dynamicErrOf[bool](nil, "Bool", dynamicTypeName(d.Inner))
	}
	return Ok[bool, DynamicDecodeError](b)
}

// DynamicAsList is `std/dynamic.Dynamic.as_list`.
//
// Each element stays WRAPPED, so per-element decoding composes. Built back to
// front over `Cons` so the shared-tail invariant holds and `Len` is right at
// every cell.
func DynamicAsList(d Dynamic) Result[*List[Dynamic], DynamicDecodeError] {
	raw, ok := d.Inner.([]any)
	if !ok {
		return dynamicErrOf[*List[Dynamic]](nil, "List", dynamicTypeName(d.Inner))
	}
	var out *List[Dynamic]
	for i := len(raw) - 1; i >= 0; i-- {
		out = Cons(Dynamic{Inner: raw[i]}, out)
	}
	return Ok[*List[Dynamic], DynamicDecodeError](out)
}

// DynamicAsDict is `std/dynamic.Dynamic.as_dict`.
//
// Keys are inserted in SORTED order, and that is an observable decision rather
// than tidiness: Go map iteration is randomized, and `rt.MapEntries` reports
// INSERTION order, so an unsorted walk would give a Nomi `Map` whose rendering
// differed between runs of one program.
func DynamicAsDict(d Dynamic) Result[Map[string, Dynamic], DynamicDecodeError] {
	raw, ok := d.Inner.(map[string]any)
	if !ok {
		return dynamicErrOf[Map[string, Dynamic]](nil, "Dict", dynamicTypeName(d.Inner))
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := Map[string, Dynamic]{}
	for _, k := range keys {
		out = MapPut(out, HashString, Eq[string], k, Dynamic{Inner: raw[k]})
	}
	return Ok[Map[string, Dynamic], DynamicDecodeError](out)
}

// --- predicates ------------------------------------------------------------

// DynamicIsNull is `std/dynamic.Dynamic.null?`. Never fails, so no Result.
func DynamicIsNull(d Dynamic) bool { return d.Inner == nil }

// DynamicHas is `std/dynamic.Dynamic.has?`: True iff d is a dict with that key.
//
// False for a non-dict and for a missing key alike, and the conflation is the
// point — the use case is "decode if present", where allocating a DecodeError to
// discriminate two shapes the caller treats identically is waste.
func DynamicHas(d Dynamic, name string) bool {
	dict, ok := d.Inner.(map[string]any)
	if !ok {
		return false
	}
	_, exists := dict[name]
	return exists
}

// --- rendering -------------------------------------------------------------

// DynamicInspect backs `impl Debug for Dynamic`.
//
// JSON-shaped, because the commonest source of a Dynamic IS a JSON parser and
// reading the rendering against the source document is the likeliest diagnostic
// activity. It is a development aid and NOT a serialization API — `Json.encode`
// is that — which is why it renders an unknown Go type as `<%T>` instead of
// failing.
func DynamicInspect(d Dynamic) string { return dynamicRender(d.Inner) }

// dynamicRender is the recursion behind DynamicInspect.
//
// A separate function because the recursion needs a name, and unexported because
// the boundary a caller should see is a `Dynamic` rather than a bare `any`.
// Pinned by TestDynamicInspectRenderings.
func dynamicRender(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return strconv.Quote(x)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = dynamicRender(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = strconv.Quote(k) + ": " + dynamicRender(x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("<%T>", v)
	}
}

// --- the Json bridge -------------------------------------------------------

// JsonToDynamic is `std/json.Json.to_dynamic`.
//
// A wrapper over JsonToAny rather than a second walk: the tree conversion is
// json.go's and this is the one line that gives it the Nomi type.
func JsonToDynamic(jv Json) Dynamic { return Dynamic{Inner: JsonToAny(jv)} }

// InspectDynamic is how an assertion's `values:` row shows a Dynamic, and it is
// NOT DynamicInspect.
//
// MEASURED rather than reasoned from the type, which is the rule inspect.go's Decimal arm states and the reason this function exists at
// all. A failing `assert Debug.inspect(dyn) == "…"` over a Dynamic wrapping the
// string "hello" reports:
//
//	values:
//	  dyn
//	    = <dynamic: string>
//	  Debug.inspect(dyn)
//	    = ""hello""
//
// Two renderings of ONE value, in ONE assertion, two lines apart. So this is the
// sharpest instance of the four-renderings rule in the tree: `Debug.inspect` is
// the payload and the row is the WRAPPER's Go type, and reaching for Debug here —
// the natural mistake, since `impl Debug for Dynamic` exists and is bound — would
// be a wrong ANSWER that only a deliberately failing fixture could catch.
//
// ONE OWNER FOR THE FORMAT: RowText reaches this `%T` for a Dynamic, and
// `JsonToAny` produces string/int64/float64/bool/[]any/map[string]any, so the
// `%T` a program sees is fixed by construction.
func InspectDynamic(d Dynamic) string { return fmt.Sprintf("<dynamic: %T>", d.Inner) }

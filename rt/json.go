package rt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// `std/json`'s value tree and its decode failure, as Go types, plus the one
// implementation of `decode`, `encode` and the Dynamic projection.
//
// This file declares NO `init`. That matters because everything that runs
// Nomi links `nomi/rt`, so an `init` here would run in every such process;
// nothing in rt has one today and this does not start.
//
// # Json is the FIRST SELF-REFERENTIAL type in any of the shared-def families
//
// `Arr List<Json>` and `Obj Map<String, Json>` name the enclosing type, which
// four separate comments in internal/irbuild said made `Json` ineligible for
// stdenum.go — "that family admits only a SCALAR payload, and the reason is the
// whole safety argument for sharing a *typeDef process-wide". That reason is
// wrong, and the correction belongs here because this file is the thing it was
// wrong about.
//
// The safety argument is foreign.go's, and it is about RENDERING: a shared def
// is only correct if every component kind renders to the same Go text wherever
// the builder names it, which is `kind.packageNeutral`. `Json` is `rtDeclared`, so
// `named(jsonDef)` is neutral by that predicate's own `rtDeclared` arm — and
// therefore so are `*rt.List[rt.Json]` and `rt.Map[string, rt.Json]`, which name
// rt and nothing else. Neutrality is SATISFIED, not violated.
//
// What the self-reference really costs is BOOTSTRAP ORDER: the payload kinds
// cannot be built until the def exists, and the def is what the payload kinds
// are part of. stdstruct.go had already solved exactly that with a two-pass
// build — every shell first, every field kind second — and stdenum.go now does
// the same. So the obstacle was one level of indirection in a table's
// constructor, not a property of the type.
//
// # The Go layout is legal because both recursive payloads are INDIRECT
//
// `*List[Json]` is a pointer. `Map[string, Json]` is a value struct whose only
// path back to `Json` is through `root *mapNode[string, Json]`, also a pointer.
// So `Json` has a finite size and Go accepts the declaration. Neither type
// argument introduces a new instantiation either — both are fixed at `Json` — so
// there is no generic instantiation cycle. A payload that reached `Json` through
// a TUPLE or a record would not be indirect and Go would reject it; std declares
// none, and stdenum.go's shape check admits only the annotations spelled below.
//
// # Every observable string here is Nomi's, and there is ONE copy of each
//
// `decode`'s error vocabulary is a designed contract, pinned by
// tests/18-ffi-and-dynamic/json_decode_error_text_test.nomi, and it
// lives here once, for rt/trap.go's stated reason: an observable string has
// one copy. The same goes for the Int/Float split at parse time, the SORTED
// object key order `decode` produces, and `encode`'s float format — each is
// one function rather than two that have to agree.
//
// `encoding/json`'s error TEXT is never forwarded. Only its structured facts are
// read: a `*json.SyntaxError`'s offset, and `io.EOF` vs `io.ErrUnexpectedEOF`.
// Go 1.27 backs `encoding/json` with v2 and its release notes warn that "the
// exact text of error messages may differ", so forwarding it would make a Go
// implementation detail part of Nomi's observable behaviour. The invariant is grep-checkable and TestJsonNeverForwardsHostErrorText
// checks it: nothing in this file calls `.Error()` on a host error.

// Tag values, written out rather than left implicit at the construction sites
// because they are the one thing a reader has to be able to check against
// std/json.nomi by eye. Declaration order there is String, Int, Float, Bool,
// Arr, Obj, Null.
const (
	TagJsonString = 1
	TagJsonInt    = 2
	TagJsonFloat  = 3
	TagJsonBool   = 4
	TagJsonArr    = 5
	TagJsonObj    = 6
	TagJsonNull   = 7
)

// Json is Nomi's `std/json.Json`: a parsed JSON value.
//
// Tag and every payload field are exported because code in other Go packages
// writes the composite literal and reads the payload back. The fields are named
// after the Nomi VARIANTS rather than after the builder's slot spelling, for
// prelude.go's reason: this file is read by people.
//
// Seven variants and SIX payload fields, one per variant that carries data.
// Unlike `CalendarError` — whose five variants all carry a String and share one
// `Msg` — no two of these have the same Go type, so none can share a slot.
type Json struct {
	// Tag is 1..7 in std's declaration order. 0 means never constructed, which
	// is what makes a Go zero value detectable rather than silently reading as
	// the first variant.
	Tag uint8
	// String is the payload of `String String`.
	String string
	// Int is the payload of `Int Int`.
	Int int64
	// Float is the payload of `Float Float`.
	Float float64
	// Bool is the payload of `Bool Bool`.
	Bool bool
	// Arr is the payload of `Arr List<Json>`. A nil *List is the empty array.
	Arr *List[Json]
	// Obj is the payload of `Obj Map<String, Json>`. The ZERO Map is the empty
	// object, so an `Obj` with no entries needs no constructor.
	Obj Map[string, Json]
}

// JsonDecodeError is Nomi's `std/json.Json.DecodeError`: a structured decode
// failure.
//
// Field order and Go names are a CONTRACT with internal/irbuild/stdstruct.go,
// which writes field selectors from its own spec and refuses to anchor a
// declaration that disagrees. `Line` and `Col` are 1-based for display; `Offset`
// is the 0-based byte position of the same spot and points AT the offending
// character rather than one past it.
type JsonDecodeError struct {
	Message string
	Line    int64
	Col     int64
	Offset  int64
}

// JsonShapeError is Nomi's `std/json.Json.ShapeError`: a mismatch found while
// converting an already-parsed `Json` tree into a typed Nomi value.
//
// Distinct from JsonDecodeError and std says why: a parse error is `DecodeError`
// and a shape error describes the PATH inside a well-formed tree where the typed
// conversion failed. One record with both field sets would let the builder
// construct either shape for either function, which is a wrong ANSWER rather
// than a compile error — the same argument rt.Project and rt.RunFile rest on.
//
// `Path` is a `List<String>`, which is why this type needs no new machinery:
// `*rt.List[string]` names rt and a Go builtin and nothing else, so it is
// package-neutral and stdstruct.go's stdListField covers it.
//
// It is here for the `Result<..., Json.ShapeError>` in `ToJson`/`FromJson`'s
// declared signatures. Nothing in rt CONSTRUCTS one: `shape_error_root` and
// `shape_error_prepend` are ordinary Nomi in std/json.nomi and lower through the
// ordinary path, so there is one builder rather than a Go copy of it.
type JsonShapeError struct {
	Path     *List[string]
	Expected string
	Got      string
}

// --- decode ----------------------------------------------------------------

// JsonDecode is `std/json.Json.decode`: parse JSON text into a `Json` tree.
//
// Numbers are decoded as `Int` when they fit in int64 with no fractional part or
// exponent, otherwise as `Float`. Object keys are inserted in SORTED order,
// which is what makes `encode(decode(s))` reproducible — Go's map iteration is
// randomized, so the sort is the determinism and not a nicety.
func JsonDecode(source string) Result[Json, JsonDecodeError] {
	dec := json.NewDecoder(strings.NewReader(source))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return Err[Json, JsonDecodeError](JsonDecodeErrorFor(err, source))
	}
	// Trailing content past the first JSON value is a parse error for the
	// "single value per source" decoder. The offset comes from the decoder's
	// InputOffset so the position points at the start of the unexpected token.
	if dec.More() {
		return Err[Json, JsonDecodeError](JsonDecodeErrorAt(
			"trailing content after JSON value", source, int(dec.InputOffset())))
	}
	jv, err := jsonFromAny(raw)
	if err != nil {
		return Err[Json, JsonDecodeError](JsonWalkFailure(err))
	}
	return Ok[Json, JsonDecodeError](jv)
}

// JsonDecodeErrorFor builds a decode failure from a parser error against the
// original source, reading the parser's STRUCTURED facts only.
//
// Exported so a caller that builds a decode error in its own value shape can
// reuse the classification without this Go type.
//
//	unexpected character 'x'                        a locatable bad byte
//	unexpected byte 0xff                            that byte begins no valid rune
//	unexpected end of input: incomplete JSON value  io.ErrUnexpectedEOF
//	unexpected end of input: expected a JSON value  io.EOF — empty/blank source
//	malformed JSON                                  no structured fact to go on
//
// A `*json.SyntaxError`'s Offset counts bytes CONSUMED, so the offending byte is
// the one before it, and that is what the reported offset points at — the
// character the message names, which is also what an editor wants to underline.
// The two end-of-input shapes carry no position at all from encoding/json, so
// len(source) is supplied: exactly where the value ran out.
func JsonDecodeErrorFor(err error, source string) JsonDecodeError {
	message, offset := jsonDecodeFailure(err, source)
	return JsonDecodeErrorAt(message, source, offset)
}

// JsonDecodeErrorAt fills in the 1-based (line, col) pair for a message and a
// byte offset, so that derivation exists once for every construction site.
//
// Exported for a caller that has the message and the offset already, such as
// the trailing-content arm, and must not re-derive the position rule.
func JsonDecodeErrorAt(message, source string, offset int) JsonDecodeError {
	line, col := JsonLineCol(source, offset)
	return JsonDecodeError{
		Message: message,
		Line:    int64(line),
		Col:     int64(col),
		Offset:  int64(offset),
	}
}

// JsonWalkFailure is the decode error for a failure of the `any`-tree walk,
// which is a different channel from a parser failure: the walk tracks no
// offsets, so there is no position to report and inventing one would be a lie.
//
// The only reachable case is a number token the tokenizer accepted but that
// neither int64 nor float64 can represent (`1e999`). Its lexeme arrives TYPED so
// the Nomi message is written here rather than lifted off Go's strconv range
// error. Anything else is a bug in jsonFromAny's shape switch — unreachable for
// encoding/json output — and gets the generic message rather than a Go type
// name.
//
// Exported for a caller whose own walk can fail the same way.
func JsonWalkFailure(err error) JsonDecodeError {
	message := "malformed JSON"
	var rangeErr *jsonNumberRangeError
	if errors.As(err, &rangeErr) {
		message = "number out of range: " + rangeErr.lexeme
	}
	return JsonDecodeError{Message: message, Line: 1, Col: 1, Offset: 0}
}

// JsonNumberOutOfRange is the typed range failure, so a caller's own walk can raise
// the one JsonWalkFailure recognises instead of declaring a second error type
// that would have to agree with this one.
func JsonNumberOutOfRange(lexeme string) error { return &jsonNumberRangeError{lexeme: lexeme} }

// jsonDecodeFailure classifies a decoder failure as a Nomi-owned message plus a
// byte offset into source.
func jsonDecodeFailure(err error, source string) (string, int) {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		if at := int(syntaxErr.Offset) - 1; at >= 0 && at < len(source) {
			return jsonUnexpectedByte(source[at:]), at
		}
	}
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected end of input: incomplete JSON value", len(source)
	case errors.Is(err, io.EOF):
		return "unexpected end of input: expected a JSON value", len(source)
	}
	return "malformed JSON", 0
}

// jsonUnexpectedByte names the offending byte at the head of rest.
//
// A valid UTF-8 rune is rendered as a Go-syntax rune literal by
// strconv.QuoteRune — a FORMAT Nomi pins, the same way encode pins
// strconv.FormatFloat, not a message it inherits. A byte that begins no valid
// rune is reported numerically instead, because quoting it would print U+FFFD
// and hide which byte was actually there.
func jsonUnexpectedByte(rest string) string {
	r, size := utf8.DecodeRuneInString(rest)
	if r == utf8.RuneError && size <= 1 {
		return fmt.Sprintf("unexpected byte %#02x", rest[0])
	}
	return "unexpected character " + strconv.QuoteRune(r)
}

// JsonLineCol translates a 0-based byte offset into a 1-based (line, col) pair.
//
// O(offset) walk; only called on the error path, so the cost is irrelevant in
// the common case. Tab stops are not honoured — each character including a tab
// advances col by 1 — which matches the JSON spec's character-stream view of
// position.
func JsonLineCol(source string, offset int) (int, int) {
	line, col := 1, 1
	limit := offset
	if limit > len(source) {
		limit = len(source)
	}
	for i := 0; i < limit; i++ {
		if source[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// jsonNumberRangeError is jsonFromAny's only reachable failure: a number token
// encoding/json's tokenizer accepted but that neither int64 nor float64 can
// represent (`1e999` overflows to ±Inf with a strconv range error).
type jsonNumberRangeError struct{ lexeme string }

func (e *jsonNumberRangeError) Error() string {
	return "json: number out of range: " + e.lexeme
}

// JsonFromNumber is the Int-or-Float decision for one JSON number token, and the
// ONLY place that decision exists.
//
// Int64 first — that preserves a whole-number source as `Int`. The Float64
// fallback handles fractional and exponent forms, and integers that overflow
// int64. Exported and returning the three cases separately (`isInt`, the two
// payloads, an error) so a caller walking its own value shape makes the same
// decision here rather than a second time: the split is observable — it
// decides whether `case jv { Int(n) -> … }` matches — and two implementations
// of an observable rule drift apart.
func JsonFromNumber(lexeme string) (i int64, f float64, isInt bool, err error) {
	n := json.Number(lexeme)
	if v, convErr := n.Int64(); convErr == nil {
		return v, 0, true, nil
	}
	v, convErr := n.Float64()
	if convErr != nil {
		return 0, 0, false, JsonNumberOutOfRange(lexeme)
	}
	return 0, v, false, nil
}

// jsonFromAny walks the `any` tree `json.Decoder.Decode` produces under
// UseNumber() and builds the corresponding Json tree.
func jsonFromAny(v any) (Json, error) {
	switch x := v.(type) {
	case nil:
		return Json{Tag: TagJsonNull}, nil
	case bool:
		return Json{Tag: TagJsonBool, Bool: x}, nil
	case string:
		return Json{Tag: TagJsonString, String: x}, nil
	case json.Number:
		i, f, isInt, err := JsonFromNumber(x.String())
		switch {
		case err != nil:
			return Json{}, err
		case isInt:
			return Json{Tag: TagJsonInt, Int: i}, nil
		}
		return Json{Tag: TagJsonFloat, Float: f}, nil
	case []any:
		// Built tail-first, which is the cheap direction for a cons list, and
		// Len counts the cells from each node to the end — the invariant
		// list.go's Cons maintains.
		var out *List[Json]
		for i := len(x) - 1; i >= 0; i-- {
			child, err := jsonFromAny(x[i])
			if err != nil {
				return Json{}, err
			}
			out = &List[Json]{Head: child, Tail: out, Len: len(x) - i}
		}
		return Json{Tag: TagJsonArr, Arr: out}, nil
	case map[string]any:
		obj := Map[string, Json]{}
		for _, k := range JsonSortedKeys(x) {
			child, err := jsonFromAny(x[k])
			if err != nil {
				return Json{}, err
			}
			obj = MapPut(obj, HashString, Eq[string], k, child)
		}
		return Json{Tag: TagJsonObj, Obj: obj}, nil
	}
	return Json{}, fmt.Errorf("jsonFromAny: unhandled Go shape %T", v)
}

// JsonSortedKeys is the key order `decode` inserts an object's entries in.
//
// Exported and factored out because it is OBSERVABLE: an object's insertion
// order is the order `encode` emits, so `Json.encode(Json.decode(s))` produces
// sorted keys and that text is asserted in the corpus. Go's map iteration is
// randomized, so without this the output would differ run to run.
func JsonSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- encode ----------------------------------------------------------------

// JsonEncode is `std/json.Json.encode`: compact JSON text with no whitespace
// between tokens.
//
// Object keys are emitted in the Map's own INSERTION order (MapEntries), which
// is why a hand-built `Json.Obj{"x" => …, "y" => …}` round-trips its own
// spelling while a decoded object comes out sorted — decode inserts sorted.
//
// Number rendering: `Float(36.0)` renders as `36`, not `36.0`, because 'g' with
// -1 precision emits the shortest form that round-trips to the same float64. The
// Int/Float distinction decode captures therefore does not survive a text round
// trip, which std/json.nomi's own doc comment states. Deliberately NOT
// rt.FormatFloat: that is Nomi's Float DISPLAY, which renders 36.0 as "36.0",
// and JSON is a different format.
func JsonEncode(jv Json) string {
	var sb strings.Builder
	jsonEncodeInto(&sb, jv)
	return sb.String()
}

func jsonEncodeInto(sb *strings.Builder, jv Json) {
	switch jv.Tag {
	case TagJsonNull:
		sb.WriteString("null")
	case TagJsonBool:
		sb.WriteString(JsonEncodeBool(jv.Bool))
	case TagJsonString:
		sb.WriteString(JsonEncodeString(jv.String))
	case TagJsonInt:
		sb.WriteString(JsonEncodeInt(jv.Int))
	case TagJsonFloat:
		sb.WriteString(JsonEncodeFloat(jv.Float))
	case TagJsonArr:
		sb.WriteByte('[')
		first := true
		for node := jv.Arr; node != nil; node = node.Tail {
			if !first {
				sb.WriteByte(',')
			}
			first = false
			jsonEncodeInto(sb, node.Head)
		}
		sb.WriteByte(']')
	case TagJsonObj:
		sb.WriteByte('{')
		for i, e := range MapEntries(jv.Obj) {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(JsonEncodeString(e.Key))
			sb.WriteByte(':')
			jsonEncodeInto(sb, e.Val)
		}
		sb.WriteByte('}')
	default:
		// Tag 0. Unreachable from a Nomi program — every Json a program holds
		// was constructed by a variant — so an arrival here is a lowering or rt
		// bug, and a trap names it instead of silently encoding `null`.
		Trap("Json.encode: unconstructed Json value")
	}
}

// The four LEAF renderings, exported because they are exactly the observable
// formats: the escape set, the integer base, the float shortest-round-trip form
// and the two boolean spellings. An encoder that walks some other value shape
// and cannot call jsonEncodeInto still shares these, which keeps "one
// implementation of an observable string" true.

// JsonEncodeString is one JSON string literal.
//
// Routed through json.Marshal rather than hand-escaped, because the escape set
// is observable: control characters, quotes
// and backslashes escaped, non-ASCII passed through as UTF-8. json.Marshal of a
// Go string cannot fail, and a trap rather than a swallowed error says so.
func JsonEncodeString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		Trap("Json.encode: unencodable string")
		return ""
	}
	return string(b)
}

// JsonEncodeInt is a JSON integer.
func JsonEncodeInt(v int64) string { return strconv.FormatInt(v, 10) }

// JsonEncodeFloat is a JSON number from a Float. NaN and ±Inf are not valid
// JSON; the design accepts that lossiness rather than erroring — see
// std/json.nomi's encode doc comment.
func JsonEncodeFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// JsonEncodeBool is a JSON boolean.
func JsonEncodeBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// --- the Dynamic projection ------------------------------------------------

// JsonToAny is `std/json.Json.to_dynamic`'s payload: the Go `any` shape
// `encoding/json.Unmarshal` would have produced — string, int64, float64, bool,
// []any, map[string]any, nil.
//
// The inverse of jsonFromAny, kept one declaration away from it so the two
// walks stay in step. Note that `Int` produces int64 rather than int, which
// matches json.Number.Int64()'s return type. JsonToDynamic (dynamicops.go) is
// its caller.
func JsonToAny(jv Json) any {
	switch jv.Tag {
	case TagJsonNull:
		return nil
	case TagJsonBool:
		return jv.Bool
	case TagJsonString:
		return jv.String
	case TagJsonInt:
		return jv.Int
	case TagJsonFloat:
		return jv.Float
	case TagJsonArr:
		out := make([]any, 0, ListCount(jv.Arr))
		for node := jv.Arr; node != nil; node = node.Tail {
			out = append(out, JsonToAny(node.Head))
		}
		return out
	case TagJsonObj:
		entries := MapEntries(jv.Obj)
		out := make(map[string]any, len(entries))
		for _, e := range entries {
			out[e.Key] = JsonToAny(e.Val)
		}
		return out
	}
	Trap("Json.to_dynamic: unconstructed Json value")
	return nil
}

package rt

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

// The tag values, the layout and the error vocabulary are pinned absolutely
// here, because an agreement test between two callers of one implementation
// passes when the implementation is wrong.

// TestJsonTagsMatchStdDeclarationOrder is the pin a permuted tag needs.
//
// A tag is not a Go type error: swapping `TagJsonInt` and `TagJsonFloat` compiles
// and produces a wrong answer — `case jv { Int(n) -> … }` would match a Float —
// so nothing but an absolute assertion catches it. The expected numbers are read
// off std/json.nomi's variant order by eye: String, Int, Float, Bool, Arr, Obj,
// Null.
func TestJsonTagsMatchStdDeclarationOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"String", TagJsonString, 1},
		{"Int", TagJsonInt, 2},
		{"Float", TagJsonFloat, 3},
		{"Bool", TagJsonBool, 4},
		{"Arr", TagJsonArr, 5},
		{"Obj", TagJsonObj, 6},
		{"Null", TagJsonNull, 7},
	} {
		if tc.got != tc.want {
			t.Errorf("Json.%s has tag %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	// Tag 0 is reserved invalid, so a never-constructed Json is detectable
	// rather than silently reading as the first variant.
	var zero Json
	if zero.Tag != 0 {
		t.Errorf("the zero Json has tag %d; 0 must be reserved invalid", zero.Tag)
	}
}

// TestJsonIsSelfReferentialAndFinite asserts that Json's recursive payloads
// leave it a finite Go type.
//
// A recursive payload needs both recursive payloads to be indirect, so `Json`
// has a finite size. This asserts the Go fact
// directly, because "it compiles" is otherwise something a reader takes on
// trust, and a payload added later as a tuple or a record would not be indirect
// and would fail with a message about recursion rather than about this design.
func TestJsonIsSelfReferentialAndFinite(t *testing.T) {
	ty := reflect.TypeFor[Json]()
	if ty.Size() == 0 {
		t.Fatalf("Json has zero size")
	}
	arr, hasArr := ty.FieldByName("Arr")
	if !hasArr || arr.Type.Kind() != reflect.Pointer {
		t.Fatalf("Arr must be a POINTER for the recursion to be finite; got %v", arr.Type)
	}
	if arr.Type.Elem() != reflect.TypeFor[List[Json]]() {
		t.Errorf("Arr is %v, want *List[Json]", arr.Type)
	}
	obj, hasObj := ty.FieldByName("Obj")
	if !hasObj || obj.Type != reflect.TypeFor[Map[string, Json]]() {
		t.Fatalf("Obj is %v, want Map[string, Json]", obj.Type)
	}
	// The Map is a value, so its finiteness rests on its own root being a
	// pointer rather than on the field being one. Asserted because that is the
	// half a reader cannot see from json.go.
	root, hasRoot := obj.Type.FieldByName("root")
	if !hasRoot || root.Type.Kind() != reflect.Pointer {
		t.Errorf("Map's root is %v; the recursion through Obj is finite only while it is a pointer",
			root.Type)
	}
}

// TestJsonDecodeErrorTextIsNomisOwn pins every message the vocabulary can
// produce, against the same inputs
// tests/18-ffi-and-dynamic/json_decode_error_text_test.nomi asserts.
//
// That corpus file is the contract and this is the unit-level pin of the same
// strings. Both are kept: the corpus file runs a whole program through the VM
// and this runs on every toolchain bump without one. They fail on
// different mutations, which is the reason for a pair rather than the stronger
// one alone.
func TestJsonDecodeErrorTextIsNomisOwn(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int64
		message   string
	}{
		{"an unexpected character is named, and located at itself",
			"not json", 1, 2, "unexpected character 'o'"},
		{"a bad string escape reports the escaped character",
			`"\q"`, 1, 3, "unexpected character 'q'"},
		{"the position tracks lines, not just columns",
			"{\n  bad}", 2, 3, "unexpected character 'b'"},
		{"a truncated object reports where the input ran out",
			`{"a":`, 1, 6, "unexpected end of input: incomplete JSON value"},
		{"a truncated array reports where the input ran out",
			"[1, 2", 1, 6, "unexpected end of input: incomplete JSON value"},
		{"an unterminated string reports where the input ran out",
			`"unterminated`, 1, 14, "unexpected end of input: incomplete JSON value"},
		{"empty input is distinguished from a truncated value",
			"", 1, 1, "unexpected end of input: expected a JSON value"},
		{"blank input is empty input",
			"   ", 1, 4, "unexpected end of input: expected a JSON value"},
		{"trailing content is reported with its column",
			"{} extra", 1, 4, "trailing content after JSON value"},
		{"a leading zero is rejected as trailing content",
			"01", 1, 2, "trailing content after JSON value"},
		{"a well-formed but unrepresentable number reports its lexeme",
			"1e999", 1, 1, "number out of range: 1e999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JsonDecode(tc.src)
			if got.Tag != TagErr {
				t.Fatalf("decode(%q) succeeded; it must fail", tc.src)
			}
			e := got.Err
			if e.Message != tc.message || e.Line != tc.line || e.Col != tc.col {
				t.Errorf("decode(%q) = line %d, col %d: %q\nwant line %d, col %d: %q",
					tc.src, e.Line, e.Col, e.Message, tc.line, tc.col, tc.message)
			}
		})
	}
}

// TestJsonDecodeOffsetPointsAtTheOffendingByte is the fourth field, which the
// corpus contract test cannot see: `Json.DecodeError.to_string` renders line and
// col and never offset, so nothing else in the repo asserts it.
func TestJsonDecodeOffsetPointsAtTheOffendingByte(t *testing.T) {
	got := JsonDecode("not json")
	if got.Tag != TagErr {
		t.Fatalf("expected a failure")
	}
	// `n` is consumed, `o` is the offending byte at index 1.
	if got.Err.Offset != 1 {
		t.Errorf("offset is %d, want 1 — it must point AT the character the message names",
			got.Err.Offset)
	}
	// End of input has no position from encoding/json, so len(source) is
	// supplied. Pinned so that "no position" never reads as offset 0.
	eof := JsonDecode(`{"a":`)
	if eof.Err.Offset != 5 {
		t.Errorf("truncated input reported offset %d, want 5 = len(source)", eof.Err.Offset)
	}
}

// TestJsonDecodeNamesABadByteNumerically is the one message the corpus file has
// no case for, because a lone invalid byte cannot be written in a Nomi string
// literal. Without this the arm has no witness at all.
func TestJsonDecodeNamesABadByteNumerically(t *testing.T) {
	got := JsonDecode("\xff")
	if got.Tag != TagErr {
		t.Fatalf("expected a failure")
	}
	if got.Err.Message != "unexpected byte 0xff" {
		t.Errorf("message is %q, want %q — quoting it would print U+FFFD and hide which byte was there",
			got.Err.Message, "unexpected byte 0xff")
	}
}

// TestJsonNumberSplitIsIntFirst pins the observable Int-vs-Float decision. It is
// observable because it decides which `case` arm matches, and JsonFromNumber is
// the only place it is made.
func TestJsonNumberSplitIsIntFirst(t *testing.T) {
	cases := []struct {
		lexeme string
		isInt  bool
		i      int64
		f      float64
	}{
		{"42", true, 42, 0},
		{"-7", true, -7, 0},
		{"0", true, 0, 0},
		// A whole number written with a decimal point is a float: the lexeme
		// carries the intent and Int64 rejects it.
		{"36.0", false, 0, 36},
		{"2.5", false, 0, 2.5},
		{"1e3", false, 0, 1000},
		// Beyond int64, so the Float fallback takes it.
		{"9223372036854775808", false, 0, 9223372036854775808},
	}
	for _, tc := range cases {
		i, f, isInt, err := JsonFromNumber(tc.lexeme)
		switch {
		case err != nil:
			t.Errorf("%s: %v", tc.lexeme, err)
		case isInt != tc.isInt:
			t.Errorf("%s: isInt=%v, want %v", tc.lexeme, isInt, tc.isInt)
		case isInt && i != tc.i:
			t.Errorf("%s: Int %d, want %d", tc.lexeme, i, tc.i)
		case !isInt && f != tc.f:
			t.Errorf("%s: Float %v, want %v", tc.lexeme, f, tc.f)
		}
	}
	if _, _, _, err := JsonFromNumber("1e999"); err == nil {
		t.Errorf("1e999 must be a range failure")
	}
}

// TestJsonRoundTripsTheCorpusSample is the whole tree, and it pins the two
// orderings that make this more than a trivial walk: decode inserts object keys
// sorted, encode emits them in the Map's insertion order, so a decoded object
// re-encodes sorted while a hand-built one keeps its own spelling. Both texts
// are asserted in tests/18-ffi-and-dynamic/json_test.nomi.
func TestJsonRoundTripsTheCorpusSample(t *testing.T) {
	src := `{
  "name": "Ada",
  "age": 36,
  "active": true,
  "tags": ["admin", "user"],
  "address": {"city": "London", "zip": "EC1"},
  "favorite": null
}`
	got := JsonDecode(src)
	if got.Tag != TagOk {
		t.Fatalf("decode failed: %+v", got.Err)
	}
	want := `{"active":true,"address":{"city":"London","zip":"EC1"},"age":36,` +
		`"favorite":null,"name":"Ada","tags":["admin","user"]}`
	if enc := JsonEncode(got.Ok); enc != want {
		t.Errorf("encode(decode(src)) =\n  %s\nwant\n  %s", enc, want)
	}
	// The hand-built direction: insertion order survives.
	hand := Json{Tag: TagJsonObj}
	for _, e := range []MapEntry[string, Json]{
		{Key: "x", Val: Json{Tag: TagJsonInt, Int: 1}},
		{Key: "y", Val: Json{Tag: TagJsonFloat, Float: 2.5}},
		{Key: "ok", Val: Json{Tag: TagJsonBool, Bool: true}},
	} {
		hand.Obj = MapPut(hand.Obj, HashString, Eq[string], e.Key, e.Val)
	}
	if enc := JsonEncode(hand); enc != `{"x":1,"y":2.5,"ok":true}` {
		t.Errorf("hand-built encode = %s", enc)
	}
	// And the documented lossiness: a Float that is whole loses its point.
	if enc := JsonEncode(Json{Tag: TagJsonFloat, Float: 36.0}); enc != "36" {
		t.Errorf("encode(Float(36.0)) = %s, want 36", enc)
	}
	// The decoded Arr's Len invariant, which nothing else here reads and which
	// a tail-first build gets wrong by one if the counter is written backwards.
	tags := JsonDecode(`["a", "b", "c"]`)
	if tags.Tag != TagOk || ListCount(tags.Ok.Arr) != 3 {
		t.Errorf("decoded array count = %d, want 3", ListCount(tags.Ok.Arr))
	}
}

// TestJsonEncodeEscapesAndEmptyContainers covers the two shapes a walk gets
// wrong by omission: an empty Arr is the nil list and an empty Obj is the zero
// Map, so neither has a constructor to have been called.
func TestJsonEncodeEscapesAndEmptyContainers(t *testing.T) {
	if got := JsonEncode(Json{Tag: TagJsonArr}); got != "[]" {
		t.Errorf("empty Arr encoded %q, want []", got)
	}
	if got := JsonEncode(Json{Tag: TagJsonObj}); got != "{}" {
		t.Errorf("empty Obj encoded %q, want {}", got)
	}
	if got := JsonEncode(Json{Tag: TagJsonString, String: "a\"b\\c\nd"}); got != `"a\"b\\c\nd"` {
		t.Errorf("escapes: %s", got)
	}
	// Non-ASCII passes through as UTF-8 rather than being \u-escaped.
	if got := JsonEncode(Json{Tag: TagJsonString, String: "café"}); got != `"café"` {
		t.Errorf("non-ASCII: %s", got)
	}
}

// TestJsonEncodeTrapsOnAnUnconstructedValue asserts the tag-0 arm rather than
// leaving it as a comment. Silently encoding `null` for a never-constructed Json
// would be a wrong answer, and the trap is what makes a lowering bug loud.
func TestJsonEncodeTrapsOnAnUnconstructedValue(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("encoding an unconstructed Json must trap")
		}
		e, isNomi := r.(*Error)
		if !isNomi || !strings.Contains(e.Msg, "unconstructed") {
			t.Fatalf("trap was %v, want a Nomi *Error naming the unconstructed value", r)
		}
	}()
	_ = JsonEncode(Json{})
}

// TestJsonToAnyIsTheInverseWalk pins the Go shapes `to_dynamic` hands to a
// Dynamic. Int must be int64 rather than int, which is what
// json.Number.Int64() returns and what the Dynamic extractors expect.
func TestJsonToAnyIsTheInverseWalk(t *testing.T) {
	got := JsonDecode(`{"n": 1, "f": 1.5, "s": "x", "b": true, "z": null, "a": [1, 2]}`)
	if got.Tag != TagOk {
		t.Fatalf("decode failed: %+v", got.Err)
	}
	projected := JsonToAny(got.Ok)
	obj, isMap := projected.(map[string]any)
	if !isMap {
		t.Fatalf("to_any of an Obj is %T, want map[string]any", projected)
	}
	for name, want := range map[string]any{
		"n": int64(1), "f": 1.5, "s": "x", "b": true, "z": nil,
	} {
		if obj[name] != want {
			t.Errorf("%s = %#v (%T), want %#v", name, obj[name], obj[name], want)
		}
	}
	arr, isSlice := obj["a"].([]any)
	if !isSlice || len(arr) != 2 || arr[0] != int64(1) || arr[1] != int64(2) {
		t.Errorf("a = %#v, want []any{int64(1), int64(2)}", obj["a"])
	}
}

// TestJsonNeverForwardsHostErrorText keeps host error text out of Nomi's output.
//
// The invariant is that no message a `Json.DecodeError` carries came from
// `encoding/json`. It is checked by parsing rather than grepping: a text scan matches this test's own
// message and can never reach zero, which is what makes a grep-checkable
// invariant unchecked in practice.
func TestJsonNeverForwardsHostErrorText(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "json.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse json.go: %v", err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel || sel.Sel.Name != "Error" {
			return true
		}
		t.Errorf("json.go:%d calls .Error() on a value; a host parser's own wording "+
			"must never reach a Json.DecodeError.",
			fset.Position(call.Pos()).Line)
		return true
	})
}

// TestJsonLineCol pins the position rule: 1-based coordinates, a newline
// resetting the column, and an offset past the end clamping to len(source)
// rather than walking off it. That last row is the one `decode`'s own
// assertions cannot reach — every real offset it produces is in range — so
// this table is its only guard.
func TestJsonLineCol(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		offset   int
		wantLine int
		wantCol  int
	}{
		{"start", "abc", 0, 1, 1},
		{"middle of line", "abc", 2, 1, 3},
		{"after newline", "ab\ncd", 3, 2, 1},
		{"second char of line 2", "ab\ncd", 4, 2, 2},
		{"past end clamps", "ab", 99, 1, 3},
		{"three lines", "a\nb\nc", 4, 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, col := JsonLineCol(tc.source, tc.offset)
			if line != tc.wantLine || col != tc.wantCol {
				t.Errorf("source=%q offset=%d: got (line=%d, col=%d), want (line=%d, col=%d)",
					tc.source, tc.offset, line, col, tc.wantLine, tc.wantCol)
			}
		})
	}
}

// TestJsonGoFileDeclaresNoInit is the claim json.go's header makes, asserted.
//
// Everything that runs Nomi links `nomi/rt`, so an `init` here would run in
// every such process. No file in rt has one; this keeps json.go from adding
// one, and it parses for the reason above.
func TestJsonGoFileDeclaresNoInit(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "json.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse json.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, isFn := decl.(*ast.FuncDecl)
		if isFn && fn.Recv == nil && fn.Name.Name == "init" {
			t.Errorf("json.go declares an init at line %d", fset.Position(fn.Pos()).Line)
		}
	}
}

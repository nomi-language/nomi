package stdtoml

import (
	"math"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestCheckPlacesSlots(t *testing.T) {
	for _, tc := range []struct {
		name    string
		statics []string
		want    []place
	}{
		{"a value", []string{"port = ", ""}, []place{placeValue}},
		{"a value then a comment", []string{"port = ", " # the port\n"}, []place{placeValue}},
		{"a key", []string{"", " = 1"}, []place{placeKey}},
		{"a dotted key's part", []string{"a.", ".c = 1"}, []place{placeKey}},
		{"a header's key", []string{"[servers.", "]\nip = 1"}, []place{placeKey}},
		{"an array of tables' key", []string{"[[", "]]\nip = 1"}, []place{placeKey}},
		{"array items", []string{"xs = [", ", ", "]"}, []place{placeValue, placeValue}},
		{"a multi-line array", []string{"xs = [\n  ", ", # c\n  ", ",\n]"}, []place{placeValue, placeValue}},
		{"an inline table", []string{"t = {", " = ", ", b = ", "}"}, []place{placeKey, placeValue, placeValue}},
		{"a nested array", []string{"m = [[", "], [", "]]"}, []place{placeValue, placeValue}},
		{"after strings that hold brackets and quotes", []string{"a = \"[\\\"#\"\nb = '''x''' \nc = ", ""}, []place{placeValue}},
		{"after a date-time with a space", []string{"d = 1979-05-27 07:32:00Z\nk = ", ""}, []place{placeValue}},
		{"after a multi-line string ending in quotes", []string{"s = \"\"\"a\"\"\"\"\"\nk = ", ""}, []place{placeValue}},
		{"in a line after a table", []string{"[t]\n", " = 1"}, []place{placeKey}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r := check(tc.statics); r != nil {
				t.Fatalf("refused: %+v", *r)
			}
			got, _ := places(tc.statics)
			if len(got) != len(tc.want) {
				t.Fatalf("places %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("places %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestCheckRefuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statics  []string
		fragment int
		offset   int
		message  string
	}{
		{"inside a basic string", []string{`a = "x`, `"`}, 0, 6, inString},
		{"inside a literal string", []string{"a = '", "'"}, 0, 5, inString},
		{"inside a multi-line string", []string{"a = \"\"\"\n", "\"\"\""}, 0, 8, inString},
		{"inside a quoted key", []string{`"a`, `" = 1`}, 0, 2, inString},
		{"in a comment", []string{"a = 1 # ", "\n"}, 0, 8, inComment},
		{"after a bare key's text", []string{"ab", " = 1"}, 0, 2, notWhole},
		{"before a bare key's text", []string{"", "b = 1"}, 0, 0, notWhole},
		{"after a number", []string{"a = 1", ""}, 0, 5, notWhole},
		{"before a number", []string{"a = ", "0"}, 0, 4, notWhole},
		{"two slots touching", []string{"a = ", "", ""}, 1, 0, notWhole},
		{"after a value", []string{"a = 1 ", ""}, 0, 6, notWhole},
		{"in a header, touching", []string{"[a", "]"}, 0, 2, notWhole},
		{"after a header", []string{"[a] ", ""}, 0, 4, notWhole},
		{"after `=`'s key", []string{"a ", " = 1"}, 0, 2, notWhole},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := check(tc.statics)
			if r == nil {
				t.Fatal("accepted")
			}
			if r.fragment != tc.fragment || r.offset != tc.offset || r.message != tc.message {
				t.Fatalf("refused %+v, want {%d %d %s}", *r, tc.fragment, tc.offset, tc.message)
			}
		})
	}
}

func TestCheckReadsTheTextAsToml(t *testing.T) {
	r := check([]string{"a = 1\nb = = 2\nc = ", ""})
	if r == nil {
		t.Fatal("accepted text that is not TOML")
	}
	if r.fragment != 0 || r.offset < 6 || r.offset > 10 {
		t.Fatalf("refused at %+v; want a place on the second line", *r)
	}
	if r := check([]string{"a = 1\na = ", ""}); r == nil {
		t.Fatal("accepted a key defined twice")
	}
	// Two key slots stand for different keys.
	if r := check([]string{"", " = 1\n", " = 2"}); r != nil {
		t.Fatalf("refused two key slots: %+v", *r)
	}
	// An error after a slot is placed in the static it is in.
	r = check([]string{"a = ", "\nb = = 1"})
	if r == nil || r.fragment != 1 {
		t.Fatalf("refused at %+v; want a place in the second static", r)
	}
	// Text that is not TOML before a slot is reported, not the slot it
	// misplaces.
	r = check([]string{"a = 1\nb = = ", ""})
	if r == nil || r.message == notWhole || r.fragment != 0 || r.offset != 10 {
		t.Fatalf("refused %+v; want the validator's error at the second `=`", r)
	}
	if r := check([]string{""}); r != nil {
		t.Fatalf("refused an empty document: %+v", *r)
	}
}

func TestBuildWritesSlotsForWhereTheyStand(t *testing.T) {
	statics := []string{"", " = ", "\n[t.", "]\nx = [", ", ", "]"}
	slots := []string{`"a b"`, `"v"`, "42", "1", `"two"`}
	got := FFIBuild(statics, slots)
	want := "\"a b\" = \"v\"\n[t.\"42\"]\nx = [1, \"two\"]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	var m map[string]any
	if _, err := toml.Decode(got, &m); err != nil {
		t.Fatalf("built text is not TOML: %v", err)
	}
}

func TestBuildQuotesAKeyThatIsNotOneString(t *testing.T) {
	for _, v := range []string{`"a".b`, `"a" = 1`, "1.5", `"a"` + "\n" + `"b"`, `"""x"""`, `"x\"`} {
		got := FFIBuild([]string{"", " = 1"}, []string{v})
		var m map[string]any
		if _, err := toml.Decode(got, &m); err != nil {
			t.Errorf("%q: built %q, not TOML: %v", v, got, err)
			continue
		}
		if len(m) != 1 {
			t.Errorf("%q: built %q, which defines %d keys", v, got, len(m))
		}
	}
}

func TestQuoteRoundTrips(t *testing.T) {
	for _, s := range []string{"", "plain", `x"` + "\nadmin = true", `back\slash`, "tab\there", "\x00\x01\x1f\x7f", "é ü 字 🎉", "\b\f\r"} {
		var m map[string]string
		if _, err := toml.Decode("k = "+FFIQuote(s), &m); err != nil {
			t.Errorf("%q: %v", s, err)
			continue
		}
		if m["k"] != s {
			t.Errorf("%q read back as %q", s, m["k"])
		}
	}
}

func TestFloatReadsBackAsAFloat(t *testing.T) {
	for _, f := range []float64{0, 1, -2.5, 1e21, 1e-7, 123456789, math.MaxFloat64, math.SmallestNonzeroFloat64} {
		var m map[string]any
		text := FFIFloat(f)
		if _, err := toml.Decode("k = "+text, &m); err != nil {
			t.Errorf("%v as %q: %v", f, text, err)
			continue
		}
		if got, ok := m["k"].(float64); !ok || got != f {
			t.Errorf("%v as %q read back as %#v", f, text, m["k"])
		}
	}
	for f, want := range map[float64]string{math.Inf(1): "inf", math.Inf(-1): "-inf", math.NaN(): "nan"} {
		if got := FFIFloat(f); got != want {
			t.Errorf("%v: %q, want %q", f, got, want)
		}
	}
}

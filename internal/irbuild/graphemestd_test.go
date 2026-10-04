package irbuild

import (
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// The grapheme-based half of std/strings.
//
// `String.length`, `String.slice` and `String.reverse` are grapheme-cluster
// operations per UAX #29; std/strings.nomi states `String.length("héllo") == 5`
// as the definition. There is no correct implementation of them over
// `unicode/utf8`, so they are implemented in rt over github.com/rivo/uniseg.
// `String.length` is an ordinary language primitive that most programs call,
// and grapheme counting is runtime work, so the dependency belongs to rt
// rather than to the compiler's module. rt/consumer_test.go asserts that
// uniseg is rt's only `require`, so a second dependency fails a test.
//
// `String.normalize` needs golang.org/x/text, a much larger dependency, and is
// an operation over a form the caller names. It lives in nomi/stdstrings
// (stringsHostPath), outside rt, so only the programs that normalize link
// x/text's tables, and nomi/stdstrings imports nothing of the compiler.
//
// `to_codepoints` is bound to rt.StringToCodepoints, because code points are
// Go's own UTF-8 decode.

// TestGraphemeCountIsNotACodepointCount is the discriminator, and it runs in
// both directions.
//
// The first half proves that a code-point count and a cluster count disagree
// on every input below, which is the positive control for the second half.
//
// The second half is the assertion: the bound function must answer the
// cluster count on every input. "Implement it over rt.StringToCodepoints" is
// the plausible wrong implementation, and it is wrong for combining marks, ZWJ
// emoji and regional-indicator pairs rather than only for exotic input.
func TestGraphemeCountIsNotACodepointCount(t *testing.T) {
	// Each row is a string whose three lengths differ. Measured with
	// uniseg.GraphemeClusterCount and utf8.RuneCountInString rather than typed
	// in, and then pinned, so a wrong constant here cannot pass.
	cases := []struct {
		name     string
		s        string
		clusters int
		runes    int
	}{
		{"combining acute", "he\u0301llo", 5, 6},
		{"lone combining mark", "a\u0301", 1, 2},
		{"zwj family", "\U0001F469\u200D\U0001F467", 1, 3},
		{"regional indicators", "\U0001F1FA\U0001F1F8", 1, 2},
	}
	for _, c := range cases {
		if got := uniseg.GraphemeClusterCount(c.s); got != c.clusters {
			t.Errorf("%s: uniseg counts %d clusters, this table says %d", c.name, got, c.clusters)
		}
		if got := utf8.RuneCountInString(c.s); got != c.runes {
			t.Errorf("%s: %d code points, this table says %d", c.name, got, c.runes)
		}
		if c.clusters == c.runes {
			t.Errorf("%s: cluster count equals code-point count, so this row discriminates "+
				"nothing and the guard below would be vacuous on it", c.name)
		}
	}

	fn := graphemeLengthBinding()
	if fn == nil {
		t.Fatal("strings.String.length has no binding in internal/stdlibbindings' RtFuncs")
	}
	for _, c := range cases {
		got := reflect.ValueOf(fn).Call([]reflect.Value{reflect.ValueOf(c.s)})[0].Int()
		if got != int64(c.clusters) {
			t.Errorf("%s: the bound strings.String.length answered %d; `String.length` is "+
				"GRAPHEME CLUSTERS (%d), and %d is the code-point count. An implementation over "+
				"code points is a silent wrong answer.",
				c.name, got, c.clusters, c.runes)
		}
	}
}

// graphemeLengthBinding is the rt/host function bound to
// `strings.String.length`, or nil when there is none.
//
// Read off internal/stdlibbindings rather than off stdlibHostFuncs, because
// the registry keeps the derived NAME and this needs the callable value.
func graphemeLengthBinding() any {
	for _, b := range stdlibbindings.RtFuncs() {
		if b.Name == "strings.String.length" {
			return b.Fn
		}
	}
	return nil
}

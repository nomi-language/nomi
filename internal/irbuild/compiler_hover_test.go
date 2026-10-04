package irbuild

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/stdcompiler"
)

// TestHoverResultProjectsThroughTheHoverSpec is the registry-side assertion, and
// it is here because `compiler.hover` is the first bound row whose result kind is
// reached RECURSIVELY: `rt.Result[rt.Hover, string]` projects only because
// stdStructSpecs has a `Hover` row for preludeKindOfGoType to bottom out in.
//
// Both halves are checked. Without the spec row the projection must FAIL rather
// than answer something plausible, because an unrepresentable payload that
// projected anyway is a call lowered against a type the builder cannot represent.
func TestHoverResultProjectsThroughTheHoverSpec(t *testing.T) {
	k := kindOfGoType(reflect.TypeFor[rt.Result[rt.Hover, string]]())
	if k == kindInvalid {
		t.Fatal("rt.Result[rt.Hover, string] projects onto nothing, so compiler.hover cannot be bound")
	}
	if got, want := k.nomi(), "Result<Hover, String>"; got != want {
		t.Fatalf("rt.Result[rt.Hover, string] projects as %s, want %s", got, want)
	}
	// The spec row is what makes it work, and it must be the row for
	// std/compiler's Hover rather than any struct with two String fields: a
	// second Go type for one Nomi type is what TestStdStructHasExactlyONEGoRepresentation
	// exists to prevent, and this is the same claim read from the registry side.
	if got := kindOfGoType(reflect.TypeFor[rt.Hover]()); got == kindInvalid {
		t.Fatal("rt.Hover projects onto nothing")
	} else if got.nomi() != "Hover" {
		t.Fatalf("rt.Hover projects as %s, want Hover", got.nomi())
	}
	// And the declaration is admitted as a crossing that internal/compilerhosts
	// answers, with the declared signature.
	f := buildStdlibIndex().byKey["compiler.hover"]
	if f == nil || f.why != "" {
		t.Fatalf("compiler.hover is not retained: %+v", f)
	}
	if name, ok := irCompilerHost(f); !ok || name != "compiler.hover" {
		t.Errorf("compiler.hover is not a std/compiler crossing (%q, %v)", name, ok)
	}
	if len(f.params) != 1 || f.params[0] != kindString {
		t.Errorf("compiler.hover takes %v, want one String", f.params)
	}
	if f.result != k {
		t.Errorf("compiler.hover's declared result kind is not the projection of Result<Hover, String>")
	}
}

// TestHoverMarkerIsStrictlyOne pins the two rejections nothing else can reach:
// they are the reason a fixture can say WHERE it means without pinning a line
// and a column, and they are silent failures if either check is dropped.
//
// Zero markers would resolve to line 1 column 1, and two would resolve to the
// first — both a hover answer for a position the fixture did not name, and both
// indistinguishable from a correct answer in any output comparison.
func TestHoverMarkerIsStrictlyOne(t *testing.T) {
	for _, tc := range []struct{ name, src, wantErr string }{
		{"none", "fn main() {\n}\n", "found none"},
		{"two", "fn maˇin() {\nˇ}\n", "found multiple"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := stdcompiler.StripSingleHoverMarker(tc.src)
			if err == nil {
				t.Fatalf("%q was accepted", tc.src)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error is %q, want it to name %q", err, tc.wantErr)
			}
		})
	}
	// One marker: the position is 1-based and counted in BYTES from the start of
	// its line, which is what analysis.Pos means everywhere else. A multibyte
	// rune before the marker on the same line is the case that separates a byte
	// column from a rune column, and getting it wrong resolves to a neighbouring
	// token rather than to an error.
	clean, pos, err := stdcompiler.StripSingleHoverMarker("fn main() {\n  x = \"é\" + bˇar\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clean, stdcompiler.HoverMarker) {
		t.Error("the marker survived into the parsed source")
	}
	if pos.Line != 2 {
		t.Errorf("line is %d, want 2", pos.Line)
	}
	// `  x = "é" + b` is 14 bytes (é is two), so the marker stands at column 15.
	if pos.Col != 15 {
		t.Errorf("col is %d, want 15 — counted in bytes, as analysis.Pos is everywhere else", pos.Col)
	}
}
